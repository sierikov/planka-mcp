package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

const (
	fixtureArchiveListID = "1887856284598797319"
	fixtureReadmeID      = "1887856285739648016"
	fixtureResearchID    = "1887856285890642962"
	fixtureArchivedID    = "1887856287392203808"
)

// cardsFixture wires Cards to mocks. The mocks fail the test on any call a
// test did not expect.
type cardsFixture struct {
	projects *MockProjectsAPI
	boards   *MockBoardsAPI
	cards    *MockCardsReadAPI
	comments *MockCommentsReadAPI
	users    *MockUsersAPI
	write    *MockCardsWriteAPI
	labels   *MockLabelsAPI
	members  *MockMembersAPI
	svc      *Cards
}

func newCardsFixture(t *testing.T) *cardsFixture {
	t.Helper()
	f := &cardsFixture{
		projects: NewMockProjectsAPI(t),
		boards:   NewMockBoardsAPI(t),
		cards:    NewMockCardsReadAPI(t),
		comments: NewMockCommentsReadAPI(t),
		users:    NewMockUsersAPI(t),
		write:    NewMockCardsWriteAPI(t),
		labels:   NewMockLabelsAPI(t),
		members:  NewMockMembersAPI(t),
	}
	f.svc = NewCards(NewCatalog(f.projects, f.boards), f.boards, f.cards, f.comments, f.users, f.write, f.labels, f.members)
	return f
}

func fixtureCard(t *testing.T) *planka.CardResponse {
	t.Helper()
	var resp planka.CardResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "card"), &resp))
	return &resp
}

func fixtureComments(t *testing.T) *planka.CommentsResponse {
	t.Helper()
	var resp planka.CommentsResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "comments"), &resp))
	return &resp
}

func fixtureArchiveCards(t *testing.T) *planka.CardsResponse {
	t.Helper()
	var resp planka.CardsResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "list_cards_archive"), &resp))
	return &resp
}

// spec describes a card of a synthetic board; labels and users are names.
type spec struct {
	id, title, desc, list string
	labels, users         []string
	pos                   float64
}

// synthBoard builds a board with the active lists Todo and Doing, an archive
// list "<id>-arch", the labels bug and ui, and the users Alice and Me.
func synthBoard(id, projectID, name string, specs ...spec) *planka.BoardResponse {
	f := func(v float64) *float64 { return &v }
	s := func(v string) *string { return &v }
	r := &planka.BoardResponse{Item: planka.Board{ID: id, ProjectID: projectID, Name: name}}
	r.Included.Lists = []planka.List{
		{ID: id + "-doing", BoardID: id, Type: "active", Name: s("Doing"), Position: f(2)},
		{ID: id + "-todo", BoardID: id, Type: "active", Name: s("Todo"), Position: f(1)},
		{ID: id + "-arch", BoardID: id, Type: "archive"},
	}
	r.Included.Labels = []planka.Label{
		{ID: id + "-bug", BoardID: id, Name: s("bug"), Position: 1},
		{ID: id + "-ui", BoardID: id, Name: s("ui"), Position: 2},
	}
	r.Included.Users = []planka.User{
		{ID: "u-alice", Name: "Alice", Username: "alice"},
		{ID: "u-me", Name: "Me Myself", Username: "me.myself"},
	}
	for _, sp := range specs {
		c := planka.Card{ID: sp.id, BoardID: id, ListID: id + "-" + sp.list, Name: sp.title, Position: f(sp.pos)}
		if sp.desc != "" {
			c.Description = s(sp.desc)
		}
		r.Included.Cards = append(r.Included.Cards, c)
		for _, l := range sp.labels {
			r.Included.CardLabels = append(r.Included.CardLabels, planka.CardLabel{CardID: sp.id, LabelID: id + "-" + l})
		}
		for _, u := range sp.users {
			r.Included.CardMemberships = append(r.Included.CardMemberships, planka.CardMembership{CardID: sp.id, UserID: "u-" + u})
		}
	}
	return r
}

// synthProjects has projects Work (boards B1, B2) and Home (board B3).
func synthProjects() *planka.ProjectsResponse {
	r := &planka.ProjectsResponse{Items: []planka.Project{{ID: "pw", Name: "Work"}, {ID: "ph", Name: "Home"}}}
	r.Included.Boards = []planka.Board{
		{ID: "b1", ProjectID: "pw", Name: "B1", Position: 1},
		{ID: "b2", ProjectID: "pw", Name: "B2", Position: 2},
		{ID: "b3", ProjectID: "ph", Name: "B3", Position: 1},
	}
	return r
}

