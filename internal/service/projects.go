package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	// maxProjectName is the longest project name Planka stores, in UTF-16 units.
	maxProjectName = 128
	// maxProjectDescription is the longest project description Planka stores.
	maxProjectDescription = 1024
)

// ProjectTypes are the kinds of project. A private project has one manager
// and takes no others; a shared one can have several. The first is the
// default, as in Planka's web UI.
var ProjectTypes = []string{"private", "shared"}

// Projects creates, changes and deletes projects, sets their backgrounds, and
// manages who may manage them. Listing projects is (*Catalog).ListBoards.
type Projects struct {
	cards  *Cards
	api    ProjectWriteAPI
	photos PhotosAPI
}

// NewProjects returns a Projects service. It reads projects and the current
// user through cards, writes through api, and fetches background photos
// through photos.
func NewProjects(cards *Cards, api ProjectWriteAPI, photos PhotosAPI) *Projects {
	return &Projects{cards: cards, api: api, photos: photos}
}

// CreateProject describes a new project. Type is one of ProjectTypes; empty
// is private. An empty Description is left out.
type CreateProject struct {
	Name, Type, Description string
}

// UpdateProject changes Project (ID or name). A nil field is left alone; an
// empty Description clears it.
type UpdateProject struct {
	Project           string
	Name, Description *string
}

// ProjectResult is a project after a write.
type ProjectResult struct {
	ID, Name, Type, Description string
}

// ProjectManager is a manager of a project. ID is the manager record's.
// Managers lists the project's managers after the change, by name.
type ProjectManager struct {
	ID, UserID, Name, Username string
	Project                    string
	Managers                   []string
}

// Create adds a project. Its name must differ from the projects the user can
// see, case ignored. Planka makes the user its manager.
func (s *Projects) Create(ctx context.Context, in CreateProject) (*ProjectResult, error) {
	name, err := projectName(in.Name)
	if err != nil {
		return nil, err
	}
	kind := in.Type
	if kind == "" {
		kind = ProjectTypes[0]
	}
	if !slices.Contains(ProjectTypes, kind) {
		return nil, fmt.Errorf("unknown project type %q: use private or shared", kind)
	}
	desc, err := projectDescription(in.Description)
	if err != nil {
		return nil, err
	}
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	if err := uniqueProjectName(resp, name, ""); err != nil {
		return nil, err
	}
	req := planka.CreateProjectRequest{Type: kind, Name: name}
	if desc != "" {
		req.Description = &desc
	}
	created, err := s.api.CreateProject(ctx, req)
	if err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusForbidden {
			return nil, errors.New("only a Planka admin or project owner can create projects, and Planka refused (HTTP 403)")
		}
		return nil, err
	}
	return toProjectResult(*created), nil
}

// Update renames a project or changes its description. A new name must
// differ from the other projects the user can see.
func (s *Projects) Update(ctx context.Context, in UpdateProject) (*ProjectResult, error) {
	if in.Name == nil && in.Description == nil {
		return nil, errors.New("nothing to change: give name or description")
	}
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	p, err := pickProject(resp, in.Project)
	if err != nil {
		return nil, err
	}
	patch, err := projectPatch(resp, p, in)
	if err != nil {
		return nil, err
	}
	if patch == (planka.UpdateProjectRequest{}) {
		return toProjectResult(p), nil
	}
	updated, err := s.api.UpdateProject(ctx, p.ID, patch)
	if err != nil {
		return nil, managerOnly(err, fmt.Sprintf("change project %q", p.Name))
	}
	return toProjectResult(*updated), nil
}

// projectPatch is the PATCH an update asks for, without what is already so.
func projectPatch(resp *planka.ProjectsResponse, p planka.Project, in UpdateProject) (planka.UpdateProjectRequest, error) {
	var patch planka.UpdateProjectRequest
	if in.Name != nil {
		name, err := projectName(*in.Name)
		if err != nil {
			return patch, err
		}
		if err := uniqueProjectName(resp, name, p.ID); err != nil {
			return patch, err
		}
		if name != p.Name {
			patch.Name = planka.Some(name)
		}
	}
	if in.Description != nil {
		desc, err := projectDescription(*in.Description)
		if err != nil {
			return patch, err
		}
		switch {
		case desc == deref(p.Description):
		case desc == "":
			patch.Description = planka.Null[string]()
		default:
			patch.Description = planka.Some(desc)
		}
	}
	return patch, nil
}

