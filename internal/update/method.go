package update

import (
	"path"
	"path/filepath"
	"strings"
)

// Method is the way this binary was installed, which decides the command that
// upgrades it.
type Method string

// The install methods DetectMethod tells apart.
const (
	// MethodNPM is the npm package, usually run through npx.
	MethodNPM Method = "npm"
	// MethodGo is go install.
	MethodGo Method = "go"
	// MethodUnknown is anything else: a downloaded release, a repo build.
	MethodUnknown Method = "unknown"
)

// npmPackageMarker starts the path of the npm package and of its per-platform
// packages (@sierikov/planka-mcp-darwin-arm64 and so on).
const npmPackageMarker = "/node_modules/@sierikov/planka-mcp"

// DetectMethod works out how the binary at exe was installed. exe must
// already be resolved through symlinks, because npm links into its own bin
// directory. goBin is the directory go install writes to
// (see GoBin); empty means unknown. Windows paths are recognised on any
// system.
func DetectMethod(exe, goBin string) Method {
	p := toSlash(exe)
	if strings.Contains(p, npmPackageMarker) {
		return MethodNPM
	}
	if goBin != "" && path.Dir(p) == path.Clean(toSlash(goBin)) {
		return MethodGo
	}
	return MethodUnknown
}

// GoBin returns the directory go install writes binaries to: $GOBIN, else the
// bin directory of the first $GOPATH entry, else $HOME/go/bin. It returns ""
// when none of those is known.
func GoBin(getenv func(string) string, home string) string {
	if dir := getenv("GOBIN"); dir != "" {
		return dir
	}
	if paths := filepath.SplitList(getenv("GOPATH")); len(paths) > 0 && paths[0] != "" {
		return filepath.Join(paths[0], "bin")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "go", "bin")
}

// UpgradeCommand returns the instruction that upgrades an install made with m.
// It is phrased to follow "Upgrade: " and to fit in parentheses after "how to
// upgrade".
func UpgradeCommand(m Method) string {
	switch m {
	case MethodNPM:
		return "make the server command npx -y @sierikov/planka-mcp@latest, which fetches the newest release when the app starts"
	case MethodGo:
		return "go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest"
	default:
		return "download the .mcpb or binary for your platform from https://github.com/sierikov/planka-mcp/releases/latest"
	}
}

// toSlash turns Windows separators into slashes, so one set of rules matches
// paths from any system.
func toSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}
