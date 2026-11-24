//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChecklistTools reorganises a card's checklists on a real Planka: creates
// and places checklists, moves, reorders and assigns tasks, renames and moves a
// checklist, deletes checklists (refused while they hold tasks), and deletes
// several tasks at once, reading the card back with get_card.
func TestChecklistTools(t *testing.T) {
	p, b := newFlowBoard(t)
	s := connectStdio(t, env.key)
	card := p.newCard(t, b, "Open", "Regroup me")
	read := func() cardDetail { return callInto[cardDetail](t, s, "get_card", map[string]any{"card": card}) }

	callInto[checklistResult](t, s, "create_checklist", map[string]any{"card": card, "name": "Steps"})
	callInto[checklistResult](t, s, "create_checklist", map[string]any{"card": card, "name": "Later", "hide_completed_tasks": true})
	first := callInto[checklistResult](t, s, "create_checklist", map[string]any{
		"card": card, "name": "First", "before": "steps", "show_on_front_of_card": false,
	})
	assert.Equal(t, []string{"First", "Steps", "Later"}, first.Order)
	assert.False(t, first.Checklist.ShowOnFrontOfCard)
	text, isError := call(t, s, "create_checklist", map[string]any{"card": card, "name": "LATER"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `already has a checklist "Later"`)

	added := callInto[taskList](t, s, "add_tasks", map[string]any{
		"card": card, "task_list": "Steps", "items": []map[string]any{{"name": "one"}, {"name": "two"}, {"name": "three"}},
	})
	require.Len(t, added.Tasks, 3)
	one, two, three := added.Tasks[0].ID, added.Tasks[1].ID, added.Tasks[2].ID

	// Tick one, then move one and two to Later in order, and put three first, assigned to me.
	callInto[[]task](t, s, "update_tasks", map[string]any{"items": []map[string]any{{"task_id": one, "completed": true}}})
	moved := callInto[[]task](t, s, "update_tasks", map[string]any{"card": card, "items": []map[string]any{
		{"task_id": one, "checklist": "Later"}, {"task_id": two, "checklist": "later"}, {"task_id": three, "position": "top", "assignee": "me"},
	}})
	assert.Equal(t, []string{"Later", "Later", "Steps"}, []string{moved[0].Checklist, moved[1].Checklist, moved[2].Checklist})

	got := read()
	assert.Equal(t, env.url+"/cards/"+card, got.URL)
	require.Len(t, got.TaskLists, 3)
	firstList, steps, later := got.TaskLists[0], got.TaskLists[1], got.TaskLists[2]
	require.NotNil(t, firstList.ShowOnFrontOfCard)
	assert.False(t, *firstList.ShowOnFrontOfCard)
	assert.Equal(t, "Later", later.Name)
	assert.True(t, later.HideCompletedTasks)
	require.Len(t, later.Tasks, 2)
	assert.Equal(t, []string{one, two}, []string{later.Tasks[0].ID, later.Tasks[1].ID}, "moved tasks keep their ids and order")
	assert.True(t, later.Tasks[0].Done, "and their done state")
	require.Len(t, steps.Tasks, 1)
	assert.Equal(t, p.meName(t), steps.Tasks[0].Assignee)

	renamed := callInto[checklistResult](t, s, "update_checklist", map[string]any{
		"card": card, "checklist": "Later", "name": "Done steps", "before": "First",
	})
	assert.Equal(t, []string{"Done steps", "First", "Steps"}, renamed.Order)

	text, isError = call(t, s, "delete_checklist", map[string]any{"card": card, "checklist": "Done steps"})
	assert.True(t, isError, text)
	assert.Contains(t, text, "has 2 tasks")
	deleted := callInto[deletedChecklist](t, s, "delete_checklist", map[string]any{"card": card, "checklist": "Done steps", "with_tasks": true})
	require.Len(t, deleted.Tasks, 2)
	assert.Equal(t, "one", deleted.Tasks[0].Name)
	callInto[deletedChecklist](t, s, "delete_checklist", map[string]any{"card": card, "checklist": "First"})

	more := callInto[taskList](t, s, "add_tasks", map[string]any{
		"card": card, "task_list": "Steps", "items": []map[string]any{{"name": "four"}, {"name": "five"}},
	})
	require.Len(t, more.Tasks, 3)
	callInto[struct{ Deleted []string }](t, s, "delete_task", map[string]any{"task_ids": []string{more.Tasks[1].ID, more.Tasks[2].ID}})
	got = read()
	require.Len(t, got.TaskLists, 1)
	require.Len(t, got.TaskLists[0].Tasks, 1)
	assert.Equal(t, "three", got.TaskLists[0].Tasks[0].Name)
}

// TestSearchByDueDate: overdue finds a card past its due date and not done;
// none finds a card without one.
func TestSearchByDueDate(t *testing.T) {
	_, b := newFlowBoard(t)
	s := connectStdio(t, env.key)
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "title": "Long overdue", "due": "2020-01-01"})
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "title": "Done long ago", "due": "2020-01-02"})
	callInto[writeResult](t, s, "update_card", map[string]any{"card": "Done long ago", "board": b.ID, "due_completed": true})
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "title": "Whenever"})

	titles := func(due string) []string {
		res := callInto[searchResult](t, s, "search_cards", map[string]any{"board": b.ID, "due": due})
		out := make([]string, 0, len(res.Cards))
		for _, c := range res.Cards {
			out = append(out, c.Title)
		}
		return out
	}
	assert.Equal(t, []string{"Long overdue"}, titles("overdue"))
	assert.Equal(t, []string{"Whenever"}, titles("none"))
}