func titles(sums []CardSummary) []string {
	out := make([]string, len(sums))
	for i, s := range sums {
		out[i] = s.Title
	}
	return out
}

// serve answers Projects with synthProjects and each of boards, exactly once.
func (f *cardsFixture) serve(boards ...*planka.BoardResponse) {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	for _, b := range boards {
		f.boards.EXPECT().Board(mock.Anything, b.Item.ID).Return(b, nil).Once()
	}
}

func (f *cardsFixture) serveFixtureBoard(t *testing.T) {
	t.Helper()
	f.projects.EXPECT().Projects(mock.Anything).Return(fixtureProjects(t), nil)
	f.boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(fixtureBoard(t), nil)
}

func TestSearchCarriesCommentCount(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "research search"})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].CommentsTotal)
}

func TestSearchAllWordsAnyOrder(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "readme write"})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Write README", got[0].Title)
	assert.Equal(t, fixtureReadmeID, got[0].ID)
	assert.Equal(t, "Planka MCP", got[0].Board)
	assert.Equal(t, "Open", got[0].List)
	assert.Equal(t, []string{"feature"}, got[0].Labels)
}

func TestSearchMatchesDescription(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "INSTALL configure"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Write README"}, titles(got))
}

// searchWorld is B1 and B2 of project Work and B3 of project Home. B2 has no
// "bug" label.
func searchWorld() (b1, b2, b3 *planka.BoardResponse) {
	b1 = synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Login bug", list: "todo", labels: []string{"bug"}, users: []string{"alice"}, pos: 1},
		spec{id: "c2", title: "Login page", list: "doing", labels: []string{"ui"}, users: []string{"me"}, pos: 1},
	)
	b2 = synthBoard("b2", "pw", "B2", spec{id: "c3", title: "Login docs", list: "todo", pos: 1})
	b2.Included.Labels = b2.Included.Labels[1:]
	b3 = synthBoard("b3", "ph", "B3",
		spec{id: "c4", title: "Login at home", list: "doing", labels: []string{"bug"}, users: []string{"me"}, pos: 1},
	)
	return b1, b2, b3
}

func TestSearchUnicodeCaseInsensitive(t *testing.T) {
	f := newCardsFixture(t)
	b1 := synthBoard("b1", "pw", "B1", spec{id: "c1", title: "Über plan", list: "todo", pos: 1})
	f.serve(b1)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "über", Board: "B1"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Über plan"}, titles(got))
}

func TestSearchTitleMatchesRankFirst(t *testing.T) {
	f := newCardsFixture(t)
	b1 := synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Plan", desc: "mentions the parser", list: "todo", pos: 1},
		spec{id: "c2", title: "Parser rewrite", list: "todo", pos: 2},
		spec{id: "c3", title: "Other", desc: "parser", list: "doing", pos: 1},
	)
	b2 := synthBoard("b2", "pw", "B2", spec{id: "c4", title: "Parser tests", list: "todo", pos: 1})
	b3 := synthBoard("b3", "ph", "B3")
	f.serve(b1, b2, b3)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "parser"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Parser rewrite", "Parser tests", "Plan", "Other"}, titles(got))
}

func TestSearchOrdersByBoardListAndPosition(t *testing.T) {
	f := newCardsFixture(t)
	b1, b2, b3 := searchWorld()
	b1.Included.Cards = append(b1.Included.Cards, planka.Card{
		ID: "c5", BoardID: "b1", ListID: "b1-todo", Name: "Login first", Position: new(0.5),
	})
	f.serve(b1, b2, b3)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Login first", "Login bug", "Login page", "Login docs", "Login at home"}, titles(got))
}

func TestSearchLimit(t *testing.T) {
	f := newCardsFixture(t)
	b1, b2, b3 := searchWorld()
	f.serve(b1, b2, b3)

	got, more, err := f.svc.Search(t.Context(), SearchQuery{Text: "login", Limit: 2})

	require.NoError(t, err)
	assert.Equal(t, []string{"Login bug", "Login page"}, titles(got))
	assert.Equal(t, 2, more, "the two matches the limit left out")
}

func TestSearchWithinTheLimitHasNoMore(t *testing.T) {
	f := newCardsFixture(t)
	b1, b2, b3 := searchWorld()
	f.serve(b1, b2, b3)

	got, more, err := f.svc.Search(t.Context(), SearchQuery{Text: "login"})

	require.NoError(t, err)
	assert.Len(t, got, 4)
	assert.Zero(t, more)
}

