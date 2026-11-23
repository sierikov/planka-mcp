package planka

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listItem is a list as Planka answers it, inside the {"item": ...} envelope.
const listItem = `{"item":{"id":"l9","createdAt":"2026-11-23T10:00:00.000Z","updatedAt":null,"type":"active",` +
	`"position":98304,"name":"Review","color":"lagoon-blue","boardId":"b1"}}`

// listWithCards is how Planka answers a list delete or move-cards: the list,
// with the cards it moved.
const listWithCards = `{"item":{"id":"l9","type":"closed","position":98304,"name":"Review","boardId":"b1"},` +
	`"included":{"cards":[{"id":"c1","listId":"arch","name":"One"},{"id":"c2","listId":"arch","name":"Two"}]}}`

func TestCreateList(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/boards/b1/lists": listItem})

	l, err := c.CreateList(t.Context(), "b1", CreateListRequest{Type: "closed", Position: 98304, Name: "Review"})

	require.NoError(t, err)
	assert.Equal(t, "l9", l.ID)
	assert.Equal(t, "Review", *l.Name)
	assert.Equal(t, "lagoon-blue", *l.Color)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/boards/b1/lists", srv.Last().Path)
	assert.JSONEq(t, `{"type":"closed","position":98304,"name":"Review"}`, string(srv.Last().Body))
}

func TestUpdateListSendsOnlySetFields(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/lists/l9": listItem})

	_, err := c.UpdateList(t.Context(), "l9", UpdateListRequest{
		Type: Some("active"), Position: Some(4096.0), Name: Some("Review"), Color: Some("lagoon-blue"),
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, srv.Last().Method)
	assert.Equal(t, "/api/lists/l9", srv.Last().Path)
	assert.JSONEq(t, `{"type":"active","position":4096,"name":"Review","color":"lagoon-blue"}`, string(srv.Last().Body))

	_, err = c.UpdateList(t.Context(), "l9", UpdateListRequest{Color: Null[string]()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"color":null}`, string(srv.Last().Body), "a null colour clears it")
}

func TestDeleteList(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/lists/l9": listWithCards})

	cards, err := c.DeleteList(t.Context(), "l9")

	require.NoError(t, err)
	assert.Len(t, cards, 2, "the cards Planka moved to the trash")

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/lists/l9", srv.Last().Path)
}

func TestSortList(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/lists/l9/sort": listItem})

	require.NoError(t, c.SortList(t.Context(), "l9", "dueDate", "desc"))

	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/lists/l9/sort", srv.Last().Path)
	assert.JSONEq(t, `{"fieldName":"dueDate","order":"desc"}`, string(srv.Last().Body))
}

func TestMoveListCards(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/lists/l9/move-cards": listWithCards})

	cards, err := c.MoveListCards(t.Context(), "l9", "arch")

	require.NoError(t, err)
	assert.Equal(t, []string{"c1", "c2"}, []string{cards[0].ID, cards[1].ID})

	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/lists/l9/move-cards", srv.Last().Path)
	assert.JSONEq(t, `{"listId":"arch"}`, string(srv.Last().Body))
}
