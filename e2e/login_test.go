//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStdioPasswordLogin signs in as a non-admin user who has no API key, the case password
// login exists for: the user sees only their own boards and writes appear under their name.
func TestStdioPasswordLogin(t *testing.T) {
	p := requirePlanka(t)
	id := unique(t)
	project := p.newProject(t, "e2e-"+id)
	shared := p.newBoard(t, project, "Shared "+id, "Open")
	p.newBoard(t, project, "Private "+id, "Open")
	cardID := p.newCard(t, shared, "Open", "Password login "+id)
	userID, username, password := p.newLoginUser(t, id)
	p.addMember(t, shared, userID)
	p.acceptTerms(t, username, password)

	user := connectStdioLogin(t, username, password)

	boards := callInto[[]projectBoards](t, user, "list_boards", map[string]any{"query": id})
	assert.Equal(t, []string{shared.ID}, boardIDs(boards), "the user is a member of one board only")

	text, isError := call(t, user, "add_comment", map[string]any{"card": cardID, "text": "written with a password login"})
	require.False(t, isError, text)
	assert.Contains(t, text, "E2E pw "+id, "the comment is written as the logged-in user")
}

// TestStdioWrongPasswordIsReported checks that a refused login comes back as a tool error that
// points at the username and password, not at an API key.
func TestStdioWrongPasswordIsReported(t *testing.T) {
	p := requirePlanka(t)
	_, username, _ := p.newLoginUser(t, unique(t))

	user := connectStdioLogin(t, username, "not-the-password")

	text, isError := call(t, user, "list_boards", map[string]any{})
	assert.True(t, isError)
	assert.Equal(t, "Planka rejected the username or password: check PLANKA_USERNAME and PLANKA_PASSWORD", text)
}

// TestStdioLoginBeforeTermsAreAccepted checks Planka's refusal of an account that has not accepted
// its terms yet (every new account, until its first browser login) reads as what to do about it.
func TestStdioLoginBeforeTermsAreAccepted(t *testing.T) {
	p := requirePlanka(t)
	_, username, password := p.newLoginUser(t, unique(t))

	user := connectStdioLogin(t, username, password)

	text, isError := call(t, user, "list_boards", map[string]any{})
	assert.True(t, isError)
	assert.Equal(t, "Planka wants this account to accept its terms first: log in once in the browser, then try again", text)
}
