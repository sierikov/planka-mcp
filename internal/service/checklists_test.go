package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// twoLists is card c1 with the checklists Steps (tl1: t1, t2) and Later (tl2: t3).
func twoLists() *planka.CardResponse {
	return cardOf([]planka.TaskList{list("tl1", "Steps", 65536), list("tl2", "Later", 131072)},
		task("t1", "tl1", "one", 65536, true), task("t2", "tl1", "two", 131072, false),
		task("t3", "tl2", "three", 65536, false))
}

// lockedCard expects the reads of a checklist write on c1: the board, the card
// under the lock, then the card read back.
func (f *tasksFixture) lockedCard(before, after *planka.CardResponse) {
	f.serveLocked(writeBoard())
	f.readCard(before)
	if after != nil {
		f.readCard(after)
	}
}

func TestCreateChecklistGoesLastByDefault(t *testing.T) {
	f := newTasksFixture(t)
	after := twoLists()
	after.Included.TaskLists = append(after.Included.TaskLists, list("tl9", "Review", 131072+65536))
	f.lockedCard(twoLists(), after)
	f.api.EXPECT().CreateTaskList(mock.Anything, "c1", planka.CreateTaskListRequest{Name: "Review", Position: 131072 + 65536}).
		Return(&planka.TaskList{ID: "tl9", Name: "Review"}, nil).Once()

	got, err := f.tasks.CreateChecklist(t.Context(), CreateChecklist{Card: alphaRef, Name: " Review "})

	require.NoError(t, err)
	assert.Equal(t, "tl9", got.Checklist.ID)
	assert.Equal(t, []string{"Steps", "Later", "Review"}, got.Order)
}

func TestCreateChecklistPlacementAndOptions(t *testing.T) {
	for name, tc := range map[string]struct {
		place ListPlace
		want  float64
	}{
		"after the first":  {ListPlace{After: "steps"}, (65536 + 131072) / 2},
		"before the first": {ListPlace{Before: "Steps"}, 65536 / 2},
		"after by ID":      {ListPlace{After: "5002"}, 131072 + 65536},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTasksFixture(t)
			// Numeric IDs, as Planka's are, so the checklist can be named by ID.
			card := cardOf([]planka.TaskList{list("5001", "Steps", 65536), list("5002", "Later", 131072)})
			f.lockedCard(card, card)
			f.api.EXPECT().CreateTaskList(mock.Anything, "c1", planka.CreateTaskListRequest{
				Name: "Review", Position: tc.want, ShowOnFrontOfCard: new(false), HideCompletedTasks: new(true),
			}).Return(&planka.TaskList{ID: "tl9"}, nil).Once()

			_, err := f.tasks.CreateChecklist(t.Context(), CreateChecklist{
				Card: alphaRef, Name: "Review", Place: tc.place, ShowOnFrontOfCard: new(false), HideCompletedTasks: new(true),
			})

			require.NoError(t, err)
		})
	}
}

// TestChecklistWritesRefuseBeforeWriting: each is an error, and no write mock
// is set up, so nothing may be written.
func TestChecklistWritesRefuseBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		run  func(*Tasks) error
		want string
	}{
		"empty name": {func(s *Tasks) error {
			_, err := s.CreateChecklist(t.Context(), CreateChecklist{Card: alphaRef, Name: " "})
			return err
		}, "checklist name is empty"},
		"name too long in UTF-16": {func(s *Tasks) error {
			_, err := s.CreateChecklist(t.Context(), CreateChecklist{Card: alphaRef, Name: strings.Repeat("🚀", 65)})
			return err
		}, "checklist name is longer than 128 characters"},
		"duplicate name": {func(s *Tasks) error {
			_, err := s.CreateChecklist(t.Context(), CreateChecklist{Card: alphaRef, Name: "STEPS"})
			return err
		}, `card "Alpha" already has a checklist "Steps" (id tl1): pick another name`},
		"unknown neighbour": {func(s *Tasks) error {
			_, err := s.CreateChecklist(t.Context(), CreateChecklist{Card: alphaRef, Name: "X", Place: ListPlace{After: "Ste"}})
			return err
		}, `no checklist "Ste" on card "Alpha"; checklists: Steps, Later`},
		"nothing to change": {func(s *Tasks) error {
			_, err := s.UpdateChecklist(t.Context(), UpdateChecklist{Card: alphaRef, Checklist: "Steps"})
			return err
		}, "nothing to change"},
		"rename onto a sibling": {func(s *Tasks) error {
			_, err := s.UpdateChecklist(t.Context(), UpdateChecklist{Card: alphaRef, Checklist: "Steps", Name: new("later")})
			return err
		}, `already has a checklist "Later"`},
		"after itself": {func(s *Tasks) error {
			_, err := s.UpdateChecklist(t.Context(), UpdateChecklist{Card: alphaRef, Checklist: "Steps", Place: ListPlace{After: "steps"}})
			return err
		}, "a checklist cannot go after or before itself"},
		"delete with tasks": {func(s *Tasks) error {
			_, err := s.DeleteChecklist(t.Context(), alphaRef, "Steps", false)
			return err
		}, `checklist "Steps" has 2 tasks, and deleting it deletes them too, for good: move them first with update_tasks, or set with_tasks=true`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTasksFixture(t)
			f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil).Maybe()
			f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Maybe()
			f.cards.EXPECT().Card(mock.Anything, "c1").Return(twoLists(), nil).Maybe()

			require.ErrorContains(t, tc.run(f.tasks), tc.want)
		})
	}
}

