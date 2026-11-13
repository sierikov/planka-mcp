package main

import (
	"runtime/debug"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...". Release builds
// stamp the tag without its leading "v", so the binary, manifest.json, server.json
// and the image tag all read the same.
var version = "dev"

// buildVersion is the version the server reports: the stamped one, else the module
// version Go records in the binary, else "dev". That module version is the tag for
// `go install …@vX.Y.Z`; for a plain build in a git checkout with commits, Go 1.24+
// records a pseudo-version such as 0.0.0-20261020125636-5168b518c43f+dirty, which
// says exactly which commit the binary came from.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	if ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "dev"
}
