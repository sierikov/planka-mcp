package planka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// loginPath is Planka's login route; DELETE loginPath+"/me" ends the session.
const loginPath = "/api/access-tokens"

// LoginError is Planka refusing a username/password login. It deliberately has
// no StatusCode method, so callers that turn a 401 into API-key advice do not
// mistake it for a rejected API key.
type LoginError struct {
	msg string
	// final marks a refusal of the credentials themselves: sending them again
	// cannot succeed and only adds failed logins, which Planka logs per client IP
	// and newer versions rate-limit.
	final bool
}

// Error implements error.
func (e *LoginError) Error() string { return e.msg }

// session is a username/password login and the token it last obtained. The token
// lives in memory only. mu also makes concurrent callers share one login.
type session struct {
	username, password string

	mu      sync.Mutex
	token   string
	refused *LoginError // a final refusal, returned without asking Planka again
}

// WithLogin makes the client log in with a Planka username (or email) and
// password instead of an API key, for users who cannot create API keys: in
// Planka only admins can. The client logs in on its first request, sends the
// session token as "Authorization: Bearer", and logs in again once when Planka
// answers 401. An API key in the request context still wins.
func WithLogin(username, password string) Option {
	return func(c *Client) { c.session = &session{username: username, password: password} }
}

// sessionToken returns the current session token, logging in first if there is none.
func (c *Client) sessionToken(ctx context.Context) (string, error) {
	s := c.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	if s.refused != nil {
		return "", s.refused
	}
	token, err := c.login(ctx)
	if err != nil {
		if le, ok := err.(*LoginError); ok && le.final { //nolint:errorlint // login returns *LoginError unwrapped
			s.refused = le
		}
		return "", err
	}
	s.token = token
	return token, nil
}

// dropSession forgets token if it is still the current one, so the next request
// logs in again. A token another caller has already replaced is left alone.
func (c *Client) dropSession(token string) {
	s := c.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == token {
		s.token = ""
	}
}

// login posts the credentials to Planka and returns the new session token.
func (c *Client) login(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{
		"emailOrUsername": c.session.username,
		"password":        c.session.password,
	})
	if err != nil {
		return "", fmt.Errorf("encoding login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+loginPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("logging in to Planka: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return "", redirectError(req, resp)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("logging in to Planka: reading response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", loginRefusal(resp.StatusCode, respBody)
	}
	var out struct {
		Item string `json:"item"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.Item == "" {
		// Not echoed: an answer in an unknown shape may still carry a token.
		return "", fmt.Errorf("logging in to Planka: unexpected answer (HTTP %d, %d bytes)", resp.StatusCode, len(respBody))
	}
	return out.Item, nil
}

// loginRefusal explains why Planka refused the login, in terms of what the user can do.
// Every answer is a *LoginError, so no caller mistakes it for a rejected API key or a
// missing board right.
func loginRefusal(status int, body []byte) *LoginError {
	var answer struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &answer)
	msg := strings.ToLower(answer.Message)
	switch {
	case status == http.StatusUnauthorized:
		return &LoginError{msg: "Planka rejected the username or password: check PLANKA_USERNAME and PLANKA_PASSWORD", final: true}
	case status == http.StatusTooManyRequests || strings.Contains(msg, "rate limit"):
		return &LoginError{msg: "Planka is limiting login attempts for this account: wait a little, then try again"}
	case strings.Contains(msg, "single sign-on"):
		// Planka 2.1 answers this for SSO-only accounts and when SSO is enforced;
		// 2.2.1's login has no such answer, so there the branch is unused.
		return &LoginError{msg: "this Planka account signs in with single sign-on, so it has no password: ask a Planka admin for an API key (PLANKA_API_KEY)", final: true}
	case strings.Contains(msg, "terms"):
		return &LoginError{msg: "Planka wants this account to accept its terms first: log in once in the browser, then try again"}
	case strings.Contains(msg, "totp"):
		return &LoginError{msg: "this Planka account uses two-factor authentication, which password login cannot complete: ask a Planka admin for an API key (PLANKA_API_KEY)", final: true}
	case strings.Contains(msg, "initialize"):
		return &LoginError{msg: "this Planka instance is not set up yet: an admin has to log in once in the browser first"}
	case answer.Message != "":
		return &LoginError{msg: fmt.Sprintf("Planka refused the login (HTTP %d): %s", status, truncate(answer.Message))}
	default:
		return &LoginError{msg: fmt.Sprintf("Planka refused the login (HTTP %d)", status)}
	}
}

// errSessionRejected is a 401 for a session token Planka issued moments ago. The
// login itself works, so the token is lost on the way: typically a proxy in front
// of Planka that drops the Authorization header (and passes X-Api-Key).
var errSessionRejected = &LoginError{msg: "Planka accepted the login but rejected its session token (HTTP 401): a proxy in front of Planka may be dropping the Authorization header"}

// Logout ends the login session, if the client has one open, so sessions do not
// pile up in the user's Planka account. A session Planka already dropped is not an
// error. Clients using an API key have nothing to end. Call it after the last
// request: a request made afterwards logs in again and opens a new session.
func (c *Client) Logout(ctx context.Context) error {
	if c.session == nil {
		return nil
	}
	c.session.mu.Lock()
	token := c.session.token
	c.session.token = ""
	c.session.mu.Unlock()
	if token == "" {
		return nil
	}
	err := c.send(ctx, credential{header: "Authorization", value: "Bearer " + token}, http.MethodDelete, loginPath+"/me", nil, nil, nil)
	if apiErr, ok := err.(*APIError); ok && apiErr.Status == http.StatusUnauthorized { //nolint:errorlint // send returns *APIError unwrapped
		return nil
	}
	return err
}