func TestSearchFilters(t *testing.T) {
	tests := []struct {
		name    string
		q       SearchQuery
		fetched []string // boards whose Board call is expected
		me      bool
		want    []string
	}{
		{name: "list", q: SearchQuery{List: "doing"}, fetched: []string{"b1", "b2", "b3"}, want: []string{"Login page", "Login at home"}},
		{name: "label skips boards without it", q: SearchQuery{Label: "bug"}, fetched: []string{"b1", "b2", "b3"}, want: []string{"Login bug", "Login at home"}},
		{name: "assignee me", q: SearchQuery{Assignee: "me"}, fetched: []string{"b1", "b2", "b3"}, me: true, want: []string{"Login page", "Login at home"}},
		{name: "assignee by name", q: SearchQuery{Assignee: "alice"}, fetched: []string{"b1", "b2", "b3"}, want: []string{"Login bug"}},
		{name: "board", q: SearchQuery{Board: "B2"}, fetched: []string{"b2"}, want: []string{"Login docs"}},
		{name: "project", q: SearchQuery{Project: "home"}, fetched: []string{"b3"}, want: []string{"Login at home"}},
		{name: "project and board", q: SearchQuery{Project: "work", Board: "B1"}, fetched: []string{"b1"}, want: []string{"Login bug", "Login page"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCardsFixture(t)
			b1, b2, b3 := searchWorld()
			byID := map[string]*planka.BoardResponse{"b1": b1, "b2": b2, "b3": b3}
			boards := make([]*planka.BoardResponse, len(tc.fetched))
			for i, id := range tc.fetched {
				boards[i] = byID[id]
			}
			f.serve(boards...)
			if tc.me {
				f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me"}, nil).Once()
			}
			tc.q.Text = "login"

			got, _, err := f.svc.Search(t.Context(), tc.q)

			require.NoError(t, err)
			assert.Equal(t, tc.want, titles(got))
		})
	}
}

func TestSearchFilterErrors(t *testing.T) {
	t.Run("unknown list on every board", func(t *testing.T) {
		f := newCardsFixture(t)
		b1, b2, b3 := searchWorld()
		f.serve(b1, b2, b3)

		_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login", List: "Nope"})

		var unk *UnknownNameError
		require.ErrorAs(t, err, &unk)
		assert.Equal(t, "list", unk.Kind)
	})
	t.Run("label missing on the named board", func(t *testing.T) {
		f := newCardsFixture(t)
		_, b2, _ := searchWorld()
		f.serve(b2)

		_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login", Board: "B2", Label: "bug"})

		var unk *UnknownNameError
		require.ErrorAs(t, err, &unk)
		assert.Equal(t, "label", unk.Kind)
	})
	t.Run("unknown project", func(t *testing.T) {
		f := newCardsFixture(t)
		f.serve()

		_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login", Project: "Nope"})

		var unk *UnknownNameError
		require.ErrorAs(t, err, &unk)
		assert.Equal(t, "project", unk.Kind)
	})
	t.Run("board fetch fails", func(t *testing.T) {
		f := newCardsFixture(t)
		boom := errors.New("boom")
		f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
		f.boards.EXPECT().Board(mock.Anything, mock.Anything).Return(nil, boom).Maybe()

		_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login"})

		require.ErrorIs(t, err, boom)
	})
}

func TestSearchFetchesBoardsConcurrentlyOncePerBoard(t *testing.T) {
	f := newCardsFixture(t)
	boards := make([]*planka.BoardResponse, 0, 9)
	projects := &planka.ProjectsResponse{Items: []planka.Project{{ID: "p", Name: "P"}}}
	for i := range 9 {
		id := fmt.Sprintf("b%d", i)
		projects.Included.Boards = append(projects.Included.Boards, planka.Board{ID: id, ProjectID: "p", Name: id, Position: float64(i)})
		boards = append(boards, synthBoard(id, "p", id, spec{id: "c" + id, title: "Card " + id, list: "todo", pos: 1}))
	}
	f.projects.EXPECT().Projects(mock.Anything).Return(projects, nil)
	for _, b := range boards {
		f.boards.EXPECT().Board(mock.Anything, b.Item.ID).Return(b, nil).Once()
	}

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "card", Limit: 50})

	require.NoError(t, err)
	require.Len(t, got, 9)
	assert.Equal(t, "Card b0", got[0].Title, "results keep board order however the fetches finish")
	assert.Equal(t, "Card b8", got[8].Title)
}

