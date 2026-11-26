package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createProjectInput struct {
	Name        string `json:"name" jsonschema:"the new project's name; it must differ from your other projects"`
	Type        string `json:"type,omitempty" jsonschema:"private (default): you are its only manager; shared: it can have more managers"`
	Description string `json:"description,omitempty" jsonschema:"what the project is for"`
}

type updateProjectInput struct {
	Project     string  `json:"project" jsonschema:"project ID or name; list_boards shows them"`
	Name        *string `json:"name,omitempty" jsonschema:"new name; it must differ from your other projects"`
	Description *string `json:"description,omitempty" jsonschema:"new description; an empty one clears it"`
}

type deleteProjectInput struct {
	Project string `json:"project" jsonschema:"project ID or name; list_boards shows them"`
}

type setBackgroundInput struct {
	Project  string `json:"project" jsonschema:"project ID or name; list_boards shows them"`
	Gradient string `json:"gradient,omitempty" jsonschema:"one of Planka's gradients"`
	Unsplash string `json:"unsplash,omitempty" jsonschema:"link to a photo on Unsplash: its page, for example https://unsplash.com/photos/green-hills-1Z2niiBPg5A, or its image (https://images.unsplash.com/photo-…)"`
	Search   string `json:"search,omitempty" jsonschema:"words describing a photo, for example misty mountains: the best landscape match on Unsplash is used"`
	None     bool   `json:"none,omitempty" jsonschema:"remove the background"`
}

type projectManagerInput struct {
	Project string `json:"project" jsonschema:"project ID or name; list_boards shows them"`
	Person  string `json:"person" jsonschema:"username, full name or email of a Planka user, or me"`
}

// projectWriteOut is a project after a write.
type projectWriteOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// deletedProjectOut confirms a deleted project.
type deletedProjectOut struct {
	Deleted string `json:"deleted"`
	Name    string `json:"name"`
}

// projectManagerOut is a manager added to or removed from a project, with the
// project's managers after the change.
type projectManagerOut struct {
	Added    string   `json:"added,omitempty"`
	Removed  string   `json:"removed,omitempty"`
	Username string   `json:"username,omitempty"`
	Project  string   `json:"project"`
	Managers []string `json:"managers"`
}

// backgroundOut is a project's background after a change: gradient, image or
// none. Photo and Attribution describe an Unsplash photo, when known.
type backgroundOut struct {
	Project     string `json:"project"`
	Background  string `json:"background"`
	Gradient    string `json:"gradient,omitempty"`
	ImageID     string `json:"image_id,omitempty"`
	Photo       string `json:"photo,omitempty"`
	Attribution string `json:"credit,omitempty"`
}

// setsBackground is set_project_background's annotation: it replaces the
// background, and a photo comes from Unsplash, outside Planka.
var setsBackground = &mcp.ToolAnnotations{OpenWorldHint: new(true)}

// registerProjectTools adds the tools that create, change and delete projects,
// set their backgrounds and manage their managers. photoSearch offers
// set_project_background's search, which needs an Unsplash access key.
func registerProjectTools(server *mcp.Server, projects ProjectService, photoSearch bool) {
	addTool(server, &mcp.Tool{
		Name:  "create_project",
		Title: "Create project",
		Description: "Use this to add a Planka project, which holds boards; then add boards with create_board. " +
			"Only a Planka admin or project owner can, and becomes its manager. Returns the project (id, name, type, description).",
		Annotations: additive,
		InputSchema: inputSchema[createProjectInput](map[string][]string{"type": service.ProjectTypes}),
	}, createProject(projects))

	addTool(server, &mcp.Tool{
		Name:        "update_project",
		Title:       "Update project",
		Description: "Use this to rename a Planka project or change its description. Only a project manager can. Returns the project.",
		Annotations: idempotent,
	}, updateProject(projects))

	addTool(server, &mcp.Tool{
		Name:  "delete_project",
		Title: "Delete project",
		Description: "Use this only when a Planka project must go. A project that still has boards is refused: delete them first with delete_board. " +
			"Only a project manager can. Returns the deleted project's id and name.",
		Annotations: destructive,
	}, deleteProject(projects))

	addTool(server, &mcp.Tool{
		Name:        "set_project_background",
		Title:       "Set project background",
		Description: backgroundDescription(photoSearch),
		Annotations: setsBackground,
		InputSchema: backgroundSchema(photoSearch),
	}, setBackground(projects))

	addTool(server, &mcp.Tool{
		Name:  "add_project_manager",
		Title: "Add project manager",
		Description: "Use this to let someone manage a shared Planka project: create, change and delete its boards and add people to them. " +
			"A private project takes no other managers. Only a manager of the project or a Planka admin can, and the person must be a Planka admin or project owner. " +
			"Returns the person and the project's managers.",
		Annotations: additive,
	}, addProjectManager(projects))

	addTool(server, &mcp.Tool{
		Name:  "remove_project_manager",
		Title: "Remove project manager",
		Description: "Use this to take away someone's right to manage a shared Planka project. The last manager cannot be removed. " +
			"Only a manager of the project or a Planka admin can. Returns who was removed and the project's managers.",
		Annotations: destructive,
	}, removeProjectManager(projects))
}

