package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

// connect runs a server on deps and returns a client session on it.
func connect(t *testing.T, deps Deps) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(deps, "test")
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

func TestReadToolsAreReadOnly(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	var readOnlyNames []string
	for _, tool := range res.Tools {
		require.NotNil(t, tool.Annotations, tool.Name)
		assert.Nil(t, tool.OutputSchema, "%s must declare no output schema", tool.Name)
		if tool.Annotations.ReadOnlyHint {
			readOnlyNames = append(readOnlyNames, tool.Name)
		}
	}
	// Every other tool writes: TestWriteToolAnnotations covers those.
	assert.ElementsMatch(t, []string{"list_boards", "get_board", "search_cards", "get_card", "list_comments"}, readOnlyNames)
}

// TestEveryToolNamesPlanka: the tool names are generic (get_card, search_cards), so the descriptions
// are what tells a model this tool is Planka's and not another tracker's.
func TestEveryToolNamesPlanka(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	for _, tool := range res.Tools {
		assert.Contains(t, tool.Description, "Planka", tool.Name)
	}
}

func TestToolDescriptionsAndSchemas(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	required := map[string][]string{
		"list_boards":   nil,
		"get_board":     {"board"},
		"search_cards":  nil,
		"get_card":      {"card"},
		"list_comments": {"card"},

		"create_card":    {"board", "title"},
		"move_card":      {"card", "list"},
		"update_card":    {"card"},
		"delete_card":    {"card"},
		"add_comment":    {"card", "text"},
		"update_comment": {"comment_id", "text"},
		"delete_comment": {"comment_id"},
		"add_tasks":      {"card", "items"},
		"update_tasks":   {"items"},
		"delete_task":    {"task_id"},
	}
	for _, tool := range res.Tools {
		assert.NotEmpty(t, tool.Description, tool.Name)
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Required   []string                  `json:"required"`
			Properties map[string]map[string]any `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		assert.ElementsMatch(t, required[tool.Name], schema.Required, tool.Name)
		for prop, def := range schema.Properties {
			assert.NotEmpty(t, def["description"], "%s.%s needs a description", tool.Name, prop)
		}
	}

	var search *mcp.Tool
	for _, tool := range res.Tools {
		if tool.Name == "search_cards" {
			search = tool
		}
	}
	require.NotNil(t, search)
	assert.Contains(t, search.Description, "all")
	assert.Contains(t, search.Description, "description")
}

func TestServerInstructions(t *testing.T) {
	assert.LessOrEqual(t, strings.Count(instructions, "\n")+1, 12)
	for _, want := range []string{"list_boards", "get_card", "move_card", "add_comment", "include_archived",
		"lists, labels and people are given by name", "or by ID", "candidates with their IDs: retry with one of the IDs"} {
		assert.Contains(t, instructions, want)
	}
}

func TestListBoardsPassesQuery(t *testing.T) {
	catalog := NewMockCatalogService(t)
	catalog.EXPECT().ListBoards(mock.Anything, "plan").Return([]service.ProjectBoards{
		{Project: "P", Boards: []service.BoardRef{{ID: "b1", Name: "Planka MCP", Project: "P"}}},
	}, nil)

	res := call(t, connect(t, Deps{Catalog: catalog}), "list_boards", map[string]any{"query": "plan"})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `[{"project":"P","boards":[{"id":"b1","name":"Planka MCP"}]}]`, resultText(t, res))
}

func TestGetBoardOutput(t *testing.T) {
	catalog := NewMockCatalogService(t)
	catalog.EXPECT().GetBoard(mock.Anything, "Planka MCP", "Open").Return(&service.Board{
		ID: "b1", Name: "Planka MCP", Project: "P", Labels: []string{"bug"}, Members: []string{"Ann"},
		Lists: []service.List{
			// The service fills Board and List on every summary; the enclosing board and list already say them.
			{ID: "l1", Name: "Open", Cards: []service.CardSummary{{ID: "c1", Title: "A", Board: "Planka MCP", List: "Open", TasksDone: 1, TasksTotal: 2}}},
			{ID: "l2", Name: "Done", Closed: true},
		},
	}, nil)

	res := call(t, connect(t, Deps{Catalog: catalog}), "get_board", map[string]any{"board": "Planka MCP", "list": "Open"})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"id":"b1","name":"Planka MCP","project":"P","labels":["bug"],"members":["Ann"],`+
		`"lists":[{"name":"Open","cards":[{"id":"c1","title":"A","tasks":"1/2"}]},{"name":"Done","closed":true}]}`, resultText(t, res))
	assert.NotContains(t, resultText(t, res), `"board":`, "card lines do not repeat the board")
	assert.NotContains(t, resultText(t, res), `"list":`, "card lines do not repeat the list")
}

