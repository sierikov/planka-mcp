package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

type boardsFixture struct {
	*cardsFixture
	api   *MockBoardWriteAPI
	lists *MockListsAPI
	admin *Boards
}

func newBoardsFixture(t *testing.T) *boardsFixture {
	t.Helper()
	f := &boardsFixture{cardsFixture: newCardsFixture(t), api: NewMockBoardWriteAPI(t), lists: NewMockListsAPI(t)}
	f.admin = NewBoards(f.svc, f.lists, f.api)
	return f
}

// memberBoard is writeBoard with membership IDs: Alice (m-alice) and me
// (m-me) are editors.
func memberBoard() *planka.BoardResponse {
	b := writeBoard()
	b.Included.BoardMemberships[0].ID = "m-alice"
	b.Included.BoardMemberships[1].ID = "m-me"
	return b
}

// lockedBoard expects the reads of a board write on B1 found by name.
func (f *boardsFixture) lockedBoard(b *planka.BoardResponse) {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(b, nil).Once()
}

func TestCreateBoardWithListsGoesLastInItsProject(t *testing.T) {
	f := newBoardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()
	f.api.EXPECT().CreateBoard(mock.Anything, "pw", "Roadmap", 2.0+positionGap).Return(&planka.Board{ID: "b9", Name: "Roadmap"}, nil).Once()
	for i, l := range []planka.CreateListRequest{
		{Type: "active", Position: positionGap, Name: "Todo"},
		{Type: "active", Position: 2 * positionGap, Name: "Doing"},
		{Type: "closed", Position: 3 * positionGap, Name: "Done"},
	} {
		f.lists.EXPECT().CreateList(mock.Anything, "b9", l).Return(&planka.List{ID: "l" + string(rune('1'+i))}, nil).Once()
	}
	created := synthBoard("b9", "pw", "Roadmap")
	f.boards.EXPECT().Board(mock.Anything, "b9").Return(created, nil).Once()

	got, err := f.admin.Create(t.Context(), CreateBoard{Project: "work", Name: " Roadmap ", Lists: []string{"Todo", "Doing"}, ClosedList: "Done"})

	require.NoError(t, err)
	assert.Equal(t, "b9", got.ID)
	assert.Equal(t, "Work", got.Project)
}

func TestCreateBoardRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		in   CreateBoard
		read bool
		want string
	}{
		"no name":         {CreateBoard{Project: "Work", Name: " "}, false, "board name is empty"},
		"list twice":      {CreateBoard{Project: "Work", Name: "X", Lists: []string{"Todo"}, ClosedList: "todo"}, false, `list "todo" is given twice`},
		"unknown project": {CreateBoard{Project: "Nope", Name: "X"}, true, `no project "Nope"`},
		"name taken":      {CreateBoard{Project: "Work", Name: "b2"}, true, `project "Work" already has a board "B2"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBoardsFixture(t)
			if tc.read {
				f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()
			}

			_, err := f.admin.Create(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCreateBoardExplainsAProjectManagerIsNeeded(t *testing.T) {
	f := newBoardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()
	f.api.EXPECT().CreateBoard(mock.Anything, "ph", "X", mock.Anything).Return(nil, &planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.admin.Create(t.Context(), CreateBoard{Project: "Home", Name: "X"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `only a project manager of the project can create a board in project "Home", and Planka refused (HTTP 404)`)
}

func TestCreateBoardSaysTheBoardExistsWhenAListFails(t *testing.T) {
	f := newBoardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()
	f.api.EXPECT().CreateBoard(mock.Anything, "ph", "X", mock.Anything).Return(&planka.Board{ID: "b9"}, nil).Once()
	f.lists.EXPECT().CreateList(mock.Anything, "b9", mock.Anything).Return(nil, &planka.APIError{Status: http.StatusBadGateway}).Once()

	_, err := f.admin.Create(t.Context(), CreateBoard{Project: "Home", Name: "X", Lists: []string{"Todo"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `board "X" (id b9) was created, but creating its list "Todo" failed`)
}

func TestRenameBoard(t *testing.T) {
	f := newBoardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()
	f.api.EXPECT().UpdateBoard(mock.Anything, "b1", "Roadmap").Return(&planka.Board{ID: "b1", Name: "Roadmap"}, nil).Once()

	got, err := f.admin.Rename(t.Context(), "B1", "Roadmap")

	require.NoError(t, err)
	assert.Equal(t, &BoardRef{ID: "b1", Name: "Roadmap", Project: "Work"}, got)
}

func TestRenameBoardRefusesAClashInItsProject(t *testing.T) {
	f := newBoardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Once()

	_, err := f.admin.Rename(t.Context(), "B1", "b2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `project "Work" already has a board "B2"`)
}

func TestDeleteBoardWithCardsIsRefused(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(writeBoard())
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", planka.CardQuery{}).Return(&planka.CardsResponse{Items: []planka.Card{{ID: "c9"}}}, nil).Once()

	_, err := f.admin.Delete(t.Context(), "B1", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `board "B1" has 4 cards, archived ones included`)
	assert.Contains(t, err.Error(), "with_cards=true")
}

func TestDeleteBoardWithCards(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(writeBoard())
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", planka.CardQuery{}).Return(&planka.CardsResponse{Items: fullPage()}, nil).Once()
	f.api.EXPECT().DeleteBoard(mock.Anything, "b1").Return(nil).Once()

	got, err := f.admin.Delete(t.Context(), "B1", true)

	require.NoError(t, err)
	assert.Equal(t, &DeletedBoard{ID: "b1", Name: "B1", Project: "Work", Cards: 3 + plankaCardsPage, AtLeast: true}, got)
}

func TestDeleteEmptyBoard(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(synthBoard("b1", "pw", "B1"))
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", planka.CardQuery{}).Return(&planka.CardsResponse{}, nil).Once()
	f.api.EXPECT().DeleteBoard(mock.Anything, "b1").Return(&planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.admin.Delete(t.Context(), "B1", false)

	require.Error(t, err, "nothing guards an empty board, but Planka may still refuse")
	assert.Contains(t, err.Error(), "only a project manager")
}

func TestAddMemberFromAllUsers(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(memberBoard())
	f.api.EXPECT().Users(mock.Anything).Return([]planka.User{{ID: "u-bob", Name: "Bob Builder", Username: "bob", Email: "bob@example.com"}}, nil).Once()
	f.api.EXPECT().CreateBoardMembership(mock.Anything, "b1", planka.BoardMembershipRequest{
		UserID: planka.Some("u-bob"), Role: planka.Some("viewer"), CanComment: planka.Some(true),
	}).Return(&planka.BoardMembership{ID: "m-bob", UserID: "u-bob", Role: "viewer", CanComment: new(true)}, nil).Once()

	got, err := f.admin.AddMember(t.Context(), AddMember{Board: "B1", Person: "BOB@example.com", Role: "viewer", CanComment: new(true)})

	require.NoError(t, err)
	assert.Equal(t, &Member{ID: "m-bob", UserID: "u-bob", Name: "Bob Builder", Username: "bob", Role: "viewer", CanComment: true}, got)
}

func TestAddMemberRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		in    AddMember
		read  bool
		users func(*boardsFixture)
		want  string
	}{
		"bad role":           {AddMember{Board: "B1", Person: "bob", Role: "owner"}, false, nil, `unknown role "owner"`},
		"editor can_comment": {AddMember{Board: "B1", Person: "bob", Role: "editor", CanComment: new(true)}, false, nil, "an editor can always comment"},
		"already on board":   {AddMember{Board: "B1", Person: "alice", Role: "viewer"}, true, nil, "Alice is already on board \"B1\" as editor"},
		"cannot list users": {AddMember{Board: "B1", Person: "bob", Role: "viewer"}, true, func(f *boardsFixture) {
			f.api.EXPECT().Users(mock.Anything).Return(nil, &planka.APIError{Status: http.StatusForbidden}).Once()
		}, "only a Planka admin or project owner can add other people"},
		"unknown person": {AddMember{Board: "B1", Person: "zed", Role: "viewer"}, true, func(f *boardsFixture) {
			f.api.EXPECT().Users(mock.Anything).Return([]planka.User{{ID: "u-bob", Username: "bob"}}, nil).Once()
		}, `no user "zed"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBoardsFixture(t)
			if tc.read {
				f.lockedBoard(memberBoard())
			}
			if tc.users != nil {
				tc.users(f)
			}

			_, err := f.admin.AddMember(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestAddMeWhenPlankaAllowsOnlyThat(t *testing.T) {
	f := newBoardsFixture(t)
	b := memberBoard()
	b.Included.BoardMemberships = b.Included.BoardMemberships[:1] // only Alice
	f.lockedBoard(b)
	f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me", Name: "Me Myself", Username: "me.myself"}, nil).Once()
	f.api.EXPECT().CreateBoardMembership(mock.Anything, "b1", planka.BoardMembershipRequest{UserID: planka.Some("u-me"), Role: planka.Some("editor")}).
		Return(nil, &planka.APIError{Status: http.StatusForbidden}).Once()

	_, err := f.admin.AddMember(t.Context(), AddMember{Board: "B1", Person: "me", Role: "editor"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only a Planka admin or project owner can add people to board \"B1\" other than themselves")
}

func TestUpdateMemberRole(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(memberBoard())
	f.api.EXPECT().UpdateBoardMembership(mock.Anything, "m-alice", planka.BoardMembershipRequest{Role: planka.Some("viewer"), CanComment: planka.Some(true)}).
		Return(&planka.BoardMembership{ID: "m-alice", UserID: "u-alice", Role: "viewer", CanComment: new(true)}, nil).Once()

	got, err := f.admin.UpdateMember(t.Context(), UpdateMember{Board: "B1", Person: "Alice", Role: new("viewer"), CanComment: new(true)})

	require.NoError(t, err)
	assert.Equal(t, "viewer", got.Role)
	assert.True(t, got.CanComment)
}

func TestUpdateMemberWithNoChangeWritesNothing(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(memberBoard())

	got, err := f.admin.UpdateMember(t.Context(), UpdateMember{Board: "B1", Person: "alice", Role: new("editor")})

	require.NoError(t, err)
	assert.Equal(t, "editor", got.Role)
}

func TestRemoveMember(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(memberBoard())
	f.api.EXPECT().DeleteBoardMembership(mock.Anything, "m-alice").Return(nil).Once()

	got, err := f.admin.RemoveMember(t.Context(), "B1", "alice")

	require.NoError(t, err)
	assert.Equal(t, &Member{ID: "m-alice", UserID: "u-alice", Name: "Alice", Username: "alice", Role: "editor", CanComment: true}, got)
}

func TestRemoveSomeoneNotOnTheBoard(t *testing.T) {
	f := newBoardsFixture(t)
	f.lockedBoard(memberBoard())

	_, err := f.admin.RemoveMember(t.Context(), "B1", "bob")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `no user "bob"`)
}