func TestSearchIncludeArchivedUsesListCards(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: "old"}).
		Return(fixtureArchiveCards(t), nil).Once()

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "old", IncludeArchived: true})

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, fixtureArchivedID, got[0].ID)
	assert.Equal(t, "Old archived task", got[0].Title)
	assert.Equal(t, "Planka MCP", got[0].Board)
	assert.Equal(t, "(archived)", got[0].List, "never mistaken for a kanban list called Archive")
}

func TestSearchWithoutIncludeArchivedNeverReadsArchive(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t) // no ListCards expectation: a call would fail the test

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "old"})

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResolveCardByIDAndURL(t *testing.T) {
	for _, ref := range []string{fixtureReadmeID, "https://plan.example/cards/" + fixtureReadmeID} {
		f := newCardsFixture(t) // no Projects or ListCards expectation: no search
		f.cards.EXPECT().Card(mock.Anything, fixtureReadmeID).Return(fixtureCard(t), nil).Once()
		f.boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(fixtureBoard(t), nil).Once()

		id, snap, err := f.svc.resolve(t.Context(), CardRef{Ref: ref})

		require.NoError(t, err, ref)
		assert.Equal(t, fixtureReadmeID, id)
		assert.Equal(t, fixtureBoardID, snap.resp.Item.ID)
		assert.Equal(t, "Projects", snap.project)
	}
}

func TestResolveCardUnknownID(t *testing.T) {
	f := newCardsFixture(t)
	f.cards.EXPECT().Card(mock.Anything, "99").Return(nil, &planka.APIError{Status: 404, Body: "nope"}).Once()

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "99"})

	require.ErrorIs(t, err, ErrNotFound)
}

func TestResolveCardSingleMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	id, snap, err := f.svc.resolve(t.Context(), CardRef{Ref: "research"})

	require.NoError(t, err)
	assert.Equal(t, fixtureResearchID, id)
	assert.Equal(t, fixtureBoardID, snap.resp.Item.ID)
	assert.Equal(t, "Projects", snap.project)
}

func TestResolveCardAmbiguousListsUpTo10Candidates(t *testing.T) {
	f := newCardsFixture(t)
	specs := make([]spec, 0, 12)
	for i := range 12 {
		specs = append(specs, spec{id: fmt.Sprintf("c%02d", i), title: fmt.Sprintf("Fix %02d", i), list: "doing", pos: float64(i)})
	}
	f.serve(synthBoard("b1", "pw", "B1", specs...))

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "fix", Board: "B1"})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, "card", amb.Kind)
	require.Len(t, amb.Candidates, 10)
	assert.Equal(t, Candidate{ID: "c00", Label: "Fix 00 — B1 / Doing"}, amb.Candidates[0])
	assert.Equal(t, "c09", amb.Candidates[9].ID)
}

func TestResolveCardNoMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.serve(synthBoard("b1", "pw", "B1", spec{id: "c1", title: "Login", list: "todo", pos: 1}))

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "zzz", Board: "B1"})

	var nm *NoMatchError
	require.ErrorAs(t, err, &nm)
	assert.Equal(t, &NoMatchError{Kind: "card", Query: "zzz", Scope: `board "B1"`, ArchiveHint: true}, nm)
}

func TestResolveCardIgnoresArchiveByDefault(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t) // no ListCards expectation: the archive is not read

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "old archived"})

	var nm *NoMatchError
	require.ErrorAs(t, err, &nm)
	assert.True(t, nm.ArchiveHint)
	assert.Contains(t, err.Error(), "include_archived")
}

func TestResolveCardFindsArchivedWhenAsked(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: "old archived"}).
		Return(fixtureArchiveCards(t), nil).Once()

	id, snap, err := f.svc.resolve(t.Context(), CardRef{Ref: "old archived", IncludeArchived: true})

	require.NoError(t, err)
	assert.Equal(t, fixtureArchivedID, id)
	assert.Equal(t, fixtureBoardID, snap.resp.Item.ID)
}

func TestResolveCardNoMatchWithArchiveSearchedHasNoHint(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: "zzz"}).
		Return(&planka.CardsResponse{}, nil).Once()

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "zzz", IncludeArchived: true})

	var nm *NoMatchError
	require.ErrorAs(t, err, &nm)
	assert.False(t, nm.ArchiveHint)
}

