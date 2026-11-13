package planka

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

// newTestClient returns a client wired to a fake server that serves routes.
func newTestClient(t *testing.T, routes map[string]any) (*Client, *plankatest.Server) {
	t.Helper()
	srv := plankatest.NewServer(t, routes)
	return NewClient(srv.URL, WithStaticKey(plankatest.APIKey)), srv
}

func TestMe(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/users/me": plankatest.Fixture(t, "users_me")})

	u, err := c.Me(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "Admin", u.Name)
	assert.Equal(t, "admin", u.Username)
	assert.Equal(t, "admin@example.com", u.Email)
	assert.NotEmpty(t, u.ID)
	assert.Equal(t, "/api/users/me", srv.Last().Path)
}

func TestProjects(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/projects": plankatest.Fixture(t, "projects")})

	p, err := c.Projects(t.Context())

	require.NoError(t, err)
	require.Len(t, p.Items, 1)
	assert.Equal(t, "Projects", p.Items[0].Name)
	require.Len(t, p.Included.Boards, 1)
	board := p.Included.Boards[0]
	assert.Equal(t, "Planka MCP", board.Name)
	assert.Equal(t, p.Items[0].ID, board.ProjectID)
	assert.Equal(t, "project", board.DefaultCardType)
	assert.Equal(t, http.MethodGet, srv.Last().Method)
}

func TestBoard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/boards/b1": plankatest.Fixture(t, "board")})

	b, err := c.Board(t.Context(), "b1")

	require.NoError(t, err)
	assert.Equal(t, "/api/boards/b1", srv.Last().Path)
	assert.Equal(t, "Planka MCP", b.Item.Name)
	assert.Equal(t, "project", b.Item.DefaultCardType)

	// 5 kanban lists plus the archive and trash lists Planka adds to every board.
	require.Len(t, b.Included.Lists, 7)
	types := map[string]int{}
	for _, l := range b.Included.Lists {
		types[l.Type]++
		if l.Type == "archive" || l.Type == "trash" {
			assert.Nil(t, l.Name)
			assert.Nil(t, l.Position)
		}
	}
	assert.Equal(t, map[string]int{"active": 4, "closed": 1, "archive": 1, "trash": 1}, types)

	var readme *Card
	for i := range b.Included.Cards {
		if b.Included.Cards[i].Name == "Write README" {
			readme = &b.Included.Cards[i]
		}
	}
	require.NotNil(t, readme)
	assert.NotNil(t, readme.Description)
	require.NotNil(t, readme.DueDate)
	assert.Equal(t, 2026, readme.DueDate.Year())
	assert.False(t, readme.ListChangedAt.IsZero())
	var labels int
	for _, cl := range b.Included.CardLabels {
		if cl.CardID == readme.ID {
			labels++
		}
	}
	assert.Equal(t, 1, labels)
	assert.Len(t, b.Included.Labels, 2)
	assert.Len(t, b.Included.BoardMemberships, 1)
	assert.Len(t, b.Included.TaskLists, 1)
	assert.Len(t, b.Included.Tasks, 3)
}

func TestCard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/cards/c1": plankatest.Fixture(t, "card")})

	card, err := c.Card(t.Context(), "c1")

	require.NoError(t, err)
	assert.Equal(t, "/api/cards/c1", srv.Last().Path)
	assert.Equal(t, "Write README", card.Item.Name)
	require.Len(t, card.Included.Tasks, 3)
	var linked []Task
	for _, task := range card.Included.Tasks {
		if task.LinkedCardID != nil {
			linked = append(linked, task)
		}
	}
	require.Len(t, linked, 1)
	// Planka fills a linked task's name with the linked card's name.
	require.NotNil(t, linked[0].Name)
	assert.Equal(t, "Research search API", *linked[0].Name)
	assert.Len(t, card.Included.CardLabels, 1)
	assert.Len(t, card.Included.CardMemberships, 1)
	assert.Len(t, card.Included.TaskLists, 1)
}

