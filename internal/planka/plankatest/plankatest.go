// Package plankatest provides a fake Planka API server and JSON fixtures for
// tests that exercise the real HTTP client.
package plankatest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// APIKey is the API key the fake server accepts.
const APIKey = "test-key"

// Reply is a route value that sets the HTTP status. Body is encoded as JSON,
// except that a json.RawMessage or a string is written as is (a string lets a
// test simulate a non-JSON answer such as a proxy's HTML error page) and a nil
// Body writes nothing.
type Reply struct {
	Status int
	Body   any
}

// ByKey is a route value that answers each API key differently: the value is picked by the
// request's X-Api-Key, and a key without an entry gets 404. Each value is any route value
// (JSON, json.RawMessage or Reply). It lets a test tell which caller a response was meant for.
type ByKey map[string]any

// Recorded is a request the fake server received.
type Recorded struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
	APIKey string
	// Bearer is the token of an "Authorization: Bearer" header, if any.
	Bearer string
}

// loginPath is Planka's login route; it is the one route served without
// authentication.
const loginPath = "/api/access-tokens"

// Server is a fake Planka API that answers each route with canned JSON.
type Server struct {
	*httptest.Server

	routes map[string]any
	keys   map[string]bool

	username, password string // set by WithLogin

	mu       sync.Mutex
	requests []Recorded
	issued   int             // session tokens handed out so far
	sessions map[string]bool // live session tokens
}

// Option configures a Server.
type Option func(*Server)

// WithKeys makes the server accept every listed API key instead of only APIKey.
func WithKeys(keys ...string) Option {
	return func(s *Server) {
		s.keys = make(map[string]bool, len(keys))
		for _, k := range keys {
			s.keys[k] = true
		}
	}
}

// WithLogin makes POST /api/access-tokens accept these credentials, as Planka's
// login does, and answer each login with a new session token. A live token in an
// "Authorization: Bearer" header then authenticates like an API key, and
// DELETE /api/access-tokens/me ends that session. Wrong credentials get Planka's
// 401. A route registered for "POST /api/access-tokens" overrides all of this, so
// a test can return Planka's other login refusals.
func WithLogin(username, password string) Option {
	return func(s *Server) { s.username, s.password = username, password }
}

// ExpireSessions makes every session token issued so far invalid, as if it had
// expired or been revoked.
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.sessions)
}

// Logins returns how many successful logins the server answered.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issued
}

// NewServer starts a fake API serving routes. A route key is "METHOD /path"
// (e.g. "GET /api/users/me"). A route value is any JSON value, a
// json.RawMessage, or a Reply; the first two answer 200.
//
// A request without an accepted X-Api-Key header (APIKey, or the keys given to
// WithKeys) or a live session token from WithLogin gets 401, and an unknown route
// gets 404 with Planka's own error body. The server is closed when the test
// ends.
func NewServer(tb testing.TB, routes map[string]any, opts ...Option) *Server {
	tb.Helper()
	s := &Server{routes: routes, keys: map[string]bool{APIKey: true}, sessions: map[string]bool{}}
	for _, opt := range opts {
		opt(s)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.Close)
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Recorded{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Body:   body,
		APIKey: r.Header.Get("X-Api-Key"),
		Bearer: bearer(r),
	})
	s.mu.Unlock()

	if r.Method == http.MethodPost && r.URL.Path == loginPath {
		if _, overridden := s.routes[r.Method+" "+r.URL.Path]; !overridden {
			s.serveLogin(w, body)
			return
		}
	} else if !s.authenticated(r) {
		write(w, Reply{Status: http.StatusUnauthorized, Body: map[string]any{
			"code": "E_UNAUTHORIZED", "message": "Access token is missing, invalid or expired",
		}})
		return
	}
	if r.Method == http.MethodDelete && r.URL.Path == loginPath+"/me" && bearer(r) != "" {
		s.mu.Lock()
		delete(s.sessions, bearer(r))
		s.mu.Unlock()
		write(w, Reply{Status: http.StatusOK, Body: map[string]any{"item": bearer(r)}})
		return
	}
	resp, ok := s.routes[r.Method+" "+r.URL.Path]
	if !ok {
		write(w, Reply{Status: http.StatusNotFound, Body: map[string]any{
			"code": "E_NOT_FOUND", "message": "Not found",
		}})
		return
	}
	if byKey, isByKey := resp.(ByKey); isByKey {
		if resp, ok = byKey[r.Header.Get("X-Api-Key")]; !ok {
			write(w, Reply{Status: http.StatusNotFound, Body: map[string]any{
				"code": "E_NOT_FOUND", "message": "Not found",
			}})
			return
		}
	}
	if reply, isReply := resp.(Reply); isReply {
		write(w, reply)
		return
	}
	write(w, Reply{Status: http.StatusOK, Body: resp})
}

// bearer returns the token of an "Authorization: Bearer" header, or "".
func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

// authenticated reports whether r carries an accepted API key or a live session token.
func (s *Server) authenticated(r *http.Request) bool {
	if s.keys[r.Header.Get("X-Api-Key")] {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[bearer(r)]
}

// serveLogin answers Planka's login: a new session token for the WithLogin
// credentials, 401 for anything else.
func (s *Server) serveLogin(w http.ResponseWriter, body []byte) {
	var in struct {
		EmailOrUsername string `json:"emailOrUsername"`
		Password        string `json:"password"`
	}
	_ = json.Unmarshal(body, &in)
	if s.username == "" || in.EmailOrUsername != s.username || in.Password != s.password {
		write(w, Reply{Status: http.StatusUnauthorized, Body: map[string]any{
			"code": "E_UNAUTHORIZED", "message": "Invalid credentials",
		}})
		return
	}
	s.mu.Lock()
	s.issued++
	token := fmt.Sprintf("session-%d", s.issued)
	s.sessions[token] = true
	s.mu.Unlock()
	write(w, Reply{Status: http.StatusOK, Body: map[string]any{"item": token}})
}

func write(w http.ResponseWriter, reply Reply) {
	switch b := reply.Body.(type) {
	case nil:
		w.WriteHeader(reply.Status)
	case string:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(reply.Status)
		_, _ = io.WriteString(w, b)
	case json.RawMessage:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_, _ = w.Write(b)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_ = json.NewEncoder(w).Encode(b)
	}
}

// Requests returns every request the server received, oldest first.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

// Last returns the most recent request the server received, or the zero value.
func (s *Server) Last() Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return Recorded{}
	}
	return s.requests[len(s.requests)-1]
}

// Fixture returns internal/planka/testdata/<name>.json, a response captured
// from a real Planka. It fails the test if the file cannot be read.
func Fixture(tb testing.TB, name string) json.RawMessage {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatalf("plankatest: cannot locate the source file")
	}
	root, err := moduleRoot(filepath.Dir(file))
	if err != nil {
		tb.Fatalf("plankatest: %v", err)
	}
	path := filepath.Join(root, "internal", "planka", "testdata", name+".json")
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is built from a test-supplied fixture name under the module root
	if err != nil {
		tb.Fatalf("plankatest: fixture %q: %v", name, err)
	}
	return data
}

// moduleRoot walks up from dir to the directory containing go.mod.
func moduleRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
