// Package planka is a minimal client for the Planka v2 HTTP API.
//
// It only speaks HTTP: one method per endpoint, returning the API's own wire
// types. It knows nothing about MCP, formatting, or how results are used.
package planka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// maxErrorBody is the most response-body bytes kept in an APIError, so a
// reverse proxy answering with a large HTML page cannot flood a tool result.
const maxErrorBody = 200

// ErrNoAPIKey is returned, before any request is made, when neither the
// context nor the client carries a Planka API key, and the client has no login.
var ErrNoAPIKey = errors.New("no Planka API key")

// APIError is a non-2xx answer from Planka.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Body is the response body, truncated to 200 bytes.
	Body string
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("planka API %d: %s", e.Status, e.Body)
}

// StatusCode returns the HTTP status code. It lets callers detect 401/403
// through an interface without importing this package.
func (e *APIError) StatusCode() int {
	return e.Status
}

// ctxKey is the type of the context key holding the per-request API key.
type ctxKey struct{}

// WithAPIKey returns a context that makes the client authenticate with key
// instead of its static key.
func WithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, ctxKey{}, key)
}

// Client is an HTTP client for the Planka API.
type Client struct {
	baseURL    string
	staticKey  string
	session    *session // set by WithLogin
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithStaticKey sets the API key used when the request context carries none
// (the stdio transport, where one key serves every call).
func WithStaticKey(key string) Option {
	return func(c *Client) { c.staticKey = key }
}

// WithHTTPClient replaces the default HTTP client (15 second timeout). The
// client uses a copy of h that never follows redirects; h itself is left as it
// is.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// NewClient creates a client for the Planka instance at baseURL.
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	// Following a redirect would turn a POST, PATCH or DELETE into a GET that
	// looks like success, and would send X-Api-Key to whatever host the
	// redirect names. do reports a redirect as an error instead.
	h := *c.httpClient
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.httpClient = &h
	return c
}

// pathParam escapes a caller-supplied ID for safe use in URL paths.
// Prevents path traversal (e.g. "../../admin") by percent-encoding slashes.
func pathParam(id string) string {
	return url.PathEscape(id)
}

// credential is how one request authenticates: an API key in X-Api-Key, or a
// login session token in "Authorization: Bearer".
type credential struct {
	header, value string
	session       bool
}

// credential picks the context key, then the static key, then the login session
// (logging in if needed).
func (c *Client) credential(ctx context.Context) (credential, error) {
	if key, ok := ctx.Value(ctxKey{}).(string); ok && key != "" {
		return credential{header: "X-Api-Key", value: key}, nil
	}
	if c.staticKey != "" {
		return credential{header: "X-Api-Key", value: c.staticKey}, nil
	}
	if c.session != nil {
		token, err := c.sessionToken(ctx)
		if err != nil {
			return credential{}, err
		}
		return credential{header: "Authorization", value: "Bearer " + token, session: true}, nil
	}
	return credential{}, ErrNoAPIKey
}

// truncate cuts s to at most maxErrorBody bytes without splitting a rune.
func truncate(s string) string {
	if len(s) <= maxErrorBody {
		return s
	}
	cut := maxErrorBody
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// newRequest builds an authenticated request with an optional JSON body.
func (c *Client) newRequest(ctx context.Context, cred credential, method, path string, query url.Values, body any) (*http.Request, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}

	reqBody := io.Reader(http.NoBody)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set(cred.header, cred.value)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do sends a request with an optional JSON body and decodes a JSON response
// into out when out is non-nil. Any 2xx status is success; a 3xx is an error
// that names the redirect target, and anything else is an *APIError. It fails
// with ErrNoAPIKey, before any request, when there is no key and no login.
//
// With a login, a 401 means the session expired or was revoked: do logs in again
// and repeats the request once. Planka did not apply a request it answered 401,
// so repeating a write is safe.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	cred, err := c.credential(ctx)
	if err != nil {
		return err
	}
	err = c.send(ctx, cred, method, path, query, body, out)
	if apiErr, ok := err.(*APIError); cred.session && ok && apiErr.Status == http.StatusUnauthorized { //nolint:errorlint // send returns *APIError unwrapped
		c.dropSession(strings.TrimPrefix(cred.value, "Bearer "))
		if cred, err = c.credential(ctx); err != nil {
			return err
		}
		err = c.send(ctx, cred, method, path, query, body, out)
		if apiErr, ok := err.(*APIError); ok && apiErr.Status == http.StatusUnauthorized { //nolint:errorlint // send returns *APIError unwrapped
			return errSessionRejected
		}
	}
	return err
}

// send makes one request with cred and handles its answer as do describes.
func (c *Client) send(ctx context.Context, cred credential, method, path string, query url.Values, body, out any) error {
	req, err := c.newRequest(ctx, cred, method, path, query, body)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return redirectError(req, resp)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &APIError{Status: resp.StatusCode, Body: truncate(string(respBody))}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// redirectError explains a 3xx answer, which the client never follows. When
// the target is the same API path on another base URL (http to https, another
// host, a path prefix), it names that base as the PLANKA_URL to use.
func redirectError(req *http.Request, resp *http.Response) error {
	loc, err := resp.Location()
	if err != nil {
		return fmt.Errorf("planka answered with a redirect (HTTP %d) but no target: check PLANKA_URL", resp.StatusCode)
	}
	target := loc.String()
	base := "that address"
	if prefix, ok := strings.CutSuffix(loc.Path, req.URL.Path); ok && loc.RawQuery == req.URL.RawQuery {
		b := *loc
		b.Path, b.RawPath, b.RawQuery, b.Fragment = prefix, "", "", ""
		base = b.String()
	}
	return fmt.Errorf("planka redirected to %s (HTTP %d): set PLANKA_URL to %s", target, resp.StatusCode, base)
}
