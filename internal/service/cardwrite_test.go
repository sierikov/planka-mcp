package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

func ptr[T any](v T) *T { return &v }

// writeBoard is board B1 with the cards Alpha (Todo, label bug, assigned to
// Alice), Beta and Gamma (Doing).
func writeBoard() *planka.BoardResponse {
	b := synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Alpha", list: "todo", pos: 65536, labels: []string{"bug"}, users: []string{"alice"}},
		spec{id: "c2", title: "Beta", list: "doing", pos: 100},
		spec{id: "c3", title: "Gamma", list: "doing", pos: 200})
	b.Included.BoardMemberships = []planka.BoardMembership{
		{BoardID: "b1", UserID: "u-alice", Role: "editor"},
		{BoardID: "b1", UserID: "u-me", Role: "editor"},
	}
	return b
}

// alphaRef finds Alpha by title on board B1, which reads B1 once.
var alphaRef = CardRef{Ref: "Alpha", Board: "B1"}

// serveWrite answers the reads of a write: the search board, the board again
// under the write lock, then the fresh board after the write.
func (f *cardsFixture) serveWrite(before, after *planka.BoardResponse) {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, before.Item.ID).Return(before, nil).Times(2)
	f.boards.EXPECT().Board(mock.Anything, after.Item.ID).Return(after, nil).Once()
}

// serveLocked answers the board reads of a write to a card found by title,
// up to its first write: the search board, then the board again under the
// write lock.
func (f *cardsFixture) serveLocked(b *planka.BoardResponse) {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, b.Item.ID).Return(b, nil).Times(2)
}

// updated expects one UpdateCard of c1 with exactly req.
func (f *cardsFixture) updated(req planka.UpdateCardRequest) {
	f.write.EXPECT().UpdateCard(mock.Anything, "c1", req).Return(&planka.Card{ID: "c1"}, nil).Once()
}

func TestPositions(t *testing.T) {
	assert.InDelta(t, 65536, topPosition(nil), 0)
	assert.InDelta(t, 65536, bottomPosition([]float64{}), 0)
	assert.InDelta(t, 32768, topPosition([]float64{65536, 131072}), 0)
	assert.InDelta(t, 196608, bottomPosition([]float64{131072, 65536}), 0)
}

func TestParseDue(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)

	got, err := parseDue("2026-10-20", berlin)
	require.NoError(t, err)
	assert.True(t, time.Date(2026, 10, 20, 0, 0, 0, 0, berlin).Equal(got), "date only is local midnight, got %s", got)

	got, err = parseDue("2026-10-20 14:30", berlin)
	require.NoError(t, err)
	assert.True(t, time.Date(2026, 10, 20, 14, 30, 0, 0, berlin).Equal(got), got.String())

	got, err = parseDue("2026-10-20T14:30:00+05:00", berlin)
	require.NoError(t, err)
	assert.True(t, time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC).Equal(got), got.String())

	_, err = parseDue("next week", berlin)
	require.Error(t, err)
	for _, format := range []string{"YYYY-MM-DD", "YYYY-MM-DD HH:MM", "RFC 3339"} {
		assert.Contains(t, err.Error(), format)
	}
}

func TestMoveResolvesListOnOwnBoard(t *testing.T) {
	f := newCardsFixture(t)
	after := synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Alpha", list: "doing", pos: 50, labels: []string{"bug"}, users: []string{"alice"}},
		spec{id: "c2", title: "Beta", list: "doing", pos: 100})
	f.serveWrite(writeBoard(), after)
	// Top of Doing: half of the smallest position there (100).
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("b1-doing"), Position: planka.Some(50.0)})

	got, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "doing"})

	require.NoError(t, err)
	assert.Equal(t, "Doing", got.Card.List)
	assert.Equal(t, "Alpha", got.Card.Title)
	assert.Equal(t, []string{"bug"}, got.Card.Labels)
}

