//go:build e2e

package e2e

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rulesCard is shaped like the dogfood card that needed section edits.
const rulesCard = "Rules for sorting mail.\n\n## Folders\n**Invoice:**\n- Fahrschule (16895;\n- Telekom\n\n" +
	"**Newsletters**\nCount (842)\n\n## Filters\n- Deleted in the UI\n"

// TestEditDescription reads a long description's outline and one section,
// edits by section and by exact text with the version from the read, and
// checks that the rest is kept byte for byte and a stale version is refused.
func TestEditDescription(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Mail rules")
	p.send(t, http.MethodPatch, "/cards/"+card, map[string]any{"description": rulesCard})
	s := connectStdio(t, env.key)

	outline := callInto[struct {
		Version string `json:"description_version"`
		Outline []struct {
			ID      string `json:"id"`
			Heading string `json:"heading"`
		} `json:"description_outline"`
		Description string `json:"description"`
	}](t, s, "get_card", map[string]any{"card": card, "description": "outline", "comments_limit": 0})
	assert.Empty(t, outline.Description)
	require.Len(t, outline.Outline, 5)
	assert.Equal(t, "Newsletters", outline.Outline[3].Heading)

	section := callInto[struct{ Description string }](t, s, "get_card", map[string]any{"card": card, "description": "1.2", "comments_limit": 0})
	assert.Equal(t, "**Newsletters**\nCount (842)\n\n", section.Description)

	edited := callInto[struct {
		Diff     string   `json:"diff"`
		Version  string   `json:"description_version"`
		Sections []string `json:"sections"`
	}](t, s, "edit_description", map[string]any{"card": card, "version": outline.Version, "edits": []map[string]any{
		{"section": "1.1", "find": "16895;", "replace": "16895, 16936;"},
		{"section": "1.2", "body": "Count (805)"},
		{"section": "2", "append": "- Archived"},
	}})
	assert.Contains(t, edited.Diff, "-Count (842)\n+Count (805)\n")
	assert.Equal(t, []string{"1.1", "1.2", "2"}, edited.Sections)
	assert.NotEqual(t, outline.Version, edited.Version)

	want := strings.NewReplacer("16895;", "16895, 16936;", "(842)", "(805)", "- Deleted in the UI\n", "- Deleted in the UI\n- Archived\n").Replace(rulesCard)
	assert.Equal(t, want, callInto[cardDetail](t, s, "get_card", map[string]any{"card": card}).Description)

	text, isError := call(t, s, "edit_description", map[string]any{"card": card, "version": outline.Version, "edits": []map[string]any{{"append": "x"}}})
	assert.True(t, isError, text)
	assert.Contains(t, text, "the description changed since you read it")
}

// TestHTTPModeHidesPlankasAddress: with Planka down, a remote caller learns
// that, but not the address PLANKA_URL points at.
func TestHTTPModeHidesPlankasAddress(t *testing.T) {
	requirePlanka(t)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	downAddr := ln.Addr().String()
	require.NoError(t, ln.Close())
	ln, err = (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	cmd := exec.CommandContext(t.Context(), env.binary, "-transport", "http", "-addr", addr) //nolint:gosec // the binary is the one TestMain just built
	cmd.Env = serverEnv("PLANKA_URL=http://" + downAddr)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/healthz") //nolint:noctx // plain probe of a local server
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond)

	text, isError := call(t, connectHTTP(t, "http://"+addr+"/mcp", env.key), "list_boards", map[string]any{})

	assert.True(t, isError, text)
	assert.Contains(t, text, "Planka did not answer (the connection was refused)")
	assert.NotContains(t, text, downAddr)
}
