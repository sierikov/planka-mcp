package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
	"github.com/sierikov/planka-mcp/internal/tools"
)

// Fixture IDs: projects.json lists the board that board.json describes, and card.json is
// its "Write README" card.
const (
	boardID = "1887856284582020101"
	cardID  = "1887856285739648016"
)

// newTestServer wires the real tools, services and HTTP client to a fake Planka that serves
// the captured fixtures, plus extra routes.
func newTestServer(t *testing.T, extra map[string]any) *mcp.Server {
	t.Helper()
	server, _ := newRecordedServer(t, extra)
	return server
}

// newRecordedServer is newTestServer that also returns the fake Planka, to inspect the requests it got.
func newRecordedServer(t *testing.T, extra map[string]any) (server *mcp.Server, fake *plankatest.Server) {
	t.Helper()
	routes := map[string]any{
		"GET /api/users/me":                      plankatest.Fixture(t, "users_me"),
		"GET /api/projects":                      plankatest.Fixture(t, "projects"),
		"GET /api/boards/" + boardID:             plankatest.Fixture(t, "board"),
		"GET /api/cards/" + cardID:               plankatest.Fixture(t, "card"),
		"GET /api/cards/" + cardID + "/comments": plankatest.Fixture(t, "comments"),
	}
	for k, v := range extra {
		routes[k] = v
	}
	srv := plankatest.NewServer(t, routes)
	client := planka.NewClient(srv.URL, planka.WithStaticKey(plankatest.APIKey))
	return tools.NewServer(newDeps(client), "test"), srv
}

func callText(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.False(t, res.IsError, text.Text)
	return text.Text
}

func TestWiringSearchAndGetCard(t *testing.T) {
	session := connectSession(t, newTestServer(t, nil))

	var search struct {
		Cards []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			List  string `json:"list"`
		} `json:"cards"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "search_cards", map[string]any{"query": "readme"})), &search))
	require.Len(t, search.Cards, 1)
	assert.Equal(t, cardID, search.Cards[0].ID)
	assert.Equal(t, "Write README", search.Cards[0].Title)
	assert.Equal(t, "Open", search.Cards[0].List)

	var card struct {
		Title     string `json:"title"`
		TaskLists []struct {
			Tasks []struct {
				Name string `json:"name"`
				Link *struct {
					ID   string `json:"id"`
					List string `json:"list"`
				} `json:"link"`
			} `json:"tasks"`
		} `json:"task_lists"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "get_card", map[string]any{"card": "readme"})), &card))
	assert.Equal(t, "Write README", card.Title)
	require.Len(t, card.TaskLists, 1)
	require.Len(t, card.TaskLists[0].Tasks, 3)
	linked := card.TaskLists[0].Tasks[2]
	assert.Equal(t, "Research search API", linked.Name)
	require.NotNil(t, linked.Link, "the task is a linked card")
	assert.Equal(t, "In progress", linked.Link.List)
}