func TestMoveToBoardResolvesListOnTargetAndSendsBoardID(t *testing.T) {
	f := newCardsFixture(t)
	target := synthBoard("b2", "pw", "B2",
		spec{id: "d1", title: "Delta", list: "doing", pos: 10},
		spec{id: "d2", title: "Epsilon", list: "doing", pos: 20})
	after := synthBoard("b2", "pw", "B2",
		spec{id: "c1", title: "Alpha", list: "doing", pos: 65556},
		spec{id: "d1", title: "Delta", list: "doing", pos: 10})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b2").Return(target, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b2").Return(after, nil).Once()
	f.updated(planka.UpdateCardRequest{
		BoardID:  planka.Some("b2"),
		ListID:   planka.Some("b2-doing"),
		Position: planka.Some(65556.0), // bottom: 20 + 65536
	})

	got, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "Doing", ToBoard: "B2", Position: "bottom"})

	require.NoError(t, err)
	assert.Equal(t, "B2", got.Card.Board)
	assert.Equal(t, "Doing", got.Card.List)
}

func TestMoveBoardScopesSearchNotDestination(t *testing.T) {
	f := newCardsFixture(t)
	// The scope limits the search to B1: the other boards are never read, and
	// the list is looked up on the card's own board.
	after := synthBoard("b1", "pw", "B1", spec{id: "c1", title: "Alpha", list: "doing", pos: 65536})
	f.serveWrite(writeBoard(), after)
	// A move on the card's own board sends no board ID.
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("b1-doing"), Position: planka.Some(50.0)})

	got, err := f.svc.Move(t.Context(), MoveCard{Card: CardRef{Ref: "Alpha", Board: "B1"}, List: "doing"})

	require.NoError(t, err)
	assert.Equal(t, "B1", got.Card.Board)
}

func TestMoveToArchive(t *testing.T) {
	f := newCardsFixture(t)
	// The fresh board no longer lists the archived card: it is read on its own.
	after := writeBoard()
	after.Included.Cards = after.Included.Cards[1:]
	after.Included.CardLabels = nil
	f.serveWrite(writeBoard(), after)
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("b1-arch"), Position: planka.Some(65536.0)})
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(&planka.CardResponse{
		Item:     planka.Card{ID: "c1", BoardID: "b1", ListID: "b1-arch", Name: "Alpha"},
		Included: planka.CardIncluded{CardLabels: []planka.CardLabel{{CardID: "c1", LabelID: "b1-bug"}}},
	}, nil).Once()

	got, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "archive", Position: "bottom"})

	require.NoError(t, err)
	assert.Equal(t, "(archived)", got.Card.List)
	assert.Equal(t, "Alpha", got.Card.Title)
	assert.Equal(t, []string{"bug"}, got.Card.Labels)
}

// withArchiveColumn is board B1 with a kanban list that is really called "Archive".
func withArchiveColumn() *planka.BoardResponse {
	b := writeBoard()
	b.Included.Lists = append(b.Included.Lists, planka.List{ID: "b1-col", BoardID: "b1", Type: "closed", Name: ptr("Archive"), Position: ptr(1.0)})
	return b
}

// TestMoveToListCalledArchive: a kanban list called "Archive" is where list=archive goes; the
// reserved word only means the board's archive when no list has that name.
func TestMoveToListCalledArchive(t *testing.T) {
	f := newCardsFixture(t)
	after := withArchiveColumn()
	after.Included.Cards[0].ListID = "b1-col"
	f.serveWrite(withArchiveColumn(), after)
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("b1-col"), Position: planka.Some(65536.0)})

	got, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "archive"})

	require.NoError(t, err)
	assert.Equal(t, "Archive", got.Card.List, "the kanban list, shown by its name")
}

func TestMoveUnknownListListsValidNames(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)

	_, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "Nope"})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "list", unknown.Kind)
	assert.Equal(t, []string{"Todo", "Doing"}, unknown.Valid)
}

func TestMoveRejectsUnknownPositionWord(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)

	_, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "doing", Position: "sideways"})

	require.ErrorContains(t, err, "top")
}

