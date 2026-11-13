package planka

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

func TestDoSendsContextKeyAsHeader(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{"GET /api/users/me": map[string]any{"item": 1}})
	c := NewClient(srv.URL, WithStaticKey("static"))

	err := c.do(WithAPIKey(t.Context(), plankatest.APIKey), http.MethodGet, "/api/users/me", nil, nil, nil)

	// The server only accepts plankatest.APIKey, so success proves the ctx key won.
	require.NoError(t, err)
	assert.Equal(t, plankatest.APIKey, srv.Last().APIKey)
}

func TestDoPrefersContextKeyOverStaticKey(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{})
	c := NewClient(srv.URL, WithStaticKey("static"))

	_ = c.do(WithAPIKey(t.Context(), "ctx-key"), http.MethodGet, "/x", nil, nil, nil)

	assert.Equal(t, "ctx-key", srv.Last().APIKey)
}

func TestDoFallsBackToStaticKey(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{"GET /api/users/me": map[string]any{"item": 1}})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	err := c.do(t.Context(), http.MethodGet, "/api/users/me", nil, nil, nil)

	require.NoError(t, err)
	assert.Equal(t, plankatest.APIKey, srv.Last().APIKey)
}

func TestDoWithoutKeyFailsBeforeRequest(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{})
	c := NewClient(srv.URL)

	err := c.do(t.Context(), http.MethodGet, "/api/users/me", nil, nil, nil)

	require.ErrorIs(t, err, ErrNoAPIKey)
	assert.Empty(t, srv.Requests())
}

func TestDoMapsNon2xxToAPIError(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"GET /api/boards/b1": plankatest.Reply{Status: http.StatusForbidden, Body: map[string]any{"code": "E_FORBIDDEN"}},
	})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	err := c.do(t.Context(), http.MethodGet, "/api/boards/b1", nil, nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Status)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode())
	assert.Contains(t, err.Error(), "planka API 403: ")
	assert.Contains(t, apiErr.Body, "E_FORBIDDEN")
}

func TestDoMapsUnknownRouteToNotFound(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	err := c.do(t.Context(), http.MethodGet, "/api/nope", nil, nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Status)
	assert.Contains(t, apiErr.Body, `"code":"E_NOT_FOUND"`)
}

func TestDoWrongKeyIsUnauthorized(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{"GET /x": map[string]any{}})
	c := NewClient(srv.URL, WithStaticKey("wrong"))

	err := c.do(t.Context(), http.MethodGet, "/x", nil, nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Status)
}

func TestDoTruncatesNonJSONBody(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"GET /api/x": plankatest.Reply{Status: http.StatusBadGateway, Body: "<html>" + strings.Repeat("a", 1000) + "</html>"},
	})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	err := c.do(t.Context(), http.MethodGet, "/api/x", nil, nil, nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.Status)
	assert.LessOrEqual(t, len(apiErr.Body), 200)
}

func TestDoDecodesAnyTwoXXAndSendsBody(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"POST /api/cards": plankatest.Reply{Status: http.StatusCreated, Body: map[string]any{"item": map[string]any{"id": "c1"}}},
	})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	var out struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	err := c.do(t.Context(), http.MethodPost, "/api/cards", url.Values{"q": {"a b"}}, map[string]string{"name": "x"}, &out)

	require.NoError(t, err)
	assert.Equal(t, "c1", out.Item.ID)
	last := srv.Last()
	assert.Equal(t, http.MethodPost, last.Method)
	assert.Equal(t, "/api/cards", last.Path)
	assert.Equal(t, "a b", last.Query.Get("q"))
	assert.JSONEq(t, `{"name":"x"}`, string(last.Body))
}

func TestDoRawMessageRouteIsServedVerbatim(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{"GET /api/users/me": plankatest.Fixture(t, "users_me")})
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	var out map[string]any
	require.NoError(t, c.do(t.Context(), http.MethodGet, "/api/users/me", nil, nil, &out))
	assert.Contains(t, out, "item")
}

func TestPathParamEscapes(t *testing.T) {
	assert.Equal(t, "..%2Fx", pathParam("../x"))
}

// redirector answers every request with the given redirect status to the same path on target.
func redirector(t *testing.T, status int, target string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target+r.URL.Path)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDoRefusesRedirects: following a 301 turns a write into a GET that looks like success, and
// any redirect would carry X-Api-Key to the host it names. The client follows none of them.
func TestDoRefusesRedirects(t *testing.T) {
	// The target would answer the GET a followed redirect becomes, as a proxy in front of Planka would.
	target := plankatest.NewServer(t, map[string]any{"GET /api/cards/1": map[string]any{"item": map[string]any{"id": "1"}}})

	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		front := redirector(t, status, target.URL)
		for name, h := range map[string]*http.Client{
			"default client": nil,
			"caller client":  {Timeout: time.Second},
		} {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
				t.Run(fmt.Sprintf("%d %s %s", status, name, method), func(t *testing.T) {
					opts := []Option{WithStaticKey(plankatest.APIKey)}
					if h != nil {
						opts = append(opts, WithHTTPClient(h))
					}
					c := NewClient(front.URL, opts...)

					err := c.do(t.Context(), method, "/api/cards/1", nil, map[string]string{"text": "x"}, nil)

					require.Error(t, err)
					assert.Contains(t, err.Error(), fmt.Sprintf("planka redirected to %s/api/cards/1 (HTTP %d)", target.URL, status))
					assert.Contains(t, err.Error(), "set PLANKA_URL to "+target.URL)
				})
			}
		}
	}
	// Not one request reached the redirect target, so it never saw the key either.
	assert.Empty(t, target.Requests())
}

func TestWithHTTPClientIsNotMutated(t *testing.T) {
	h := &http.Client{Timeout: time.Second}

	_ = NewClient("http://planka.invalid", WithHTTPClient(h))

	assert.Nil(t, h.CheckRedirect, "the caller's client keeps its own redirect policy")
}

func TestRedirectWithoutLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, WithStaticKey(plankatest.APIKey))

	err := c.do(t.Context(), http.MethodGet, "/api/users/me", nil, nil, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect")
	assert.Contains(t, err.Error(), "PLANKA_URL")
}
