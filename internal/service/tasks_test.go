package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// tasksFixture wires Tasks to mocks; the Cards behind it read board B1.
type tasksFixture struct {
	*cardsFixture
	api   *MockTasksAPI
	tasks *Tasks
}

func newTasksFixture(t *testing.T) *tasksFixture {
	t.Helper()
	f := &tasksFixture{cardsFixture: newCardsFixture(t), api: NewMockTasksAPI(t)}
	f.tasks = NewTasks(f.svc, f.api)
	return f
}

// cardOf is card c1 with the given checklists, as GET /api/cards/c1 returns it.
func cardOf(lists []planka.TaskList, tasks ...planka.Task) *planka.CardResponse {
	r := &planka.CardResponse{Item: planka.Card{ID: "c1", BoardID: "b1", Name: "Alpha"}}
	r.Included.TaskLists = lists
	r.Included.Tasks = tasks
	return r
}

func list(id, name string, pos float64) planka.TaskList {
	return planka.TaskList{ID: id, CardID: "c1", Name: name, Position: pos}
}

func task(id, listID, name string, pos float64, done bool) planka.Task {
	return planka.Task{ID: id, TaskListID: listID, Name: ptr(name), Position: pos, IsCompleted: done}
}

// readCard expects one read of card c1 answering r. Reads answer in order.
func (f *tasksFixture) readCard(r *planka.CardResponse) {
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(r, nil).Once()
}

// created expects one CreateTask in list tl with exactly req.
func (f *tasksFixture) created(tl string, req planka.CreateTaskRequest) {
	f.api.EXPECT().CreateTask(mock.Anything, tl, req).Return(&planka.Task{ID: "new"}, nil).Once()
}

func names(in []Task) []string {
	out := make([]string, len(in))
	for i, tk := range in {
		out[i] = tk.Name
	}
	return out
}

func TestAddTasksCreatesTaskListWhenMissing(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf(nil))
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", "Tasks", 65536.0).Return(&planka.TaskList{ID: "tl1", CardID: "c1", Name: "Tasks"}, nil).Once()
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, Name: ptr("Write")})
	f.created("tl1", planka.CreateTaskRequest{Position: 131072, Name: ptr("Test")})
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Tasks", 65536)},
		task("t1", "tl1", "Write", 65536, false), task("t2", "tl1", "Test", 131072, false)))

	got, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "Write"}, {Name: "Test"}})

	require.NoError(t, err)
	assert.Equal(t, "tl1", got.ID)
	assert.Equal(t, "Tasks", got.Name)
	assert.Equal(t, []string{"Write", "Test"}, names(got.Tasks))
}

func TestAddTasksNewNamedListGoesAtTheBottom(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Tasks", 100), list("tl2", "Other", 300)}))
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", "Release", 300.0+65536).Return(&planka.TaskList{ID: "tl3", Name: "Release"}, nil).Once()
	f.created("tl3", planka.CreateTaskRequest{Position: 65536, Name: ptr("Tag")})
	f.readCard(cardOf([]planka.TaskList{list("tl3", "Release", 300+65536)}, task("t1", "tl3", "Tag", 65536, false)))

	got, err := f.tasks.Add(t.Context(), alphaRef, " Release ", []TaskInput{{Name: "Tag"}})

	require.NoError(t, err)
	assert.Equal(t, "Release", got.Name)
}

func TestAddTasksReusesExistingListCaseInsensitive(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	lists := []planka.TaskList{list("tl1", "Checklist", 65536), list("tl2", "Release Steps", 131072)}
	f.readCard(cardOf(lists,
		task("t1", "tl2", "Build", 1000, true), task("t2", "tl2", "Sign", 2000, false), task("t3", "tl1", "Other", 9e6, false)))
	f.created("tl2", planka.CreateTaskRequest{Position: 2000 + 65536, Name: ptr("Publish")})
	f.created("tl2", planka.CreateTaskRequest{Position: 2000 + 2*65536, Name: ptr("Announce")})
	f.readCard(cardOf(lists,
		task("t2", "tl2", "Sign", 2000, false), task("t1", "tl2", "Build", 1000, true),
		task("t4", "tl2", "Publish", 2000+65536, false), task("t5", "tl2", "Announce", 2000+2*65536, false)))

	got, err := f.tasks.Add(t.Context(), alphaRef, "RELEASE steps", []TaskInput{{Name: "Publish"}, {Name: "Announce"}})

	require.NoError(t, err)
	assert.Equal(t, "tl2", got.ID)
	assert.Equal(t, []string{"Build", "Sign", "Publish", "Announce"}, names(got.Tasks))
	assert.True(t, got.Tasks[0].Completed)
}

