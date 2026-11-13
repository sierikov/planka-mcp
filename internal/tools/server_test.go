package tools

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryToolHasATitleAndIsClosedWorld: clients show the title, and openWorldHint=false tells
// them the tools only touch the user's own Planka (the MCP default is true).
func TestEveryToolHasATitleAndIsClosedWorld(t *testing.T) {
	session := connect(t, Deps{})
	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	for _, tool := range res.Tools {
		assert.NotEmpty(t, tool.Title, tool.Name)
		require.NotNil(t, tool.Annotations, tool.Name)
		if assert.NotNil(t, tool.Annotations.OpenWorldHint, tool.Name) {
			assert.False(t, *tool.Annotations.OpenWorldHint, tool.Name)
		}
	}
}

func TestInstructionsNameTheTimeZone(t *testing.T) {
	session := connect(t, Deps{})

	zone := zoneText(time.Now(), os.Getenv, os.Readlink)
	assert.Contains(t, session.InitializeResult().Instructions, "in the server's time zone, "+zone+".")
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
		{"TZ names the zone", cest, "Europe/Berlin", noLink, "Europe/Berlin (currently UTC+02:00)"},
		{"TZ with a leading colon", cest, ":Europe/Berlin", noLink, "Europe/Berlin (currently UTC+02:00)"},
		{"TZ as a zoneinfo path", cest, "/usr/share/zoneinfo/Europe/Berlin", noLink, "Europe/Berlin (currently UTC+02:00)"},
		{
			"the /etc/localtime link names the zone", cest, "",
			func(path string) (string, error) {
				if path != "/etc/localtime" {
					return "", errors.New("unexpected path " + path)
				}
				return "/var/db/timezone/zoneinfo/Europe/Berlin", nil
			},
			"Europe/Berlin (currently UTC+02:00)",
		},
		{"no name falls back to the abbreviation", cest, "", noLink, "CEST (currently UTC+02:00)"},
		{"UTC is just UTC", time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), "", noLink, "UTC"},
		{"a negative half-hour offset", time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("NST", -(3*60+30)*60)), "America/St_Johns", noLink,
			"America/St_Johns (currently UTC-03:30)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, zoneText(tc.now, env(tc.tz), tc.readlink))
		})
	}
}