func TestGetBoardOutputHasNoDescription(t *testing.T) {
	// CardSummary has no description, so a board never carries one, however large it is on the card.
	catalog := NewMockCatalogService(t)
	catalog.EXPECT().GetBoard(mock.Anything, "b", "").Return(&service.Board{
		ID: "b1", Name: "B", Lists: []service.List{{Name: "L", Cards: []service.CardSummary{{ID: "c1", Title: "A"}}}},
	}, nil)

	res := call(t, connect(t, Deps{Catalog: catalog}), "get_board", map[string]any{"board": "b"})
	text := resultText(t, res)
	assert.NotContains(t, text, "description")
	assert.Less(t, len(text), 1024)

	// A card summary carries no description field at all.
	raw, err := json.Marshal(toCardLine(service.CardSummary{ID: "c1", Title: "A"}))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "description")
}

func TestSearchOutputHasNoDescription(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, mock.Anything).Return([]service.CardSummary{
		{ID: "c1", Title: "A", Board: "B", List: "L"},
	}, 0, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"query": "a"})
	text := resultText(t, res)
	assert.NotContains(t, text, "description")
	assert.Less(t, len(text), 1024)
}

func TestSearchCardsPassesFilters(t *testing.T) {
	cards := NewMockCardService(t)
	want := service.SearchQuery{
		Text: "login bug", Board: "Planka MCP", Project: "P", List: "Open", Label: "bug", Assignee: "me",
		IncludeArchived: true, Limit: 5,
	}
	cards.EXPECT().Search(mock.Anything, want).Return([]service.CardSummary{{ID: "c1", Title: "Login bug"}}, 0, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{
		"query": "login bug", "board": "Planka MCP", "project": "P", "list": "Open", "label": "bug",
		"assignee": "me", "include_archived": true, "limit": 5,
	})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"cards":[{"id":"c1","title":"Login bug"}]}`, resultText(t, res))
}

func TestSearchCardsDefaultLimit20(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, service.SearchQuery{Text: "x", Limit: 20}).Return([]service.CardSummary{}, 0, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"query": "x"})
	assert.JSONEq(t, `{"cards":[]}`, resultText(t, res))
}

func TestSearchCardsSaysHowManyMoreMatched(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, service.SearchQuery{Text: "login", Limit: 2}).Return([]service.CardSummary{
		{ID: "c1", Title: "Login bug"}, {ID: "c2", Title: "Login page"},
	}, 3, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"query": "login", "limit": 2})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{
		"cards":[{"id":"c1","title":"Login bug"},{"id":"c2","title":"Login page"}],
		"more":3,
		"hint":"3 more cards matched: narrow with board, list, label or more words, or raise limit (at most 100)"
	}`, resultText(t, res))
}

func TestSearchCardsCapsLimitAt100(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, service.SearchQuery{Text: "x", Limit: 100}).
		Return([]service.CardSummary{{ID: "c1", Title: "X"}}, 7, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"query": "x", "limit": 500})
	assert.False(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "7 more cards matched: narrow with board, list, label or more words")
	assert.NotContains(t, text, "raise limit", "the limit is already at its maximum")
}

func TestGetBoardShowsTheFirstCardsOfEachList(t *testing.T) {
	cardsOf := func(n int) []service.CardSummary {
		out := make([]service.CardSummary, n)
		for i := range out {
			out[i] = service.CardSummary{ID: fmt.Sprint(i), Title: fmt.Sprint("card ", i)}
		}
		return out
	}
	catalog := NewMockCatalogService(t)
	catalog.EXPECT().GetBoard(mock.Anything, "b", "").Return(&service.Board{ID: "b1", Name: "B", Lists: []service.List{
		{Name: "Open", Cards: cardsOf(12)},
		{Name: "Short", Cards: cardsOf(2)},
		{Name: "Done", Closed: true, Cards: cardsOf(5)},
	}}, nil)

	var out boardOut
	res := call(t, connect(t, Deps{Catalog: catalog}), "get_board", map[string]any{"board": "b"})
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	require.Len(t, out.Lists, 3)
	assert.Len(t, out.Lists[0].Cards, 10)
	assert.Equal(t, "0", out.Lists[0].Cards[0].ID, "the top of the column comes first")
	assert.Equal(t, 2, out.Lists[0].More)
	assert.Len(t, out.Lists[1].Cards, 2)
	assert.Zero(t, out.Lists[1].More)
	assert.Len(t, out.Lists[2].Cards, 3, "a closed list shows fewer")
	assert.Equal(t, 2, out.Lists[2].More)
	assert.Equal(t, "lists with more cards were cut: pass list to see up to 100 cards of one, or find cards with search_cards", out.Hint)
}

