package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManifestListsEveryTool keeps the tool list in manifest.json, which marketplaces
// display, in sync with the tools the server registers.
func TestManifestListsEveryTool(t *testing.T) {
	data, err := os.ReadFile("../../manifest.json")
	require.NoError(t, err)
	var manifest struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))

	session := connectSession(t, newTestServer(t, nil))
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	registered := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		registered = append(registered, tool.Name)
	}
	declared := make([]string, 0, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		declared = append(declared, tool.Name)
		assert.NotEmpty(t, tool.Description, tool.Name)
	}
	assert.ElementsMatch(t, registered, declared, "manifest.json tools differ from the registered tools")
}

// TestManifestUserConfigHasDefaults checks that every ${user_config.X} the server's env refers
// to is required or has a default. Without one, a host passes the literal placeholder, and an
// unexpanded username would silently switch an API-key user to a bogus login.
func TestManifestUserConfigHasDefaults(t *testing.T) {
	data, err := os.ReadFile("../../manifest.json")
	require.NoError(t, err)
	var manifest struct {
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		UserConfig map[string]struct {
			Required bool    `json:"required"`
			Default  *string `json:"default"`
		} `json:"user_config"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.NotEmpty(t, manifest.Server.MCPConfig.Env)

	for name, value := range manifest.Server.MCPConfig.Env {
		key, ok := strings.CutPrefix(value, "${user_config.")
		require.True(t, ok, "%s is not a user_config placeholder: %s", name, value)
		key = strings.TrimSuffix(key, "}")
		entry, found := manifest.UserConfig[key]
		require.True(t, found, "%s refers to unknown user_config %q", name, key)
		assert.True(t, entry.Required || entry.Default != nil, "user_config %q (for %s) is optional without a default", key, name)
	}
}
