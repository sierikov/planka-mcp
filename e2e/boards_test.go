//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBoardTools creates a board with its lists in a project, renames it, and
// deletes it: refused while it has a card, then with with_cards.
func TestBoardTools(t *testing.T) {
	p := requirePlanka(t)
	id := unique(t)
	project := "e2e-" + id
	p.newProject(t, project)
	s := connectStdio(t, env.key)

	created := callInto[struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Project string `json:"project"`
		Lists   []struct {
			Name   string `json:"name"`
			Closed bool   `json:"closed"`
		} `json:"lists"`
	}](t, s, "create_board", map[string]any{
		"project": project, "name": "Roadmap", "lists": []string{"Todo", "Doing"}, "closed_list": "Done",
	})
	assert.Equal(t, project, created.Project)
	require.Len(t, created.Lists, 3)
	assert.Equal(t, "Todo", created.Lists[0].Name)
	assert.Equal(t, "Done", created.Lists[2].Name)
	assert.True(t, created.Lists[2].Closed)

	text, isError := call(t, s, "create_board", map[string]any{"project": project, "name": "roadmap"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `already has a board "Roadmap"`)

	renamed := callInto[struct{ Name string }](t, s, "update_board", map[string]any{"board": created.ID, "name": "Roadmap 2027"})
	assert.Equal(t, "Roadmap 2027", renamed.Name)

	callInto[writeResult](t, s, "create_card", map[string]any{"board": created.ID, "title": "Plan Q1"})
	text, isError = call(t, s, "delete_board", map[string]any{"board": "Roadmap 2027"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `board "Roadmap 2027" has 1 cards`)

	deleted := callInto[struct {
		Deleted    string `json:"deleted"`
		CardsTotal int    `json:"cards_total"`
	}](t, s, "delete_board", map[string]any{"board": created.ID, "with_cards": true})
	assert.Equal(t, created.ID, deleted.Deleted)
	assert.Equal(t, 1, deleted.CardsTotal)
	err := p.request(t.Context(), http.MethodGet, "/boards/"+created.ID, "", nil, nil)
	assert.ErrorContains(t, err, "404", "the board is gone")
}

// TestBoardMemberTools adds a user to a board as a viewer who may comment,
// makes them an editor and removes them again; a board member who is not a
// project manager cannot remove someone else.
func TestBoardMemberTools(t *testing.T) {
	p, b := newFlowBoard(t)
	id := unique(t)
	_, userKey := p.newUser(t, id)
	username := "e2e" + id
	s := connectStdio(t, env.key)

	added := callInto[struct {
		Name       string `json:"name"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		CanComment bool   `json:"can_comment"`
	}](t, s, "add_board_member", map[string]any{"board": b.ID, "person": username, "role": "viewer", "can_comment": true})
	assert.Equal(t, username, added.Username)
	assert.Equal(t, "viewer", added.Role)
	assert.True(t, added.CanComment)

	text, isError := call(t, s, "add_board_member", map[string]any{"board": b.ID, "person": "me", "role": "editor"})
	assert.True(t, isError, text)
	assert.Contains(t, text, "is already on board")

	// The new member can see the board, but only a project manager may change who else is on it.
	user := connectStdio(t, userKey)
	text, isError = call(t, user, "remove_board_member", map[string]any{"board": b.ID, "person": p.meName(t)})
	assert.True(t, isError, text)
	assert.Contains(t, text, "only a project manager")

	updated := callInto[struct{ Role string }](t, s, "update_board_member", map[string]any{"board": b.ID, "person": "E2E " + id, "role": "editor"})
	assert.Equal(t, "editor", updated.Role)

	removed := callInto[struct {
		Removed string `json:"removed"`
		Role    string `json:"role"`
	}](t, s, "remove_board_member", map[string]any{"board": b.ID, "person": username})
	assert.Equal(t, "E2E "+id, removed.Removed)
	assert.Equal(t, "editor", removed.Role)
}
