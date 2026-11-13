//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	positionGap = 65536
)

// planka is a minimal client for Planka's REST API, used to build the fixtures each test needs.
// It never goes through the MCP server, so the tests only trust the server for what they assert on.
type planka struct {
	base string
	key  string
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// unique returns a short random suffix, so every test gets names no other data on the instance has.
func unique(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// randomPassword is a new password for a throwaway user, different on every run.
func randomPassword(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return "Aa1!" + hex.EncodeToString(b)
}

// roundTrip sends req with the given auth header and decodes a JSON response into out (when not nil).
func roundTrip(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decode %q: %w", req.Method, req.URL.Path, raw, err)
	}
	return nil
}

func (p *planka) request(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, p.base+"/api"+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if p.key != "" {
		req.Header.Set("X-Api-Key", p.key)
	}
	return roundTrip(req, out)
}

// item is Planka's {"item": ...} envelope, reduced to what the tests read.
type item struct {
	Item struct {
		ID string `json:"id"`
	} `json:"item"`
	Included struct {
		APIKey string `json:"apiKey"`
	} `json:"included"`
}

// send does a JSON request and returns the created or changed item.
func (p *planka) send(t *testing.T, method, path string, body any) item {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		buf = bytes.NewReader(raw)
	}
	var out item
	require.NoError(t, p.request(t.Context(), method, path, "application/json", buf, &out))
	return out
}

// createBoard creates a board, which Planka only accepts as multipart/form-data.
func (p *planka) createBoard(t *testing.T, projectID, name string) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("position", fmt.Sprint(positionGap)))
	require.NoError(t, w.WriteField("name", name))
	require.NoError(t, w.Close())
	var out item
	require.NoError(t, p.request(t.Context(), http.MethodPost, "/projects/"+projectID+"/boards", w.FormDataContentType(), &buf, &out))
	return out.Item.ID
}

// board is a board built for one test.
type board struct {
	Name      string
	ID        string
	ProjectID string
	lists     map[string]string // list name -> id
}

// newProject creates a project that is deleted, boards first, when the test ends.
func (p *planka) newProject(t *testing.T, name string) string {
	t.Helper()
	id := p.send(t, http.MethodPost, "/projects", map[string]any{"type": "shared", "name": name}).Item.ID
	t.Cleanup(func() { p.deleteProject(t, id) })
	return id
}

// deleteProject removes a project and its boards. It runs in cleanups, where t.Context() is already
// done, so it uses its own context and only reports failures.
func (p *planka) deleteProject(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var boards struct {
		Included struct {
			Boards []struct {
				ID        string `json:"id"`
				ProjectID string `json:"projectId"`
			} `json:"boards"`
		} `json:"included"`
	}
	if err := p.request(ctx, http.MethodGet, "/projects/"+id, "", nil, &boards); err != nil {
		t.Logf("e2e cleanup: %v", err)
		return
	}
	for _, b := range boards.Included.Boards {
		if err := p.request(ctx, http.MethodDelete, "/boards/"+b.ID, "", nil, nil); err != nil {
			t.Logf("e2e cleanup: %v", err)
		}
	}
	if err := p.request(ctx, http.MethodDelete, "/projects/"+id, "", nil, nil); err != nil {
		t.Logf("e2e cleanup: %v", err)
	}
}

// newBoard creates a board in the project with the given active lists, in order.
func (p *planka) newBoard(t *testing.T, projectID, name string, lists ...string) *board {
	t.Helper()
	b := &board{Name: name, ProjectID: projectID, ID: p.createBoard(t, projectID, name), lists: map[string]string{}}
	for i, l := range lists {
		b.lists[l] = p.send(t, http.MethodPost, "/boards/"+b.ID+"/lists", map[string]any{
			"type": "active", "position": positionGap * (i + 1), "name": l,
		}).Item.ID
	}
	return b
}

// newCard creates a card in the named list and returns its ID.
func (p *planka) newCard(t *testing.T, b *board, list, name string) string {
	t.Helper()
	return p.send(t, http.MethodPost, "/lists/"+b.lists[list]+"/cards", map[string]any{
		"type": "project", "position": positionGap, "name": name,
	}).Item.ID
}

// addMember gives the user editor rights on the board.
func (p *planka) addMember(t *testing.T, b *board, userID string) {
	t.Helper()
	p.send(t, http.MethodPost, "/boards/"+b.ID+"/board-memberships", map[string]any{"userId": userID, "role": "editor"})
}

// newList adds an active list to the board at the given position and returns its ID. Unlike
// newBoard it does not record the list by name, so two lists may share one.
func (p *planka) newList(t *testing.T, b *board, name string, position int) string {
	t.Helper()
	return p.send(t, http.MethodPost, "/boards/"+b.ID+"/lists", map[string]any{
		"type": "active", "position": position, "name": name,
	}).Item.ID
}