func TestUpdateAddsMissingLabelWithDefaultColor(t *testing.T) {
	f := newCardsFixture(t)
	after := writeBoard()
	after.Included.Labels = append(after.Included.Labels,
		planka.Label{ID: "b1-research2", BoardID: "b1", Name: ptr("research2"), Position: 65538})
	after.Included.CardLabels = append(after.Included.CardLabels, planka.CardLabel{CardID: "c1", LabelID: "b1-research2"})
	f.serveWrite(writeBoard(), after)
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "research2", "muddy-grey", 65538.0).
		Return(&planka.Label{ID: "b1-research2", Name: ptr("research2")}, nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "b1-research2").Return(nil).Once()

	// "bug" is already on Alpha: no call for it.
	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{"bug", "research2"}, CreateLabels: true})

	require.NoError(t, err)
	assert.Equal(t, []string{"research2"}, got.CreatedLabels)
	assert.Equal(t, []string{"bug", "research2"}, got.Card.Labels)
}

func TestUpdateRemoveLabelNotOnCardIsNoop(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())

	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, RemoveLabels: []string{"ui"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"bug"}, got.Card.Labels)
	assert.Empty(t, got.CreatedLabels)
}

func TestUpdateIdempotentLabelAndMemberOps(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())

	// Alpha already has bug and Alice, and has neither ui nor Me.
	_, err := f.svc.Update(t.Context(), UpdateCard{
		Card:      alphaRef,
		AddLabels: []string{"bug"}, RemoveLabels: []string{"ui"},
		AddAssignees: []string{"alice"}, RemoveAssignees: []string{"Me Myself"},
	})

	require.NoError(t, err)
}

func TestUpdateAssigneeMe(t *testing.T) {
	f := newCardsFixture(t)
	after := writeBoard()
	after.Included.CardMemberships = append(after.Included.CardMemberships, planka.CardMembership{CardID: "c1", UserID: "u-me"})
	f.serveWrite(writeBoard(), after)
	f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me"}, nil).Once()
	f.members.EXPECT().AddCardMember(mock.Anything, "c1", "u-me").Return(nil).Once()

	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddAssignees: []string{"me"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"Alice", "Me Myself"}, got.Card.Assignees)
}

func TestUpdateAssigneeMeMustBeBoardMember(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)
	f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-stranger", Name: "Stranger"}, nil).Once()

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddAssignees: []string{"me"}})

	require.ErrorContains(t, err, `you (Stranger) are not a member of board "B1"; members: Alice, Me Myself`)
}

func TestUpdateResolvesAllNamesBeforeFirstWrite(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2) // no write mock is set up

	_, err := f.svc.Update(t.Context(), UpdateCard{
		Card:         alphaRef,
		Title:        ptr("Renamed"),
		AddLabels:    []string{"ui", "brand-new"},
		CreateLabels: true,
		AddAssignees: []string{"nobody"},
	})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "user", unknown.Kind)
}

func TestUpdateWriteOrder(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	var order []string
	note := func(s string) { order = append(order, s) }
	f.write.EXPECT().UpdateCard(mock.Anything, "c1", mock.Anything).Return(&planka.Card{}, nil).
		Run(func(context.Context, string, planka.UpdateCardRequest) { note("fields") }).Once()
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "fresh", "muddy-grey", mock.Anything).
		Return(&planka.Label{ID: "l-fresh"}, nil).
		Run(func(context.Context, string, string, string, float64) { note("create label") }).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "b1-ui").Return(nil).
		Run(func(context.Context, string, string) { note("add label") }).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "l-fresh").Return(nil).
		Run(func(context.Context, string, string) { note("add label") }).Once()
	f.labels.EXPECT().RemoveCardLabel(mock.Anything, "c1", "b1-bug").Return(nil).
		Run(func(context.Context, string, string) { note("remove label") }).Once()
	f.members.EXPECT().AddCardMember(mock.Anything, "c1", "u-me").Return(nil).
		Run(func(context.Context, string, string) { note("add member") }).Once()
	f.members.EXPECT().RemoveCardMember(mock.Anything, "c1", "u-alice").Return(nil).
		Run(func(context.Context, string, string) { note("remove member") }).Once()

	_, err := f.svc.Update(t.Context(), UpdateCard{
		Card:      alphaRef,
		Title:     ptr("Renamed"),
		AddLabels: []string{"ui", "fresh"}, RemoveLabels: []string{"bug"}, CreateLabels: true,
		AddAssignees: []string{"Me Myself"}, RemoveAssignees: []string{"alice"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{
		"fields", "create label", "add label", "add label", "remove label", "add member", "remove member",
	}, order)
}

func TestUpdateFields(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	f.updated(planka.UpdateCardRequest{
		Name:           planka.Some("Renamed"),
		Description:    planka.Null[string](),
		IsDueCompleted: planka.Some(true),
	})

	_, err := f.svc.Update(t.Context(), UpdateCard{
		Card: alphaRef, Title: ptr("Renamed"), Description: ptr(""), DueCompleted: ptr(true),
	})

	require.NoError(t, err)
}

func TestUpdateRejectsEmptyTitleAndBadInput(t *testing.T) {
	f := newCardsFixture(t) // no read or write is expected: validation comes first
	for name, in := range map[string]UpdateCard{
		"empty title":   {Card: alphaRef, Title: ptr("  ")},
		"bad due":       {Card: alphaRef, Due: ptr("tomorrow")},
		"bad stopwatch": {Card: alphaRef, Stopwatch: "pause"},
	} {
		_, err := f.svc.Update(t.Context(), in)
		require.Error(t, err, name)
	}
}

func TestUpdateDue(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	f.updated(planka.UpdateCardRequest{DueDate: planka.Some(time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local))})

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, Due: ptr("2026-10-20")})

	require.NoError(t, err)
}

