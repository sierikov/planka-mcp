package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sierikov/planka-mcp/internal/update"
)

// updateFetchTimeout bounds the request that looks up the latest release.
const updateFetchTimeout = 5 * time.Second

// newChecker returns the update checker for the running version, with getenv
// as the environment. It is false when the check is off: a build that is not
// a release, an opt-out through the environment, or no cache directory to
// keep the result in. Only stdio mode checks: the users of a shared HTTP
// server cannot upgrade it.
func newChecker(version string, getenv func(string) string) (*update.Checker, bool) {
	if !update.Enabled(version, getenv) {
		return nil, false
	}
	cachePath, err := update.DefaultCachePath()
	if err != nil {
		return nil, false
	}
	return &update.Checker{
		Current:   version,
		CachePath: cachePath,
		Now:       time.Now,
		Fetch: func(ctx context.Context) (string, error) {
			client := &http.Client{Timeout: updateFetchTimeout}
			return update.FetchLatest(ctx, client, update.ReleasesURL, version)
		},
	}, true
}

// updateInstructions is the server-instructions sentence that asks the agent
// to tell the user about a newer release, or "" when there is none or checker
// is nil. It reads only the cache an earlier start refreshed, so it never
// waits on the network.
func updateInstructions(checker *update.Checker) string {
	if checker == nil {
		return ""
	}
	if latest, method, ok := pendingUpdate(checker); ok {
		return update.AgentNotice(latest, checker.Current, method)
	}
	return ""
}

// reportUpdate prints the command-line notice on out when a newer release
// exists. It refreshes the cache first if it is older than a day; a failed
// lookup only means the cache answers.
func reportUpdate(ctx context.Context, out io.Writer, checker *update.Checker) {
	_ = checker.Refresh(ctx)
	if latest, method, ok := pendingUpdate(checker); ok {
		_, _ = fmt.Fprintln(out, update.CLINotice(latest, checker.Current, method))
	}
}

// pendingUpdate returns the newer release the cache records and how this
// binary was installed, which decides the upgrade command. It is false when
// nothing newer is recorded or the binary cannot be located.
func pendingUpdate(checker *update.Checker) (latest string, method update.Method, ok bool) {
	latest, ok = checker.Latest()
	if !ok || !update.Newer(latest, checker.Current) {
		return "", "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", "", false
	}
	home, _ := os.UserHomeDir() // without a home directory, GOBIN or GOPATH may still say
	return latest, update.DetectMethod(exe, update.GoBin(os.Getenv, home)), true
}
