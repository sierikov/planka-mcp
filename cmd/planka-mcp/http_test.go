package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
	"github.com/sierikov/planka-mcp/internal/tools"
)

// headerTransport adds fixed headers to every request.
type headerTransport struct {
	header http.Header
	next   http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.header {
		r.Header[k] = v
	}
	return h.next.RoundTrip(r)
}

// httpHarness is the HTTP handler in front of the real tools, wired to a fake Planka that
// accepts the given keys, with no static key on the client (the HTTP-mode setup).
func httpHarness(t *testing.T, logger *slog.Logger, keys ...string) (srv *httptest.Server, fake *plankatest.Server) {
	t.Helper()
	return httpHarnessWith(t, logger, nil, keys...)
}

// httpHarnessWith is httpHarness with routes added to (or replacing) the fixture routes.
func httpHarnessWith(t *testing.T, logger *slog.Logger, extra map[string]any, keys ...string) (srv *httptest.Server, fake *plankatest.Server) {
	t.Helper()
	routes := map[string]any{
		"GET /api/users/me":          plankatest.Fixture(t, "users_me"),
		"GET /api/projects":          plankatest.Fixture(t, "projects"),
		"GET /api/boards/" + boardID: plankatest.Fixture(t, "board"),
	}
	for k, v := range extra {
		routes[k] = v
	}
	fake = plankatest.NewServer(t, routes, plankatest.WithKeys(keys...))
	client := planka.NewClient(fake.URL)
	server := tools.NewServer(newDeps(client), "test")
	srv = httptest.NewServer(newHTTPHandler(server, logger))
	t.Cleanup(srv.Close)
	return srv, fake
}

// connectHTTP opens an MCP session on srv that sends Authorization: Bearer key.
func connectHTTP(t *testing.T, srv *httptest.Server, key string) *mcp.ClientSession {
	t.Helper()
	httpClient := &http.Client{Transport: headerTransport{
		header: http.Header{"Authorization": {"Bearer " + key}},
		next:   http.DefaultTransport,
	}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: httpClient}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// hostTransport sends every request with the given Host header, as a reverse proxy that preserves
// the public host name does.
type hostTransport struct {
	host string
	next http.RoundTripper
}

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = h.host
	return h.next.RoundTrip(r)
}

// TestProxiedHostIsServed: behind Caddy or Traefik on the same machine, a request arrives on
// 127.0.0.1 with the public Host. The SDK's DNS-rebinding guard would answer 403; the bearer key
// is what protects /mcp, so the request is served.
func TestProxiedHostIsServed(t *testing.T) {
	srv, _ := httpHarness(t, discardLogger(), "k1")
	require.Contains(t, srv.URL, "127.0.0.1", "the request must arrive on a loopback address")

	httpClient := &http.Client{Transport: hostTransport{host: "mcp.example.com", next: headerTransport{
		header: http.Header{"Authorization": {"Bearer k1"}},
		next:   http.DefaultTransport,
	}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: httpClient}, nil)
	require.NoError(t, err, "a proxied Host must not be refused")
	t.Cleanup(func() { _ = session.Close() })

	assert.Contains(t, callText(t, session, "list_boards", nil), boardID)
}

// syncBuffer is a bytes.Buffer safe to write from handler goroutines while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHealthz(t *testing.T) {
	srv, _ := httpHarness(t, discardLogger(), plankatest.APIKey)

	resp, err := http.Get(srv.URL + "/healthz") //nolint:noctx // test against a local server
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
}

func TestMCPWithoutBearerIs401(t *testing.T) {
	srv, fake := httpHarness(t, discardLogger(), plankatest.APIKey)

	for name, header := range map[string]string{
		"missing":      "",
		"wrong scheme": "Basic abc",
		"empty key":    "Bearer ",
		"no space":     "Bearer",
		"only spaces":  "Bearer    ",
	} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
			require.NoError(t, err)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Equal(t, "Bearer", resp.Header.Get("WWW-Authenticate"))
			assert.Equal(t, "send your Planka API key as \"Authorization: Bearer <key>\"\n", string(body))
		})
	}
	assert.Empty(t, fake.Requests(), "a rejected request must not reach Planka")
}

func TestBearerKeyForwardedAsXAPIKey(t *testing.T) {
	srv, fake := httpHarness(t, discardLogger(), "k1")
	session := connectHTTP(t, srv, "k1")

	text := callText(t, session, "list_boards", nil)

	assert.Contains(t, text, boardID)
	reqs := fake.Requests()
	require.NotEmpty(t, reqs)
	for _, r := range reqs {
		assert.Equal(t, "k1", r.APIKey, "%s %s", r.Method, r.Path)
	}
}

// callOutcome is the result of a tool call made off the test goroutine.
type callOutcome struct {
	text string
	err  error
}