// meName is the display name of the key's own user, as the server shows people.
func (p *planka) meName(t *testing.T) string {
	t.Helper()
	var out struct {
		Item struct {
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"item"`
	}
	require.NoError(t, p.request(t.Context(), http.MethodGet, "/users/me", "", nil, &out))
	if strings.TrimSpace(out.Item.Name) != "" {
		return out.Item.Name
	}
	return out.Item.Username
}

// storedCard is a card as Planka itself stores it, read past the MCP server.
type storedCard struct {
	ID        string     `json:"id"`
	BoardID   string     `json:"boardId"`
	ListID    string     `json:"listId"`
	DueDate   *time.Time `json:"dueDate"`
	Stopwatch *struct {
		StartedAt *time.Time `json:"startedAt"`
		Total     int64      `json:"total"`
	} `json:"stopwatch"`
}

// card reads a card straight from Planka.
func (p *planka) card(t *testing.T, id string) storedCard {
	t.Helper()
	var out struct {
		Item storedCard `json:"item"`
	}
	require.NoError(t, p.request(t.Context(), http.MethodGet, "/cards/"+id, "", nil, &out))
	return out.Item
}

// bootstrapAvailable reports whether the instance answers on its public bootstrap endpoint.
func bootstrapAvailable(ctx context.Context, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/bootstrap", http.NoBody)
	if err != nil {
		return false
	}
	return roundTrip(req, nil) == nil
}

// waitForPlanka polls the bootstrap endpoint until it answers or the deadline passes.
func waitForPlanka(ctx context.Context, base string, wait time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		if bootstrapAvailable(ctx, base) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// newUser creates a board user and an API key for them. Only an admin may issue a key (policy "is-admin" on
// POST /api/users/:id/api-key; the user's own token gets a 404), so the admin does it and no login is needed.
func (p *planka) newUser(t *testing.T, suffix string) (id, key string) {
	t.Helper()
	username := "e2e" + suffix
	id = p.send(t, http.MethodPost, "/users", map[string]any{
		"email": username + "@example.com", "password": randomPassword(t), "role": "boardUser",
		"name": "E2E " + suffix, "username": username,
	}).Item.ID
	t.Cleanup(func() { p.deleteUser(t, id) })

	out := p.send(t, http.MethodPost, "/users/"+id+"/api-key", nil)
	require.NotEmpty(t, out.Included.APIKey)
	return id, out.Included.APIKey
}

// newLoginUser creates a board user without an API key and returns their ID, username and
// password, for signing in with a password the way a non-admin user has to.
func (p *planka) newLoginUser(t *testing.T, suffix string) (id, username, password string) {
	t.Helper()
	username, password = "e2epw"+suffix, randomPassword(t)
	id = p.send(t, http.MethodPost, "/users", map[string]any{
		"email": username + "@example.com", "password": password, "role": "boardUser",
		"name": "E2E pw " + suffix, "username": username,
	}).Item.ID
	t.Cleanup(func() { p.deleteUser(t, id) })
	return id, username, password
}

// acceptTerms does what a new user's first browser login does: accept Planka's terms (Planka asks
// every new account to), then log out again. Until then a password login is refused.
func (p *planka) acceptTerms(t *testing.T, username, password string) {
	t.Helper()
	call := func(method, path, token string, body any) (int, map[string]any) {
		var buf io.Reader = http.NoBody
		if body != nil {
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			buf = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, p.base+"/api"+path, buf)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	status, out := call(http.MethodPost, "/access-tokens", "", map[string]any{"emailOrUsername": username, "password": password})
	if status != http.StatusOK {
		require.Equal(t, http.StatusForbidden, status, "login: %v", out)
		require.Equal(t, "accept-terms", out["step"], "login: %v", out)
		_, terms := call(http.MethodGet, "/terms", "", nil)
		termsItem, _ := terms["item"].(map[string]any)
		status, out = call(http.MethodPost, "/access-tokens/accept-terms", "", map[string]any{
			"pendingToken": out["pendingToken"], "signature": termsItem["signature"],
		})
		require.Equal(t, http.StatusOK, status, "accept-terms: %v", out)
	}
	token, _ := out["item"].(string)
	require.NotEmpty(t, token)
	status, out = call(http.MethodDelete, "/access-tokens/me", token, nil)
	require.Equal(t, http.StatusOK, status, "logout: %v", out)
}

func (p *planka) deleteUser(t *testing.T, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.request(ctx, http.MethodDelete, "/users/"+id, "", nil, nil); err != nil {
		t.Logf("e2e cleanup: %v", err)
	}
}

// containsFold reports whether s contains substr, ignoring case.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
