package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestWriteToolAnnotations(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	want := map[string]*mcp.ToolAnnotations{
		"create_card":    {DestructiveHint: new(false), OpenWorldHint: new(false)},
		"move_card":      {IdempotentHint: true, OpenWorldHint: new(false)},
		"update_card":    {IdempotentHint: true, OpenWorldHint: new(false)},
		"delete_card":    {DestructiveHint: new(true), OpenWorldHint: new(false)},
		"add_comment":    {DestructiveHint: new(false), OpenWorldHint: new(false)},
		"update_comment": {IdempotentHint: true, OpenWorldHint: new(false)},
		"delete_comment": {DestructiveHint: new(true), OpenWorldHint: new(false)},
		"add_tasks":      {DestructiveHint: new(false), OpenWorldHint: new(false)},
		"update_tasks":   {IdempotentHint: true, OpenWorldHint: new(false)},
		"delete_task":    {DestructiveHint: new(true), OpenWorldHint: new(false)},
	}
	seen := 0
	for _, tool := range res.Tools {
		w, ok := want[tool.Name]
		if !ok {
			continue
		}
		seen++
		assert.Equal(t, w, tool.Annotations, tool.Name)
		assert.Nil(t, tool.OutputSchema, "%s must declare no output schema", tool.Name)
	}
	assert.Equal(t, len(want), seen, "every write tool is registered")
}

func TestWriteToolDescriptions(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	require.Contains(t, byName, "delete_card")
	assert.Contains(t, byName["delete_card"].Description, "permanently deletes")
	assert.Contains(t, byName["delete_card"].Description, "move_card list=archive")
	require.Contains(t, byName, "update_card")
	assert.Contains(t, byName["update_card"].Description, "description replaces the whole text")
}

func TestUpdateCardEmptyDueClears(t *testing.T) {
	cards := NewMockCardService(t)
	var got []service.UpdateCard
	cards.EXPECT().Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in service.UpdateCard) (*service.WriteResult, error) {
			got = append(got, in)
			return &service.WriteResult{Card: service.CardSummary{ID: "c1", Title: "A"}}, nil
		})
	session := connect(t, Deps{Cards: cards})

	call(t, session, "update_card", map[string]any{"card": "a", "due": ""})
	call(t, session, "update_card", map[string]any{"card": "a", "title": "B"})

	require.Len(t, got, 2)
	require.NotNil(t, got[0].Due, "an empty due is a pointer to an empty string")
	assert.Empty(t, *got[0].Due)
	assert.Nil(t, got[1].Due, "an absent due is nil")

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		if tool.Name != "update_card" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "or null/empty string to clear")
	}
}

// TestUpdateCardNullDueClears: the schema allows "due": null, and spec §7 says null clears the due
// date, so an explicit null must not be read as "left out".
func TestUpdateCardNullDueClears(t *testing.T) {
	cards := NewMockCardService(t)
	var got []service.UpdateCard
	cards.EXPECT().Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in service.UpdateCard) (*service.WriteResult, error) {
			got = append(got, in)
			return &service.WriteResult{Card: service.CardSummary{ID: "c1", Title: "A"}}, nil
		})
	session := connect(t, Deps{Cards: cards})

	res := call(t, session, "update_card", map[string]any{"card": "a", "due": nil, "title": nil})
	require.False(t, res.IsError, resultText(t, res))
	call(t, session, "update_card", map[string]any{"card": "a", "due": "2026-10-20"})

	require.Len(t, got, 2)
	require.NotNil(t, got[0].Due, "a null due is a pointer to an empty string")
	assert.Empty(t, *got[0].Due)
	assert.Nil(t, got[0].Title, "null stays \"left out\" for the other fields")
	assert.Equal(t, "a", got[0].Card.Ref, "the other fields still decode")
	require.NotNil(t, got[1].Due)
	assert.Equal(t, "2026-10-20", *got[1].Due)
}

func TestUpdateCardPassesEverything(t *testing.T) {
	cards := NewMockCardService(t)
	want := service.UpdateCard{
		Card:  service.CardRef{Ref: "readme", Board: "Planka MCP"},
		Title: new("T"), Description: new("D"), Due: new("2026-10-20"), DueCompleted: new(true),
		AddLabels: []string{"a"}, RemoveLabels: []string{"b"}, CreateLabels: true, AddAssignees: []string{"me"}, RemoveAssignees: []string{"Ann"},
		Stopwatch: "start",
	}
	cards.EXPECT().Update(mock.Anything, want).Return(&service.WriteResult{
		Card:          service.CardSummary{ID: "c1", Title: "T", Labels: []string{"a"}},
		CreatedLabels: []string{"a"},
	}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "update_card", map[string]any{
		"card": "readme", "board": "Planka MCP", "title": "T", "description": "D", "due": "2026-10-20", "due_completed": true,
		"add_labels": []string{"a"}, "remove_labels": []string{"b"}, "create_labels": true, "add_assignees": []string{"me"},
		"remove_assignees": []string{"Ann"}, "stopwatch": "start",
	})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"card":{"id":"c1","title":"T","labels":["a"]},"created_labels":["a"]}`, resultText(t, res))
}

func TestCreateCardOutput(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Create(mock.Anything, service.CreateCard{
		Board: "b", List: "Open", Title: "New", Description: "d", Labels: []string{"x"}, CreateLabels: true, Assignees: []string{"me"},
		Due: "2026-10-20 14:30", Position: "bottom",
	}).Return(&service.WriteResult{Card: service.CardSummary{ID: "c9", Title: "New", Board: "B", List: "Open"}}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "create_card", map[string]any{
		"board": "b", "list": "Open", "title": "New", "description": "d", "labels": []string{"x"}, "create_labels": true,
		"assignees": []string{"me"}, "due": "2026-10-20 14:30", "position": "bottom",
	})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"card":{"id":"c9","title":"New","board":"B","list":"Open"}}`, resultText(t, res))
}