// TestWiringFillInTicketFlow runs spec flow 1 through the real tools, services and HTTP client:
// find the card by words, give it a description and a label, add a plain task and a linked card to
// its checklist, then read it back. It pins the writes Planka gets, in order, with their bodies.
func TestWiringFillInTicketFlow(t *testing.T) {
	const (
		researchLabelID = "1887856285521544207"
		researchCardID  = "1887856285890642962"
		taskListID      = "1887856286310073368" // card.json's "Tasks", whose last task is at 196608
	)
	server, fake := newRecordedServer(t, map[string]any{
		"PATCH /api/cards/" + cardID:                    plankatest.Fixture(t, "card"),
		"POST /api/cards/" + cardID + "/card-labels":    plankatest.Fixture(t, "card_label_created"),
		"POST /api/task-lists/" + taskListID + "/tasks": plankatest.Fixture(t, "task_created"),
	})
	session := connectSession(t, server)

	const description = "Install, configure, use.\n\n- **stdio**\n- http"
	var updated struct {
		Card struct {
			ID string `json:"id"`
		} `json:"card"`
		CreatedLabels []string `json:"created_labels"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "update_card", map[string]any{
		"card": "readme", "description": description, "add_labels": []string{"research"},
	})), &updated))
	assert.Equal(t, cardID, updated.Card.ID)
	assert.Empty(t, updated.CreatedLabels, "research is a label of the board already")

	var list struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "add_tasks", map[string]any{
		"card": "readme", "items": []map[string]any{{"name": "Write the config section"}, {"link": "research search"}},
	})), &list))
	assert.Equal(t, "Tasks", list.Name, "the card's own checklist is used, not a new one")

	writesBefore := len(writesOf(fake.Requests()))
	var card struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "get_card", map[string]any{"card": cardID})), &card))
	assert.Equal(t, "Write README", card.Title)

	writes := writesOf(fake.Requests())
	assert.Len(t, writes, writesBefore, "get_card writes nothing")
	require.Len(t, writes, 4)
	assert.Equal(t, "PATCH /api/cards/"+cardID, writes[0].Method+" "+writes[0].Path)
	assert.JSONEq(t, `{"description":"Install, configure, use.\n\n- **stdio**\n- http"}`, string(writes[0].Body))
	assert.Equal(t, "POST /api/cards/"+cardID+"/card-labels", writes[1].Method+" "+writes[1].Path)
	assert.JSONEq(t, `{"labelId":"`+researchLabelID+`"}`, string(writes[1].Body))
	assert.Equal(t, "POST /api/task-lists/"+taskListID+"/tasks", writes[2].Method+" "+writes[2].Path)
	assert.JSONEq(t, `{"position":262144,"name":"Write the config section","isCompleted":false}`, string(writes[2].Body))
	assert.Equal(t, "POST /api/task-lists/"+taskListID+"/tasks", writes[3].Method+" "+writes[3].Path)
	assert.JSONEq(t, `{"position":327680,"linkedCardId":"`+researchCardID+`","isCompleted":false}`, string(writes[3].Body))

	// The last requests are get_card's reads of the card and its comments, after every write.
	reqs := fake.Requests()
	last := reqs[len(reqs)-1]
	assert.Equal(t, "GET /api/cards/"+cardID+"/comments", last.Method+" "+last.Path)
}

// writesOf keeps the requests that are not GETs, in order.
func writesOf(reqs []plankatest.Recorded) []plankatest.Recorded {
	var out []plankatest.Recorded
	for _, r := range reqs {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

// TestWiringWorkOnTicketFlow runs spec flow 2 through the real tools, services and HTTP client:
// move the card to "In progress", comment on it, move it to "Done".
func TestWiringWorkOnTicketFlow(t *testing.T) {
	const (
		inProgressID = "1887856284900787210"
		doneID       = "1887856285144056844"
	)
	routes := map[string]any{
		"PATCH /api/cards/" + cardID: plankatest.Fixture(t, "card"),
		"POST /api/cards/" + cardID + "/comments": map[string]any{"item": map[string]any{
			"id": "9001", "cardId": cardID, "userId": "1887856237849084929", "text": "started", "createdAt": "2026-10-17T12:00:00.000Z",
		}},
	}
	server, fake := newRecordedServer(t, routes)
	session := connectSession(t, server)

	var moved struct {
		Card struct {
			ID string `json:"id"`
		} `json:"card"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "move_card", map[string]any{"card": "readme", "list": "In progress"})), &moved))
	assert.Equal(t, cardID, moved.Card.ID)

	var comment struct {
		ID     string `json:"id"`
		Author string `json:"author"`
		Text   string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(callText(t, session, "add_comment", map[string]any{"card": "readme", "text": "started"})), &comment))
	assert.Equal(t, "9001", comment.ID)
	assert.Equal(t, "started", comment.Text)
	assert.NotEmpty(t, comment.Author, "the author is resolved to a name")

	callText(t, session, "move_card", map[string]any{"card": "readme", "list": "Done"})

	var writes []plankatest.Recorded
	for _, r := range fake.Requests() {
		if r.Method != http.MethodGet {
			writes = append(writes, r)
		}
	}
	require.Len(t, writes, 3)
	assert.Equal(t, http.MethodPatch, writes[0].Method)
	assert.Equal(t, "/api/cards/"+cardID, writes[0].Path)
	assert.JSONEq(t, `{"listId":"`+inProgressID+`","position":`+positionOf(t, writes[0].Body)+`}`, string(writes[0].Body))
	assert.Equal(t, http.MethodPost, writes[1].Method)
	assert.Equal(t, "/api/cards/"+cardID+"/comments", writes[1].Path)
	assert.JSONEq(t, `{"text":"started"}`, string(writes[1].Body))
	assert.Equal(t, http.MethodPatch, writes[2].Method)
	assert.JSONEq(t, `{"listId":"`+doneID+`","position":`+positionOf(t, writes[2].Body)+`}`, string(writes[2].Body))
}

