package planka

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

func ptr[T any](v T) *T { return &v }

func TestCreateCard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/lists/l1/cards": plankatest.Fixture(t, "card_created")})
	due := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)

	card, err := c.CreateCard(t.Context(), "l1", CreateCardRequest{
		Type: "project", Position: 524288, Name: "Scratch card", Description: ptr("scratch"), DueDate: &due,
	})

	require.NoError(t, err)
	assert.Equal(t, "Scratch card", card.Name)
	assert.Equal(t, "project", card.Type)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/lists/l1/cards", srv.Last().Path)
	assert.JSONEq(t, `{"type":"project","position":524288,"name":"Scratch card","description":"scratch","dueDate":"2026-12-31T12:00:00.000Z"}`, string(srv.Last().Body))
}

func TestCreateCardMinimalOmitsOptionalFields(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/lists/l1/cards": plankatest.Fixture(t, "card_created")})

	_, err := c.CreateCard(t.Context(), "l1", CreateCardRequest{Type: "story", Position: 65536, Name: "x"})

	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"story","position":65536,"name":"x"}`, string(srv.Last().Body))
}

func TestUpdateCard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})
	due := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)

	card, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{
		BoardID:        Some("b2"),
		ListID:         Some("l2"),
		Position:       Some(131072.0),
		Name:           Some("Scratch card renamed"),
		Description:    Some("updated"),
		DueDate:        Some(due),
		IsDueCompleted: Some(false),
		Stopwatch:      Some(Stopwatch{Total: 5}),
	})

	require.NoError(t, err)
	assert.Equal(t, "Scratch card renamed", card.Name)
	assert.Equal(t, http.MethodPatch, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1", srv.Last().Path)
	assert.JSONEq(t, `{
		"boardId":"b2","listId":"l2","position":131072,"name":"Scratch card renamed",
		"description":"updated","dueDate":"2026-12-31T12:00:00.000Z","isDueCompleted":false,
		"stopwatch":{"startedAt":null,"total":5}
	}`, string(srv.Last().Body))
}

// TestTimesAreSentAsUTCMilliseconds pins the time encoding. Planka 2.2.1 stores a due date's wall
// clock and drops its offset: "2026-12-01T14:30:00+01:00" comes back as 14:30Z, an hour off. So
// every time goes out in UTC with millisecond precision, the form Planka's own web client sends.
func TestTimesAreSentAsUTCMilliseconds(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	due := time.Date(2026, 12, 1, 14, 30, 0, 123456789, cet)
	started := time.Date(2026, 10, 17, 16, 0, 0, 987654321, time.FixedZone("CEST", 7200))
	c, srv := newTestClient(t, map[string]any{
		"POST /api/lists/l1/cards": plankatest.Fixture(t, "card_created"),
		"PATCH /api/cards/c1":      plankatest.Fixture(t, "card_updated"),
	})

	_, err := c.CreateCard(t.Context(), "l1", CreateCardRequest{Type: "project", Position: 1, Name: "x", DueDate: &due})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"project","position":1,"name":"x","dueDate":"2026-12-01T13:30:00.123Z"}`, string(srv.Last().Body))

	_, err = c.UpdateCard(t.Context(), "c1", UpdateCardRequest{
		DueDate: Some(due), Stopwatch: Some(Stopwatch{StartedAt: &started, Total: 5}),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"dueDate":"2026-12-01T13:30:00.123Z","stopwatch":{"startedAt":"2026-10-17T14:00:00.987Z","total":5}}`,
		string(srv.Last().Body))
}

func TestUpdateCardSendsOnlySetFields(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	_, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{Name: Some("n")})

	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"n"}`, string(srv.Last().Body))
}

func TestUpdateCardEmptyRequestIsEmptyObject(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	_, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{})

	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(srv.Last().Body))
}

func TestUpdateCardSendsExplicitNull(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	_, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{DueDate: Null[time.Time]()})

	require.NoError(t, err)
	assert.JSONEq(t, `{"dueDate":null}`, string(srv.Last().Body))
}

func TestUpdateCardEmptyStringIsNotNull(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	_, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{Description: Some("")})

	require.NoError(t, err)
	assert.JSONEq(t, `{"description":""}`, string(srv.Last().Body))
}

func TestDeleteCard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	require.NoError(t, c.DeleteCard(t.Context(), "c1"))

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1", srv.Last().Path)
	assert.Empty(t, srv.Last().Body)
}

func TestAddCardLabel(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/card-labels": plankatest.Fixture(t, "card_label_created")})

	require.NoError(t, c.AddCardLabel(t.Context(), "c1", "lab1"))

	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/card-labels", srv.Last().Path)
	assert.JSONEq(t, `{"labelId":"lab1"}`, string(srv.Last().Body))
}

func TestRemoveCardLabelPath(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/cards/c1/card-labels/labelId:lab1": plankatest.Fixture(t, "card_label_created")})

	require.NoError(t, c.RemoveCardLabel(t.Context(), "c1", "lab1"))

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/card-labels/labelId:lab1", srv.Last().Path)
}

