package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// listsFixture wires Lists to mocks; the board is writeBoard: Todo (position
// 1), Doing (2) and the archive.
type listsFixture struct {
	*cardsFixture
	api   *MockListsAPI
	lists *Lists
}

func newListsFixture(t *testing.T) *listsFixture {
	t.Helper()
	f := &listsFixture{cardsFixture: newCardsFixture(t), api: NewMockListsAPI(t)}
	f.lists = NewLists(f.svc, f.api)
	return f
}

// withList is writeBoard plus one more column.
func withList(l planka.List) *planka.BoardResponse {
	b := writeBoard()
	b.Included.Lists = append(b.Included.Lists, l)
	return b
}

// serveBoards answers the board name lookup, then each board read in order:
// the read under the write lock, then the read back.
func (f *listsFixture) serveBoards(boards ...*planka.BoardResponse) {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	for _, b := range boards {
		f.boards.EXPECT().Board(mock.Anything, "b1").Return(b, nil).Once()
	}
}

func TestCreateListGoesLastByDefault(t *testing.T) {
	f := newListsFixture(t)
	review := planka.List{ID: "l9", BoardID: "b1", Type: "active", Name: new("Review"), Position: new(65538.0)}
	f.serveBoards(writeBoard(), withList(review))
	f.api.EXPECT().CreateList(mock.Anything, "b1", planka.CreateListRequest{Type: "active", Position: 2 + 65536, Name: "Review"}).
		Return(&review, nil).Once()

	got, err := f.lists.Create(t.Context(), CreateList{Board: "B1", Name: " Review "})

	require.NoError(t, err)
	assert.Equal(t, Column{ID: "l9", Name: "Review"}, got.List)
	assert.Equal(t, []string{"Todo", "Doing", "Review"}, got.Order)
}

func TestCreateListPlacement(t *testing.T) {
	// Later has a numeric ID, as real Planka IDs are, so it can be named by ID.
	later := planka.List{ID: "5005", BoardID: "b1", Type: "active", Name: new("Later"), Position: new(3.0)}
	for name, tc := range map[string]struct {
		place ListPlace
		want  float64
	}{
		"after the first":  {ListPlace{After: "todo"}, 1.5},
		"after the last":   {ListPlace{After: "Later"}, 3 + 65536},
		"before the first": {ListPlace{Before: "Todo"}, 0.5},
		"before by ID":     {ListPlace{Before: "5005"}, 2.5},
	} {
		t.Run(name, func(t *testing.T) {
			f := newListsFixture(t)
			l := planka.List{ID: "l9", BoardID: "b1", Type: "active", Name: new("Review"), Position: new(tc.want)}
			f.serveBoards(withList(later), withList(l))
			f.api.EXPECT().CreateList(mock.Anything, "b1", planka.CreateListRequest{Type: "active", Position: tc.want, Name: "Review"}).
				Return(&l, nil).Once()

			_, err := f.lists.Create(t.Context(), CreateList{Board: "B1", Name: "Review", Place: tc.place})

			require.NoError(t, err)
		})
	}
}

func TestCreateListClosedWithColor(t *testing.T) {
	f := newListsFixture(t)
	done := planka.List{ID: "l9", BoardID: "b1", Type: "closed", Name: new("Done"), Position: new(65538.0), Color: new("bright-moss")}
	f.serveBoards(writeBoard(), withList(done))
	f.api.EXPECT().CreateList(mock.Anything, "b1", planka.CreateListRequest{Type: "closed", Position: 65538, Name: "Done"}).
		Return(&planka.List{ID: "l9", Type: "closed"}, nil).Once()
	f.api.EXPECT().UpdateList(mock.Anything, "l9", planka.UpdateListRequest{Color: planka.Some("bright-moss")}).Return(&done, nil).Once()

	got, err := f.lists.Create(t.Context(), CreateList{Board: "B1", Name: "Done", Closed: true, Color: "bright-moss"})

	require.NoError(t, err)
	assert.Equal(t, Column{ID: "l9", Name: "Done", Color: "bright-moss", Closed: true}, got.List)
}