// serveCard serves the card and board given and the comments fixture.
func (f *cardsFixture) serveCard(t *testing.T, card *planka.CardResponse, board *planka.BoardResponse) {
	t.Helper()
	f.cards.EXPECT().Card(mock.Anything, fixtureReadmeID).Return(card, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(board, nil).Once()
	f.comments.EXPECT().Comments(mock.Anything, fixtureReadmeID, "").Return(fixtureComments(t), nil).Once()
}

func TestGetCardAssemblesEverything(t *testing.T) {
	f := newCardsFixture(t)
	f.serveCard(t, fixtureCard(t), fixtureBoard(t))

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, "Write README", got.Title)
	assert.Equal(t, "Projects", got.Project)
	assert.Equal(t, "Planka MCP", got.Board)
	assert.Equal(t, "Open", got.List)
	assert.Equal(t, "project", got.Type)
	assert.Equal(t, "Document how to install and configure planka-mcp.", got.Description)
	assert.Equal(t, []string{"feature"}, got.Labels)
	assert.Equal(t, []string{"Admin"}, got.Assignees)
	assert.Equal(t, 1, got.TasksDone)
	assert.Equal(t, 3, got.TasksTotal)
	assert.False(t, got.Created.IsZero())
	assert.Nil(t, got.Stopwatch)

	require.Len(t, got.TaskLists, 1)
	assert.Equal(t, "Tasks", got.TaskLists[0].Name)
	tasks := got.TaskLists[0].Tasks
	require.Len(t, tasks, 3)
	assert.Equal(t, "Draft the install section", tasks[0].Name)
	assert.True(t, tasks[0].Completed)
	assert.Nil(t, tasks[0].Linked)
	require.NotNil(t, tasks[2].Linked)
	assert.Equal(t, fixtureResearchID, tasks[2].Linked.ID)
	assert.Equal(t, "Research search API", tasks[2].Linked.Title)
	assert.Equal(t, "In progress", tasks[2].Linked.List)
	assert.Equal(t, "Planka MCP", tasks[2].Linked.Board)

	require.Len(t, got.Comments, 2)
	assert.Equal(t, "Admin", got.Comments[0].Author)
	assert.Equal(t, "Board-level search is client side for now.", got.Comments[0].Text)
	assert.Equal(t, "Planka has a card search endpoint on lists.", got.Comments[1].Text)
	assert.True(t, got.Comments[0].Created.After(got.Comments[1].Created) || got.Comments[0].Created.Equal(got.Comments[1].Created))
}

func TestGetArchivedCardShowsArchived(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Item.ListID = fixtureArchiveListID
	f.serveCard(t, card, fixtureBoard(t))

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, "(archived)", got.List)
}

func TestGetCardTruncatesComments(t *testing.T) {
	f := newCardsFixture(t)
	f.serveCard(t, fixtureCard(t), fixtureBoard(t))

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 1)

	require.NoError(t, err)
	require.Len(t, got.Comments, 1)
	assert.Equal(t, "Board-level search is client side for now.", got.Comments[0].Text)
}

func TestGetCardByText(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(fixtureProjects(t), nil)
	f.cards.EXPECT().Card(mock.Anything, fixtureReadmeID).Return(fixtureCard(t), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(fixtureBoard(t), nil).Once() // the search's fetch is reused
	f.comments.EXPECT().Comments(mock.Anything, fixtureReadmeID, "").Return(&planka.CommentsResponse{}, nil).Once()

	got, err := f.svc.Get(t.Context(), CardRef{Ref: "write readme"}, 20)

	require.NoError(t, err)
	assert.Equal(t, fixtureReadmeID, got.ID)
	assert.Empty(t, got.Comments)
}

// boardWithoutResearch is the board fixture minus the card the README links to.
func boardWithoutResearch(t *testing.T) *planka.BoardResponse {
	t.Helper()
	b := fixtureBoard(t)
	b.Included.Cards = slices.DeleteFunc(b.Included.Cards, func(c planka.Card) bool { return c.ID == fixtureResearchID })
	return b
}

func TestGetCardFetchesLinkedCardFromForeignBoard(t *testing.T) {
	f := newCardsFixture(t)
	f.serveCard(t, fixtureCard(t), boardWithoutResearch(t))
	f.cards.EXPECT().Card(mock.Anything, fixtureResearchID).Return(&planka.CardResponse{Item: planka.Card{
		ID: fixtureResearchID, BoardID: "other", ListID: "other-doing", Name: "Research search API",
	}}, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "other").Return(synthBoard("other", "pw", "Other"), nil).Once()

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	linked := got.TaskLists[0].Tasks[2].Linked
	require.NotNil(t, linked)
	assert.Equal(t, "Research search API", linked.Title)
	assert.Equal(t, "Other", linked.Board)
	assert.Equal(t, "Doing", linked.List)
}

func TestGetCardLinkTaskShowsTheLinkedCardsCurrentTitle(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	for i, tk := range card.Included.Tasks {
		if tk.LinkedCardID != nil {
			card.Included.Tasks[i].Name = new("Name from before the card was renamed")
		}
	}
	f.serveCard(t, card, fixtureBoard(t))

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, "Research search API", got.TaskLists[0].Tasks[2].Name)
}