func TestListCardsDecodesArchivedCard(t *testing.T) {
	c, _ := newTestClient(t, map[string]any{"GET /api/lists/l1/cards": plankatest.Fixture(t, "list_cards_archive")})

	r, err := c.ListCards(t.Context(), "l1", CardQuery{})

	require.NoError(t, err)
	require.Len(t, r.Items, 1)
	assert.Equal(t, "Old archived task", r.Items[0].Name)
	assert.Nil(t, r.Items[0].Position, "cards in the archive list have a null position")
	assert.Nil(t, r.Items[0].Description)
	assert.Nil(t, r.Items[0].DueDate)
	assert.Len(t, r.Included.Users, 1)
}

func TestListCardsWithoutQuerySendsNoParams(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/lists/l1/cards": plankatest.Fixture(t, "list_cards_archive")})

	_, err := c.ListCards(t.Context(), "l1", CardQuery{})

	require.NoError(t, err)
	assert.Empty(t, srv.Last().Query)
}

func TestListCardsSendsSearch(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/lists/l1/cards": plankatest.Fixture(t, "list_cards_archive")})

	_, err := c.ListCards(t.Context(), "l1", CardQuery{Search: "README"})

	require.NoError(t, err)
	assert.Equal(t, "/api/lists/l1/cards", srv.Last().Path)
	assert.Equal(t, "README", srv.Last().Query.Get("search"))
	assert.False(t, srv.Last().Query.Has("before[id]"))
}

func TestListCardsSendsCursor(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/lists/l1/cards": plankatest.Fixture(t, "list_cards_archive")})
	changed := time.Date(2026, 10, 17, 11, 36, 15, 218_000_000, time.FixedZone("CEST", 2*3600))

	_, err := c.ListCards(t.Context(), "l1", CardQuery{Before: &CardCursor{ListChangedAt: changed, ID: "42"}})

	require.NoError(t, err)
	q := srv.Last().Query
	// Planka parses `before` as an object, so the cursor goes in as before[...] in UTC.
	assert.Equal(t, "2026-10-17T09:36:15.218Z", q.Get("before[listChangedAt]"))
	assert.Equal(t, "42", q.Get("before[id]"))
	assert.False(t, q.Has("search"))
}

func TestCommentsNewestFirst(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/cards/c1/comments": plankatest.Fixture(t, "comments")})

	r, err := c.Comments(t.Context(), "c1", "")

	require.NoError(t, err)
	assert.Equal(t, "/api/cards/c1/comments", srv.Last().Path)
	assert.Empty(t, srv.Last().Query, "no beforeId when it is empty")
	require.Len(t, r.Items, 2)
	assert.False(t, r.Items[0].CreatedAt.Before(r.Items[1].CreatedAt), "newest first")
	assert.Equal(t, "Board-level search is client side for now.", r.Items[0].Text)
	assert.Len(t, r.Included.Users, 1)
}

func TestCommentsPagesWithBeforeID(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"GET /api/cards/c1/comments": plankatest.Fixture(t, "comments")})

	_, err := c.Comments(t.Context(), "c1", "123")

	require.NoError(t, err)
	assert.Equal(t, "123", srv.Last().Query.Get("beforeId"))
}

func TestIDsAreEscapedInPaths(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{})

	_, err := c.Card(t.Context(), "../users/me")

	require.Error(t, err)
	assert.Equal(t, "/api/cards/../users/me", srv.Last().Path, "decoded server-side; the slash stays inside one segment")
	assert.NotEqual(t, "/api/users/me", srv.Last().Path)
}

func TestNotFoundIsAPIError404(t *testing.T) {
	c, _ := newTestClient(t, map[string]any{
		"GET /api/cards/1": plankatest.Reply{Status: http.StatusNotFound, Body: plankatest.Fixture(t, "error_404")},
	})

	card, err := c.Card(t.Context(), "1")

	require.Error(t, err)
	assert.Nil(t, card)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode())
	assert.Contains(t, apiErr.Body, "E_NOT_FOUND")
}