func TestAddTasksDoesNotPrefixMatchTaskList(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Release Steps", 131072)}))
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", "Release", 131072.0+65536).Return(&planka.TaskList{ID: "tl2", Name: "Release"}, nil).Once()
	f.created("tl2", planka.CreateTaskRequest{Position: 65536, Name: ptr("Tag")})
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Release Steps", 131072), list("tl2", "Release", 196608)},
		task("t1", "tl2", "Tag", 65536, false)))

	got, err := f.tasks.Add(t.Context(), alphaRef, "Release", []TaskInput{{Name: "Tag"}})

	require.NoError(t, err)
	assert.Equal(t, "tl2", got.ID)
}

func TestAddTasksTwoListsWithTheSameNameIsAmbiguous(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Tasks", 1), list("tl2", "tasks", 2)}))

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "x"}})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Len(t, amb.Candidates, 2)
}

// Board IDs of the link tests are numeric, as Planka's are: a link search is
// scoped to the parent's board by ID, and only a numeric string reads as one.
const (
	linkB1 = "101"
	linkB2 = "102"
	linkB3 = "103"
)

// linkProjects is project Work with boards B1 and B2, and Home with B3.
func linkProjects() *planka.ProjectsResponse {
	r := &planka.ProjectsResponse{Items: []planka.Project{{ID: "pw", Name: "Work"}, {ID: "ph", Name: "Home"}}}
	r.Included.Boards = []planka.Board{
		{ID: linkB1, ProjectID: "pw", Name: "B1", Position: 1},
		{ID: linkB2, ProjectID: "pw", Name: "B2", Position: 2},
		{ID: linkB3, ProjectID: "ph", Name: "B3", Position: 1},
	}
	return r
}

// linkBoard is B1 with the cards Alpha (the parent), Beta and Gamma.
func linkBoard() *planka.BoardResponse {
	return synthBoard(linkB1, "pw", "B1",
		spec{id: "c1", title: "Alpha", list: "todo", pos: 65536},
		spec{id: "c2", title: "Beta", list: "doing", pos: 100},
		spec{id: "c3", title: "Gamma", list: "doing", pos: 200})
}

func TestAddTasksLinkResolvesCardAndSendsLinkedCardID(t *testing.T) {
	f := newTasksFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(linkProjects(), nil)
	// The parent search and the link search each read B1.
	f.boards.EXPECT().Board(mock.Anything, linkB1).Return(linkBoard(), nil).Times(3)
	lists := []planka.TaskList{list("tl1", "Tasks", 65536)}
	f.readCard(cardOf(lists))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, LinkedCardID: ptr("c2")})
	linked := planka.Task{ID: "t1", TaskListID: "tl1", LinkedCardID: ptr("c2"), Name: ptr("Beta"), Position: 65536}
	f.readCard(cardOf(lists, linked))

	got, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "Beta"}})

	require.NoError(t, err)
	require.Len(t, got.Tasks, 1)
	require.NotNil(t, got.Tasks[0].Linked)
	assert.Equal(t, "c2", got.Tasks[0].Linked.ID)
	assert.Equal(t, "Beta", got.Tasks[0].Linked.Title)
	assert.Equal(t, "Doing", got.Tasks[0].Linked.List)
	assert.Equal(t, "B1", got.Tasks[0].Linked.Board)
}

func TestAddTasksLinkTitleStaysOnParentBoardWhenBoardNamesRepeat(t *testing.T) {
	f := newTasksFixture(t)
	// Work and Home each have a board called "Board". The link must not need
	// the name to be unique: it searches the parent's board by ID.
	projects := &planka.ProjectsResponse{Items: []planka.Project{{ID: "pw", Name: "Work"}, {ID: "ph", Name: "Home"}}}
	projects.Included.Boards = []planka.Board{
		{ID: "201", ProjectID: "pw", Name: "Board", Position: 1},
		{ID: "202", ProjectID: "ph", Name: "Board", Position: 1},
	}
	board := synthBoard("201", "pw", "Board",
		spec{id: "c1", title: "Alpha", list: "todo", pos: 1},
		spec{id: "c2", title: "Beta", list: "doing", pos: 1})
	f.projects.EXPECT().Projects(mock.Anything).Return(projects, nil)
	f.boards.EXPECT().Board(mock.Anything, "201").Return(board, nil).Times(3) // board 202 is never read
	lists := []planka.TaskList{list("tl1", "Tasks", 65536)}
	f.readCard(cardOf(lists))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, LinkedCardID: ptr("c2")})
	f.readCard(cardOf(lists, planka.Task{ID: "t1", TaskListID: "tl1", LinkedCardID: ptr("c2"), Name: ptr("Beta"), Position: 65536}))

	got, err := f.tasks.Add(t.Context(), CardRef{Ref: "Alpha", Board: "201"}, "", []TaskInput{{Link: "Beta"}})

	require.NoError(t, err)
	assert.Equal(t, "Beta", got.Tasks[0].Linked.Title)
}