func TestUpdateDueEmptyClears(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	f.updated(planka.UpdateCardRequest{DueDate: planka.Null[time.Time]()})

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, Due: ptr("")})

	require.NoError(t, err)
}

func TestUpdateStopwatch(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	started := now.Add(-90 * time.Second)
	tests := []struct {
		name  string
		prev  *planka.Stopwatch
		op    string
		patch *planka.UpdateCardRequest // nil: nothing is sent
	}{
		{"start", &planka.Stopwatch{Total: 120}, "start",
			&planka.UpdateCardRequest{Stopwatch: planka.Some(planka.Stopwatch{StartedAt: &now, Total: 120})}},
		{"start without a stopwatch", nil, "start",
			&planka.UpdateCardRequest{Stopwatch: planka.Some(planka.Stopwatch{StartedAt: &now})}},
		{"stop after 90 seconds", &planka.Stopwatch{StartedAt: &started, Total: 120}, "stop",
			&planka.UpdateCardRequest{Stopwatch: planka.Some(planka.Stopwatch{Total: 210})}},
		{"reset", &planka.Stopwatch{Total: 120}, "reset",
			&planka.UpdateCardRequest{Stopwatch: planka.Null[planka.Stopwatch]()}},
		{"start a running one", &planka.Stopwatch{StartedAt: &started, Total: 120}, "start", nil},
		{"stop a stopped one", &planka.Stopwatch{Total: 120}, "stop", nil},
		{"stop without a stopwatch", nil, "stop", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCardsFixture(t)
			f.svc.now = func() time.Time { return now }
			before := writeBoard()
			before.Included.Cards[0].Stopwatch = tt.prev
			f.serveWrite(before, writeBoard())
			if tt.patch != nil {
				f.updated(*tt.patch)
			}

			_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, Stopwatch: tt.op})

			require.NoError(t, err)
		})
	}
}

