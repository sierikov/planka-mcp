//go:build e2e

// Package e2e drives the real planka-mcp binary against a real Planka v2 (deploy/compose.yml).
//
// Run `make planka-up && make e2e`. Without a reachable Planka the tests skip, except when CI=true,
// where they fail. Set PLANKA_E2E_URL (and PLANKA_E2E_API_KEY, an admin's key) to use another
// instance; otherwise they run against http://localhost:3333, and scripts/planka-bootstrap.sh signs
// in as its default admin. The tests never read the server's own PLANKA_URL and PLANKA_API_KEY, so a
// shell that has them set for a real Planka cannot point the tests at it by accident.
//
// Every test builds its own uniquely named project and deletes it again, so tests never depend on
// the data the bootstrap script seeds or on each other.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	defaultURL = "http://localhost:3333"
	readyWait  = 60 * time.Second
	setupLimit = 2 * time.Minute // for the build and for the bootstrap script
)

// env is what TestMain found out, shared by all tests.
var env struct {
	url    string
	key    string // an admin's API key
	binary string // the planka-mcp under test
	skip   string // why the tests cannot run here, when they cannot
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// errUnreachable means no Planka answered within the wait. It is the only setup failure a local run
// may skip on; everything else (a failing build or bootstrap, a rejected key) fails the run.
var errUnreachable = errors.New("no Planka reachable")

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "planka-mcp-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	root, err := filepath.Abs("..")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	env.binary = filepath.Join(dir, "planka-mcp")
	if err := buildBinary(root, env.binary); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build:", err)
		return 1
	}
	if err := setupPlanka(root); err != nil {
		if errors.Is(err, errUnreachable) && os.Getenv("CI") != "true" {
			env.skip = err.Error()
			return m.Run()
		}
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	return m.Run()
}

func buildBinary(root, out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/planka-mcp") //nolint:gosec // a fixed command; out is a temp path made here
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// setupPlanka waits for Planka and settles on an admin API key that Planka accepts.
func setupPlanka(root string) error {
	env.url = strings.TrimRight(os.Getenv("PLANKA_E2E_URL"), "/")
	if env.url == "" {
		env.url = defaultURL
	}
	if !waitForPlanka(context.Background(), env.url, readyWait) {
		return fmt.Errorf("%w at %s after %s (Planka is not up); start it with `make planka-up`", errUnreachable, env.url, readyWait)
	}
	if env.key = os.Getenv("PLANKA_E2E_API_KEY"); env.key == "" {
		if err := bootstrap(root); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupLimit)
	defer cancel()
	p := &planka{base: env.url, key: env.key}
	if err := p.request(ctx, http.MethodGet, "/users/me", "", nil, nil); err != nil {
		return fmt.Errorf("the admin API key is not accepted: %w", err)
	}
	return nil
}

// bootstrap runs scripts/planka-bootstrap.sh and reads the two lines it prints.
func bootstrap(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupLimit)
	defer cancel()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "planka-bootstrap.sh")) //nolint:gosec // the repository's own script
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PLANKA_URL="+env.url)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scripts/planka-bootstrap.sh: %w", err)
	}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if v, ok := strings.CutPrefix(line, "PLANKA_API_KEY="); ok {
			env.key = strings.TrimSpace(v)
		}
	}
	if env.key == "" {
		return errors.New("scripts/planka-bootstrap.sh printed no PLANKA_API_KEY")
	}
	return nil
}