func TestCreateListColorFailureSaysTheListWasCreated(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard())
	f.api.EXPECT().CreateList(mock.Anything, "b1", mock.Anything).Return(&planka.List{ID: "l9"}, nil).Once()
	f.api.EXPECT().UpdateList(mock.Anything, "l9", mock.Anything).Return(nil, errors.New("boom")).Once()

	_, err := f.lists.Create(t.Context(), CreateList{Board: "B1", Name: "Done", Color: "bright-moss"})

	require.EqualError(t, err, `list "Done" (id l9) was created, but setting its color failed: boom`)
}

// TestCreateListRefusesBeforeWriting: each of these is an error, and no write
// mock is set up, so nothing may be written.
func TestCreateListRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		in   CreateList
		want string
	}{
		"empty name": {CreateList{Board: "B1", Name: "  "}, "list name is empty"},
		"long name":  {CreateList{Board: "B1", Name: string(make([]rune, 129))}, "list name is longer than 128 characters"},
		// Planka counts UTF-16 units, as JavaScript does: 65 emoji are 130.
		"long in UTF-16":    {CreateList{Board: "B1", Name: strings.Repeat("🚀", 65)}, "list name is longer than 128 characters"},
		"unknown color":     {CreateList{Board: "B1", Name: "X", Color: "green"}, `unknown list color "green"; colors: berry-red, pumpkin-orange`},
		"after and before":  {CreateList{Board: "B1", Name: "X", Place: ListPlace{After: "Todo", Before: "Doing"}}, "give after or before, not both"},
		"duplicate name":    {CreateList{Board: "B1", Name: "doing"}, `board "B1" already has a list "Doing" (id b1-doing): pick another name`},
		"unknown neighbour": {CreateList{Board: "B1", Name: "X", Place: ListPlace{After: "Nope"}}, `no list "Nope" on board "B1"; lists: Todo, Doing`},
		"prefix neighbour":  {CreateList{Board: "B1", Name: "X", Place: ListPlace{After: "Do"}}, `no list "Do" on board "B1"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newListsFixture(t)
			f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Maybe()
			f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Maybe()

			_, err := f.lists.Create(t.Context(), tc.in)

			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestUpdateListChangesFieldsThenSortsThenArchives(t *testing.T) {
	f := newListsFixture(t)
	before := writeBoard()
	before.Included.Lists[0].Color = new("berry-red") // Doing, so there is a colour to clear
	after := writeBoard()
	after.Included.Lists[0].Name = new("Finished")
	after.Included.Lists[0].Type = "closed"
	f.serveBoards(before, after)
	var order []string
	f.api.EXPECT().UpdateList(mock.Anything, "b1-doing", planka.UpdateListRequest{
		Type: planka.Some("closed"), Position: planka.Some(0.5), Name: planka.Some("Finished"), Color: planka.Null[string](),
	}).RunAndReturn(func(_ context.Context, _ string, _ planka.UpdateListRequest) (*planka.List, error) {
		order = append(order, "update")
		return &planka.List{ID: "b1-doing"}, nil
	}).Once()
	f.api.EXPECT().SortList(mock.Anything, "b1-doing", "dueDate", "desc").RunAndReturn(func(context.Context, string, string, string) error {
		order = append(order, "sort")
		return nil
	}).Once()
	// Planka answers with the cards it moved: three, though the board read
	// before the write held two, as when someone adds a card in the meantime.
	f.api.EXPECT().MoveListCards(mock.Anything, "b1-doing", "b1-arch").RunAndReturn(func(context.Context, string, string) ([]planka.Card, error) {
		order = append(order, "archive")
		return make([]planka.Card, 3), nil
	}).Once()

	got, err := f.lists.Update(t.Context(), UpdateList{
		Board: "B1", List: "doing", Name: new("Finished"), Color: "none", Closed: new(true),
		Place: ListPlace{Before: "Todo"}, SortBy: "due_date", SortOrder: "desc", ArchiveCards: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"update", "sort", "archive"}, order)
	assert.Equal(t, 3, got.Archived, "the cards Planka moved")
	assert.Equal(t, "Finished", got.List.Name)
	assert.True(t, got.List.Closed)
}

func TestUpdateListSortsAscendingByDefault(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard(), writeBoard())
	f.api.EXPECT().SortList(mock.Anything, "b1-todo", "name", "asc").Return(nil).Once()

	got, err := f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Todo", SortBy: "name"})

	require.NoError(t, err)
	assert.Zero(t, got.Archived)
}

// TestUpdateListRefusesBeforeWriting: each of these is an error, and no write
// mock is set up, so nothing may be written.
func TestUpdateListRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		in   UpdateList
		want string
	}{
		"nothing to change": {UpdateList{Board: "B1", List: "Todo"}, "nothing to change: give name, color, closed, after, before, sort_cards or archive_cards"},
		"archive an active list": {UpdateList{Board: "B1", List: "Todo", ArchiveCards: true},
			`list "Todo" is not closed: Planka archives all of a list's cards only from a closed list; set closed=true too, or move the cards one by one`},
		"unknown sort":       {UpdateList{Board: "B1", List: "Todo", SortBy: "size"}, `unknown sort "size": use name, due_date or created`},
		"order without sort": {UpdateList{Board: "B1", List: "Todo", SortOrder: "desc"}, "sort_order needs sort_cards"},
		"after itself":       {UpdateList{Board: "B1", List: "Todo", Place: ListPlace{After: "todo"}}, "a list cannot go after or before itself"},
		"rename onto another": {UpdateList{Board: "B1", List: "Todo", Name: new("DOING")},
			`board "B1" already has a list "Doing" (id b1-doing): pick another name`},
		"prefix target": {UpdateList{Board: "B1", List: "Do", Name: new("X")}, `no list "Do" on board "B1"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newListsFixture(t)
			f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Maybe()
			f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Maybe()

			_, err := f.lists.Update(t.Context(), tc.in)

			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestUpdateListArchiveFailureSaysWhatWasApplied(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard())
	f.api.EXPECT().UpdateList(mock.Anything, "b1-doing", mock.Anything).Return(&planka.List{ID: "b1-doing"}, nil).Once()
	f.api.EXPECT().SortList(mock.Anything, "b1-doing", "name", "asc").Return(nil).Once()
	f.api.EXPECT().MoveListCards(mock.Anything, "b1-doing", "b1-arch").Return(nil, errors.New("boom")).Once()

	_, err := f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Doing", Closed: new(true), SortBy: "name", ArchiveCards: true})

	require.EqualError(t, err, `list "Doing" was partly updated (fields updated, cards sorted), but archiving its cards failed: boom`)
}

func TestUpdateListFailureAfterAWriteSaysWhatWasApplied(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard())
	f.api.EXPECT().UpdateList(mock.Anything, "b1-todo", mock.Anything).Return(&planka.List{ID: "b1-todo"}, nil).Once()
	f.api.EXPECT().SortList(mock.Anything, "b1-todo", "name", "asc").Return(errors.New("boom")).Once()

	_, err := f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Todo", Name: new("Backlog"), SortBy: "name"})

	require.EqualError(t, err, `list "Todo" was partly updated (fields updated), but sorting its cards failed: boom`)
}

func TestDeleteListMovesItsCardsToTrash(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard())
	f.api.EXPECT().DeleteList(mock.Anything, "b1-doing").Return(make([]planka.Card, 2), nil).Once()

	got, err := f.lists.Delete(t.Context(), "B1", "Doing")

	require.NoError(t, err)
	assert.Equal(t, &DeletedList{ID: "b1-doing", Name: "Doing", CardsToTrash: 2}, got)
}

func TestDeleteListThatIsGoneIsNotFound(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard())
	f.api.EXPECT().DeleteList(mock.Anything, "b1-doing").Return(nil, &planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.lists.Delete(t.Context(), "B1", "Doing")

	require.ErrorIs(t, err, ErrNotFound)
}

// TestListWritesRefuseADuplicateName: two lists made with one name in
// Planka's web UI make the name ambiguous; the error carries both IDs.
func TestListWritesRefuseADuplicateName(t *testing.T) {
	twin := planka.List{ID: "b1-doing2", BoardID: "b1", Type: "active", Name: new("Doing"), Position: new(3.0)}
	f := newListsFixture(t)
	f.serveBoards(withList(twin), withList(twin))

	_, err := f.lists.Delete(t.Context(), "B1", "doing")
	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{{ID: "b1-doing", Label: "Doing"}, {ID: "b1-doing2", Label: "Doing"}}, amb.Candidates)

	_, err = f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Doing", Name: new("Busy")})
	require.ErrorAs(t, err, &amb)
}

func TestUpdateListRenameThatOnlyChangesCase(t *testing.T) {
	f := newListsFixture(t)
	f.serveBoards(writeBoard(), writeBoard())
	f.api.EXPECT().UpdateList(mock.Anything, "b1-todo", planka.UpdateListRequest{Name: planka.Some("TODO")}).
		Return(&planka.List{ID: "b1-todo"}, nil).Once()

	_, err := f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Todo", Name: new("TODO")})

	require.NoError(t, err, "a list does not clash with its own name")
}

func TestUpdateListArchiveNeedsAnArchiveList(t *testing.T) {
	f := newListsFixture(t)
	b := writeBoard()
	b.Included.Lists = b.Included.Lists[:2] // Doing and Todo, no archive
	f.serveBoards(b)

	_, err := f.lists.Update(t.Context(), UpdateList{Board: "B1", List: "Doing", Closed: new(true), ArchiveCards: true})

	require.EqualError(t, err, `board "B1" has no archive list`)
}

// TestListNameCountsUTF16Units: Planka checks the length as JavaScript
// measures it, so an emoji counts two and an accented letter one.
func TestListNameCountsUTF16Units(t *testing.T) {
	for name, tc := range map[string]struct {
		name string
		ok   bool
	}{
		"128 accented letters": {strings.Repeat("é", 128), true},
		"64 emoji":             {strings.Repeat("🚀", 64), true},
		"65 emoji":             {strings.Repeat("🚀", 65), false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := listName(tc.name)
			assert.Equal(t, tc.ok, err == nil, "error: %v", err)
		})
	}
}

// TestListPositionSkipsListsWithoutOne: Planka allows a null position, which
// sorts last; such a list has no neighbours to go between.
func TestListPositionSkipsListsWithoutOne(t *testing.T) {
	imported := planka.List{ID: "b1-imp", BoardID: "b1", Type: "active", Name: new("Imported")}
	for name, place := range map[string]ListPlace{
		"last":                    {},
		"after the last":          {After: "Doing"},
		"after the unpositioned":  {After: "Imported"},
		"before the unpositioned": {Before: "Imported"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := listPosition(newSnapshot(withList(imported), "P"), place, "")

			require.NoError(t, err)
			assert.InDelta(t, 2.0+65536, got, 1e-9)
		})
	}
}

func TestDeleteListNeedsTheExactNameOrID(t *testing.T) {
	f := newListsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil)

	_, err := f.lists.Delete(t.Context(), "B1", "Do")

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown, "a prefix must not delete a list")
	assert.Equal(t, []string{"Todo", "Doing"}, unknown.Valid)
}

// TestListSortsHaveAField: every sort the tools offer maps to a Planka field.
func TestListSortsHaveAField(t *testing.T) {
	for _, s := range ListSorts {
		assert.NotEmpty(t, sortFields[s], s)
	}
	assert.Len(t, sortFields, len(ListSorts))
}