func TestCreateDefaultsToFirstActiveListAndTop(t *testing.T) {
	f := newCardsFixture(t)
	before := synthBoard("b1", "pw", "B1",
		spec{id: "c1", title: "Alpha", list: "todo", pos: 65536},
		spec{id: "c2", title: "Beta", list: "todo", pos: 131072},
		spec{id: "c3", title: "Gamma", list: "doing", pos: 5})
	after := synthBoard("b1", "pw", "B1", spec{id: "n1", title: "Fresh", list: "todo", pos: 32768})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(before, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(after, nil).Once()
	f.write.EXPECT().CreateCard(mock.Anything, "b1-todo", planka.CreateCardRequest{
		Type: "project", Position: 32768, Name: "Fresh",
	}).Return(&planka.Card{ID: "n1"}, nil).Once()

	got, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh"})

	require.NoError(t, err)
	assert.Equal(t, "n1", got.Card.ID)
	assert.Equal(t, "Todo", got.Card.List)
	assert.Empty(t, got.CreatedLabels)
}

func TestCreateUsesBoardDefaultCardType(t *testing.T) {
	f := newCardsFixture(t)
	before := writeBoard()
	before.Item.DefaultCardType = "story"
	after := synthBoard("b1", "pw", "B1", spec{id: "n1", title: "Fresh", list: "doing", pos: 1})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(before, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(after, nil).Once()
	f.write.EXPECT().CreateCard(mock.Anything, "b1-doing", planka.CreateCardRequest{
		Type: "story", Position: 50, Name: "Fresh",
	}).Return(&planka.Card{ID: "n1"}, nil).Once()

	_, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh", List: "doing"})

	require.NoError(t, err)
}

func TestCreateAutoCreatesLabels(t *testing.T) {
	f := newCardsFixture(t)
	after := synthBoard("b1", "pw", "B1", spec{id: "n1", title: "Fresh", list: "doing", pos: 65536})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(after, nil).Once()
	due := time.Date(2026, 10, 20, 14, 30, 0, 0, time.Local)
	f.write.EXPECT().CreateCard(mock.Anything, "b1-doing", planka.CreateCardRequest{
		Type: "project", Position: 65736, Name: "Fresh", Description: ptr("Details"), DueDate: &due,
	}).Return(&planka.Card{ID: "n1"}, nil).Once()
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "research2", "muddy-grey", 65538.0).
		Return(&planka.Label{ID: "l-new"}, nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "n1", "b1-bug").Return(nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "n1", "l-new").Return(nil).Once()
	f.members.EXPECT().AddCardMember(mock.Anything, "n1", "u-alice").Return(nil).Once()

	got, err := f.svc.Create(t.Context(), CreateCard{
		Board: "B1", Title: "Fresh", List: "doing", Description: "Details", Due: "2026-10-20 14:30",
		Labels: []string{"bug", "research2"}, CreateLabels: true, Assignees: []string{"alice"}, Position: "bottom",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"research2"}, got.CreatedLabels)
}

func TestCreateValidatesBeforeWriting(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()

	_, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh", Assignees: []string{"nobody"}})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)

	_, err = f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: " "})
	require.Error(t, err)
}

func TestDeleteReturnsSummaryOfDeletedCard(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once() // read once: no read after the delete
	f.write.EXPECT().DeleteCard(mock.Anything, "c1").Return(nil).Once()

	got, err := f.svc.Delete(t.Context(), alphaRef)

	require.NoError(t, err)
	assert.Equal(t, "c1", got.ID)
	assert.Equal(t, "Alpha", got.Title)
	assert.Equal(t, "Todo", got.List)
	assert.Equal(t, []string{"bug"}, got.Labels)
	assert.Equal(t, []string{"Alice"}, got.Assignees)
}

func TestUpdateAssigneeMustBeBoardMemberNotJustListedUser(t *testing.T) {
	f := newCardsFixture(t)
	before := writeBoard()
	// Carol is among the board's users (she created a card) but is no member.
	before.Included.Users = append(before.Included.Users, planka.User{ID: "u-carol", Name: "Carol", Username: "carol"})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(before, nil).Times(2) // no write mock is set up

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddAssignees: []string{"carol"}})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "user", unknown.Kind)
	assert.Equal(t, []string{"Alice", "Me Myself"}, unknown.Valid)
}

func TestUpdateUnknownLabelIsAnErrorWithoutCreateLabels(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(onlyBugfix(), nil).Times(2) // no write mock is set up

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{"bugfx"}})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "label", unknown.Kind)
	assert.Equal(t, "bugfx", unknown.Name)
	assert.Equal(t, []string{"bugfix"}, unknown.Valid)
	assert.ErrorContains(t, err, "set create_labels to create it")
}

func TestCreateUnknownLabelIsAnErrorWithoutCreateLabels(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(onlyBugfix(), nil).Once() // no card is created

	_, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh", Labels: []string{"bugfx"}})

	var unknown *UnknownNameError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, []string{"bugfix"}, unknown.Valid)
}