func TestUpdateChecklistSendsOnlyWhatChanges(t *testing.T) {
	f := newTasksFixture(t)
	f.lockedCard(twoLists(), twoLists())
	f.api.EXPECT().UpdateTaskList(mock.Anything, "tl2", planka.UpdateTaskListRequest{
		Name: planka.Some("LATER"), Position: planka.Some(65536.0 / 2), HideCompletedTasks: planka.Some(true),
	}).Return(&planka.TaskList{ID: "tl2"}, nil).Once()

	// show_on_front_of_card is already true, so it is not sent; a case-only rename is allowed.
	_, err := f.tasks.UpdateChecklist(t.Context(), UpdateChecklist{
		Card: alphaRef, Checklist: "later", Name: new("LATER"), Place: ListPlace{Before: "Steps"},
		ShowOnFrontOfCard: new(true), HideCompletedTasks: new(true),
	})

	require.NoError(t, err)
}

func TestChecklistOfOneNameIsAmbiguous(t *testing.T) {
	f := newTasksFixture(t)
	twins := cardOf([]planka.TaskList{list("tl1", "Steps", 1), list("tl2", "steps", 2)})
	f.lockedCard(twins, nil)

	_, err := f.tasks.DeleteChecklist(t.Context(), alphaRef, "Steps", true)

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, "checklist", amb.Kind)
	assert.Equal(t, []Candidate{{ID: "tl1", Label: "Steps"}, {ID: "tl2", Label: "steps"}}, amb.Candidates)
}

func TestDeleteChecklistWithTasksReturnsThem(t *testing.T) {
	f := newTasksFixture(t)
	f.lockedCard(twoLists(), nil)
	f.api.EXPECT().DeleteTaskList(mock.Anything, "tl1").Return(nil).Once()

	got, err := f.tasks.DeleteChecklist(t.Context(), alphaRef, "Steps", true)

	require.NoError(t, err)
	assert.Equal(t, "tl1", got.ID)
	assert.Equal(t, []Task{{ID: "t1", Name: "one", Completed: true}, {ID: "t2", Name: "two"}}, got.Tasks)
}

func TestDeleteEmptyChecklistNeedsNoFlag(t *testing.T) {
	f := newTasksFixture(t)
	f.lockedCard(cardOf([]planka.TaskList{list("tl1", "Taks", 1)}), nil)
	f.api.EXPECT().DeleteTaskList(mock.Anything, "tl1").Return(nil).Once()

	got, err := f.tasks.DeleteChecklist(t.Context(), alphaRef, "taks", false)

	require.NoError(t, err)
	assert.Empty(t, got.Tasks)
}

// updated expects one UpdateTask of id with exactly req, answering a task in list.
func (f *tasksFixture) updated(id, list string, req planka.UpdateTaskRequest) {
	f.api.EXPECT().UpdateTask(mock.Anything, id, req).Return(&planka.Task{ID: id, TaskListID: list, Name: new(id)}, nil).Once()
}

func TestUpdateTasksMovesKeepTheirOrder(t *testing.T) {
	f := newTasksFixture(t)
	f.lockedCard(twoLists(), nil)
	// Both go to the bottom of Later, one after the other.
	f.updated("t1", "tl2", planka.UpdateTaskRequest{TaskListID: new("tl2"), Position: new(65536.0 * 2)})
	f.updated("t2", "tl2", planka.UpdateTaskRequest{TaskListID: new("tl2"), Position: new(65536.0 * 3)})

	got, err := f.tasks.Update(t.Context(), alphaRef, []TaskUpdate{{ID: "t1", Checklist: "Later"}, {ID: "t2", Checklist: "later"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"Later", "Later"}, []string{got[0].Checklist, got[1].Checklist})
}

func TestUpdateTasksPlacesInTheChecklist(t *testing.T) {
	for name, tc := range map[string]struct {
		item TaskUpdate
		req  planka.UpdateTaskRequest
	}{
		"top":                     {TaskUpdate{ID: "t2", Position: "top"}, planka.UpdateTaskRequest{Position: new(65536.0 / 2)}},
		"before another":          {TaskUpdate{ID: "t2", Before: "t1"}, planka.UpdateTaskRequest{Position: new(65536.0 / 2)}},
		"after another, moved":    {TaskUpdate{ID: "t1", Checklist: "Later", After: "t3"}, planka.UpdateTaskRequest{TaskListID: new("tl2"), Position: new(65536.0 * 2)}},
		"before another, moved":   {TaskUpdate{ID: "t1", Checklist: "Later", Before: "t3"}, planka.UpdateTaskRequest{TaskListID: new("tl2"), Position: new(65536.0 / 2)}},
		"bottom of its checklist": {TaskUpdate{ID: "t1", Position: "bottom"}, planka.UpdateTaskRequest{Position: new(131072.0 + 65536)}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTasksFixture(t)
			f.lockedCard(twoLists(), nil)
			f.updated(tc.item.ID, "tl1", tc.req)

			_, err := f.tasks.Update(t.Context(), alphaRef, []TaskUpdate{tc.item})

			require.NoError(t, err)
		})
	}
}

