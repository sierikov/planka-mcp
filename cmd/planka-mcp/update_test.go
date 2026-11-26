package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/update"
)

func TestNewCheckerIsOffForDevBuildsAndTheOptOut(t *testing.T) {
	_, ok := newChecker("dev", func(string) string { return "" })
	assert.False(t, ok, "a dev build never checks")

	_, ok = newChecker("0.4.0", func(k string) string {
		if k == update.OptOutEnv {
			return "1"
		}
		return ""
	})
	assert.False(t, ok, "the opt-out turns it off")
}

func TestUpdateInstructionsComeFromTheCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "update-check.json")
	data, err := json.Marshal(map[string]any{"checked_at": time.Now(), "latest": "9.9.9"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cache, data, 0o600))
	checker := &update.Checker{Current: "0.4.0", CachePath: cache, Now: time.Now}

	got := updateInstructions(checker)

	assert.Contains(t, got, "planka-mcp, the program behind the Planka tools, has a newer release: 9.9.9 (running 0.4.0)")
	assert.Empty(t, updateInstructions(nil))
	checker.Current = "9.9.9"
	assert.Empty(t, updateInstructions(checker), "nothing newer, no notice")
}