// onlyBugfix is board B1 with the one label "bugfix" and the card Alpha.
func onlyBugfix() *planka.BoardResponse {
	b := writeBoard()
	b.Included.Labels = []planka.Label{{ID: "b1-bugfix", BoardID: "b1", Name: ptr("bugfix"), Position: 1}}
	b.Included.CardLabels = nil
	return b
}

func TestUpdateAddLabelDoesNotPrefixMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(onlyBugfix(), onlyBugfix())
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "bug", "muddy-grey", 65537.0).
		Return(&planka.Label{ID: "l-bug"}, nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "l-bug").Return(nil).Once()

	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{"bug"}, CreateLabels: true})

	require.NoError(t, err)
	assert.Equal(t, []string{"bug"}, got.CreatedLabels)
}

func TestCreateLabelDoesNotPrefixMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	after := synthBoard("b1", "pw", "B1", spec{id: "n1", title: "Fresh", list: "todo", pos: 1})
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(onlyBugfix(), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(after, nil).Once()
	f.write.EXPECT().CreateCard(mock.Anything, "b1-todo", mock.Anything).Return(&planka.Card{ID: "n1"}, nil).Once()
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "BUG", "muddy-grey", 65537.0).
		Return(&planka.Label{ID: "l-bug"}, nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "n1", "l-bug").Return(nil).Once()

	got, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh", Labels: []string{"BUG"}, CreateLabels: true})

	require.NoError(t, err)
	assert.Equal(t, []string{"BUG"}, got.CreatedLabels)
}

func TestUpdateAddLabelExactMatchIgnoresCase(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "b1-ui").Return(nil).Once()

	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{" UI "}})

	require.NoError(t, err)
	assert.Empty(t, got.CreatedLabels)
}

func TestUpdateAddLabelTwoExactMatchesAreAmbiguous(t *testing.T) {
	f := newCardsFixture(t)
	before := writeBoard()
	before.Included.Labels = append(before.Included.Labels, planka.Label{ID: "b1-ui2", BoardID: "b1", Name: ptr("UI"), Position: 3})
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(before, nil).Times(2)

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{"ui"}})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{{ID: "b1-ui", Label: "ui"}, {ID: "b1-ui2", Label: "UI"}}, amb.Candidates,
		"the candidates carry the IDs to retry with")
}

// withNumericIDs is writeBoard plus the label "ui" again under the ID 4242, and two members both
// called Bob, under the IDs 7001 and 7002: real Planka IDs are numeric.
func withNumericIDs() *planka.BoardResponse {
	b := writeBoard()
	b.Included.Labels = append(b.Included.Labels, planka.Label{ID: "4242", BoardID: "b1", Name: ptr("ui"), Position: 3})
	b.Included.Users = append(b.Included.Users,
		planka.User{ID: "7001", Name: "Bob", Username: "bob1"}, planka.User{ID: "7002", Name: "Bob", Username: "bob2"})
	b.Included.BoardMemberships = append(b.Included.BoardMemberships,
		planka.BoardMembership{BoardID: "b1", UserID: "7001"}, planka.BoardMembership{BoardID: "b1", UserID: "7002"})
	return b
}

// TestUpdateLabelsAndAssigneesByID: an ID picks out a label or member whose name is not unique,
// and is never mistaken for the name of a label to create.
func TestUpdateLabelsAndAssigneesByID(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(withNumericIDs(), withNumericIDs())
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "4242").Return(nil).Once()
	f.members.EXPECT().AddCardMember(mock.Anything, "c1", "7002").Return(nil).Once()

	got, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddLabels: []string{" 4242 "}, AddAssignees: []string{"7002"}})

	require.NoError(t, err)
	assert.Empty(t, got.CreatedLabels, "an ID names an existing label")
}

func TestUpdateAmbiguousAssigneeListsIDs(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(withNumericIDs(), nil).Times(2)

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, AddAssignees: []string{"bob"}})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{{ID: "7001", Label: "Bob"}, {ID: "7002", Label: "Bob"}}, amb.Candidates)
}

// twoDoings is board B1 with a second list called "Doing", under a numeric ID.
func twoDoings() *planka.BoardResponse {
	b := writeBoard()
	b.Included.Lists = append(b.Included.Lists, planka.List{ID: "5005", BoardID: "b1", Type: "active", Name: ptr("Doing"), Position: ptr(3.0)})
	return b
}

