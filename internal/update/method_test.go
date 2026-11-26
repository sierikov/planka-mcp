package update

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectMethod(t *testing.T) {
	tests := []struct {
		name  string
		exe   string
		goBin string
		want  Method
	}{
		{"npm platform package", "/Users/a/.npm/_npx/1a2b/node_modules/@sierikov/planka-mcp-darwin-arm64/bin/planka-mcp", "", MethodNPM},
		{"npm on windows", `C:\Users\a\AppData\Local\npm-cache\_npx\1a2b\node_modules\@sierikov\planka-mcp-win32-x64\bin\planka-mcp.exe`, "", MethodNPM},
		{"go install", "/Users/a/go/bin/planka-mcp", "/Users/a/go/bin", MethodGo},
		{"go install with trailing slash in GOBIN", "/Users/a/go/bin/planka-mcp", "/Users/a/go/bin/", MethodGo},
		{"go install on windows", `C:\Users\a\go\bin\planka-mcp.exe`, `C:\Users\a\go\bin`, MethodGo},
		{"repo build", "/Users/a/work/planka-mcp/planka-mcp", "/Users/a/go/bin", MethodUnknown},
		{"unknown GOBIN does not match a bare file name", "planka-mcp", "", MethodUnknown},
		{"empty path", "", "", MethodUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DetectMethod(tt.exe, tt.goBin))
		})
	}
}

func TestGoBin(t *testing.T) {
	sep := string(filepath.ListSeparator)
	tests := []struct {
		name string
		env  map[string]string
		home string
		want string
	}{
		{"GOBIN wins", map[string]string{"GOBIN": "/custom/bin", "GOPATH": "/gp"}, "/home/a", "/custom/bin"},
		{"first GOPATH entry", map[string]string{"GOPATH": "/gp1" + sep + "/gp2"}, "/home/a", filepath.FromSlash("/gp1/bin")},
		{"home default", map[string]string{}, "/home/a", filepath.FromSlash("/home/a/go/bin")},
		{"empty GOBIN and GOPATH fall through", map[string]string{"GOBIN": "", "GOPATH": ""}, "/home/a", filepath.FromSlash("/home/a/go/bin")},
		{"nothing known", map[string]string{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GoBin(func(k string) string { return tt.env[k] }, tt.home)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUpgradeCommand(t *testing.T) {
	tests := []struct {
		method Method
		want   string
	}{
		{MethodNPM, "make the server command npx -y @sierikov/planka-mcp@latest, which fetches the newest release when the app starts"},
		{MethodGo, "go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest"},
		{MethodUnknown, "download the .mcpb or binary for your platform from https://github.com/sierikov/planka-mcp/releases/latest"},
		{Method("something else"), "download the .mcpb or binary for your platform from https://github.com/sierikov/planka-mcp/releases/latest"},
	}
	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			assert.Equal(t, tt.want, UpgradeCommand(tt.method))
		})
	}
}
