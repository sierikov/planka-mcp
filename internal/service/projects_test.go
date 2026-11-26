package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

type projectsFixture struct {
	*cardsFixture
	api      *MockProjectWriteAPI
	photos   *MockPhotosAPI
	projects *Projects
}

func newProjectsFixture(t *testing.T) *projectsFixture {
	t.Helper()
	f := &projectsFixture{cardsFixture: newCardsFixture(t), api: NewMockProjectWriteAPI(t), photos: NewMockPhotosAPI(t)}
	f.projects = NewProjects(f.svc, f.api, f.photos)
	return f
}

// serveProjects answers the projects read with teamProjects.
func (f *projectsFixture) serveProjects() {
	f.cardsFixture.projects.EXPECT().Projects(mock.Anything).Return(teamProjects(), nil).Once()
}

// teamProjects is synthProjects with managers: Work (shared, boards B1 and
// B2) is managed by Alice and Me, Home (private, board B3) by Me, and Empty
// (shared, no boards, described "Old") by Me alone.
func teamProjects() *planka.ProjectsResponse {
	r := synthProjects()
	r.Items[1].OwnerProjectManagerID = new("pm-home")
	r.Items = append(r.Items, planka.Project{ID: "pe", Name: "Empty", Description: new("Old")})
	r.Included.ProjectManagers = []planka.ProjectManager{
		{ID: "pm-alice", ProjectID: "pw", UserID: "u-alice"},
		{ID: "pm-me", ProjectID: "pw", UserID: "u-me"},
		{ID: "pm-home", ProjectID: "ph", UserID: "u-me"},
		{ID: "pm-empty", ProjectID: "pe", UserID: "u-me"},
	}
	r.Included.Users = []planka.User{
		{ID: "u-alice", Name: "Alice", Username: "alice"},
		{ID: "u-me", Name: "Me Myself", Username: "me.myself"},
	}
	return r
}

func apiError(status int) error { return &planka.APIError{Status: status} }

func TestCreateProjectIsPrivateByDefault(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().CreateProject(mock.Anything, planka.CreateProjectRequest{Type: "private", Name: "Garden"}).
		Return(&planka.Project{ID: "pg", Name: "Garden", OwnerProjectManagerID: new("pm9")}, nil).Once()

	got, err := f.projects.Create(t.Context(), CreateProject{Name: " Garden "})

	require.NoError(t, err)
	assert.Equal(t, &ProjectResult{ID: "pg", Name: "Garden", Type: "private"}, got)
}

func TestCreateSharedProjectWithADescription(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().CreateProject(mock.Anything, planka.CreateProjectRequest{Type: "shared", Name: "Garden", Description: new("Plants")}).
		Return(&planka.Project{ID: "pg", Name: "Garden", Description: new("Plants")}, nil).Once()

	got, err := f.projects.Create(t.Context(), CreateProject{Name: "Garden", Type: "shared", Description: "Plants "})

	require.NoError(t, err)
	assert.Equal(t, &ProjectResult{ID: "pg", Name: "Garden", Type: "shared", Description: "Plants"}, got)
}

func TestCreateProjectRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		in     CreateProject
		read   bool
		failed error
		want   string
	}{
		"empty name":       {CreateProject{Name: " "}, false, nil, "project name is empty"},
		"unknown type":     {CreateProject{Name: "x", Type: "team"}, false, nil, `unknown project type "team"`},
		"long description": {CreateProject{Name: "x", Description: strings.Repeat("d", 1025)}, false, nil, "longer than 1024"},
		"taken name":       {CreateProject{Name: "WORK"}, true, nil, `a project "Work" (id pw) already exists`},
		"not allowed":      {CreateProject{Name: "x"}, true, apiError(http.StatusForbidden), "only a Planka admin or project owner can create projects"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectsFixture(t)
			if tc.read {
				f.serveProjects()
			}
			if tc.failed != nil {
				f.api.EXPECT().CreateProject(mock.Anything, mock.Anything).Return(nil, tc.failed).Once()
			}

			_, err := f.projects.Create(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestUpdateProjectRenames(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().UpdateProject(mock.Anything, "pw", planka.UpdateProjectRequest{Name: planka.Some("Office")}).
		Return(&planka.Project{ID: "pw", Name: "Office"}, nil).Once()

	got, err := f.projects.Update(t.Context(), UpdateProject{Project: "work", Name: new("Office"), Description: new("")})

	require.NoError(t, err)
	assert.Equal(t, &ProjectResult{ID: "pw", Name: "Office", Type: "shared"}, got, "an empty description that is already empty is not sent")
}

func TestUpdateProjectClearsTheDescription(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().UpdateProject(mock.Anything, "pe", planka.UpdateProjectRequest{Description: planka.Null[string]()}).
		Return(&planka.Project{ID: "pe", Name: "Empty"}, nil).Once()

	got, err := f.projects.Update(t.Context(), UpdateProject{Project: "Empty", Description: new(" ")})

	require.NoError(t, err)
	assert.Empty(t, got.Description)
}

func TestUpdateProjectThatChangesNothingWritesNothing(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()

	got, err := f.projects.Update(t.Context(), UpdateProject{Project: "Empty", Name: new("Empty"), Description: new("Old")})

	require.NoError(t, err)
	assert.Equal(t, &ProjectResult{ID: "pe", Name: "Empty", Type: "shared", Description: "Old"}, got)
}

func TestUpdateProjectRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		in     UpdateProject
		read   bool
		failed error
		want   string
	}{
		"nothing":     {UpdateProject{Project: "Work"}, false, nil, "nothing to change: give name or description"},
		"taken name":  {UpdateProject{Project: "Work", Name: new("home")}, true, nil, `a project "Home" (id ph) already exists`},
		"unknown":     {UpdateProject{Project: "Garden", Name: new("x")}, true, nil, `no project "Garden"`},
		"not manager": {UpdateProject{Project: "Work", Name: new("x")}, true, apiError(http.StatusForbidden), `only a project manager of the project can change project "Work"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectsFixture(t)
			if tc.read {
				f.serveProjects()
			}
			if tc.failed != nil {
				f.api.EXPECT().UpdateProject(mock.Anything, "pw", mock.Anything).Return(nil, tc.failed).Once()
			}

			_, err := f.projects.Update(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDeleteProject(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().DeleteProject(mock.Anything, "pe").Return(nil).Once()

	got, err := f.projects.Delete(t.Context(), "Empty")

	require.NoError(t, err)
	assert.Equal(t, "pe", got.ID)
	assert.Equal(t, "Empty", got.Name)
}

func TestDeleteProjectRefusesOneWithBoards(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()

	_, err := f.projects.Delete(t.Context(), "Work")

	require.Error(t, err)
	assert.Equal(t, `project "Work" still has boards "B1" (id b1), "B2" (id b2): delete them first with delete_board`, err.Error())
}

func TestDeleteProjectExplainsPlankasRefusals(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnprocessableEntity: `project "Empty" still has boards you cannot see`,
		http.StatusNotFound:            `only a project manager of the project can delete project "Empty"`,
	} {
		f := newProjectsFixture(t)
		f.serveProjects()
		f.api.EXPECT().DeleteProject(mock.Anything, "pe").Return(apiError(status)).Once()

		_, err := f.projects.Delete(t.Context(), "Empty")

		require.Error(t, err)
		assert.Contains(t, err.Error(), want)
	}
}

func TestAddProjectManagerLooksUpAnyUser(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().Users(mock.Anything).Return([]planka.User{{ID: "u-bob", Name: "Bob", Username: "bob"}}, nil).Once()
	f.api.EXPECT().CreateProjectManager(mock.Anything, "pw", "u-bob").
		Return(&planka.ProjectManager{ID: "pm-bob", ProjectID: "pw", UserID: "u-bob"}, nil).Once()

	got, err := f.projects.AddManager(t.Context(), "Work", "BOB")

	require.NoError(t, err)
	assert.Equal(t, &ProjectManager{
		ID: "pm-bob", UserID: "u-bob", Name: "Bob", Username: "bob", Project: "Work",
		Managers: []string{"Alice", "Me Myself", "Bob"},
	}, got)
}

func TestAddProjectManagerRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		project, person string
		setup           func(f *projectsFixture)
		want            string
	}{
		"private": {"Home", "alice", nil, `project "Home" is private: it has one manager, and Planka does not let you add managers to it`},
		"already": {"Work", "alice", nil, `Alice already manages project "Work"`},
		"me already": {"Empty", "me", func(f *projectsFixture) {
			f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me", Name: "Me Myself"}, nil).Once()
		}, `Me Myself already manages project "Empty"`},
		"cannot list users": {"Work", "bob", func(f *projectsFixture) {
			f.api.EXPECT().Users(mock.Anything).Return(nil, apiError(http.StatusForbidden)).Once()
		}, "only a Planka admin or project owner can look up other people"},
		"board user": {"Empty", "alice", func(f *projectsFixture) {
			f.api.EXPECT().CreateProjectManager(mock.Anything, "pe", "u-alice").Return(nil, apiError(http.StatusUnprocessableEntity)).Once()
		}, "Alice cannot manage projects: Planka allows only admins and project owners to"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectsFixture(t)
			f.serveProjects()
			if tc.setup != nil {
				tc.setup(f)
			}

			_, err := f.projects.AddManager(t.Context(), tc.project, tc.person)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRemoveProjectManager(t *testing.T) {
	f := newProjectsFixture(t)
	f.serveProjects()
	f.api.EXPECT().DeleteProjectManager(mock.Anything, "pm-alice").Return(nil).Once()

	got, err := f.projects.RemoveManager(t.Context(), "Work", "alice")

	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, []string{"Me Myself"}, got.Managers)
}

func TestRemoveProjectManagerRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		project, person string
		setup           func(f *projectsFixture)
		want            string
	}{
		"private":     {"Home", "me", nil, `project "Home" is private`},
		"not manager": {"Work", "bob", nil, `no manager "bob" on project "Work"; managers: Alice, Me Myself`},
		"last": {"Empty", "me", func(f *projectsFixture) {
			f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-me", Name: "Me Myself"}, nil).Once()
		}, `Me Myself is the last manager of project "Empty": add another manager first`},
		"me not manager": {"Work", "me", func(f *projectsFixture) {
			f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-carol", Name: "Carol"}, nil).Once()
		}, `Carol is not a manager of project "Work"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectsFixture(t)
			f.serveProjects()
			if tc.setup != nil {
				tc.setup(f)
			}

			_, err := f.projects.RemoveManager(t.Context(), tc.project, tc.person)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
