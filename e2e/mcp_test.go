//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // serverZone loads on any runner

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	callTimeout = 30 * time.Second
	// serverZone is the time zone of every planka-mcp the tests start. It is not UTC, and its
	// offset is not a whole hour, so a due date sent with the wrong offset shows up even on a
	// runner whose own zone is UTC.
	serverZone = "Asia/Kolkata"
)

// serverLoc is serverZone loaded; dates the tests expect are in it.
var serverLoc = func() *time.Location {
	loc, err := time.LoadLocation(serverZone)
	if err != nil {
		panic(err)
	}
	return loc
}()

// requirePlanka skips the test when no Planka is reachable (TestMain fails the run instead on CI).
func requirePlanka(t *testing.T) *planka {
	t.Helper()
	if env.skip != "" {
		t.Skip(env.skip)
	}
	return &planka{base: env.url, key: env.key}
}

// serverEnv is the environment of a planka-mcp process: the parent's, minus every PLANKA_ and UNSPLASH_ variable
// and TZ, which is serverZone.
func serverEnv(extra ...string) []string {
	out := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "PLANKA_") || strings.HasPrefix(kv, "UNSPLASH_") || strings.HasPrefix(kv, "TZ=")
	})
	return append(append(out, "TZ="+serverZone), extra...)
}

func connect(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "planka-mcp-e2e", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// connectStdio starts the binary under test over stdio, signed in with key.
func connectStdio(t *testing.T, key string) *mcp.ClientSession {
	t.Helper()
	return connectStdioEnv(t, key)
}

// connectStdioEnv is connectStdio with extra environment variables, such as UNSPLASH_ACCESS_KEY.
func connectStdioEnv(t *testing.T, key string, extra ...string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), env.binary) //nolint:gosec // the binary is the one TestMain just built
	cmd.Env = serverEnv(append([]string{"PLANKA_URL=" + env.url, "PLANKA_API_KEY=" + key}, extra...)...)
	cmd.Stderr = os.Stderr
	return connect(t, &mcp.CommandTransport{Command: cmd})
}

// connectStdioLogin starts the binary under test over stdio, signed in with a username and password.
func connectStdioLogin(t *testing.T, username, password string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), env.binary) //nolint:gosec // the binary is the one TestMain just built
	cmd.Env = serverEnv("PLANKA_URL="+env.url, "PLANKA_USERNAME="+username, "PLANKA_PASSWORD="+password)
	cmd.Stderr = os.Stderr
	return connect(t, &mcp.CommandTransport{Command: cmd})
}

// bearerTransport adds the caller's Planka key to every request.
type bearerTransport struct{ key string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(req)
}

// connectHTTP connects to an HTTP planka-mcp at endpoint as the owner of key.
func connectHTTP(t *testing.T, endpoint, key string) *mcp.ClientSession {
	t.Helper()
	return connect(t, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearerTransport{key: key}, Timeout: 2 * callTimeout},
	})
}

// call runs a tool and returns its text and whether it was an error result.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (text string, isError bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "a tool failure is a result, never a protocol error")
	require.Len(t, res.Content, 1)
	content, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return content.Text, res.IsError
}

// callInto runs a tool that must succeed and decodes its JSON into a fresh value, so a field the
// server omits can never be satisfied by an earlier result.
func callInto[T any](t *testing.T, s *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	text, isError := call(t, s, name, args)
	require.False(t, isError, "%s: %s", name, text)
	var out T
	require.NoError(t, json.Unmarshal([]byte(text), &out), "%s: %s", name, text)
	return out
}

// What the tests read from tool results.
type (
	cardLine struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Board     string   `json:"board"`
		List      string   `json:"list"`
		Labels    []string `json:"labels"`
		Assignees []string `json:"assignees"`
		Due       string   `json:"due"`
		URL       string   `json:"url"`
	}
	searchResult struct {
		Cards []struct {
			cardLine
			InDescription bool `json:"in_description"`
		} `json:"cards"`
		More int `json:"more"`
	}
	commentsResult struct {
		Comments []comment `json:"comments"`
		Older    bool      `json:"older"`
	}
	writeResult struct {
		Card          cardLine `json:"card"`
		CreatedLabels []string `json:"created_labels"`
	}
	task struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Done      bool   `json:"done"`
		Checklist string `json:"checklist"`
		Assignee  string `json:"assignee"`
		Link      *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"link"`
	}
	taskList struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		ShowOnFrontOfCard  *bool  `json:"show_on_front_of_card"`
		HideCompletedTasks bool   `json:"hide_completed_tasks"`
		Tasks              []task `json:"tasks"`
	}
	checklistResult struct {
		Checklist struct {
			ID                 string `json:"id"`
			Name               string `json:"name"`
			ShowOnFrontOfCard  bool   `json:"show_on_front_of_card"`
			HideCompletedTasks bool   `json:"hide_completed_tasks"`
			TasksTotal         int    `json:"tasks_total"`
		} `json:"checklist"`
		Order []string `json:"checklist_order"`
	}
	deletedChecklist struct {
		Deleted string `json:"deleted"`
		Name    string `json:"name"`
		Tasks   []task `json:"tasks"`
	}
	deletedOut struct {
		Deleted string `json:"deleted"`
		Title   string `json:"title"`
	}
	comment struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	customFieldGroup struct {
		Group  string `json:"group"`
		Fields []struct {
			Name              string `json:"name"`
			Value             string `json:"value"`
			ShowOnFrontOfCard bool   `json:"show_on_front_of_card"`
		} `json:"fields"`
		Unset []string `json:"unset"`
	}
	cardDetail struct {
		cardLine
		Description  string             `json:"description"`
		Stopwatch    string             `json:"stopwatch"`
		TaskLists    []taskList         `json:"task_lists"`
		CustomFields []customFieldGroup `json:"custom_fields"`
		Comments     []comment          `json:"comments"`
	}
	boardDetail struct {
		Lists []struct {
			Name   string     `json:"name"`
			Color  string     `json:"color"`
			Closed bool       `json:"closed"`
			Cards  []cardLine `json:"cards"`
		} `json:"lists"`
	}
	listResult struct {
		List struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Color  string `json:"color"`
			Closed bool   `json:"closed"`
			Cards  int    `json:"cards_total"`
		} `json:"list"`
		Lists    []string `json:"list_order"`
		Archived int      `json:"archived"`
	}
	deletedList struct {
		Deleted      string `json:"deleted"`
		Name         string `json:"name"`
		CardsToTrash int    `json:"cards_moved_to_trash"`
	}
	projectBoards struct {
		Project string `json:"project"`
		Boards  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"boards"`
	}
)

// boardIDs lists the IDs of every board in a list_boards result.
func boardIDs(projects []projectBoards) []string {
	var ids []string
	for _, p := range projects {
		for _, b := range p.Boards {
			ids = append(ids, b.ID)
		}
	}
	return ids
}