func TestAddTasksLinkByIDIsCheckedBeforeAnyWrite(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.cards.EXPECT().Card(mock.Anything, "1887856287392203808").
		Return(nil, &planka.APIError{Status: 404}).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "first"}, {Link: "1887856287392203808"}})

	require.ErrorIs(t, err, ErrNotFound)
	assert.ErrorContains(t, err, "item 2")
}

func TestAddTasksLinkByURLIsCheckedBeforeAnyWrite(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.cards.EXPECT().Card(mock.Anything, "1887856287392203808").
		Return(nil, &planka.APIError{Status: 404}).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "http://planka.example/cards/1887856287392203808"}})

	require.ErrorIs(t, err, ErrNotFound)
}

func TestAddTasksLinkByIDSendsItAndShowsNameWhenCardIsOffBoard(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	lists := []planka.TaskList{list("tl1", "Tasks", 65536)}
	other := &planka.CardResponse{Item: planka.Card{ID: "1887856287392203808", BoardID: "elsewhere"}}
	f.cards.EXPECT().Card(mock.Anything, "1887856287392203808").Return(other, nil).Once()
	f.readCard(cardOf(lists))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, LinkedCardID: ptr("1887856287392203808")})
	// A card that is not on the parent's board shows by its task name.
	off := planka.Task{ID: "t1", TaskListID: "tl1", LinkedCardID: ptr("1887856287392203808"), Name: ptr("Elsewhere"), Position: 65536}
	f.readCard(cardOf(lists, off))

	got, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "1887856287392203808"}})

	require.NoError(t, err)
	assert.Equal(t, &CardSummary{ID: "1887856287392203808", Title: "Elsewhere"}, got.Tasks[0].Linked)
}

func TestAddTasksLinkToTheParentCardIsError(t *testing.T) {
	f := newTasksFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(linkProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, linkB1).Return(linkBoard(), nil).Times(3)

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "first"}, {Link: "Alpha"}})

	require.ErrorContains(t, err, "item 2")
	assert.ErrorContains(t, err, "cannot link to its own card")
}

func TestAddTasksLinkToTheParentCardByIDIsError(t *testing.T) {
	f := newTasksFixture(t)
	// The parent is given by ID, so it is read and its board is loaded. The
	// link is then known to be the parent without another read.
	f.cards.EXPECT().Card(mock.Anything, "1887856287392203808").Return(&planka.CardResponse{
		Item: planka.Card{ID: "1887856287392203808", BoardID: "b1", Name: "Alpha"},
	}, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()
	parent := CardRef{Ref: "1887856287392203808"}

	_, err := f.tasks.Add(t.Context(), parent, "", []TaskInput{{Link: "1887856287392203808"}})

	require.ErrorContains(t, err, "cannot link to its own card")
}

func TestAddTasksLinkNotOnParentBoardFallsBackToAllBoards(t *testing.T) {
	f := newTasksFixture(t)
	b2 := synthBoard(linkB2, "pw", "B2", spec{id: "d1", title: "Delta", list: "todo", pos: 1})
	b3 := synthBoard(linkB3, "ph", "B3")
	f.projects.EXPECT().Projects(mock.Anything).Return(linkProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, linkB1).Return(linkBoard(), nil).Times(4) // parent, parent under the lock, link on B1, link on all
	f.boards.EXPECT().Board(mock.Anything, linkB2).Return(b2, nil).Once()
	f.boards.EXPECT().Board(mock.Anything, linkB3).Return(b3, nil).Once()
	lists := []planka.TaskList{list("tl1", "Tasks", 65536)}
	f.readCard(cardOf(lists))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, LinkedCardID: ptr("d1")})
	f.readCard(cardOf(lists, planka.Task{ID: "t1", TaskListID: "tl1", LinkedCardID: ptr("d1"), Name: ptr("Delta"), Position: 65536}))

	got, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "Delta"}})

	require.NoError(t, err)
	assert.Equal(t, "Delta", got.Tasks[0].Name)
}

