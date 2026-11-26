package tools

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryToolHasATitleAndIsClosedWorld: clients show the title, and openWorldHint=false tells
// them the tools only touch the user's own Planka (the MCP default is true).
// TestEveryToolHasATitleAndIsClosedWorld: every tool stays inside Planka,
// except set_project_background, which fetches photos from Unsplash.
func TestEveryToolHasATitleAndIsClosedWorld(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	for _, tool := range res.Tools {
		assert.NotEmpty(t, tool.Title, tool.Name)
		require.NotNil(t, tool.Annotations, tool.Name)
		if assert.NotNil(t, tool.Annotations.OpenWorldHint, tool.Name) {
			assert.Equal(t, tool.Name == "set_project_background", *tool.Annotations.OpenWorldHint, tool.Name)
		}
	}
}

func TestInstructionsNameTheTimeZone(t *testing.T) {
	session := connect(t, Deps{})

	zone := zoneText(time.Now(), os.Getenv, os.Readlink)
	assert.Contains(t, session.InitializeResult().Instructions, "in the server's time zone, "+zone+".")
}

// TestDueTextNamesTheZone: the due parameters name the zone themselves, for
// clients that never pass the server's instructions on to the model.
func TestDueTextNamesTheZone(t *testing.T) {
	props := inputProps(t, connect(t, Deps{}))

	zone := zoneText(time.Now(), os.Getenv, os.Readlink)
	for _, tool := range []string{"create_card", "update_card"} {
		require.Contains(t, props[tool], "due", tool)
		assert.Contains(t, props[tool]["due"]["description"], "without an offset is in "+zone+", the server's time zone", tool)
	}
}

func TestZoneText(t *testing.T) {
	cest := time.Date(2026, 10, 20, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	noLink := func(string) (string, error) { return "", errors.New("no link") }
	env := func(tz string) func(string) string {
		return func(key string) string {
			if key == "TZ" {
				return tz
			}
			return ""
		}
	}

	tests := []struct {
		name     string
		now      time.Time
		tz       string
		readlink func(string) (string, error)
		want     string
	}{
		{"TZ names the zone", cest, "Europe/Berlin", noLink, "Europe/Berlin"},
		{"TZ with a leading colon", cest, ":Europe/Berlin", noLink, "Europe/Berlin"},
		{"TZ as a zoneinfo path", cest, "/usr/share/zoneinfo/Europe/Berlin", noLink, "Europe/Berlin"},
		{
			"the /etc/localtime link names the zone", cest, "",
			func(path string) (string, error) {
				if path != "/etc/localtime" {
					return "", errors.New("unexpected path " + path)
				}
				return "/var/db/timezone/zoneinfo/Europe/Berlin", nil
			},
			"Europe/Berlin",
		},
		{"no name falls back to the abbreviation", cest, "", noLink, "CEST"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, zoneText(tc.now, env(tc.tz), tc.readlink))
		})
	}
}

func TestNoticeIsAddedToTheInstructions(t *testing.T) {
	session := connect(t, Deps{Notice: "planka-mcp has a newer release: 9.9.9."})

	assert.True(t, strings.HasSuffix(session.InitializeResult().Instructions, "\n\nplanka-mcp has a newer release: 9.9.9."))
}