// callToolText calls a tool and returns its text without using testify's require, so it can run
// on any goroutine; the caller asserts on the outcome from the test goroutine.
func callToolText(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) callOutcome {
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return callOutcome{err: err}
	}
	if len(res.Content) != 1 {
		return callOutcome{err: fmt.Errorf("want 1 content block, got %d", len(res.Content))}
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return callOutcome{err: fmt.Errorf("content is %T", res.Content[0])}
	}
	if res.IsError {
		return callOutcome{err: fmt.Errorf("tool error: %s", text.Text)}
	}
	return callOutcome{text: text.Text}
}

// TestConcurrentClientsKeepTheirKeys runs two callers in parallel against a fake Planka whose
// project list depends on the API key, so each response proves which key its request carried.
func TestConcurrentClientsKeepTheirKeys(t *testing.T) {
	const calls = 20
	projects := plankatest.Fixture(t, "projects")
	require.Contains(t, string(projects), "Projects")
	srv, fake := httpHarnessWith(t, discardLogger(), map[string]any{
		"GET /api/projects": plankatest.ByKey{
			"k1": projects,
			"k2": json.RawMessage(strings.ReplaceAll(string(projects), "Projects", "ProjectsOfK2")),
		},
	}, "k1", "k2")
	sessions := map[string]*mcp.ClientSession{"k1": connectHTTP(t, srv, "k1"), "k2": connectHTTP(t, srv, "k2")}

	outcomes := map[string][]callOutcome{"k1": make([]callOutcome, calls), "k2": make([]callOutcome, calls)}
	var wg sync.WaitGroup
	for key, session := range sessions {
		for i := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[key][i] = callToolText(t.Context(), session, "list_boards", nil)
			}()
		}
	}
	wg.Wait()

	for i, o := range outcomes["k1"] {
		require.NoError(t, o.err, "k1 call %d", i)
		assert.Contains(t, o.text, `"project":"Projects"`, "k1 call %d got another caller's data", i)
	}
	for i, o := range outcomes["k2"] {
		require.NoError(t, o.err, "k2 call %d", i)
		assert.Contains(t, o.text, `"project":"ProjectsOfK2"`, "k2 call %d got another caller's data", i)
	}

	// list_boards makes exactly one Planka request, so every request is accounted for here.
	perKey := map[string]int{}
	for _, r := range fake.Requests() {
		assert.Equal(t, "/api/projects", r.Path)
		perKey[r.APIKey]++
	}
	assert.Equal(t, map[string]int{"k1": calls, "k2": calls}, perKey)
}

func TestKeyNeverLogged(t *testing.T) {
	const key = "super-secret-planka-key"
	var buf syncBuffer
	srv, _ := httpHarness(t, slog.New(slog.NewTextHandler(&buf, nil)), key)

	// A rejected request that carries the secret in the wrong scheme.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Basic "+key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// A successful call with the secret as the bearer key.
	callText(t, connectHTTP(t, srv, key), "list_boards", nil)

	// The line is written after the response, so wait for both requests to be logged.
	require.Eventually(t, func() bool { return strings.Count(buf.String(), "method=") >= 2 }, 2*time.Second, 10*time.Millisecond)

	sum := sha256.Sum256([]byte(key))
	logged := buf.String()
	assert.NotContains(t, logged, key)
	assert.Contains(t, logged, "key="+hex.EncodeToString(sum[:])[:8])
	assert.Contains(t, logged, "status=401")
	assert.Contains(t, logged, "status=200")
	assert.Contains(t, logged, "path=/mcp")
}

func TestBearerKey(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{"plain", "Bearer k", "k", true},
		{"lowercase scheme", "bearer k", "k", true},
		{"mixed case scheme", "BeArEr k", "k", true},
		{"double space is trimmed", "Bearer  k", "k", true},
		{"trailing space is trimmed", "Bearer k ", "k", true},
		{"inner space is rejected", "Bearer a b", "", false},
		{"inner tab is rejected", "Bearer a\tb", "", false},
		{"blank key", "Bearer \t", "", false},
		{"spaces only", "Bearer    ", "", false},
		{"no space", "Bearer", "", false},
		{"tab separator", "Bearer\tk", "", false},
		{"other scheme", "Basic x", "", false},
		{"bare key", "k", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bearerKey(tt.header)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestCrossOriginBrowserRequestIs403: the MCP spec says a server MUST answer 403
// to an invalid Origin. A browser request from another site is refused before the
// bearer check; a same-site browser request and a request with no Origin go on to
// it, and without a key get its 401.
func TestCrossOriginBrowserRequestIs403(t *testing.T) {
	srv, fake := httpHarness(t, discardLogger(), plankatest.APIKey)

	for name, tc := range map[string]struct {
		origin string
		want   int
	}{
		"another site": {"https://evil.example", http.StatusForbidden},
		"same site":    {srv.URL, http.StatusUnauthorized},
		"no Origin":    {"", http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
			require.NoError(t, err)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = resp.Body.Close() })

			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
	assert.Empty(t, fake.Requests(), "no request without a key reaches Planka")
}