func TestGetBoardWithListShowsUpTo100Cards(t *testing.T) {
	cards := make([]service.CardSummary, 130)
	for i := range cards {
		cards[i] = service.CardSummary{ID: fmt.Sprint(i), Title: "T"}
	}
	catalog := NewMockCatalogService(t)
	catalog.EXPECT().GetBoard(mock.Anything, "b", "Done").Return(&service.Board{ID: "b1", Name: "B", Lists: []service.List{
		{Name: "Done", Closed: true, Cards: cards},
	}}, nil)

	var out boardOut
	res := call(t, connect(t, Deps{Catalog: catalog}), "get_board", map[string]any{"board": "b", "list": "Done"})
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	assert.Len(t, out.Lists[0].Cards, 100)
	assert.Equal(t, 30, out.Lists[0].More)
	assert.Equal(t, "the list has more cards: find them with search_cards (list, label, assignee or words)", out.Hint)
}

func TestGetCardShowsCommentsUpTo5000Characters(t *testing.T) {
	comment := func(id string, n int) service.Comment {
		return service.Comment{ID: id, Text: strings.Repeat("x", n), Created: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)}
	}
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, mock.Anything, 10).Return(&service.Card{
		CardSummary: service.CardSummary{ID: "c1", Title: "T", CommentsTotal: 7},
		Comments:    []service.Comment{comment("cm7", 3000), comment("cm6", 1500), comment("cm5", 1000)},
	}, nil)

	var out cardOut
	res := call(t, connect(t, Deps{Cards: cards}), "get_card", map[string]any{"card": "c1"})
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	require.Len(t, out.Comments, 2, "the third comment would pass 5,000 characters")
	assert.Equal(t, `2 of 7 comments shown: list_comments with before="cm6" reads older ones`, out.Hint)
}

func TestGetCardShowsTheNewestCommentHoweverLong(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, mock.Anything, 10).Return(&service.Card{
		CardSummary: service.CardSummary{ID: "c1", Title: "T", CommentsTotal: 1},
		Comments:    []service.Comment{{ID: "cm1", Text: strings.Repeat("x", 9000)}},
	}, nil)

	var out cardOut
	res := call(t, connect(t, Deps{Cards: cards}), "get_card", map[string]any{"card": "c1"})
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	assert.Len(t, out.Comments, 1)
	assert.Empty(t, out.Hint, "every comment is shown")
}

func TestListCommentsCapsLimitAt50(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Comments(mock.Anything, service.CardRef{Ref: "c1"}, "", 50).Return(nil, false, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "list_comments", map[string]any{"card": "c1", "limit": 500})
	assert.False(t, res.IsError)
}

func TestListCommentsPageEndsAt20000Characters(t *testing.T) {
	long := strings.Repeat("x", 8000)
	cards := NewMockCardService(t)
	cards.EXPECT().Comments(mock.Anything, service.CardRef{Ref: "c1"}, "", 20).Return([]service.Comment{
		{ID: "cm3", Text: long}, {ID: "cm2", Text: long}, {ID: "cm1", Text: long},
	}, false, nil)

	var out commentsOut
	res := call(t, connect(t, Deps{Cards: cards}), "list_comments", map[string]any{"card": "c1"})
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	assert.Len(t, out.Comments, 2)
	assert.True(t, out.Older, "the cut comment is older")
	assert.Equal(t, `older comments exist: pass before="cm2" to read them`, out.Hint)
}

func TestSearchCardsErrorIsToolResult(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, mock.Anything).Return(nil, 0, &service.NoMatchError{Kind: "board", Query: "zz"})

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"query": "x", "board": "zz"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), `no board matching "zz"`)
}