func TestUpdateTasksAssigns(t *testing.T) {
	f := newTasksFixture(t)
	f.lockedCard(twoLists(), nil)
	f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me"}, nil).Once()
	f.updated("t1", "tl1", planka.UpdateTaskRequest{AssigneeUserID: planka.Some("u-alice")})
	f.updated("t2", "tl1", planka.UpdateTaskRequest{AssigneeUserID: planka.Some("u-me")})
	f.updated("t3", "tl2", planka.UpdateTaskRequest{AssigneeUserID: planka.Null[string]()})

	_, err := f.tasks.Update(t.Context(), alphaRef, []TaskUpdate{
		{ID: "t1", Assignee: new("alice")}, {ID: "t2", Assignee: new("me")}, {ID: "t3", Assignee: new("")},
	})

	require.NoError(t, err)
}

// TestUpdateTasksRefusesBeforeWriting: each is an error, and no UpdateTask
// mock is set up, so nothing may be written.
func TestUpdateTasksRefusesBeforeWriting(t *testing.T) {
	linked := twoLists()
	linked.Included.Tasks[0].LinkedCardID = new("c9")
	for name, tc := range map[string]struct {
		card  *planka.CardResponse
		items []TaskUpdate
		want  string
	}{
		"move without card": {nil, []TaskUpdate{{ID: "t1", Checklist: "Later"}}, "item 1 (t1): moving, reordering or assigning a task needs card"},
		"two places":        {nil, []TaskUpdate{{ID: "t1", Position: "top", After: "t2"}}, "give one of position, after and before"},
		"unknown position":  {nil, []TaskUpdate{{ID: "t1", Position: "middle"}}, `unknown position "middle": use top or bottom`},
		"not on the card":   {twoLists(), []TaskUpdate{{ID: "t1", Position: "top"}, {ID: "x9", Position: "top"}}, `item 2: task x9 is not on card "Alpha"`},
		"anchor elsewhere":  {twoLists(), []TaskUpdate{{ID: "t1", After: "t3"}}, `task t3 is not in checklist "Steps"`},
		"after itself":      {twoLists(), []TaskUpdate{{ID: "t1", After: "t1"}}, "a task cannot go after or before itself"},
		"unknown checklist": {twoLists(), []TaskUpdate{{ID: "t1", Checklist: "Done"}}, `item 1 (t1): no checklist "Done" on card "Alpha"`},
		"unknown assignee":  {twoLists(), []TaskUpdate{{ID: "t1", Assignee: new("bob")}}, `no user "bob"`},
		"rename a linked":   {linked, []TaskUpdate{{ID: "t1", Name: new("x")}}, "the task links a card, so it can only be moved or reordered"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTasksFixture(t)
			card := alphaRef
			if tc.card == nil {
				card = CardRef{}
			} else {
				f.lockedCard(tc.card, nil)
			}

			_, err := f.tasks.Update(t.Context(), card, tc.items)

			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestUpdateTasksMovesALinkedTask(t *testing.T) {
	f := newTasksFixture(t)
	linked := twoLists()
	linked.Included.Tasks[0].LinkedCardID = new("c9")
	f.lockedCard(linked, nil)
	f.updated("t1", "tl2", planka.UpdateTaskRequest{TaskListID: new("tl2"), Position: new(65536.0 * 2)})

	_, err := f.tasks.Update(t.Context(), alphaRef, []TaskUpdate{{ID: "t1", Checklist: "Later"}})

	require.NoError(t, err)
}

func TestDeleteTasks(t *testing.T) {
	f := newTasksFixture(t)
	f.api.EXPECT().DeleteTask(mock.Anything, "t1").Return(nil).Once()
	f.api.EXPECT().DeleteTask(mock.Anything, "t2").Return(&planka.APIError{Status: http.StatusNotFound}).Once()

	err := f.tasks.Delete(t.Context(), []string{"t1", "t2", "t3"})

	require.EqualError(t, err, "tasks were partly deleted (deleted: t1), but task t2 failed: task t2: not found")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteTasksRefusesBeforeDeleting(t *testing.T) {
	f := newTasksFixture(t)

	require.EqualError(t, f.tasks.Delete(t.Context(), nil), "no tasks to delete")
	require.EqualError(t, f.tasks.Delete(t.Context(), []string{"t1", " t1 "}), "task t1 is listed twice")
	require.EqualError(t, f.tasks.Delete(t.Context(), []string{"t1", ""}), "item 2: task ID is empty")
}
