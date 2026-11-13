package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

const fixtureBoardID = "1887856284582020101"

func fixtureBoard(t *testing.T) *planka.BoardResponse {
	t.Helper()
	var resp planka.BoardResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "board"), &resp))
	return &resp
}

func fixtureProjects(t *testing.T) *planka.ProjectsResponse {
	t.Helper()
	var resp planka.ProjectsResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "projects"), &resp))
	return &resp
}

// inboxProjects has two projects that each hold a board named "Inbox".
func inboxProjects() *planka.ProjectsResponse {
	resp := &planka.ProjectsResponse{Items: []planka.Project{
		{ID: "p1", Name: "Personal"}, {ID: "p2", Name: "Work"},
	}}
	resp.Included.Boards = []planka.Board{
		{ID: "1", ProjectID: "p1", Name: "Inbox", Position: 1},
		{ID: "3", ProjectID: "p2", Name: "Sprint", Position: 2},
		{ID: "2", ProjectID: "p2", Name: "Inbox", Position: 1},
	}
	return resp
}

func TestGetBoardOrdersListsAndFiltersByList(t *testing.T) {
	projects := NewMockProjectsAPI(t)
	boards := NewMockBoardsAPI(t)
	projects.EXPECT().Projects(mock.Anything).Return(fixtureProjects(t), nil)
	boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(fixtureBoard(t), nil)
	cat := NewCatalog(projects, boards)

	b, err := cat.GetBoard(t.Context(), "Planka MCP", "")
	require.NoError(t, err)
	assert.Equal(t, fixtureBoardID, b.ID)
	assert.Equal(t, "Projects", b.Project)

	names := make([]string, 0, len(b.Lists))
	closed := map[string]bool{}
	for _, l := range b.Lists {
		names = append(names, l.Name)
		closed[l.Name] = l.Closed
	}
	assert.Equal(t, []string{"Open", "In progress", "Ready for release", "Done", "Dropped"}, names)
	assert.True(t, closed["Dropped"])
	assert.False(t, closed["Open"])
	require.Len(t, b.Lists[0].Cards, 1)
	assert.Equal(t, "Write README", b.Lists[0].Cards[0].Title)
	assert.Equal(t, "In progress", b.Lists[1].Cards[0].List)

	filtered, err := cat.GetBoard(t.Context(), "Planka MCP", "in progress")
	require.NoError(t, err)
	require.Len(t, filtered.Lists, 1)
	assert.Equal(t, "In progress", filtered.Lists[0].Name)
	require.Len(t, filtered.Lists[0].Cards, 1)
	assert.Equal(t, "Research search API", filtered.Lists[0].Cards[0].Title)

	_, err = cat.GetBoard(t.Context(), "Planka MCP", "Doing")
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk)
	assert.Equal(t, "list", unk.Kind)
}

func TestGetBoardByIDAndURL(t *testing.T) {
	for _, ref := range []string{fixtureBoardID, "https://plan.example/boards/" + fixtureBoardID} {
		projects := NewMockProjectsAPI(t)
		boards := NewMockBoardsAPI(t)
		projects.EXPECT().Projects(mock.Anything).Return(fixtureProjects(t), nil)
		boards.EXPECT().Board(mock.Anything, fixtureBoardID).Return(fixtureBoard(t), nil)

		b, err := NewCatalog(projects, boards).GetBoard(t.Context(), ref, "")
		require.NoError(t, err, ref)
		assert.Equal(t, "Planka MCP", b.Name)
	}
}

func TestGetBoardByAmbiguousName(t *testing.T) {
	projects := NewMockProjectsAPI(t)
	boards := NewMockBoardsAPI(t)
	projects.EXPECT().Projects(mock.Anything).Return(inboxProjects(), nil)
	cat := NewCatalog(projects, boards)

	_, err := cat.GetBoard(t.Context(), "Inbox", "")

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{
		{ID: "1", Label: "Personal / Inbox"},
		{ID: "2", Label: "Work / Inbox"},
	}, amb.Candidates)
	assert.Equal(t, `"Inbox" matches several boards: Personal / Inbox (id 1), Work / Inbox (id 2)`, err.Error())
}