func TestAddTasksLinkMatchingNothingAnywhereFailsBeforeAnyWrite(t *testing.T) {
	f := newTasksFixture(t)
	f.projects.EXPECT().Projects(mock.Anything).Return(linkProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, linkB1).Return(linkBoard(), nil).Times(4)
	f.boards.EXPECT().Board(mock.Anything, linkB2).Return(synthBoard(linkB2, "pw", "B2"), nil).Once()
	f.boards.EXPECT().Board(mock.Anything, linkB3).Return(synthBoard(linkB3, "ph", "B3"), nil).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Link: "Nonexistent"}})

	var nm *NoMatchError
	require.ErrorAs(t, err, &nm)
	assert.ErrorContains(t, err, "item 1")
}

func TestAddTasksItemWithBothNameAndLinkIsError(t *testing.T) {
	f := newTasksFixture(t)

	for _, in := range []TaskInput{{Name: "x", Link: "Beta"}, {}, {Name: "  ", Link: " "}} {
		_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "ok"}, in})
		require.ErrorContains(t, err, "item 2", "%+v", in)
		require.ErrorContains(t, err, "exactly one of name and link")
	}
	_, err := f.tasks.Add(t.Context(), alphaRef, "", nil)
	require.ErrorContains(t, err, "no items")
}

func TestAddTasksAmbiguousLinkFailsBeforeAnyWrite(t *testing.T) {
	f := newTasksFixture(t)
	b1 := synthBoard(linkB1, "pw", "B1",
		spec{id: "c1", title: "Alpha", list: "todo", pos: 1},
		spec{id: "c2", title: "Beta one", list: "doing", pos: 1},
		spec{id: "c3", title: "Beta two", list: "doing", pos: 2})
	f.projects.EXPECT().Projects(mock.Anything).Return(linkProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, linkB1).Return(b1, nil).Times(3)

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "first"}, {Link: "Beta"}})

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Len(t, amb.Candidates, 2)
}

func TestAddTasksPartialFailureSaysHowManyWereAdded(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Tasks", 65536)}))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, Name: ptr("a")})
	f.api.EXPECT().CreateTask(mock.Anything, "tl1", planka.CreateTaskRequest{Position: 131072, Name: ptr("b")}).
		Return(nil, errors.New("boom")).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "a"}, {Name: "b"}, {Name: "c"}})

	require.ErrorContains(t, err, "1 of 3 items were added")
	assert.ErrorContains(t, err, "boom")
}

func TestAddTasksFirstWriteFailureIsReturnedAsIs(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf([]planka.TaskList{list("tl1", "Tasks", 65536)}))
	boom := errors.New("boom")
	f.api.EXPECT().CreateTask(mock.Anything, "tl1", mock.Anything).Return(nil, boom).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "a"}})

	assert.Equal(t, boom, err)
}

func TestAddTasksListCreatedThenItemFailsSaysSo(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	f.readCard(cardOf(nil))
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", "Tasks", 65536.0).Return(&planka.TaskList{ID: "tl1", Name: "Tasks"}, nil).Once()
	f.api.EXPECT().CreateTask(mock.Anything, "tl1", mock.Anything).Return(nil, errors.New("boom")).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "a"}})

	require.ErrorContains(t, err, `task list "Tasks" was created`)
	assert.ErrorContains(t, err, "0 of 1 items were added")
}

func TestAddTasksReadBackFailureSaysItemsWereAdded(t *testing.T) {
	f := newTasksFixture(t)
	f.serveLocked(writeBoard())
	lists := []planka.TaskList{list("tl1", "Tasks", 65536)}
	f.readCard(cardOf(lists))
	f.created("tl1", planka.CreateTaskRequest{Position: 65536, Name: ptr("a")})
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(nil, errors.New("boom")).Once()

	_, err := f.tasks.Add(t.Context(), alphaRef, "", []TaskInput{{Name: "a"}})

	require.ErrorContains(t, err, "1 of 1 items were added")
	assert.ErrorContains(t, err, "reading the card back failed")
}

