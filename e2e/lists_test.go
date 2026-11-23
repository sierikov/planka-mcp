//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListTools runs a board's lists through create_list, update_list and
// delete_list on a real Planka, reading the result back with get_board and
// get_card each time.
func TestListTools(t *testing.T) {
	p, b := newFlowBoard(t) // lists Open, In progress, Done
	s := connectStdio(t, env.key)
	columns := func() boardDetail {
		return callInto[boardDetail](t, s, "get_board", map[string]any{"board": b.ID})
	}

	created := callInto[listResult](t, s, "create_list", map[string]any{
		"board": b.ID, "name": "Review", "after": "In progress", "color": "lagoon-blue",
	})
	assert.Equal(t, []string{"Open", "In progress", "Review", "Done"}, created.Lists)
	assert.Equal(t, "lagoon-blue", created.List.Color)
	got := columns()
	require.Len(t, got.Lists, 4)
	assert.Equal(t, "Review", got.Lists[2].Name)
	assert.Equal(t, "lagoon-blue", got.Lists[2].Color, "the colour is stored")

	// Two cards with due dates in the wrong order, then rename, close and sort the list.
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "list": "Review", "title": "Later", "due": "2026-12-20"})
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "list": "Review", "title": "Sooner", "due": "2026-12-10", "position": "bottom"})
	updated := callInto[listResult](t, s, "update_list", map[string]any{
		"board": b.ID, "list": "review", "name": "In review", "closed": true, "color": "none", "sort_cards": "due_date",
	})
	assert.Equal(t, "In review", updated.List.Name)
	assert.True(t, updated.List.Closed)
	assert.Empty(t, updated.List.Color)
	got = columns()
	assert.Equal(t, "In review", got.Lists[2].Name)
	assert.True(t, got.Lists[2].Closed)
	require.Len(t, got.Lists[2].Cards, 2)
	assert.Equal(t, []string{"Sooner", "Later"}, []string{got.Lists[2].Cards[0].Title, got.Lists[2].Cards[1].Title}, "sorted by due date")

	// archive_cards: refused on an active list before anything is written, done on a closed one.
	text, isError := call(t, s, "update_list", map[string]any{"board": b.ID, "list": "Open", "archive_cards": true})
	assert.True(t, isError, text)
	assert.Contains(t, text, `list "Open" is not closed`)
	archived := callInto[listResult](t, s, "update_list", map[string]any{"board": b.ID, "list": "In review", "archive_cards": true})
	assert.Equal(t, 2, archived.Archived)
	assert.Zero(t, archived.List.Cards)

	// Deleting a list sends its cards to the board's Trash, where get_card still finds them.
	card := p.newCard(t, b, "Done", "Goes to the trash")
	deleted := callInto[deletedList](t, s, "delete_list", map[string]any{"board": b.ID, "list": "Done"})
	assert.Equal(t, "Done", deleted.Name)
	assert.Equal(t, 1, deleted.CardsToTrash)
	assert.Equal(t, []string{"Open", "In progress", "In review"}, func() []string {
		lists := columns().Lists
		names := make([]string, 0, len(lists))
		for _, l := range lists {
			names = append(names, l.Name)
		}
		return names
	}())
	trashed := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	assert.Equal(t, "Trash", trashed.List)
}
