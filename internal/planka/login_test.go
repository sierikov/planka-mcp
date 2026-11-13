package planka

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

// loginServer is a fake Planka that accepts the login ann/pw and serves the current user.
func loginServer(t *testing.T, routes map[string]any) *plankatest.Server {
	t.Helper()
	if routes == nil {
		routes = map[string]any{}
	}
	routes["GET /api/users/me"] = plankatest.Fixture(t, "users_me")
	return plankatest.NewServer(t, routes, plankatest.WithLogin("ann", "pw"))
}

func paths(reqs []plankatest.Recorded) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Method + " " + r.Path
	}
	return out
}

func TestLoginOnFirstCallThenReusesTheSession(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	_, err := c.Me(t.Context())
	require.NoError(t, err)
	_, err = c.Me(t.Context())
	require.NoError(t, err)

	reqs := srv.Requests()
	assert.Equal(t, []string{"POST /api/access-tokens", "GET /api/users/me", "GET /api/users/me"}, paths(reqs))
	assert.JSONEq(t, `{"emailOrUsername":"ann","password":"pw"}`, string(reqs[0].Body))
	assert.Empty(t, reqs[0].Bearer, "the login itself carries no token")
	for _, r := range reqs[1:] {
		assert.Equal(t, "session-1", r.Bearer)
		assert.Empty(t, r.APIKey, "a login never sends X-Api-Key")
	}
}

func TestExpiredSessionLogsInAgainAndRetriesOnce(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)

	srv.ExpireSessions()
	_, err = c.Me(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []string{
		"POST /api/access-tokens", "GET /api/users/me",
		"GET /api/users/me", "POST /api/access-tokens", "GET /api/users/me",
	}, paths(srv.Requests()))
	assert.Equal(t, "session-2", srv.Last().Bearer)
}

func TestWritesAreRetriedWithTheirBodyAfterRelogin(t *testing.T) {
	srv := loginServer(t, map[string]any{"POST /api/cards/1/comments": plankatest.Fixture(t, "comment_created")})
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)
	srv.ExpireSessions()

	_, err = c.CreateComment(t.Context(), "1", "hello")

	require.NoError(t, err)
	last := srv.Last()
	assert.Equal(t, "POST /api/cards/1/comments", last.Method+" "+last.Path)
	assert.JSONEq(t, `{"text":"hello"}`, string(last.Body))
	assert.Equal(t, "session-2", last.Bearer)
}

func TestA401AfterAFreshLoginIsNotRetriedAgain(t *testing.T) {
	srv := loginServer(t, map[string]any{
		"GET /api/boards/1": plankatest.Reply{Status: http.StatusUnauthorized, Body: map[string]any{"code": "E_UNAUTHORIZED"}},
	})
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	_, err := c.Board(t.Context(), "1")

	var loginErr *LoginError
	require.ErrorAs(t, err, &loginErr, "a password user must not be told to check an API key")
	assert.Contains(t, err.Error(), "rejected its session token")
	assert.Contains(t, err.Error(), "Authorization")
	assert.Equal(t, []string{
		"POST /api/access-tokens", "GET /api/boards/1", "POST /api/access-tokens", "GET /api/boards/1",
	}, paths(srv.Requests()))
}

func TestConcurrentCallsShareOneLogin(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Go(func() { _, errs[i] = c.Me(t.Context()) })
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, srv.Logins())
}

func TestConcurrent401sLogInAgainOnlyOnce(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)
	srv.ExpireSessions()

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Go(func() { _, errs[i] = c.Me(t.Context()) })
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 2, srv.Logins(), "callers that saw the same expired token share one new login")
}

func TestWrongPasswordIsNotTriedAgainOnEveryCall(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "wrong"))

	for range 3 {
		_, err := c.Me(t.Context())
		var loginErr *LoginError
		require.ErrorAs(t, err, &loginErr)
	}

	assert.Len(t, srv.Requests(), 1, "a refused password is not sent to Planka again: repeated failures can lock the account")
}

func TestTermsRefusalIsTriedAgainLater(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"POST /api/access-tokens": plankatest.Reply{Status: http.StatusForbidden, Body: map[string]any{"message": "Terms acceptance required"}},
	})
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	_, err := c.Me(t.Context())
	require.Error(t, err)
	_, err = c.Me(t.Context())
	require.Error(t, err)

	assert.Len(t, srv.Requests(), 2, "accepting the terms in the browser fixes this while the server runs")
}