// TestMoveToDuplicateListNameNeedsID: two lists with one name are an error that lists both IDs, and
// retrying with one of them moves the card there.
func TestMoveToDuplicateListNameNeedsID(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(twoDoings(), nil).Times(2)

	_, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "doing"})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{{ID: "b1-doing", Label: "Doing"}, {ID: "5005", Label: "Doing"}}, amb.Candidates)

	f = newCardsFixture(t)
	after := twoDoings()
	after.Included.Cards[0].ListID = "5005"
	f.serveWrite(twoDoings(), after)
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("5005"), Position: planka.Some(65536.0)})

	got, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "5005"})

	require.NoError(t, err)
	assert.Equal(t, "Doing", got.Card.List)
}

func TestUpdateRemoveLabelStillMatchesByPrefix(t *testing.T) {
	f := newCardsFixture(t)
	before := onlyBugfix()
	before.Included.CardLabels = []planka.CardLabel{{CardID: "c1", LabelID: "b1-bugfix"}}
	f.serveWrite(before, before)
	f.labels.EXPECT().RemoveCardLabel(mock.Anything, "c1", "b1-bugfix").Return(nil).Once()

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, RemoveLabels: []string{"bug"}})

	require.NoError(t, err)
}

func TestCreateReadBackFailureSaysCardWasCreated(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(nil, errors.New("boom")).Once()
	f.write.EXPECT().CreateCard(mock.Anything, "b1-todo", mock.Anything).Return(&planka.Card{ID: "n1"}, nil).Once()

	_, err := f.svc.Create(t.Context(), CreateCard{Board: "B1", Title: "Fresh"})

	require.ErrorContains(t, err, "card n1 was created, but reading it back failed")
	require.ErrorContains(t, err, "boom")
}

func TestMoveAndUpdateReadBackFailureSaysWhatHappened(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(nil, errors.New("boom")).Once()
	f.updated(planka.UpdateCardRequest{ListID: planka.Some("b1-doing"), Position: planka.Some(50.0)})

	_, err := f.svc.Move(t.Context(), MoveCard{Card: alphaRef, List: "doing"})
	require.ErrorContains(t, err, "card c1 was moved, but reading it back failed")

	f = newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(nil, errors.New("boom")).Once()
	f.updated(planka.UpdateCardRequest{Name: planka.Some("Renamed")})

	_, err = f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, Title: ptr("Renamed")})
	require.ErrorContains(t, err, "card c1 was updated, but reading it back failed")
}

func TestUpdateFailureAfterWritesSaysWhatWasApplied(t *testing.T) {
	f := newCardsFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Times(2)
	f.updated(planka.UpdateCardRequest{Name: planka.Some("Renamed")})
	f.labels.EXPECT().CreateLabel(mock.Anything, "b1", "fresh", "muddy-grey", mock.Anything).
		Return(&planka.Label{ID: "l-fresh"}, nil).Once()
	f.labels.EXPECT().AddCardLabel(mock.Anything, "c1", "l-fresh").Return(errors.New("boom")).Once()

	_, err := f.svc.Update(t.Context(), UpdateCard{Card: alphaRef, Title: ptr("Renamed"), AddLabels: []string{"fresh"}, CreateLabels: true})

	require.ErrorContains(t, err, "card c1 was partly updated (fields updated; labels created: fresh)")
	require.ErrorContains(t, err, "boom")
}

func TestMovePositionExcludesTheMovedCard(t *testing.T) {
	f := newCardsFixture(t)
	f.serveWrite(writeBoard(), writeBoard())
	// Beta (100) goes to the top of Doing, where it already is: the others
	// (Gamma, 200) give 100; counting Beta itself would give 50.
	f.write.EXPECT().UpdateCard(mock.Anything, "c2",
		planka.UpdateCardRequest{ListID: planka.Some("b1-doing"), Position: planka.Some(100.0)}).
		Return(&planka.Card{ID: "c2"}, nil).Once()

	_, err := f.svc.Move(t.Context(), MoveCard{Card: CardRef{Ref: "Beta", Board: "B1"}, List: "doing"})

	require.NoError(t, err)
}