func TestResolveBoard(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		want    BoardRef
		wantErr string
	}{
		{name: "project qualified name", ref: "work / inbox", want: BoardRef{ID: "2", Name: "Inbox", Project: "Work"}},
		{name: "unique name", ref: "Sprint", want: BoardRef{ID: "3", Name: "Sprint", Project: "Work"}},
		{name: "unique prefix", ref: "spr", want: BoardRef{ID: "3", Name: "Sprint", Project: "Work"}},
		{name: "numeric id", ref: "3", want: BoardRef{ID: "3", Name: "Sprint", Project: "Work"}},
		{name: "unknown id in url", ref: "https://x/boards/99", wantErr: "not found"},
		{name: "unknown name", ref: "Nope", wantErr: `no board "Nope"; boards: Personal / Inbox, Work / Inbox, Work / Sprint`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projects := NewMockProjectsAPI(t)
			projects.EXPECT().Projects(mock.Anything).Return(inboxProjects(), nil)
			cat := NewCatalog(projects, NewMockBoardsAPI(t))

			got, err := cat.resolveBoard(t.Context(), tc.ref)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveBoardDigitsOnlyName(t *testing.T) {
	resp := inboxProjects()
	resp.Included.Boards = append(resp.Included.Boards, planka.Board{ID: "500", ProjectID: "p1", Name: "2026"})
	projects := NewMockProjectsAPI(t)
	projects.EXPECT().Projects(mock.Anything).Return(resp, nil)

	got, err := NewCatalog(projects, NewMockBoardsAPI(t)).resolveBoard(t.Context(), "2026")
	require.NoError(t, err)
	assert.Equal(t, "500", got.ID)
}

func TestListBoardsQueryFilters(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []ProjectBoards
	}{
		{
			name: "empty query returns everything, boards ordered by position",
			want: []ProjectBoards{
				{Project: "Personal", Boards: []BoardRef{{ID: "1", Name: "Inbox", Project: "Personal"}}},
				{Project: "Work", Boards: []BoardRef{
					{ID: "2", Name: "Inbox", Project: "Work"},
					{ID: "3", Name: "Sprint", Project: "Work"},
				}},
			},
		},
		{
			name:  "a matching project keeps all its boards",
			query: "WORK",
			want: []ProjectBoards{{Project: "Work", Boards: []BoardRef{
				{ID: "2", Name: "Inbox", Project: "Work"},
				{ID: "3", Name: "Sprint", Project: "Work"},
			}}},
		},
		{
			name:  "board name matches keep only matching boards, empty projects are dropped",
			query: "inb",
			want: []ProjectBoards{
				{Project: "Personal", Boards: []BoardRef{{ID: "1", Name: "Inbox", Project: "Personal"}}},
				{Project: "Work", Boards: []BoardRef{{ID: "2", Name: "Inbox", Project: "Work"}}},
			},
		},
		{name: "no match", query: "zzz", want: []ProjectBoards{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projects := NewMockProjectsAPI(t)
			projects.EXPECT().Projects(mock.Anything).Return(inboxProjects(), nil)

			got, err := NewCatalog(projects, NewMockBoardsAPI(t)).ListBoards(t.Context(), tc.query)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveBoardDuplicateNameInOneProject(t *testing.T) {
	resp := inboxProjects()
	resp.Included.Boards = append(resp.Included.Boards,
		planka.Board{ID: "4", ProjectID: "p2", Name: "Inbox", Position: 5})

	for _, ref := range []string{"Work / Inbox", "work / inbox"} {
		projects := NewMockProjectsAPI(t)
		projects.EXPECT().Projects(mock.Anything).Return(resp, nil)

		_, err := NewCatalog(projects, NewMockBoardsAPI(t)).resolveBoard(t.Context(), ref)

		var amb *AmbiguousError
		require.ErrorAs(t, err, &amb, ref)
		assert.Equal(t, []Candidate{
			{ID: "2", Label: "Work / Inbox"},
			{ID: "4", Label: "Work / Inbox"},
		}, amb.Candidates, ref)
	}
}
