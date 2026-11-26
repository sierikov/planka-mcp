package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/sierikov/planka-mcp/internal/service"
)

func TestCreateProjectOutput(t *testing.T) {
	projects := NewMockProjectService(t)
	projects.EXPECT().Create(mock.Anything, service.CreateProject{Name: "Garden"}).
		Return(&service.ProjectResult{ID: "pg", Name: "Garden", Type: "private"}, nil)

	res := call(t, connect(t, Deps{Projects: projects}), "create_project", map[string]any{"name": "Garden"})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"pg","name":"Garden","type":"private"}`, resultText(t, res))
}

func TestCreateProjectRefusesAnUnknownType(t *testing.T) {
	// No mock call is expected: the schema refuses the type.
	res := call(t, connect(t, Deps{Projects: NewMockProjectService(t)}), "create_project", map[string]any{"name": "x", "type": "team"})

	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "type")
}

func TestUpdateProjectPassesAnEmptyDescription(t *testing.T) {
	projects := NewMockProjectService(t)
	projects.EXPECT().Update(mock.Anything, service.UpdateProject{Project: "Home", Description: new("")}).
		Return(&service.ProjectResult{ID: "ph", Name: "Home", Type: "shared"}, nil)

	res := call(t, connect(t, Deps{Projects: projects}), "update_project", map[string]any{"project": "Home", "description": ""})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"ph","name":"Home","type":"shared"}`, resultText(t, res))
}

func TestDeleteProjectOutput(t *testing.T) {
	projects := NewMockProjectService(t)
	projects.EXPECT().Delete(mock.Anything, "Old").Return(&service.ProjectResult{ID: "po", Name: "Old", Type: "shared"}, nil)

	res := call(t, connect(t, Deps{Projects: projects}), "delete_project", map[string]any{"project": "Old"})

	assert.JSONEq(t, `{"deleted":"po","name":"Old"}`, resultText(t, res))
}

func TestProjectManagerOutputs(t *testing.T) {
	projects := NewMockProjectService(t)
	m := &service.ProjectManager{ID: "pm", UserID: "u2", Name: "Bob", Username: "bob", Project: "Work", Managers: []string{"Ann", "Bob"}}
	projects.EXPECT().AddManager(mock.Anything, "Work", "bob").Return(m, nil)
	removed := *m
	removed.Managers = []string{"Ann"}
	projects.EXPECT().RemoveManager(mock.Anything, "Work", "bob").Return(&removed, nil)
	session := connect(t, Deps{Projects: projects})

	added := call(t, session, "add_project_manager", map[string]any{"project": "Work", "person": "bob"})
	gone := call(t, session, "remove_project_manager", map[string]any{"project": "Work", "person": "bob"})

	assert.JSONEq(t, `{"added":"Bob","username":"bob","project":"Work","managers":["Ann","Bob"]}`, resultText(t, added))
	assert.JSONEq(t, `{"removed":"Bob","username":"bob","project":"Work","managers":["Ann"]}`, resultText(t, gone))
}

func TestSetBackgroundOffersSearchOnlyWithAKey(t *testing.T) {
	without := inputProps(t, connect(t, Deps{}))["set_project_background"]
	with := inputProps(t, connect(t, Deps{PhotoSearch: true}))["set_project_background"]

	assert.NotContains(t, without, "search")
	assert.Contains(t, with, "search")
	assert.Contains(t, with["gradient"]["enum"], "ocean-dive")
	for _, props := range []map[string]map[string]any{without, with} {
		assert.Contains(t, props, "unsplash")
		assert.Contains(t, props, "none")
	}
	assert.Contains(t, with["unsplash"]["description"], "https://unsplash.com/photos/")
	assert.Contains(t, without["unsplash"]["description"], "https://images.unsplash.com/photo-")
	assert.Contains(t, without["unsplash"]["description"], "needs an Unsplash access key")
}

func TestSetBackgroundDescriptionFollowsTheKey(t *testing.T) {
	assert.NotContains(t, backgroundDescription(false), "matches some words")
	assert.Contains(t, backgroundDescription(true), "matches some words")
}

func TestSetBackgroundOutput(t *testing.T) {
	projects := NewMockProjectService(t)
	projects.EXPECT().SetBackground(mock.Anything, service.SetBackground{Project: "Home", Search: "hills"}).Return(&service.Background{
		Project: "Home", Type: "image", ImageID: "bg1", Photo: "https://unsplash.com/photos/x", Attribution: "Photo by Jane Doe on Unsplash",
	}, nil)
	projects.EXPECT().SetBackground(mock.Anything, service.SetBackground{Project: "Home", Gradient: "old-lime"}).
		Return(&service.Background{Project: "Home", Type: "gradient", Gradient: "old-lime"}, nil)
	session := connect(t, Deps{Projects: projects, PhotoSearch: true})

	photo := call(t, session, "set_project_background", map[string]any{"project": "Home", "search": "hills"})
	gradient := call(t, session, "set_project_background", map[string]any{"project": "Home", "gradient": "old-lime"})

	assert.JSONEq(t, `{"project":"Home","background":"image","image_id":"bg1","photo":"https://unsplash.com/photos/x",`+
		`"credit":"Photo by Jane Doe on Unsplash"}`, resultText(t, photo))
	assert.JSONEq(t, `{"project":"Home","background":"gradient","gradient":"old-lime"}`, resultText(t, gradient))
}

func TestSetBackgroundRefusesSearchWithoutAKey(t *testing.T) {
	// No mock call is expected: without a key the schema has no search.
	res := call(t, connect(t, Deps{Projects: NewMockProjectService(t)}), "set_project_background", map[string]any{"project": "Home", "search": "hills"})

	assert.True(t, res.IsError)
}