func TestMoveCardOutput(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Move(mock.Anything, service.MoveCard{
		Card: service.CardRef{Ref: "readme", Board: "Old"}, List: "archive", ToBoard: "New", Position: "top",
	}).Return(&service.WriteResult{Card: service.CardSummary{ID: "c1", Title: "Write README", Board: "New", List: "(archived)"}}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "move_card", map[string]any{
		"card": "readme", "board": "Old", "list": "archive", "to_board": "New", "position": "top",
	})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"card":{"id":"c1","title":"Write README","board":"New","list":"(archived)"}}`, resultText(t, res))
}

func TestDeleteCardOutput(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Delete(mock.Anything, service.CardRef{Ref: "c1", Board: "B"}).
		Return(&service.CardSummary{ID: "c1", Title: "Gone"}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "delete_card", map[string]any{"card": "c1", "board": "B"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"c1","title":"Gone"}`, resultText(t, res))
}

func TestWriteErrorsAreToolResults(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().Move(mock.Anything, mock.Anything).Return(nil, &service.UnknownNameError{
		Kind: "list", Name: "Doing", Scope: "board Planka MCP", Valid: []string{"Open", "Done"},
	})

	res := call(t, connect(t, Deps{Cards: cards}), "move_card", map[string]any{"card": "c1", "list": "Doing"})
	assert.True(t, res.IsError)
	assert.Equal(t, `no list "Doing" on board Planka MCP; valid lists: Open, Done`, resultText(t, res))
}

func TestAddCommentOutput(t *testing.T) {
	comments := NewMockCommentService(t)
	comments.EXPECT().Add(mock.Anything, service.CardRef{Ref: "readme", Board: "B"}, "started").Return(&service.Comment{
		ID: "m1", Author: "Ann", Text: "started", Created: time.Date(2026, 10, 17, 9, 5, 0, 0, time.Local),
	}, nil)

	res := call(t, connect(t, Deps{Comments: comments}), "add_comment", map[string]any{"card": "readme", "board": "B", "text": "started"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"m1","author":"Ann","date":"2026-10-17 09:05","text":"started"}`, resultText(t, res))
}

func TestUpdateCommentOutputHasNoAuthor(t *testing.T) {
	comments := NewMockCommentService(t)
	comments.EXPECT().Update(mock.Anything, "m1", "edited").Return(&service.Comment{
		ID: "m1", Text: "edited", Created: time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local),
	}, nil)

	res := call(t, connect(t, Deps{Comments: comments}), "update_comment", map[string]any{"comment_id": "m1", "text": "edited"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"m1","date":"2026-10-17","text":"edited"}`, resultText(t, res))
}

func TestDeleteCommentAndTaskOutput(t *testing.T) {
	comments := NewMockCommentService(t)
	comments.EXPECT().Delete(mock.Anything, "m1").Return(nil)
	tasks := NewMockTaskService(t)
	tasks.EXPECT().Delete(mock.Anything, "t1").Return(nil)
	session := connect(t, Deps{Comments: comments, Tasks: tasks})

	res := call(t, session, "delete_comment", map[string]any{"comment_id": "m1"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"m1"}`, resultText(t, res))

	res = call(t, session, "delete_task", map[string]any{"task_id": "t1"})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"deleted":"t1"}`, resultText(t, res))
}

func TestAddTasksOutput(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().Add(mock.Anything, service.CardRef{Ref: "research"}, "Steps", []service.TaskInput{
		{Name: "step 1"}, {Link: "readme"},
	}).Return(&service.TaskList{ID: "tl1", Name: "Steps", Tasks: []service.Task{
		{ID: "t1", Name: "step 1"},
		{ID: "t2", Name: "Write README", Linked: &service.CardSummary{ID: "c1", Title: "Write README", List: "Open"}},
	}}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "add_tasks", map[string]any{
		"card": "research", "task_list": "Steps",
		"items": []map[string]any{{"name": "step 1"}, {"link": "readme"}},
	})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"name":"Steps","tasks":[{"id":"t1","name":"step 1"},`+
		`{"id":"t2","name":"Write README","link":{"id":"c1","title":"Write README","list":"Open"}}]}`, resultText(t, res))
}

func TestAddTasksRejectsEmptyItems(t *testing.T) {
	// The mock has no expectations: the service must not be called.
	tasks := NewMockTaskService(t)
	session := connect(t, Deps{Tasks: tasks})

	res := call(t, session, "add_tasks", map[string]any{"card": "c1", "items": []map[string]any{}})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "items is empty")

	// A missing items fails schema validation before the handler runs.
	res = call(t, session, "add_tasks", map[string]any{"card": "c1"})
	assert.True(t, res.IsError)
}

func TestUpdateTasksOutput(t *testing.T) {
	tasks := NewMockTaskService(t)
	tasks.EXPECT().Update(mock.Anything, []service.TaskUpdate{
		{ID: "t1", Completed: new(true)},
		{ID: "t2", Name: new("renamed")},
	}).Return([]service.Task{
		{ID: "t1", Name: "one", Completed: true},
		{ID: "t2", Name: "renamed"},
	}, nil)

	res := call(t, connect(t, Deps{Tasks: tasks}), "update_tasks", map[string]any{
		"items": []map[string]any{{"task_id": "t1", "completed": true}, {"task_id": "t2", "name": "renamed"}},
	})
	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `[{"id":"t1","name":"one","done":true},{"id":"t2","name":"renamed"}]`, resultText(t, res))
}
