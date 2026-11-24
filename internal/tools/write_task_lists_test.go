package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestCreateChecklistOutput(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().CreateChecklist(mock.Anything, service.CreateChecklist{
		Card: service.CardRef{Ref: "c1"}, Name: "Review", Place: service.ListPlace{After: "Steps"}, HideCompletedTasks: new(true),
	}).Return(&service.ChecklistResult{
		Checklist: service.TaskList{ID: "tl9", Name: "Review", ShowOnFrontOfCard: true, HideCompletedTasks: true},
		Order:     []string{"Steps", "Review"},
	}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "create_checklist", map[string]any{
		"card": "c1", "name": "Review", "after": "Steps", "hide_completed_tasks": true,
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"checklist":{"id":"tl9","name":"Review","show_on_front_of_card":true,"hide_completed_tasks":true,"tasks_total":0},`+
		`"checklist_order":["Steps","Review"]}`, resultText(t, res))
}

func TestUpdateChecklistOutput(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().UpdateChecklist(mock.Anything, service.UpdateChecklist{
		Card: service.CardRef{Ref: "c1", Board: "B"}, Checklist: "Steps", Name: new("Stages"), ShowOnFrontOfCard: new(false),
	}).Return(&service.ChecklistResult{
		Checklist: service.TaskList{ID: "tl1", Name: "Stages", Tasks: []service.Task{{ID: "t1"}, {ID: "t2"}}},
		Order:     []string{"Stages"},
	}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "update_checklist", map[string]any{
		"card": "c1", "board": "B", "checklist": "Steps", "name": "Stages", "show_on_front_of_card": false,
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"checklist":{"id":"tl1","name":"Stages","show_on_front_of_card":false,"hide_completed_tasks":false,"tasks_total":2},`+
		`"checklist_order":["Stages"]}`, resultText(t, res))
}

func TestDeleteChecklistListsTheDeletedTasks(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().DeleteChecklist(mock.Anything, service.CardRef{Ref: "c1"}, "Old", true).Return(&service.DeletedChecklist{
		ID: "tl3", Name: "Old", Tasks: []service.Task{{ID: "t1", Name: "keep me", Completed: true}},
	}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "delete_checklist", map[string]any{"card": "c1", "checklist": "Old", "with_tasks": true})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"tl3","name":"Old","tasks":[{"id":"t1","name":"keep me","done":true}]}`, resultText(t, res))
}

func TestUpdateTasksMovesAndAssigns(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().Update(mock.Anything, service.CardRef{Ref: "c1"}, []service.TaskUpdate{
		{ID: "t1", Checklist: "Later", Position: "top"},
		{ID: "t2", After: "t1", Assignee: new("me")},
		{ID: "t3", Assignee: new("")},
	}).Return([]service.Task{
		{ID: "t1", Name: "one", Checklist: "Later"},
		{ID: "t2", Name: "two", Checklist: "Later", Assignee: "Ann"},
		{ID: "t3", Name: "three", Checklist: "Steps"},
	}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "update_tasks", map[string]any{
		"card": "c1",
		"items": []map[string]any{
			{"task_id": "t1", "checklist": "Later", "position": "top"},
			{"task_id": "t2", "after": "t1", "assignee": "me"},
			{"task_id": "t3", "assignee": ""},
		},
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `[{"id":"t1","name":"one","checklist":"Later"},{"id":"t2","name":"two","checklist":"Later","assignee":"Ann"},`+
		`{"id":"t3","name":"three","checklist":"Steps"}]`, resultText(t, res))
}

func TestUpdateTasksPositionIsAnEnum(t *testing.T) {
	session := connect(t, Deps{Tasks: NewMockTaskService(t)})

	raw, err := json.Marshal(inputProps(t, session)["update_tasks"]["items"]["items"])
	require.NoError(t, err)
	var item struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &item))
	assert.Equal(t, toAny(service.Positions), item.Properties["position"].Enum)

	// The mock expects nothing: the SDK refuses the value before the handler runs.
	bad := call(t, session, "update_tasks", map[string]any{"card": "c1", "items": []map[string]any{{"task_id": "t1", "position": "middle"}}})
	assert.True(t, bad.IsError)
}

func TestDeleteTaskTakesOneOrSeveral(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().Delete(mock.Anything, []string{"t1", "t2"}).Return(nil)
	session := connect(t, Deps{Tasks: tasks})

	res := call(t, session, "delete_task", map[string]any{"task_ids": []string{"t1", "t2"}})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":["t1","t2"]}`, resultText(t, res))

	res = call(t, session, "delete_task", map[string]any{"task_id": "t1", "task_ids": []string{"t2"}})
	assert.True(t, res.IsError)
	assert.Equal(t, "give task_id or task_ids, not both", resultText(t, res))

	res = call(t, session, "delete_task", map[string]any{})
	assert.True(t, res.IsError)
	assert.Equal(t, "give task_id, or task_ids for several tasks", resultText(t, res))
}