// Delete deletes a project. Planka refuses one that still has boards, so
// that is refused here first, with the boards named.
func (s *Projects) Delete(ctx context.Context, project string) (*ProjectResult, error) {
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	p, err := pickProject(resp, project)
	if err != nil {
		return nil, err
	}
	var boards []string
	for _, b := range resp.Included.Boards {
		if b.ProjectID == p.ID {
			boards = append(boards, fmt.Sprintf("%q (id %s)", b.Name, b.ID))
		}
	}
	if len(boards) > 0 {
		return nil, fmt.Errorf("project %q still has boards %s: delete them first with delete_board", p.Name, strings.Join(boards, ", "))
	}
	if err := s.api.DeleteProject(ctx, p.ID); err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusUnprocessableEntity {
			return nil, fmt.Errorf("project %q still has boards you cannot see, and Planka refused (HTTP 422): "+
				"its managers must delete them first", p.Name)
		}
		return nil, managerOnly(err, fmt.Sprintf("delete project %q", p.Name))
	}
	return toProjectResult(p), nil
}

// AddManager makes a person ("me", username, full name or email) a manager of
// a shared project.
func (s *Projects) AddManager(ctx context.Context, project, person string) (*ProjectManager, error) {
	resp, p, err := s.sharedProject(ctx, project, "add managers to it")
	if err != nil {
		return nil, err
	}
	user, err := s.findPerson(ctx, resp, person)
	if err != nil {
		return nil, err
	}
	if _, ok := managerOf(resp, p.ID, user.ID); ok {
		return nil, fmt.Errorf("%s already manages project %q", userName(user), p.Name)
	}
	created, err := s.api.CreateProjectManager(ctx, p.ID, user.ID)
	if err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusUnprocessableEntity {
			return nil, fmt.Errorf("%s cannot manage projects: Planka allows only admins and project owners to (HTTP 422)", userName(user))
		}
		return nil, managerOnly(err, fmt.Sprintf("add managers to project %q", p.Name))
	}
	names := append(managerNames(resp, p.ID), userName(user))
	return &ProjectManager{ID: created.ID, UserID: user.ID, Name: userName(user), Username: user.Username, Project: p.Name, Managers: names}, nil
}

// RemoveManager takes a person's manager rights on a shared project away.
// The last manager cannot be removed.
func (s *Projects) RemoveManager(ctx context.Context, project, person string) (*ProjectManager, error) {
	resp, p, err := s.sharedProject(ctx, project, "remove its manager")
	if err != nil {
		return nil, err
	}
	m, user, err := s.findManager(ctx, resp, p, person)
	if err != nil {
		return nil, err
	}
	names := managerNames(resp, p.ID)
	if len(names) == 1 {
		return nil, fmt.Errorf("%s is the last manager of project %q: add another manager first", userName(user), p.Name)
	}
	if err := s.api.DeleteProjectManager(ctx, m.ID); err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusUnprocessableEntity {
			return nil, fmt.Errorf("%s is the last manager of project %q: add another manager first (HTTP 422)", userName(user), p.Name)
		}
		return nil, managerOnly(err, fmt.Sprintf("remove managers of project %q", p.Name))
	}
	names = slices.DeleteFunc(names, func(n string) bool { return n == userName(user) })
	return &ProjectManager{ID: m.ID, UserID: user.ID, Name: userName(user), Username: user.Username, Project: p.Name, Managers: names}, nil
}

// sharedProject finds a project and refuses a private one, which has one
// manager and takes no others; what says what was asked.
func (s *Projects) sharedProject(ctx context.Context, ref, what string) (*planka.ProjectsResponse, planka.Project, error) {
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, planka.Project{}, err
	}
	p, err := pickProject(resp, ref)
	if err != nil {
		return nil, planka.Project{}, err
	}
	if p.OwnerProjectManagerID != nil {
		return nil, planka.Project{}, fmt.Errorf("project %q is private: it has one manager, and Planka does not let you %s", p.Name, what)
	}
	return resp, p, nil
}