func createProject(projects ProjectService) func(context.Context, *mcp.CallToolRequest, createProjectInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createProjectInput) (*mcp.CallToolResult, any, error) {
		p, err := projects.Create(ctx, service.CreateProject{Name: in.Name, Type: in.Type, Description: in.Description})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toProjectWriteOut(p))
	}
}

func updateProject(projects ProjectService) func(context.Context, *mcp.CallToolRequest, updateProjectInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateProjectInput) (*mcp.CallToolResult, any, error) {
		p, err := projects.Update(ctx, service.UpdateProject{Project: in.Project, Name: in.Name, Description: in.Description})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toProjectWriteOut(p))
	}
}

func deleteProject(projects ProjectService) func(context.Context, *mcp.CallToolRequest, deleteProjectInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteProjectInput) (*mcp.CallToolResult, any, error) {
		p, err := projects.Delete(ctx, in.Project)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedProjectOut{Deleted: p.ID, Name: p.Name})
	}
}

// backgroundDescription describes set_project_background; search is offered
// only with an Unsplash access key.
func backgroundDescription(photoSearch bool) string {
	choices := "one of Planka's gradients, the photo an Unsplash link names, or none"
	if photoSearch {
		choices = "one of Planka's gradients, the photo an Unsplash link names, the Unsplash photo that best matches some words, or none"
	}
	return "Use this to set a Planka project's background, which all its boards show: " + choices + "; give exactly one. " +
		"A photo is uploaded to the project, and the ones it replaces stay in its image gallery. Only a project manager can. " +
		"Returns the background; for a photo also its Unsplash page and, when known, the photographer's credit, to mention with it."
}

// backgroundSchema is set_project_background's input schema. Without an
// Unsplash access key it has no search, and takes image links only.
func backgroundSchema(photoSearch bool) *jsonschema.Schema {
	s := inputSchema[setBackgroundInput](map[string][]string{"gradient": service.BackgroundGradients})
	if !photoSearch {
		delete(s.Properties, "search")
		s.Properties["unsplash"].Description = "link to a photo's image on Unsplash, https://images.unsplash.com/photo-… " +
			"(\"Copy image address\" on the photo); a photo page link (unsplash.com/photos/…) needs an Unsplash access key, which this server lacks"
	}
	return s
}

func setBackground(projects ProjectService) func(context.Context, *mcp.CallToolRequest, setBackgroundInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in setBackgroundInput) (*mcp.CallToolResult, any, error) {
		b, err := projects.SetBackground(ctx, service.SetBackground{
			Project: in.Project, Gradient: in.Gradient, Unsplash: in.Unsplash, Search: in.Search, None: in.None,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(backgroundOut{
			Project: b.Project, Background: b.Type, Gradient: b.Gradient, ImageID: b.ImageID, Photo: b.Photo, Attribution: b.Attribution,
		})
	}
}

func addProjectManager(projects ProjectService) func(context.Context, *mcp.CallToolRequest, projectManagerInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in projectManagerInput) (*mcp.CallToolResult, any, error) {
		m, err := projects.AddManager(ctx, in.Project, in.Person)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(projectManagerOut{Added: m.Name, Username: m.Username, Project: m.Project, Managers: m.Managers})
	}
}

func removeProjectManager(projects ProjectService) func(context.Context, *mcp.CallToolRequest, projectManagerInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in projectManagerInput) (*mcp.CallToolResult, any, error) {
		m, err := projects.RemoveManager(ctx, in.Project, in.Person)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(projectManagerOut{Removed: m.Name, Username: m.Username, Project: m.Project, Managers: m.Managers})
	}
}

func toProjectWriteOut(p *service.ProjectResult) projectWriteOut {
	return projectWriteOut{ID: p.ID, Name: p.Name, Type: p.Type, Description: p.Description}
}