func TestLoginRefusals(t *testing.T) {
	forbidden := func(message string) plankatest.Reply {
		return plankatest.Reply{Status: http.StatusForbidden, Body: map[string]any{"code": "E_FORBIDDEN", "message": message}}
	}
	tests := map[string]struct {
		reply plankatest.Reply
		want  string
	}{
		"wrong credentials": {
			reply: plankatest.Reply{Status: http.StatusUnauthorized, Body: map[string]any{"code": "E_UNAUTHORIZED", "message": "Invalid credentials"}},
			want:  "Planka rejected the username or password: check PLANKA_USERNAME and PLANKA_PASSWORD",
		},
		"single sign-on account": {
			reply: forbidden("Use single sign-on"),
			want:  "this Planka account signs in with single sign-on, so it has no password: ask a Planka admin for an API key (PLANKA_API_KEY)",
		},
		"terms not accepted": {
			reply: forbidden("Terms acceptance required"),
			want:  "Planka wants this account to accept its terms first: log in once in the browser, then try again",
		},
		"two-factor account": {
			reply: forbidden("TOTP verification required"),
			want:  "this Planka account uses two-factor authentication, which password login cannot complete: ask a Planka admin for an API key (PLANKA_API_KEY)",
		},
		"instance not set up": {
			reply: forbidden("Admin login required to initialize instance"),
			want:  "this Planka instance is not set up yet: an admin has to log in once in the browser first",
		},
		"rate limited": {
			reply: plankatest.Reply{Status: http.StatusConflict, Body: map[string]any{"code": "E_CONFLICT", "message": "Rate limit exceeded"}},
			want:  "Planka is limiting login attempts for this account: wait a little, then try again",
		},
		"unknown refusal": {
			reply: forbidden("Something new"),
			want:  "Planka refused the login (HTTP 403): Something new",
		},
		"server error": {
			reply: plankatest.Reply{Status: http.StatusInternalServerError, Body: json.RawMessage(`{"code":"E_INTERNAL","message":"boom"}`)},
			want:  "Planka refused the login (HTTP 500): boom",
		},
		"non-JSON answer": {
			reply: plankatest.Reply{Status: http.StatusBadGateway, Body: "<html>bad gateway</html>"},
			want:  "Planka refused the login (HTTP 502)",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := plankatest.NewServer(t, map[string]any{"POST /api/access-tokens": tc.reply})
			c := NewClient(srv.URL, WithLogin("ann", "s3cret"))

			_, err := c.Me(t.Context())

			var loginErr *LoginError
			require.ErrorAs(t, err, &loginErr)
			require.EqualError(t, err, tc.want)
			assert.NotContains(t, err.Error(), "s3cret")
			var coder interface{ StatusCode() int }
			assert.NotErrorAs(t, err, &coder, "a login refusal must not look like a rejected API key")
			assert.Len(t, srv.Requests(), 1, "no API call is attempted without a session")
		})
	}
}

func TestLoginRefusesRedirects(t *testing.T) {
	target := plankatest.NewServer(t, nil, plankatest.WithLogin("ann", "pw"))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect) //nolint:gosec // G710: the test's own redirect to its own fake server
	}))
	t.Cleanup(origin.Close)
	c := NewClient(origin.URL, WithLogin("ann", "pw"))

	_, err := c.Me(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "set PLANKA_URL to "+target.URL)
	assert.Empty(t, target.Requests(), "the password never reaches the redirect target")
}

func TestUnreadableLoginAnswerIsNotEchoed(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"POST /api/access-tokens": plankatest.Reply{Status: http.StatusOK, Body: json.RawMessage(`{"token":"secret-session-token"}`)},
	})
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	_, err := c.Me(t.Context())

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-session-token")
}

func TestContextKeyWinsOverLogin(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))

	_, err := c.Me(WithAPIKey(t.Context(), plankatest.APIKey))

	require.NoError(t, err)
	assert.Equal(t, 0, srv.Logins())
	assert.Equal(t, plankatest.APIKey, srv.Last().APIKey)
}

func TestLogoutEndsTheSession(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)

	require.NoError(t, c.Logout(t.Context()))
	require.NoError(t, c.Logout(t.Context()), "a second logout has nothing to do")

	reqs := srv.Requests()
	assert.Equal(t, []string{"POST /api/access-tokens", "GET /api/users/me", "DELETE /api/access-tokens/me"}, paths(reqs))
	assert.Equal(t, "session-1", reqs[2].Bearer)
}

func TestLogoutWithoutALoginSendsNothing(t *testing.T) {
	srv := loginServer(t, nil)

	require.NoError(t, NewClient(srv.URL, WithLogin("ann", "pw")).Logout(t.Context()))
	require.NoError(t, NewClient(srv.URL, WithStaticKey(plankatest.APIKey)).Logout(t.Context()))

	assert.Empty(t, srv.Requests())
}

func TestLogoutOfAnExpiredSessionIsNotAnError(t *testing.T) {
	srv := loginServer(t, nil)
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)
	srv.ExpireSessions()

	assert.NoError(t, c.Logout(t.Context()))
}
