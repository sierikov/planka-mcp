package planka

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBoardSendsAForm(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"POST /api/projects/p1/boards": json.RawMessage(`{"item":{"id":"b9","projectId":"p1","name":"Roadmap","position":196608}}`),
	})

	b, err := c.CreateBoard(t.Context(), "p1", "Roadmap", 196608)

	require.NoError(t, err)
	assert.Equal(t, "b9", b.ID)
	assert.Equal(t, []formPart{{name: "position", data: "196608"}, {name: "name", data: "Roadmap"}}, readForm(t, srv.Last()))
}

func TestUpdateAndDeleteBoard(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"PATCH /api/boards/b9":  json.RawMessage(`{"item":{"id":"b9","name":"Plans"}}`),
		"DELETE /api/boards/b9": json.RawMessage(`{"item":{"id":"b9"}}`),
	})

	b, err := c.UpdateBoard(t.Context(), "b9", "Plans")
	require.NoError(t, err)
	assert.Equal(t, "Plans", b.Name)
	assert.JSONEq(t, `{"name":"Plans"}`, string(srv.Last().Body))

	require.NoError(t, c.DeleteBoard(t.Context(), "b9"))
	assert.Equal(t, http.MethodDelete, srv.Last().Method)
}

func TestBoardMemberships(t *testing.T) {
	member := json.RawMessage(`{"item":{"id":"m1","boardId":"b9","userId":"u2","role":"viewer","canComment":true}}`)
	c, srv := newTestClient(t, map[string]any{
		"POST /api/boards/b9/board-memberships": member,
		"PATCH /api/board-memberships/m1":       member,
		"DELETE /api/board-memberships/m1":      member,
		"GET /api/users":                        json.RawMessage(`{"items":[{"id":"u2","name":"Ann","username":"ann","email":"ann@example.com"}]}`),
	})

	m, err := c.CreateBoardMembership(t.Context(), "b9", BoardMembershipRequest{UserID: Some("u2"), Role: Some("viewer"), CanComment: Some(true)})
	require.NoError(t, err)
	assert.Equal(t, "viewer", m.Role)
	require.NotNil(t, m.CanComment)
	assert.True(t, *m.CanComment)
	assert.JSONEq(t, `{"userId":"u2","role":"viewer","canComment":true}`, string(srv.Last().Body))

	_, err = c.UpdateBoardMembership(t.Context(), "m1", BoardMembershipRequest{Role: Some("editor")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"editor"}`, string(srv.Last().Body))

	require.NoError(t, c.DeleteBoardMembership(t.Context(), "m1"))

	users, err := c.Users(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []User{{ID: "u2", Name: "Ann", Username: "ann", Email: "ann@example.com"}}, users)
}