func TestGetCardLinkedFetchFailureKeepsTaskName(t *testing.T) {
	f := newCardsFixture(t)
	f.serveCard(t, fixtureCard(t), boardWithoutResearch(t))
	f.cards.EXPECT().Card(mock.Anything, fixtureResearchID).Return(nil, errors.New("boom")).Once()

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, &CardSummary{ID: fixtureResearchID, Title: "Research search API"}, got.TaskLists[0].Tasks[2].Linked)
}

func TestGetCardStopwatchAttachmentsAndCustomFields(t *testing.T) {
	f := newCardsFixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	card := fixtureCard(t)
	started := now.Add(-90 * time.Second)
	card.Item.Stopwatch = &planka.Stopwatch{StartedAt: &started, Total: 60}
	card.Included.Attachments = []planka.Attachment{
		{ID: "a1", Name: "spec.pdf", Type: "file", Data: map[string]any{"url": "https://files/spec.pdf", "size": 12}},
		{ID: "a2", Name: "no url", Type: "file"},
	}
	groupName := "Release"
	card.Included.CustomFieldGroups = []planka.CustomFieldGroup{{ID: "g1", Name: &groupName}, {ID: "g2"}}
	card.Included.CustomFields = []planka.CustomField{
		{ID: "f1", CustomFieldGroupID: "g1", Name: "Version"}, {ID: "f2", CustomFieldGroupID: "g2", Name: "Owner"},
	}
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CustomFieldGroupID: "g1", CustomFieldID: "f1", Content: "1.2"},
		{ID: "v2", CustomFieldGroupID: "g2", CustomFieldID: "f2", Content: "Ann"},
	}
	f.serveCard(t, card, fixtureBoard(t))

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, &Stopwatch{Total: 150 * time.Second, Running: true}, got.Stopwatch)
	assert.Equal(t, []Attachment{{Name: "spec.pdf", URL: "https://files/spec.pdf"}, {Name: "no url"}}, got.Attachments)
	assert.Equal(t, []CustomFieldGroup{
		{Name: "Release", Fields: []CustomField{{Name: "Version", Value: "1.2"}}},
		{Fields: []CustomField{{Name: "Owner", Value: "Ann"}}},
	}, got.CustomFields)
}

func TestGetCardNamesCustomFieldsOfBoardGroups(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CardID: fixtureReadmeID, CustomFieldGroupID: "bg1", CustomFieldID: "bf1", Content: "Zerobyte"},
	}
	board := fixtureBoard(t)
	board.Included.CustomFieldGroups = []planka.CustomFieldGroup{{ID: "bg1", BoardID: new(fixtureBoardID), Name: new("Backup")}}
	board.Included.CustomFields = []planka.CustomField{{ID: "bf1", CustomFieldGroupID: "bg1", Name: "Tool"}}
	f.serveCard(t, card, board)

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{{Name: "Backup", Fields: []CustomField{{Name: "Tool", Value: "Zerobyte"}}}}, got.CustomFields)
}

func TestGetCardNamesCustomFieldsOfProjectBaseGroups(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Included.CustomFieldGroups = []planka.CustomFieldGroup{
		{ID: "g1", CardID: new(fixtureReadmeID), BaseCustomFieldGroupID: new("base1")},
	}
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CardID: fixtureReadmeID, CustomFieldGroupID: "g1", CustomFieldID: "basef1", Content: "Zerobyte"},
	}
	f.serveCard(t, card, fixtureBoard(t))
	f.projects.EXPECT().Projects(mock.Anything).Return(&planka.ProjectsResponse{Included: planka.ProjectsIncluded{
		BaseCustomFieldGroups: []planka.BaseCustomFieldGroup{{ID: "base1", Name: "Backup"}},
		CustomFields:          []planka.CustomField{{ID: "basef1", BaseCustomFieldGroupID: new("base1"), Name: "Tool"}},
	}}, nil).Once()

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{{Name: "Backup", Fields: []CustomField{{Name: "Tool", Value: "Zerobyte"}}}}, got.CustomFields)
}

