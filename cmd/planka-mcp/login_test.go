package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

// stdioSession runs the stdio server on pipes with cfg, sends initialize and one
// tools/call of list_boards, and returns the call's result text and whether it was
// an error. It then stops the server and waits for run to return.
func stdioSession(t *testing.T, cfg Config) (text string, isError bool) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, cfg, inR, outW)
		_ = outW.Close()
	}()

	send := func(msg string) {
		_, err := fmt.Fprintln(inW, msg)
		require.NoError(t, err)
	}
	lines := bufio.NewScanner(outR)
	readID := func(id int) json.RawMessage {
		for lines.Scan() {
			var resp struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			require.NoError(t, json.Unmarshal(lines.Bytes(), &resp))
			if resp.ID == id {
				return resp.Result
			}
		}
		require.FailNow(t, "no response", "id %d", id)
		return nil
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	readID(1)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_boards","arguments":{}}}`)
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(readID(2), &result))
	require.NotEmpty(t, result.Content)

	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "run did not return after the context was cancelled")
	}
	_ = inW.Close()
	return result.Content[0].Text, result.IsError
}

func TestStdioLoginWorksAndLogsOutOnStop(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"GET /api/projects": plankatest.Fixture(t, "projects"),
	}, plankatest.WithLogin("ann", "pw"))

	text, isError := stdioSession(t, Config{URL: srv.URL, Username: "ann", Password: "pw", Transport: transportStdio})

	assert.False(t, isError, text)
	assert.Contains(t, text, "Planka MCP")
	reqs := srv.Requests()
	calls := make([]string, 0, len(reqs))
	for _, r := range reqs {
		calls = append(calls, r.Method+" "+r.Path)
		assert.Empty(t, r.APIKey, "a login never sends X-Api-Key")
	}
	assert.Equal(t, []string{"POST /api/access-tokens", "GET /api/projects", "DELETE /api/access-tokens/me"}, calls)
	assert.Equal(t, "session-1", srv.Last().Bearer, "logout ends the session it opened")
}

func TestStdioWrongPasswordIsAToolError(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"GET /api/projects": plankatest.Fixture(t, "projects"),
	}, plankatest.WithLogin("ann", "pw"))

	text, isError := stdioSession(t, Config{URL: srv.URL, Username: "ann", Password: "nope", Transport: transportStdio})

	assert.True(t, isError)
	assert.Equal(t, "Planka rejected the username or password: check PLANKA_USERNAME and PLANKA_PASSWORD", text)
	assert.NotContains(t, text, "PLANKA_API_KEY", "a login failure must not point at the API key")
}

func TestStdioAPIKeyStillWorks(t *testing.T) {
	srv := plankatest.NewServer(t, map[string]any{
		"GET /api/projects": plankatest.Fixture(t, "projects"),
	})

	text, isError := stdioSession(t, Config{URL: srv.URL, APIKey: plankatest.APIKey, Transport: transportStdio})

	assert.False(t, isError, text)
	require.Len(t, srv.Requests(), 1, "an API key needs no login and no logout")
	assert.Equal(t, plankatest.APIKey, srv.Last().APIKey)
}
