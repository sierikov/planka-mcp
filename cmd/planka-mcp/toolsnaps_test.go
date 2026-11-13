package main

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/toolsnaps"
)

// connectSession serves server in memory and returns a client session on it.
func connectSession(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolSnapshots(t *testing.T) {
	session := connectSession(t, newTestServer(t, nil))
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	for _, tool := range res.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			require.NoError(t, toolsnaps.Test(tool.Name, tool))
		})
	}
}
