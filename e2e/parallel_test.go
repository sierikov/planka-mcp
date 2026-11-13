//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParallelWritesOnOneBoard sends writes at once, as a client may: the
// server runs them concurrently, and each plans from the board it read, so
// without the per-board lock a new label or the Tasks checklist is created
// more than once.
func TestParallelWritesOnOneBoard(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Release 0.3")
	s := connectStdio(t, env.key)

	const n = 4
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			callFromGoroutine(t, s, "create_card", map[string]any{
				"board": b.ID, "title": fmt.Sprintf("Step %d", i), "labels": []string{"release"}, "create_labels": true,
			})
		})
		wg.Go(func() {
			callFromGoroutine(t, s, "add_tasks", map[string]any{
				"card": card, "items": []map[string]any{{"name": fmt.Sprintf("Task %d", i)}},
			})
		})
	}
	wg.Wait()

	board := callInto[struct {
		Labels []string `json:"labels"`
	}](t, s, "get_board", map[string]any{"board": b.ID})
	release := 0
	for _, l := range board.Labels {
		if l == "release" {
			release++
		}
	}
	assert.Equal(t, 1, release, "the label is created once: %v", board.Labels)

	got := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	require.Len(t, got.TaskLists, 1, "one Tasks checklist")
	assert.Len(t, got.TaskLists[0].Tasks, n)
}

// callFromGoroutine runs a tool that must succeed, from a goroutine the test
// started. It reports with assert, because require cannot stop the test from
// there.
func callFromGoroutine(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if assert.NoError(t, err, name) {
		assert.False(t, res.IsError, "%s: %v", name, res.Content)
	}
}