func TestGetCardDefaultsCommentsLimit10(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, service.CardRef{Ref: "c1"}, 10).Return(&service.Card{
		CardSummary: service.CardSummary{ID: "c1", Title: "T"}, Description: "# body",
	}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "get_card", map[string]any{"card": "c1"})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"id":"c1","title":"T","description":"# body","comments_total":0}`, resultText(t, res))
}

func TestGetCardPassesArguments(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, service.CardRef{Ref: "login", Board: "B", IncludeArchived: true}, 3).
		Return(nil, &service.AmbiguousError{Kind: "card", Query: "login", Candidates: []service.Candidate{{ID: "1", Label: "a"}, {ID: "2", Label: "b"}}})

	res := call(t, connect(t, Deps{Cards: cards}), "get_card", map[string]any{
		"card": "login", "board": "B", "include_archived": true, "comments_limit": 3,
	})
	assert.True(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "1")
	assert.Contains(t, text, "2")
}

func TestListCommentsDefaultsAndOutput(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Comments(mock.Anything, service.CardRef{Ref: "c1", Board: "B"}, "cm9", 20).Return([]service.Comment{
		{ID: "cm8", Author: "Ann", Text: "older", Created: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)},
	}, false, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "list_comments", map[string]any{"card": "c1", "board": "B", "before": "cm9"})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"comments":[{"id":"cm8","author":"Ann","date":"2026-10-01","text":"older"}]}`, resultText(t, res))
}

func TestListCommentsSaysWhenOlderExist(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Comments(mock.Anything, service.CardRef{Ref: "c1"}, "", 1).Return([]service.Comment{
		{ID: "cm2", Author: "Ann", Text: "newest", Created: time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local)},
	}, true, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "list_comments", map[string]any{"card": "c1", "limit": 1})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{
		"comments":[{"id":"cm2","author":"Ann","date":"2026-10-02","text":"newest"}],
		"older":true,
		"hint":"older comments exist: pass before=\"cm2\" to read them"
	}`, resultText(t, res))
}

func TestListCommentsPassesLimit(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Comments(mock.Anything, service.CardRef{Ref: "c1"}, "", 2).Return(nil, false, service.ErrNotFound)

	res := call(t, connect(t, Deps{Cards: cards}), "list_comments", map[string]any{"card": "c1", "limit": 2})
	assert.True(t, res.IsError)
}

func TestSearchCardsFilterOnly(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Search(mock.Anything, service.SearchQuery{Assignee: "me", Limit: 20}).
		Return([]service.CardSummary{{ID: "c1", Title: "Mine"}}, 0, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "search_cards", map[string]any{"assignee": "me"})
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"cards":[{"id":"c1","title":"Mine"}]}`, resultText(t, res))
}

func TestGetCardDescriptionIsNotHTMLEscaped(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, mock.Anything, 10).Return(&service.Card{
		CardSummary: service.CardSummary{ID: "c1", Title: "T"}, Description: "a -> b && <x>",
	}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "get_card", map[string]any{"card": "c1"})
	text := resultText(t, res)
	assert.Contains(t, text, `"description":"a -> b && <x>"`)
	assert.NotContains(t, text, `\u00`)
}

func TestSearchCardsDescriptionIsShort(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		if tool.Name == "search_cards" {
			assert.LessOrEqual(t, len(tool.Description), 250)
		}
	}
}

// panicValue is a panic payload whose text must never reach the log.
type panicValue struct{ secret string }

// syncBuffer is a bytes.Buffer safe to write from handler goroutines while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestToolPanicBecomesErrorResult checks that a panic in a service call is turned into an error
// result, that the session keeps serving, and that the log gets the panic's type but neither
// its value nor the tool's arguments.
func TestToolPanicBecomesErrorResult(t *testing.T) {
	var logs syncBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	catalog := NewMockCatalogService(t)
	catalog.EXPECT().ListBoards(mock.Anything, "boom-secret-query").RunAndReturn(
		func(context.Context, string) ([]service.ProjectBoards, error) {
			panic(panicValue{secret: "panic-secret-text"})
		})
	catalog.EXPECT().ListBoards(mock.Anything, "ok").Return([]service.ProjectBoards{
		{Project: "P", Boards: []service.BoardRef{{ID: "b1", Name: "B", Project: "P"}}},
	}, nil)
	session := connect(t, Deps{Catalog: catalog})

	res := call(t, session, "list_boards", map[string]any{"query": "boom-secret-query"})
	assert.True(t, res.IsError)
	assert.Equal(t, "internal error in list_boards", resultText(t, res))

	res = call(t, session, "list_boards", map[string]any{"query": "ok"})
	assert.False(t, res.IsError, "the session must survive a panic")
	assert.JSONEq(t, `[{"project":"P","boards":[{"id":"b1","name":"B"}]}]`, resultText(t, res))

	logged := logs.String()
	assert.Contains(t, logged, "tool handler panicked")
	assert.Contains(t, logged, "tool=list_boards")
	assert.Contains(t, logged, "tools.panicValue")
	assert.Contains(t, logged, "goroutine", "the stack is logged")
	assert.NotContains(t, logged, "panic-secret-text")
	assert.NotContains(t, logged, "boom-secret-query")
}