func TestAddCardMember(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/card-memberships": plankatest.Fixture(t, "card_membership_created")})

	require.NoError(t, c.AddCardMember(t.Context(), "c1", "u1"))

	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/card-memberships", srv.Last().Path)
	assert.JSONEq(t, `{"userId":"u1"}`, string(srv.Last().Body))
}

func TestRemoveCardMember(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/cards/c1/card-memberships/userId:u1": plankatest.Fixture(t, "card_membership_created")})

	require.NoError(t, c.RemoveCardMember(t.Context(), "c1", "u1"))

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/card-memberships/userId:u1", srv.Last().Path)
}

func TestCreateLabel(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/boards/b1/labels": plankatest.Fixture(t, "label_created")})

	l, err := c.CreateLabel(t.Context(), "b1", "scratch-label", "muddy-grey", 524288)

	require.NoError(t, err)
	require.NotNil(t, l.Name)
	assert.Equal(t, "scratch-label", *l.Name)
	assert.Equal(t, "muddy-grey", l.Color)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/boards/b1/labels", srv.Last().Path)
	assert.JSONEq(t, `{"position":524288,"name":"scratch-label","color":"muddy-grey"}`, string(srv.Last().Body))
}

func TestCreateComment(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/comments": plankatest.Fixture(t, "comment_created")})

	cm, err := c.CreateComment(t.Context(), "c1", "scratch comment")

	require.NoError(t, err)
	assert.Equal(t, "scratch comment", cm.Text)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/comments", srv.Last().Path)
	assert.JSONEq(t, `{"text":"scratch comment"}`, string(srv.Last().Body))
}

func TestUpdateComment(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/comments/cm1": plankatest.Fixture(t, "comment_created")})

	cm, err := c.UpdateComment(t.Context(), "cm1", "edited")

	require.NoError(t, err)
	assert.NotEmpty(t, cm.ID)
	assert.Equal(t, http.MethodPatch, srv.Last().Method)
	assert.Equal(t, "/api/comments/cm1", srv.Last().Path)
	assert.JSONEq(t, `{"text":"edited"}`, string(srv.Last().Body))
}

func TestDeleteComment(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/comments/cm1": plankatest.Fixture(t, "comment_created")})

	require.NoError(t, c.DeleteComment(t.Context(), "cm1"))

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/comments/cm1", srv.Last().Path)
}

func TestCreateTaskList(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/task-lists": plankatest.Fixture(t, "task_list_created")})

	tl, err := c.CreateTaskList(t.Context(), "c1", "Scratch tasks", 65536)

	require.NoError(t, err)
	assert.Equal(t, "Scratch tasks", tl.Name)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/cards/c1/task-lists", srv.Last().Path)
	assert.JSONEq(t, `{"position":65536,"name":"Scratch tasks"}`, string(srv.Last().Body))
}

func TestCreateTask(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/task-lists/tl1/tasks": plankatest.Fixture(t, "task_created")})

	task, err := c.CreateTask(t.Context(), "tl1", CreateTaskRequest{Position: 65536, Name: ptr("Scratch task")})

	require.NoError(t, err)
	require.NotNil(t, task.Name)
	assert.Equal(t, "Scratch task", *task.Name)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, "/api/task-lists/tl1/tasks", srv.Last().Path)
	assert.JSONEq(t, `{"position":65536,"name":"Scratch task","isCompleted":false}`, string(srv.Last().Body))
}

func TestCreateTaskLinked(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/task-lists/tl1/tasks": plankatest.Fixture(t, "task_created")})

	_, err := c.CreateTask(t.Context(), "tl1", CreateTaskRequest{Position: 131072, LinkedCardID: ptr("c2"), IsCompleted: true})

	require.NoError(t, err)
	assert.JSONEq(t, `{"position":131072,"linkedCardId":"c2","isCompleted":true}`, string(srv.Last().Body))
}

func TestUpdateTask(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/tasks/t1": plankatest.Fixture(t, "task_updated")})

	task, err := c.UpdateTask(t.Context(), "t1", UpdateTaskRequest{Name: ptr("renamed"), IsCompleted: ptr(true)})

	require.NoError(t, err)
	assert.True(t, task.IsCompleted)
	assert.Equal(t, http.MethodPatch, srv.Last().Method)
	assert.Equal(t, "/api/tasks/t1", srv.Last().Path)
	assert.JSONEq(t, `{"name":"renamed","isCompleted":true}`, string(srv.Last().Body))
}

func TestUpdateTaskOmitsUnsetFields(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/tasks/t1": plankatest.Fixture(t, "task_updated")})

	_, err := c.UpdateTask(t.Context(), "t1", UpdateTaskRequest{IsCompleted: ptr(false)})

	require.NoError(t, err)
	assert.JSONEq(t, `{"isCompleted":false}`, string(srv.Last().Body))
}

func TestDeleteTask(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"DELETE /api/tasks/t1": plankatest.Fixture(t, "task_updated")})

	require.NoError(t, c.DeleteTask(t.Context(), "t1"))

	assert.Equal(t, http.MethodDelete, srv.Last().Method)
	assert.Equal(t, "/api/tasks/t1", srv.Last().Path)
}

func TestWriteEscapesPathParams(t *testing.T) {
	c, _ := newTestClient(t, map[string]any{})

	err := c.DeleteCard(t.Context(), "../../admin")

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Status)
}
