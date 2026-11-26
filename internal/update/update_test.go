package update

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"0.3.0", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.3.0", false},
		{"0.10.0", "0.9.0", true},
		{"0.3.0", "dev", false},
		{"0.3.0-rc.1", "0.2.0", false},
		{"0.3.0", "0.2.1-0.20261101120000-abcdef123456+dirty", false},
		{"garbage", "0.2.0", false},
		{"", "0.2.0", false},
		{"v0.3.0", "0.2.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.latest+" vs "+tt.current, func(t *testing.T) {
			assert.Equal(t, tt.want, Newer(tt.latest, tt.current))
		})
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		current string
		env     map[string]string
		want    bool
	}{
		{"release without opt-out", "0.2.0", nil, true},
		{"release with opt-out", "0.2.0", map[string]string{OptOutEnv: "1"}, false},
		{"opt-out with any non-empty value", "0.2.0", map[string]string{OptOutEnv: "false"}, false},
		{"empty opt-out is not an opt-out", "0.2.0", map[string]string{OptOutEnv: ""}, true},
		{"dev build", "dev", nil, false},
		{"local build pseudo-version", "0.2.1-0.20261101120000-abcdef123456+dirty", nil, false},
		{"pre-release", "0.3.0-rc.1", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Enabled(tt.current, func(k string) string { return tt.env[k] })

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultCachePath(t *testing.T) {
	dir := t.TempDir()
	// os.UserCacheDir reads XDG_CACHE_HOME on Linux, $HOME/Library/Caches on
	// macOS and %LocalAppData% on Windows; point all of them at the temp dir.
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("LocalAppData", dir)
	want, err := os.UserCacheDir()
	require.NoError(t, err)

	got, err := DefaultCachePath()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(want, "planka-mcp", "update-check.json"), got)
}

// writeCache stores a cache file as the Checker would have left it.
func writeCache(t *testing.T, path string, checkedAt time.Time, latest string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"checked_at": checkedAt, "latest": latest})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// readCache decodes the cache file from disk.
func readCache(t *testing.T, path string) (checkedAt time.Time, latest string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is under t.TempDir()
	require.NoError(t, err)
	var got struct {
		CheckedAt time.Time `json:"checked_at"`
		Latest    string    `json:"latest"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	return got.CheckedAt, got.Latest
}

// newChecker returns a Checker with a fixed clock, a cache file in a temp
// dir and a Fetch that counts calls and answers with latest and err.
func newChecker(t *testing.T, latest string, err error) (checker *Checker, calls *int) {
	t.Helper()
	calls = new(int)
	return &Checker{
		Current:   "0.2.0",
		CachePath: filepath.Join(t.TempDir(), "planka-mcp", "update-check.json"),
		Now:       func() time.Time { return testNow },
		Fetch: func(context.Context) (string, error) {
			*calls++
			return latest, err
		},
	}, calls
}

func TestChecker_FreshCacheSkipsFetch(t *testing.T) {
	c, calls := newChecker(t, "9.9.9", nil)
	writeCache(t, c.CachePath, testNow.Add(-time.Hour), "0.3.0")

	require.NoError(t, c.Refresh(t.Context()))

	assert.Zero(t, *calls)
	latest, ok := c.Latest()
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", latest)
}

func TestChecker_StaleCacheFetchesAndStores(t *testing.T) {
	c, calls := newChecker(t, "0.3.0", nil)
	writeCache(t, c.CachePath, testNow.Add(-25*time.Hour), "0.2.0")

	require.NoError(t, c.Refresh(t.Context()))

	assert.Equal(t, 1, *calls)
	latest, ok := c.Latest()
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", latest)
	checkedAt, stored := readCache(t, c.CachePath)
	assert.True(t, testNow.Equal(checkedAt), "checked_at = %s, want %s", checkedAt, testNow)
	assert.Equal(t, "0.3.0", stored)
}

func TestChecker_NoCacheFetchesAndCreatesTheDirectory(t *testing.T) {
	c, calls := newChecker(t, "0.3.0", nil)

	require.NoError(t, c.Refresh(t.Context()))

	assert.Equal(t, 1, *calls)
	_, stored := readCache(t, c.CachePath)
	assert.Equal(t, "0.3.0", stored)
}

func TestChecker_FailedFetchRecordsTime(t *testing.T) {
	boom := errors.New("network down")
	c, calls := newChecker(t, "", boom)

	err := c.Refresh(t.Context())
	require.ErrorIs(t, err, boom)
	require.NoError(t, c.Refresh(t.Context()), "a second Refresh within 24h must not fetch again")

	assert.Equal(t, 1, *calls)
	latest, ok := c.Latest()
	assert.False(t, ok)
	assert.Empty(t, latest)
}

func TestChecker_CancelledRefreshIsNotRecorded(t *testing.T) {
	c, calls := newChecker(t, "0.3.0", nil)
	fetch := c.Fetch
	c.Fetch = func(ctx context.Context) (string, error) {
		if err := ctx.Err(); err != nil {
			*calls++
			return "", err
		}
		return fetch(ctx)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, c.Refresh(ctx), context.Canceled)
	assert.NoFileExists(t, c.CachePath, "a cancelled lookup must not count as a check")
	require.NoError(t, c.Refresh(t.Context()))

	assert.Equal(t, 2, *calls, "the second Refresh must fetch again")
	latest, ok := c.Latest()
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", latest)
}

func TestChecker_FailedFetchKeepsTheOldLatest(t *testing.T) {
	boom := errors.New("rate limited")
	c, calls := newChecker(t, "", boom)
	writeCache(t, c.CachePath, testNow.Add(-48*time.Hour), "0.3.0")

	require.ErrorIs(t, c.Refresh(t.Context()), boom)

	assert.Equal(t, 1, *calls)
	checkedAt, stored := readCache(t, c.CachePath)
	assert.True(t, testNow.Equal(checkedAt), "checked_at = %s, want %s", checkedAt, testNow)
	assert.Equal(t, "0.3.0", stored)
	latest, ok := c.Latest()
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", latest)
}

func TestChecker_CacheFromTheFutureIsStale(t *testing.T) {
	c, calls := newChecker(t, "0.3.0", nil)
	writeCache(t, c.CachePath, testNow.Add(72*time.Hour), "0.2.0")

	require.NoError(t, c.Refresh(t.Context()))

	assert.Equal(t, 1, *calls, "a clock that went backwards must not freeze the check")
}

func TestChecker_UnreadableCacheCountsAsMissing(t *testing.T) {
	c, calls := newChecker(t, "0.3.0", nil)
	require.NoError(t, os.MkdirAll(filepath.Dir(c.CachePath), 0o750))
	require.NoError(t, os.WriteFile(c.CachePath, []byte("{not json"), 0o600))

	_, ok := c.Latest()
	assert.False(t, ok)
	require.NoError(t, c.Refresh(t.Context()))

	assert.Equal(t, 1, *calls)
	latest, ok := c.Latest()
	assert.True(t, ok)
	assert.Equal(t, "0.3.0", latest)
}

func TestChecker_LatestWithoutCache(t *testing.T) {
	c, _ := newChecker(t, "", nil)

	latest, ok := c.Latest()

	assert.False(t, ok)
	assert.Empty(t, latest)
}

func TestChecker_RefreshPassesTheContextToFetch(t *testing.T) {
	c, _ := newChecker(t, "", nil)
	type ctxKey struct{}
	var got any
	c.Fetch = func(ctx context.Context) (string, error) {
		got = ctx.Value(ctxKey{})
		return "0.3.0", nil
	}

	require.NoError(t, c.Refresh(context.WithValue(t.Context(), ctxKey{}, "marker")))

	assert.Equal(t, "marker", got)
}

func TestChecker_CacheIsReadableAndCarriesNoTempFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	c, _ := newChecker(t, "0.3.0", nil)

	require.NoError(t, c.Refresh(t.Context()))

	fi, err := os.Stat(c.CachePath)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o644), fi.Mode().Perm())
	di, err := os.Stat(filepath.Dir(c.CachePath))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o755), di.Mode().Perm())
	entries, err := os.ReadDir(filepath.Dir(c.CachePath))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "update-check.json", entries[0].Name())
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/sierikov/planka-mcp/releases/latest", r.URL.Path)
		assert.Equal(t, "planka-mcp/0.2.0", r.Header.Get("User-Agent"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0","name":"0.3.0"}`))
	}))
	t.Cleanup(srv.Close)

	got, err := FetchLatest(t.Context(), srv.Client(), srv.URL+"/repos/sierikov/planka-mcp/releases/latest", "0.2.0")

	require.NoError(t, err)
	assert.Equal(t, "0.3.0", got)
}

