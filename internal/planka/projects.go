package planka

import (
	"context"
	"encoding/json"
	"net/http"
)

// CreateProjectRequest is the body of POST /api/projects. Type is private or
// shared; a nil Description is left out.
type CreateProjectRequest struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// CreateProject creates a project. Planka allows it to admins and project
// owners only, and makes the creator its manager.
func (c *Client) CreateProject(ctx context.Context, r CreateProjectRequest) (*Project, error) {
	return send[Project](ctx, c, http.MethodPost, "/api/projects", r)
}

// UpdateProjectRequest is the body of PATCH /api/projects/:id. Only the fields
// that are Set are sent; a Null Description clears it. BackgroundType is
// gradient (with BackgroundGradient), image (with BackgroundImageID), or Null
// for none.
type UpdateProjectRequest struct {
	Name               Optional[string]
	Description        Optional[string]
	BackgroundType     Optional[string]
	BackgroundGradient Optional[string]
	BackgroundImageID  Optional[string]
}

// MarshalJSON emits only the Set fields.
func (r UpdateProjectRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "name", r.Name)
	put(m, "description", r.Description)
	put(m, "backgroundType", r.BackgroundType)
	put(m, "backgroundGradient", r.BackgroundGradient)
	put(m, "backgroundImageId", r.BackgroundImageID)
	return json.Marshal(m)
}

// BackgroundImage is an image uploaded to a project for its background.
type BackgroundImage struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
}

// CreateBackgroundImage uploads an image to a project's background gallery.
// It does not become the background until UpdateProject says so. Planka
// allows it to project managers only.
func (c *Client) CreateBackgroundImage(ctx context.Context, projectID string, f FileUpload) (*BackgroundImage, error) {
	body, err := multipartForm(nil, &f)
	if err != nil {
		return nil, err
	}
	body.upload = true
	return send[BackgroundImage](ctx, c, http.MethodPost, "/api/projects/"+pathParam(projectID)+"/background-images", body)
}

// UpdateProject renames a project or changes its description.
func (c *Client) UpdateProject(ctx context.Context, id string, r UpdateProjectRequest) (*Project, error) {
	return send[Project](ctx, c, http.MethodPatch, "/api/projects/"+pathParam(id), r)
}

// DeleteProject deletes a project. Planka refuses one that still has boards
// (HTTP 422).
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/projects/"+pathParam(id), nil, nil, nil)
}

// CreateProjectManager makes a user a manager of a shared project. The user
// must be an admin or project owner.
func (c *Client) CreateProjectManager(ctx context.Context, projectID, userID string) (*ProjectManager, error) {
	body := map[string]string{"userId": userID}
	return send[ProjectManager](ctx, c, http.MethodPost, "/api/projects/"+pathParam(projectID)+"/project-managers", body)
}

// DeleteProjectManager takes a manager off a shared project. Planka refuses
// the last one (HTTP 422).
func (c *Client) DeleteProjectManager(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/project-managers/"+pathParam(id), nil, nil, nil)
}