func TestGetCardKeepsCustomFieldValuesWhenBaseGroupsFailToLoad(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Included.CustomFieldGroups = []planka.CustomFieldGroup{
		{ID: "g1", CardID: new(fixtureReadmeID), BaseCustomFieldGroupID: new("base1")},
	}
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CardID: fixtureReadmeID, CustomFieldGroupID: "g1", CustomFieldID: "basef1", Content: "Zerobyte"},
	}
	f.serveCard(t, card, fixtureBoard(t))
	f.projects.EXPECT().Projects(mock.Anything).Return(nil, errors.New("boom")).Once()

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{{Fields: []CustomField{{Value: "Zerobyte"}}}}, got.CustomFields)
}

// TestGetCardGroupsCustomFieldsInPlankaOrder: values are grouped by their group, the board's groups
// before the card's own, each group and each field in Planka's position order, however the values come.
func TestGetCardGroupsCustomFieldsInPlankaOrder(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Included.CustomFieldGroups = []planka.CustomFieldGroup{{ID: "cg", CardID: new(fixtureReadmeID), Name: new("Notes"), Position: 1}}
	card.Included.CustomFields = []planka.CustomField{{ID: "fC1", CustomFieldGroupID: "cg", Name: "Remark", Position: 1}}
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CustomFieldGroupID: "cg", CustomFieldID: "fC1", Content: "check"},
		{ID: "v2", CustomFieldGroupID: "bgA", CustomFieldID: "fA1", Content: "1.2"},
		{ID: "v3", CustomFieldGroupID: "bgB", CustomFieldID: "fB2", Content: "Zerobyte"},
		{ID: "v4", CustomFieldGroupID: "bgB", CustomFieldID: "fB1", Content: "Boxy, Roky"},
	}
	board := fixtureBoard(t)
	board.Included.CustomFieldGroups = []planka.CustomFieldGroup{
		{ID: "bgA", BoardID: new(fixtureBoardID), Name: new("Release"), Position: 2},
		{ID: "bgB", BoardID: new(fixtureBoardID), Name: new("Context"), Position: 1},
	}
	board.Included.CustomFields = []planka.CustomField{
		{ID: "fA1", CustomFieldGroupID: "bgA", Name: "Version", Position: 1},
		{ID: "fB2", CustomFieldGroupID: "bgB", Name: "Services affected", Position: 2},
		{ID: "fB1", CustomFieldGroupID: "bgB", Name: "Servers affected", Position: 1, ShowOnFrontOfCard: true},
	}
	f.serveCard(t, card, board)

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{
		{Name: "Context", Fields: []CustomField{
			{Name: "Servers affected", Value: "Boxy, Roky", ShowOnFrontOfCard: true},
			{Name: "Services affected", Value: "Zerobyte"},
		}},
		{Name: "Release", Fields: []CustomField{{Name: "Version", Value: "1.2"}}},
		{Name: "Notes", Fields: []CustomField{{Name: "Remark", Value: "check"}}},
	}, got.CustomFields)
}

func TestCommentsByIDWithPaging(t *testing.T) {
	f := newCardsFixture(t) // no Card or Board expectation: an ID needs neither
	f.comments.EXPECT().Comments(mock.Anything, fixtureResearchID, "c-before").Return(fixtureComments(t), nil).Once()

	got, older, err := f.svc.Comments(t.Context(), CardRef{Ref: fixtureResearchID}, "c-before", 1)

	require.NoError(t, err)
	assert.True(t, older, "the limit cut off a comment")
	require.Len(t, got, 1)
	assert.Equal(t, "Admin", got[0].Author)
	assert.Equal(t, "Board-level search is client side for now.", got[0].Text)
}

func TestCommentsByText(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	f.comments.EXPECT().Comments(mock.Anything, fixtureResearchID, "").Return(fixtureComments(t), nil).Once()

	got, older, err := f.svc.Comments(t.Context(), CardRef{Ref: "research search"}, "", 20)

	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.False(t, older, "Planka's page was not full")
}

func TestCommentsFullPlankaPageMayHaveOlder(t *testing.T) {
	f := newCardsFixture(t)
	page := &planka.CommentsResponse{}
	for i := range plankaCommentsPage {
		page.Items = append(page.Items, planka.Comment{ID: fmt.Sprint("c", i), Text: "x"})
	}
	f.comments.EXPECT().Comments(mock.Anything, fixtureResearchID, "").Return(page, nil).Once()

	got, older, err := f.svc.Comments(t.Context(), CardRef{Ref: fixtureResearchID}, "", plankaCommentsPage)

	require.NoError(t, err)
	assert.Len(t, got, plankaCommentsPage)
	assert.True(t, older, "a full page from Planka may have older comments behind it")
}