func TestUpdateTasksBatch(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().UpdateTask(mock.Anything, "t1", planka.UpdateTaskRequest{IsCompleted: ptr(true)}).
		Return(&planka.Task{ID: "t1", Name: ptr("One"), IsCompleted: true}, nil).Once()
	f.api.EXPECT().UpdateTask(mock.Anything, "t2", planka.UpdateTaskRequest{Name: ptr("Two!")}). // trimmed
													Return(&planka.Task{ID: "t2", Name: ptr("Two!")}, nil).Once()
	f.api.EXPECT().UpdateTask(mock.Anything, "t3", planka.UpdateTaskRequest{Name: ptr("Three"), IsCompleted: ptr(false)}).
		Return(&planka.Task{ID: "t3", LinkedCardID: ptr("c2"), Name: ptr("Three")}, nil).Once()

	got, err := f.tasks.Update(t.Context(), []TaskUpdate{
		{ID: " t1 ", Completed: ptr(true)},
		{ID: "t2\n", Name: ptr("  Two!\n")},
		{ID: "t3", Name: ptr("Three"), Completed: ptr(false)},
	})

	require.NoError(t, err)
	assert.Equal(t, []Task{
		{ID: "t1", Name: "One", Completed: true},
		{ID: "t2", Name: "Two!"},
		{ID: "t3", Name: "Three", Linked: &CardSummary{ID: "c2", Title: "Three"}},
	}, got)
}

func TestUpdateTasksValidatesEveryItemBeforeAnyWrite(t *testing.T) {
	f := newTasksFixture(t)
	ok := TaskUpdate{ID: "t1", Completed: ptr(true)}

	for name, bad := range map[string]TaskUpdate{
		"no id":         {Completed: ptr(true)},
		"nothing to do": {ID: "t2"},
		"empty name":    {ID: "t2", Name: ptr(" ")},
	} {
		_, err := f.tasks.Update(t.Context(), []TaskUpdate{ok, bad})
		require.ErrorContains(t, err, "item 2", name)
	}
	_, err := f.tasks.Update(t.Context(), nil)
	require.ErrorContains(t, err, "no items")
}

func TestUpdateTasksPartialFailureSaysWhichWereApplied(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().UpdateTask(mock.Anything, "t1", mock.Anything).Return(&planka.Task{ID: "t1"}, nil).Once()
	f.api.EXPECT().UpdateTask(mock.Anything, "t2", mock.Anything).Return(nil, errors.New("boom")).Once()

	_, err := f.tasks.Update(t.Context(), []TaskUpdate{
		{ID: "t1", Completed: ptr(true)}, {ID: "t2", Completed: ptr(true)}, {ID: "t3", Completed: ptr(true)},
	})

	require.ErrorContains(t, err, "applied: t1")
	require.ErrorContains(t, err, "item 2 (t2) failed")
	assert.ErrorContains(t, err, "boom")
}

func TestUpdateTasksFirstFailureIsNotAPartialApply(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().UpdateTask(mock.Anything, "t1", mock.Anything).Return(nil, &planka.APIError{Status: 404}).Once()

	_, err := f.tasks.Update(t.Context(), []TaskUpdate{{ID: "t1", Completed: ptr(true)}})

	require.ErrorIs(t, err, ErrNotFound)
	assert.NotContains(t, err.Error(), "applied")
}

func TestUpdateTasksExplainsARefusal(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().UpdateTask(mock.Anything, "t1", mock.Anything).Return(nil, &planka.APIError{Status: 403}).Once()

	_, err := f.tasks.Update(t.Context(), []TaskUpdate{{ID: "t1", Completed: ptr(true)}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "task t1")
	assert.Contains(t, err.Error(), "links a card")
	assert.Contains(t, err.Error(), "closed list such as Done")
	var status interface{ StatusCode() int }
	assert.NotErrorAs(t, err, &status, "the generic 403 hint must not be added on top")
}

func TestUpdateTasksExplainsARefusalAfterAPartialApply(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().UpdateTask(mock.Anything, "t1", mock.Anything).Return(&planka.Task{ID: "t1"}, nil).Once()
	f.api.EXPECT().UpdateTask(mock.Anything, "t2", mock.Anything).Return(nil, &planka.APIError{Status: 403}).Once()

	_, err := f.tasks.Update(t.Context(), []TaskUpdate{{ID: "t1", Completed: ptr(true)}, {ID: "t2", Completed: ptr(true)}})

	require.ErrorContains(t, err, "applied: t1")
	assert.ErrorContains(t, err, "links a card")
}

func TestDeleteTask(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().DeleteTask(mock.Anything, "t1").Return(nil).Once()

	require.NoError(t, f.tasks.Delete(t.Context(), " t1\n"))
	require.ErrorContains(t, f.tasks.Delete(t.Context(), ""), "task ID is empty")
}