// findPerson finds the user a person names: "me", a user Planka sent with the
// projects, or else any Planka user, which only admins and project owners can
// list.
func (s *Projects) findPerson(ctx context.Context, resp *planka.ProjectsResponse, person string) (planka.User, error) {
	person = strings.TrimSpace(person)
	if person == "" {
		return planka.User{}, errors.New("person is empty: give a username, full name, email or \"me\"")
	}
	if normalize(person) == "me" {
		me, err := s.cards.users.Me(ctx)
		if err != nil {
			return planka.User{}, err
		}
		return *me, nil
	}
	if u, ok := exactUser(resp.Included.Users, person); ok {
		return u, nil
	}
	users, err := s.api.Users(ctx)
	if err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusForbidden {
			return planka.User{}, errors.New("only a Planka admin or project owner can look up other people")
		}
		return planka.User{}, err
	}
	if u, ok := exactUser(users, person); ok {
		return u, nil
	}
	return matchName("user", person, "Planka", users, userName, userID)
}

// findManager finds a manager of the project by "me", username, full name or
// email.
func (s *Projects) findManager(ctx context.Context, resp *planka.ProjectsResponse, p planka.Project, person string) (planka.ProjectManager, planka.User, error) {
	var users []planka.User
	for _, m := range resp.Included.ProjectManagers {
		if m.ProjectID != p.ID {
			continue
		}
		if i := slices.IndexFunc(resp.Included.Users, func(u planka.User) bool { return u.ID == m.UserID }); i >= 0 {
			users = append(users, resp.Included.Users[i])
		}
	}
	var user planka.User
	if normalize(person) == "me" {
		me, err := s.cards.users.Me(ctx)
		if err != nil {
			return planka.ProjectManager{}, planka.User{}, err
		}
		user = *me
	} else {
		u, ok := exactUser(users, person)
		if !ok {
			names := make([]string, len(users))
			for i, u := range users {
				names[i] = userName(u)
			}
			return planka.ProjectManager{}, planka.User{}, &UnknownNameError{
				Kind: "manager", Name: person, Scope: fmt.Sprintf("project %q", p.Name), Valid: names,
			}
		}
		user = u
	}
	m, ok := managerOf(resp, p.ID, user.ID)
	if !ok {
		return planka.ProjectManager{}, planka.User{}, fmt.Errorf("%s is not a manager of project %q", userName(user), p.Name)
	}
	return m, user, nil
}

// managerOf is the user's manager record on the project, if any.
func managerOf(resp *planka.ProjectsResponse, projectID, userID string) (planka.ProjectManager, bool) {
	i := slices.IndexFunc(resp.Included.ProjectManagers, func(m planka.ProjectManager) bool {
		return m.ProjectID == projectID && m.UserID == userID
	})
	if i < 0 {
		return planka.ProjectManager{}, false
	}
	return resp.Included.ProjectManagers[i], true
}

// managerNames names the project's managers, in Planka's order.
func managerNames(resp *planka.ProjectsResponse, projectID string) []string {
	var out []string
	for _, m := range resp.Included.ProjectManagers {
		if m.ProjectID != projectID {
			continue
		}
		name := m.UserID
		if i := slices.IndexFunc(resp.Included.Users, func(u planka.User) bool { return u.ID == m.UserID }); i >= 0 {
			name = userName(resp.Included.Users[i])
		}
		out = append(out, name)
	}
	return out
}

// uniqueProjectName refuses a name another visible project has, case
// ignored; skip is the project being renamed.
func uniqueProjectName(resp *planka.ProjectsResponse, name, skip string) error {
	for _, p := range resp.Items {
		if p.ID != skip && normalize(p.Name) == normalize(name) {
			return fmt.Errorf("a project %q (id %s) already exists: pick another name", p.Name, p.ID)
		}
	}
	return nil
}

// projectName trims a project name and checks it is not empty or too long.
func projectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("project name is empty")
	case jsLen(name) > maxProjectName:
		return "", fmt.Errorf("project name is longer than %d characters", maxProjectName)
	}
	return name, nil
}

// projectDescription trims a description and checks its length.
func projectDescription(desc string) (string, error) {
	desc = strings.TrimSpace(desc)
	if jsLen(desc) > maxProjectDescription {
		return "", fmt.Errorf("project description is longer than %d characters", maxProjectDescription)
	}
	return desc, nil
}

func toProjectResult(p planka.Project) *ProjectResult {
	kind := "shared"
	if p.OwnerProjectManagerID != nil {
		kind = "private"
	}
	return &ProjectResult{ID: p.ID, Name: p.Name, Type: kind, Description: deref(p.Description)}
}
