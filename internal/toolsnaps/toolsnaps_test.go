package toolsnaps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSnapshot(t *testing.T, name, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".snap"), []byte(content), 0o600))
}

func TestUpdateWritesASortedSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("UPDATE_TOOLSNAPS", "true")

	require.NoError(t, Test("echo", map[string]any{"name": "echo", "annotations": map[string]any{"readOnlyHint": true}}))

	got, err := os.ReadFile(filepath.Join("__toolsnaps__", "echo.snap"))
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"annotations\": {\n    \"readOnlyHint\": true\n  },\n  \"name\": \"echo\"\n}\n", string(got)) //nolint:testifylint // the exact text, indentation included, is what is under test
}

func TestMatchingSnapshotPasses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSnapshot(t, "echo", `{"name": "echo", "annotations": {"readOnlyHint": true}}`)

	require.NoError(t, Test("echo", map[string]any{"annotations": map[string]any{"readOnlyHint": true}, "name": "echo"}))
}

func TestChangedToolFails(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSnapshot(t, "echo", `{"name": "echo", "description": "old"}`)

	err := Test("echo", map[string]any{"name": "echo", "description": "new"})

	require.ErrorContains(t, err, "tool echo changed")
	require.ErrorContains(t, err, "UPDATE_TOOLSNAPS=true")
}

func TestMissingSnapshotFails(t *testing.T) {
	t.Chdir(t.TempDir())

	err := Test("echo", map[string]any{"name": "echo"})

	require.ErrorContains(t, err, "no snapshot for tool echo")
}