func TestSearchWordsSplitAcrossTitleAndDescriptionDoNotMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	// "readme" is in the title of Write README, "install" only in its description.
	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "readme install"})

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSearchSplitsWordsOnCommasAndSemicolons(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "readme,write; "})

	require.NoError(t, err)
	assert.Equal(t, []string{"Write README"}, titles(got))
}

func TestSearchArchiveNeutralisesLeadingSlash(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: " /old"}).
		Return(&planka.CardsResponse{}, nil).Once()

	_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "/old", IncludeArchived: true})

	require.NoError(t, err)
}

func TestSearchArchiveTextTooLong(t *testing.T) {
	f := newCardsFixture(t) // no expectation: the check comes before any read
	long := strings.Repeat("a", 129)

	_, _, err := f.svc.Search(t.Context(), SearchQuery{Text: long, IncludeArchived: true})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "128")

	f = newCardsFixture(t)
	f.serveFixtureBoard(t)
	_, _, err = f.svc.Search(t.Context(), SearchQuery{Text: long})
	require.NoError(t, err, "without the archive there is no limit")
}

func TestSearchOrdersArchivedHitsByListChangedAt(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	archived := &planka.CardsResponse{Items: []planka.Card{
		{ID: "a1", BoardID: fixtureBoardID, ListID: fixtureArchiveListID, Name: "Old one", ListChangedAt: day(1)},
		{ID: "a3", BoardID: fixtureBoardID, ListID: fixtureArchiveListID, Name: "Old three", ListChangedAt: day(3)},
		{ID: "a2b", BoardID: fixtureBoardID, ListID: fixtureArchiveListID, Name: "Old two b", ListChangedAt: day(2)},
		{ID: "a2a", BoardID: fixtureBoardID, ListID: fixtureArchiveListID, Name: "Old two a", ListChangedAt: day(2)},
	}}
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: "old"}).Return(archived, nil).Once()

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "old", IncludeArchived: true})

	require.NoError(t, err)
	assert.Equal(t, []string{"Old three", "Old two a", "Old two b", "Old one"}, titles(got))
}

func TestSearchListFilterArchiveIsAnOrdinaryListName(t *testing.T) {
	f := newCardsFixture(t) // no ListCards expectation: the archive is never read
	b1 := synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Login kept", list: "kanbanarch", pos: 1},
		spec{id: "c2", title: "Login elsewhere", list: "todo", pos: 1},
	)
	name := "Archive"
	b1.Included.Lists = append(b1.Included.Lists, planka.List{ID: "b1-kanbanarch", BoardID: "b1", Type: "active", Name: &name, Position: new(3.0)})
	f.serve(b1)

	got, _, err := f.svc.Search(t.Context(), SearchQuery{Text: "login", Board: "B1", List: "Archive"})

	require.NoError(t, err)
	assert.Equal(t, []string{"Login kept"}, titles(got))
	assert.Equal(t, "Archive", got[0].List)

	f = newCardsFixture(t)
	f.serve(synthBoard("b1", "pw", "B1"))
	_, _, err = f.svc.Search(t.Context(), SearchQuery{Text: "login", Board: "B1", List: "Archive", IncludeArchived: true})
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk, "with no kanban list of that name, the archive does not stand in; no ListCards either")
}

func TestResolveCardAmbiguousAcrossKanbanAndArchive(t *testing.T) {
	f := newCardsFixture(t)
	f.serveFixtureBoard(t)
	archived := &planka.CardsResponse{Items: []planka.Card{
		{ID: "a1", BoardID: fixtureBoardID, ListID: fixtureArchiveListID, Name: "Research archive notes"},
	}}
	f.cards.EXPECT().ListCards(mock.Anything, fixtureArchiveListID, planka.CardQuery{Search: "research"}).Return(archived, nil).Once()

	_, _, err := f.svc.resolve(t.Context(), CardRef{Ref: "research", IncludeArchived: true})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{
		{ID: fixtureResearchID, Label: "Research search API — Planka MCP / In progress"},
		{ID: "a1", Label: "Research archive notes — Planka MCP / (archived)"},
	}, amb.Candidates)
}

func TestOverlayDoesNotDuplicateUsers(t *testing.T) {
	snap := fixtureSnapshot(t)
	ov := snap.overlay(planka.BoardIncluded{Users: snap.resp.Included.Users})

	assert.Len(t, ov.resp.Included.Users, len(snap.resp.Included.Users))
	u, err := ov.userByName("Admin", "")
	require.NoError(t, err)
	assert.Equal(t, "admin", u.Username)
}
