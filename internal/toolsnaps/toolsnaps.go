// Package toolsnaps checks that tool definitions only change on purpose, like the package of the
// same name in github-mcp-server. A tool's name, description, schemas and annotations are what
// the model sees, so a change to them should show up in review.
package toolsnaps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// dir holds the snapshots, relative to the package of the calling test.
const dir = "__toolsnaps__"

// Test compares the JSON of a tool definition with its snapshot in __toolsnaps__/<name>.snap.
// With UPDATE_TOOLSNAPS=true it writes the snapshot instead. A missing snapshot is an error, so
// a new tool can't pass without its snapshot being committed.
func Test(name string, tool any) error {
	got, err := normalize(tool)
	if err != nil {
		return fmt.Errorf("failed to marshal tool %s: %w", name, err)
	}

	path := filepath.Join(dir, name+".snap")

	if os.Getenv("UPDATE_TOOLSNAPS") == "true" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}

		return os.WriteFile(path, got, 0o644) //nolint:gosec // snapshots are committed source files
	}

	snapshot, err := os.ReadFile(path) //nolint:gosec // the path is built from the tool name in tests
	if os.IsNotExist(err) {
		return fmt.Errorf("no snapshot for tool %s: run the tests with UPDATE_TOOLSNAPS=true and commit %s", name, path)
	}
	if err != nil {
		return err
	}

	want, err := normalize(json.RawMessage(snapshot))
	if err != nil {
		return fmt.Errorf("failed to parse snapshot %s: %w", path, err)
	}

	if !bytes.Equal(want, got) {
		return fmt.Errorf("tool %s changed; if that's intended, run the tests with UPDATE_TOOLSNAPS=true and commit %s\n--- snapshot\n%s+++ now\n%s",
			name, path, want, got)
	}

	return nil
}

// normalize marshals v as indented JSON with sorted keys, so the comparison and the snapshot
// files don't depend on field order.
func normalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(out, '\n'), nil
}
