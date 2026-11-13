//go:build e2e

package e2e

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIExitCodes runs the built binary without any PLANKA_* settings: help
// and version need none and exit 0, and a bad flag or argument exits 2 with an
// error that points at the help.
func TestCLIExitCodes(t *testing.T) {
	if env.binary == "" {
		t.Skip(env.skip)
	}
	for _, tc := range []struct {
		args       []string
		code       int
		out, error string
	}{
		{args: []string{"help"}, out: "Usage:"},
		{args: []string{"-h"}, out: "Usage:"},
		{args: []string{"-url", "http://p", "-h"}, out: "Usage:"},
		{args: []string{"--version"}, out: "\n"},
		{args: []string{"-nope"}, code: 2, error: "see planka-mcp help"},
		{args: []string{"serve"}, code: 2, error: `unexpected argument \"serve\"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), env.binary, tc.args...) //nolint:gosec // the binary TestMain built
			cmd.Env = []string{}
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			err := cmd.Run()

			code := 0
			if exit, ok := errors.AsType[*exec.ExitError](err); ok {
				code = exit.ExitCode()
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.code, code, "stderr: %s", stderr.String())
			assert.Contains(t, stdout.String(), tc.out)
			assert.Contains(t, stderr.String(), tc.error)
		})
	}
}
