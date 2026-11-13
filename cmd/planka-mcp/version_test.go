package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveVersion(t *testing.T) {
	withModule := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/sierikov/planka-mcp", Version: v}}
	}

	tests := map[string]struct {
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		"release build stamps the tag":             {stamped: "0.1.0", info: withModule("(devel)"), ok: true, want: "0.1.0"},
		"stamped version wins over module version": {stamped: "0.2.0", info: withModule("v0.1.0"), ok: true, want: "0.2.0"},
		"go install @v0.1.0 reports the module":    {stamped: "dev", info: withModule("v0.1.0"), ok: true, want: "0.1.0"},
		"go install of a pre-release":              {stamped: "dev", info: withModule("v0.2.0-rc.1"), ok: true, want: "0.2.0-rc.1"},
		"local build without VCS info":             {stamped: "dev", info: withModule("(devel)"), ok: true, want: "dev"},
		"empty module version":                     {stamped: "dev", info: withModule(""), ok: true, want: "dev"},
		"no build info":                            {stamped: "dev", info: nil, ok: false, want: "dev"},
		"empty stamp falls back like dev":          {stamped: "", info: withModule("v1.2.3"), ok: true, want: "1.2.3"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveVersion(tc.stamped, tc.info, tc.ok))
		})
	}
}