// TestWiringPartialWriteKeepsContext: create_card makes the card, then Planka refuses the label. The
// error must still say the card was created, or a retry would make a second one.
func TestWiringPartialWriteKeepsContext(t *testing.T) {
	const (
		openID    = "1887856284758180873"
		createdID = "1887856288902153251" // card_created.json
	)
	server, _ := newRecordedServer(t, map[string]any{
		"POST /api/lists/" + openID + "/cards": plankatest.Fixture(t, "card_created"),
		"POST /api/cards/" + createdID + "/card-labels": plankatest.Reply{
			Status: http.StatusForbidden, Body: map[string]any{"code": "E_FORBIDDEN", "message": "Not enough rights"},
		},
	})
	session := connectSession(t, server)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_card", Arguments: map[string]any{
		"board": boardID, "title": "Scratch card", "labels": []string{"feature"},
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(text.Text, "card "+createdID+" was created, but: add label \"feature\": "), text.Text)
	assert.True(t, strings.HasSuffix(text.Text, "; Planka refused (403): you lack rights on this board, or Planka does not allow this change"), text.Text)
}

// boardWith returns the board fixture with extra lists added to it.
func boardWith(t *testing.T, lists ...map[string]any) json.RawMessage {
	t.Helper()
	var board map[string]any
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "board"), &board))
	included, ok := board["included"].(map[string]any)
	require.True(t, ok)
	all, ok := included["lists"].([]any)
	require.True(t, ok)
	for _, l := range lists {
		all = append(all, l)
	}
	included["lists"] = all
	raw, err := json.Marshal(board)
	require.NoError(t, err)
	return raw
}

// TestWiringDuplicateListNameRetriesByID: with two lists called "Done", a move by name is an error
// that shows both IDs, and a retry with one of the IDs moves the card to that list.
func TestWiringDuplicateListNameRetriesByID(t *testing.T) {
	const (
		doneID   = "1887856285144056844"
		secondID = "1887856285999999999"
	)
	board := boardWith(t, map[string]any{
		"id": secondID, "boardId": boardID, "type": "active", "name": "Done", "position": 400000,
	})
	server, fake := newRecordedServer(t, map[string]any{
		"GET /api/boards/" + boardID: board,
		"PATCH /api/cards/" + cardID: plankatest.Fixture(t, "card"),
	})
	session := connectSession(t, server)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "move_card", Arguments: map[string]any{"card": cardID, "list": "Done"}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, `"Done" matches several lists: Done (id `+doneID+`); Done (id `+secondID+`); retry with the id of the one you mean`, text.Text)

	callText(t, session, "move_card", map[string]any{"card": cardID, "list": secondID})

	writes := writesOf(fake.Requests())
	require.Len(t, writes, 1, "the ambiguous move wrote nothing")
	assert.Equal(t, "/api/cards/"+cardID, writes[0].Path)
	assert.JSONEq(t, `{"listId":"`+secondID+`","position":`+positionOf(t, writes[0].Body)+`}`, string(writes[0].Body))
}

// positionOf returns the position in a card PATCH body as JSON text.
func positionOf(t *testing.T, body []byte) string {
	t.Helper()
	var b struct {
		Position json.Number `json:"position"`
	}
	require.NoError(t, json.Unmarshal(body, &b))
	require.NotEmpty(t, b.Position)
	return b.Position.String()
}
