package planka

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProject(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"POST /api/projects": json.RawMessage(`{"item":{"id":"p9","name":"Home","description":null,"ownerProjectManagerId":"pm1"},` +
			`"included":{"projectManagers":[{"id":"pm1","projectId":"p9","userId":"u1"}]}}`),
	})

	p, err := c.CreateProject(t.Context(), CreateProjectRequest{Type: "private", Name: "Home"})
	require.NoError(t, err)
	assert.Equal(t, "p9", p.ID)
	require.NotNil(t, p.OwnerProjectManagerID)
	assert.Equal(t, "pm1", *p.OwnerProjectManagerID)
	assert.JSONEq(t, `{"type":"private","name":"Home"}`, string(srv.Last().Body), "a nil description is left out")

	desc := "Chores"
	_, err = c.CreateProject(t.Context(), CreateProjectRequest{Type: "shared", Name: "Home", Description: &desc})
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"shared","name":"Home","description":"Chores"}`, string(srv.Last().Body))
}

func TestUpdateAndDeleteProject(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"PATCH /api/projects/p9":  json.RawMessage(`{"item":{"id":"p9","name":"House","description":null}}`),
		"DELETE /api/projects/p9": json.RawMessage(`{"item":{"id":"p9"}}`),
	})

	p, err := c.UpdateProject(t.Context(), "p9", UpdateProjectRequest{Name: Some("House"), Description: Null[string]()})
	require.NoError(t, err)
	assert.Equal(t, "House", p.Name)
	assert.JSONEq(t, `{"name":"House","description":null}`, string(srv.Last().Body))

	require.NoError(t, c.DeleteProject(t.Context(), "p9"))
	assert.Equal(t, http.MethodDelete, srv.Last().Method)
}

func TestProjectManagers(t *testing.T) {
	manager := json.RawMessage(`{"item":{"id":"pm2","projectId":"p9","userId":"u2"}}`)
	c, srv := newTestClient(t, map[string]any{
		"POST /api/projects/p9/project-managers": manager,
		"DELETE /api/project-managers/pm2":       manager,
	})

	m, err := c.CreateProjectManager(t.Context(), "p9", "u2")
	require.NoError(t, err)
	assert.Equal(t, ProjectManager{ID: "pm2", ProjectID: "p9", UserID: "u2"}, *m)
	assert.JSONEq(t, `{"userId":"u2"}`, string(srv.Last().Body))

	require.NoError(t, c.DeleteProjectManager(t.Context(), "pm2"))
	assert.Equal(t, "/api/project-managers/pm2", srv.Last().Path)
}

func TestProjectBackground(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"POST /api/projects/p9/background-images": json.RawMessage(`{"item":{"id":"bg1","projectId":"p9","url":"x"}}`),
		"PATCH /api/projects/p9":                  json.RawMessage(`{"item":{"id":"p9","name":"Home","backgroundType":"image","backgroundImageId":"bg1"}}`),
	})

	img, err := c.CreateBackgroundImage(t.Context(), "p9", FileUpload{Filename: "lake.jpg", ContentType: "image/jpeg", Data: []byte("jpg")})
	require.NoError(t, err)
	assert.Equal(t, "bg1", img.ID)
	assert.Equal(t, []formPart{{name: "file", filename: "lake.jpg", contentType: "image/jpeg", data: "jpg"}}, readForm(t, srv.Last()))

	p, err := c.UpdateProject(t.Context(), "p9", UpdateProjectRequest{BackgroundType: Some("image"), BackgroundImageID: Some("bg1")})
	require.NoError(t, err)
	require.NotNil(t, p.BackgroundImageID)
	assert.Equal(t, "bg1", *p.BackgroundImageID)
	assert.JSONEq(t, `{"backgroundType":"image","backgroundImageId":"bg1"}`, string(srv.Last().Body))

	_, err = c.UpdateProject(t.Context(), "p9", UpdateProjectRequest{BackgroundType: Null[string]()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"backgroundType":null}`, string(srv.Last().Body))
}