// withDescribed is writeBoard plus Delta (c4, Todo), whose description says
// "configure the backup". No title on the board has "configure".
func withDescribed() *planka.BoardResponse {
	b := writeBoard()
	b.Included.Cards = append(b.Included.Cards, planka.Card{
		ID: "c4", BoardID: "b1", ListID: "b1-todo", Name: "Delta", Description: ptr("configure the backup"), Position: ptr(300.0),
	})
	return b
}

// describedRef matches Delta only through its description.
var describedRef = CardRef{Ref: "configure", Board: "B1"}

// TestWritesRefuseADescriptionOnlyMatch: a word in a description is too weak a
// reason to change or delete a card, so every write refuses it and names the
// card's ID to retry with. No write mock is set up: nothing may be written.
func TestWritesRefuseADescriptionOnlyMatch(t *testing.T) {
	writes := []struct {
		name  string
		write func(context.Context, *commentsFixture) error
	}{
		{"delete_card", func(ctx context.Context, f *commentsFixture) error {
			_, err := f.svc.Delete(ctx, describedRef)
			return err
		}},
		{"update_card", func(ctx context.Context, f *commentsFixture) error {
			_, err := f.svc.Update(ctx, UpdateCard{Card: describedRef, Title: ptr("Renamed")})
			return err
		}},
		{"move_card", func(ctx context.Context, f *commentsFixture) error {
			_, err := f.svc.Move(ctx, MoveCard{Card: describedRef, List: "Doing"})
			return err
		}},
		{"add_comment", func(ctx context.Context, f *commentsFixture) error {
			_, err := f.comments.Add(ctx, describedRef, "hi")
			return err
		}},
		{"add_tasks", func(ctx context.Context, f *commentsFixture) error {
			_, err := NewTasks(f.svc, NewMockTasksAPI(t)).Add(ctx, describedRef, "", []TaskInput{{Name: "x"}})
			return err
		}},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			f := newCommentsFixture(t)
			f.serve(withDescribed())

			err := w.write(t.Context(), f)

			var desc *DescriptionMatchError
			require.ErrorAs(t, err, &desc)
			assert.Equal(t, Candidate{ID: "c4", Label: "Delta — B1 / Todo"}, desc.Card)
			assert.EqualError(t, err, `"configure" matches only the description of card Delta — B1 / Todo (id c4); to change that card, retry with its id`)
		})
	}
}

// TestReadsStillFindACardByItsDescription: get_card and list_comments resolve
// with the read rule, which keeps description matches.
func TestReadsStillFindACardByItsDescription(t *testing.T) {
	f := newCardsFixture(t)
	f.serve(withDescribed())

	id, _, err := f.svc.resolveID(t.Context(), describedRef)

	require.NoError(t, err)
	assert.Equal(t, "c4", id)
}

// TestWritesStillTakeATitleMatch: a title match is still a write target; the
// rule only drops the description-only case.
func TestWritesStillTakeATitleMatch(t *testing.T) {
	f := newCardsFixture(t)
	f.serve(withDescribed())

	id, _, err := f.svc.resolveTarget(t.Context(), CardRef{Ref: "delta", Board: "B1"})

	require.NoError(t, err)
	assert.Equal(t, "c4", id)
}

// TestAddTasksLinkByDescriptionLooksOnEveryBoardThenRefuses: a link that
// matches only a description on the parent's board is looked for on every
// board, like a link that matches nothing there; when that finds the same
// description match alone, add_tasks refuses it.
func TestAddTasksLinkByDescriptionLooksOnEveryBoardThenRefuses(t *testing.T) {
	f := newTasksFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	// The parent's search, its read under the lock, the link on B1, then the link on every board.
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(withDescribed(), nil).Times(4)
	f.boards.EXPECT().Board(mock.Anything, "b2").Return(synthBoard("b2", "pw", "B2"), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b3").Return(synthBoard("b3", "ph", "B3"), nil).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "configure"}})

	var desc *DescriptionMatchError
	require.ErrorAs(t, err, &desc)
	assert.Equal(t, "c4", desc.Card.ID)
	assert.ErrorContains(t, err, "item 1: ")
}