func TestFetchLatest_Failures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			"rate limited",
			func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "rate limit exceeded", http.StatusForbidden)
			},
			"403",
		},
		{
			"not found",
			func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
			"404",
		},
		{
			"not json",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
			"decoding",
		},
		{
			"no tag",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"name":"x"}`)) },
			"tag_name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			t.Cleanup(srv.Close)

			got, err := FetchLatest(t.Context(), srv.Client(), srv.URL, "0.2.0")

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, got)
		})
	}
}

func TestFetchLatest_HonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0"}`))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := FetchLatest(ctx, srv.Client(), srv.URL, "0.2.0")

	require.ErrorIs(t, err, context.Canceled)
}

func TestAgentNotice(t *testing.T) {
	assert.Equal(t,
		"planka-mcp, the program behind the Planka tools, has a newer release: 0.3.0 (running 0.2.0). "+
			"The first time you use one of these tools in this conversation, end that reply with one sentence telling the user "+
			"how to upgrade (go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest) and to restart the app afterwards. Don't run the upgrade yourself.",
		AgentNotice("0.3.0", "0.2.0", MethodGo))
}

func TestCLINotice(t *testing.T) {
	assert.Equal(t,
		"planka-mcp 0.3.0 is available (you have 0.2.0). Upgrade: go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest",
		CLINotice("0.3.0", "0.2.0", MethodGo))
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"0.4.0": true, "1.10.2": true,
		"dev": false, "v0.4.0": false, "0.4.0-rc.1": false, "0.4": false, "": false,
		"0.4.1-0.20261101120000-abcdef123456+dirty": false,
	} {
		assert.Equal(t, want, IsRelease(v), v)
	}
}
