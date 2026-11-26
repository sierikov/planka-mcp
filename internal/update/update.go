// Package update checks GitHub for a newer release of planka-mcp and
// words the notice that tells the user how to upgrade. It never installs
// anything.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	// OptOutEnv switches the update check off when set to any non-empty value.
	OptOutEnv = "PLANKA_MCP_NO_UPDATE_CHECK"
	// ReleasesURL is the GitHub API endpoint that describes the latest release.
	ReleasesURL = "https://api.github.com/repos/sierikov/planka-mcp/releases/latest"

	// checkInterval is how long a check result, successful or not, is reused.
	checkInterval = 24 * time.Hour

	appDir        = "planka-mcp"
	cacheFileName = "update-check.json"

	// The cache holds no secret, so it is readable by others.
	cacheDirMode  = 0o755
	cacheFileMode = 0o644

	// maxResponseBytes caps how much of the GitHub response is read.
	maxResponseBytes = 1 << 20
)

// IsRelease reports whether v is a plain release version such as "0.3.0":
// not "dev", a pseudo-version, a pre-release, or one with a leading "v".
func IsRelease(v string) bool {
	if strings.HasPrefix(v, "v") {
		return false
	}
	sv := "v" + v
	return semver.IsValid(sv) && semver.Prerelease(sv) == "" && semver.Build(sv) == "" && semver.Canonical(sv) == sv
}

// Newer reports whether latest is a later release than current. Both must be
// plain releases (see IsRelease): a dev build, a pseudo-version or a
// pre-release is never compared.
func Newer(latest, current string) bool {
	if !IsRelease(latest) || !IsRelease(current) {
		return false
	}
	return semver.Compare("v"+latest, "v"+current) > 0
}

// Enabled reports whether the update check should run: only a release build
// checks, and only when OptOutEnv is unset or empty.
func Enabled(current string, getenv func(string) string) bool {
	return IsRelease(current) && getenv(OptOutEnv) == ""
}

// AgentNotice is the text for the MCP server instructions: it asks the agent
// to pass the news of release latest on to the user, once a tool of this
// server is in use.
func AgentNotice(latest, current string, m Method) string {
	return fmt.Sprintf("planka-mcp, the program behind the Planka tools, has a newer release: %s (running %s). "+
		"The first time you use one of these tools in this conversation, end that reply with one sentence telling the user "+
		"how to upgrade (%s) and to restart the app afterwards. Don't run the upgrade yourself.",
		latest, current, UpgradeCommand(m))
}

// CLINotice is the line the command-line interface prints to the user.
func CLINotice(latest, current string, m Method) string {
	return fmt.Sprintf("planka-mcp %s is available (you have %s). Upgrade: %s",
		latest, current, UpgradeCommand(m))
}

// Checker looks up the latest release at most once a day and remembers the
// answer in a cache file, so that starting the server never waits on the
// network: Latest reads the cache, Refresh updates it.
type Checker struct {
	// Current is the version of the running binary, without a leading "v".
	Current string
	// CachePath is the cache file; see DefaultCachePath.
	CachePath string
	// Now returns the current time.
	Now func() time.Time
	// Fetch returns the latest release version, without a leading "v".
	Fetch func(ctx context.Context) (string, error)
}

// cacheState is the content of the cache file.
type cacheState struct {
	// CheckedAt is when Fetch last ran, whether or not it succeeded.
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the latest release Fetch last returned, "" when it never has.
	Latest string `json:"latest"`
}

// DefaultCachePath returns the location of the cache file under the user
// cache directory.
func DefaultCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDir, cacheFileName), nil
}

// Latest returns the latest release recorded by the last Refresh. The second
// result is false when no release is recorded or the cache cannot be read.
func (c *Checker) Latest() (string, bool) {
	st, ok := c.load()
	if !ok || st.Latest == "" {
		return "", false
	}
	return st.Latest, true
}

// Refresh asks Fetch for the latest release and stores the answer, unless the
// cache is less than 24 hours old. A failed Fetch is recorded too, with the
// release that was known before, so that an offline machine or a rate limit
// does not bring a request on every start; Refresh then returns the error. A
// Fetch that failed because ctx was cancelled or timed out is not recorded:
// the lookup was cut short, so the next Refresh tries again.
func (c *Checker) Refresh(ctx context.Context) error {
	st, _ := c.load()
	now := c.Now()
	if age := now.Sub(st.CheckedAt); age >= 0 && age < checkInterval {
		return nil
	}

	latest, fetchErr := c.Fetch(ctx)
	if fetchErr != nil && ctx.Err() != nil {
		return fetchErr
	}
	st.CheckedAt = now
	if fetchErr == nil {
		st.Latest = latest
	}
	if saveErr := c.save(st); saveErr != nil {
		return errors.Join(fetchErr, saveErr)
	}
	return fetchErr
}

// load reads the cache file. A missing or unreadable file gives the zero
// state, whose CheckedAt is far in the past, and false.
func (c *Checker) load() (cacheState, bool) {
	data, err := os.ReadFile(filepath.Clean(c.CachePath))
	if err != nil {
		return cacheState{}, false
	}
	var st cacheState
	if err := json.Unmarshal(data, &st); err != nil {
		return cacheState{}, false
	}
	return st, true
}

// save writes the cache file, creating its directory when needed. The file is
// replaced atomically, so a second server starting at the same time never
// reads half of it.
func (c *Checker) save(st cacheState) error {
	dir := filepath.Dir(c.CachePath)
	if err := os.MkdirAll(dir, cacheDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encoding update check: %w", err)
	}
	data = append(data, '\n')
	if err := writeAtomic(c.CachePath, data, cacheFileMode); err != nil {
		return fmt.Errorf("writing %s: %w", c.CachePath, err)
	}
	return nil
}

// writeAtomic writes data to a temporary file next to path, then renames it
// over path, so a reader sees the old file or the new one, never half.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// FetchLatest asks the GitHub releases API at url for the latest release and
// returns its version without the leading "v". current is the running
// version, sent in the User-Agent header. The caller sets the timeout on
// client.
func FetchLatest(ctx context.Context, client *http.Client, url, current string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("building the release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "planka-mcp/"+current)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching the latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching the latest release: unexpected status %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding the latest release: %w", err)
	}
	if release.TagName == "" {
		return "", errors.New("the latest release has no tag_name")
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}
