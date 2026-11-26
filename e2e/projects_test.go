//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type projectResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type managersResult struct {
	Added    string   `json:"added"`
	Removed  string   `json:"removed"`
	Project  string   `json:"project"`
	Managers []string `json:"managers"`
}

// TestProjectTools creates a private project, finds it in list_boards,
// renames it and changes its description, and deletes it: refused while it
// has a board.
func TestProjectTools(t *testing.T) {
	p := requirePlanka(t)
	name := "e2e-proj-" + unique(t)
	s := connectStdio(t, env.key)

	created := callInto[projectResult](t, s, "create_project", map[string]any{"name": name, "description": "Made by e2e"})
	t.Cleanup(func() { p.deleteProject(t, created.ID) })
	assert.Equal(t, name, created.Name)
	assert.Equal(t, "private", created.Type)
	assert.Equal(t, "Made by e2e", created.Description)

	text, isError := call(t, s, "create_project", map[string]any{"name": name})
	assert.True(t, isError, text)
	assert.Contains(t, text, "already exists")

	listed := callInto[[]struct {
		Project   string `json:"project"`
		ProjectID string `json:"project_id"`
	}](t, s, "list_boards", map[string]any{"query": name})
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ProjectID)

	renamed := callInto[projectResult](t, s, "update_project", map[string]any{"project": name, "name": name + " v2", "description": ""})
	assert.Equal(t, name+" v2", renamed.Name)
	assert.Empty(t, renamed.Description)

	text, isError = call(t, s, "add_project_manager", map[string]any{"project": created.ID, "person": "me"})
	assert.True(t, isError, text)
	assert.Contains(t, text, "is private")

	board := callInto[struct{ ID string }](t, s, "create_board", map[string]any{"project": created.ID, "name": "Inbox"})
	text, isError = call(t, s, "delete_project", map[string]any{"project": created.ID})
	assert.True(t, isError, text)
	assert.Contains(t, text, `still has boards "Inbox"`)

	callInto[struct{ Deleted string }](t, s, "delete_board", map[string]any{"board": board.ID})
	deleted := callInto[struct {
		Deleted string `json:"deleted"`
		Name    string `json:"name"`
	}](t, s, "delete_project", map[string]any{"project": created.ID})
	assert.Equal(t, created.ID, deleted.Deleted)
	err := p.request(t.Context(), http.MethodGet, "/projects/"+created.ID, "", nil, nil)
	assert.ErrorContains(t, err, "404", "the project is gone")
}

// TestProjectManagerTools adds a project owner as a manager of a shared
// project and removes them again; a board user cannot be one, and the last
// manager stays.
func TestProjectManagerTools(t *testing.T) {
	p := requirePlanka(t)
	id := unique(t)
	me := p.meName(t)
	s := connectStdio(t, env.key)
	created := callInto[projectResult](t, s, "create_project", map[string]any{"name": "e2e-team-" + id, "type": "shared"})
	t.Cleanup(func() { p.deleteProject(t, created.ID) })
	assert.Equal(t, "shared", created.Type)
	p.newRoleUser(t, "po"+id, "projectOwner")
	p.newRoleUser(t, "bu"+id, "boardUser")

	added := callInto[managersResult](t, s, "add_project_manager", map[string]any{"project": created.ID, "person": "e2epo" + id})
	assert.Equal(t, "E2E po"+id, added.Added)
	assert.ElementsMatch(t, []string{me, "E2E po" + id}, added.Managers)

	text, isError := call(t, s, "add_project_manager", map[string]any{"project": created.ID, "person": "e2epo" + id})
	assert.True(t, isError, text)
	assert.Contains(t, text, "already manages")

	text, isError = call(t, s, "add_project_manager", map[string]any{"project": created.ID, "person": "e2ebu" + id})
	assert.True(t, isError, text)
	assert.Contains(t, text, "cannot manage projects")

	removed := callInto[managersResult](t, s, "remove_project_manager", map[string]any{"project": created.ID, "person": "e2epo" + id})
	assert.Equal(t, "E2E po"+id, removed.Removed)
	assert.Equal(t, []string{me}, removed.Managers)

	text, isError = call(t, s, "remove_project_manager", map[string]any{"project": created.ID, "person": "me"})
	assert.True(t, isError, text)
	assert.Contains(t, text, "is the last manager")
}
