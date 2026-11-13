#!/usr/bin/env bash
# Builds every file a release publishes into dist/:
#
#   planka-mcp-<platform>[.exe]  the server, one binary per platform below
#   planka-mcp-<platform>.mcpb   that binary bundled for Claude Desktop
#   server.json                  the MCP Registry entry for this version
#   LICENSE, THIRD_PARTY_NOTICES planka-mcp's license and those of the code
#                                compiled into it, also inside every bundle
#   checksums.txt                the SHA-256 of each file above, for sha256sum -c
#
# Usage: scripts/release.sh VERSION (e.g. 0.1.0), or make dist VERSION=0.1.0.
# The release workflow runs it for a tag; run it locally to see what a release
# would ship. manifest.json and server.json are read, never changed: the
# committed copies say 0.0.0-dev, and only the stamped copies carry VERSION.
set -euo pipefail

VERSION=${1:?usage: scripts/release.sh VERSION}

# The platforms, as the file names spell them. A new one also needs its bundle
# listed in server.json.
PLATFORMS="macos-amd64 macos-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64"

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

# ext PLATFORM: the file extension a binary needs on PLATFORM.
ext() {
  case $1 in
    windows-*) echo .exe ;;
    *) echo "" ;;
  esac
}

# build_binary PLATFORM: cross-compiles the server into dist/.
build_binary() {
  local platform=$1
  local goos=${platform%-*} goarch=${platform#*-}
  if [[ $goos == macos ]]; then
    goos=darwin
  fi
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "dist/planka-mcp-$platform$(ext "$platform")" ./cmd/planka-mcp
}

# mcpb_os PLATFORM: the platform's name in an MCPB manifest.
mcpb_os() {
  case $1 in
    macos-*) echo darwin ;;
    windows-*) echo win32 ;;
    *) echo "${1%-*}" ;;
  esac
}

# build_bundle PLATFORM: zips the binary, manifest.json and icon.png into
# dist/planka-mcp-PLATFORM.mcpb. The bundled manifest carries the version, the
# binary's name, which on Windows ends in .exe, and the one OS the binary runs
# on, so Claude Desktop refuses a bundle built for another.
build_bundle() {
  local platform=$1
  local exe stage
  exe=planka-mcp$(ext "$platform")
  stage=$(mktemp -d)
  jq --arg version "$VERSION" --arg exe "$exe" --arg os "$(mcpb_os "$platform")" '
    .version = $version
    | .server.entry_point = $exe
    | .server.mcp_config.command = "${__dirname}/" + $exe
    | .compatibility.platforms = [$os]
  ' manifest.json >"$stage/manifest.json"
  cp icon.png "$stage/icon.png"
  cp "dist/planka-mcp-$platform$(ext "$platform")" "$stage/$exe"
  zip -q -j "dist/planka-mcp-$platform.mcpb" "$stage/manifest.json" "$stage/icon.png" "$stage/$exe" \
    dist/LICENSE dist/THIRD_PARTY_NOTICES
  rm -rf "$stage"
}

# sha256 FILE: the file's SHA-256, in hex.
sha256() {
  shasum -a 256 "$1" | cut -d' ' -f1
}

# write_server_json: dist/server.json is server.json with this version, each
# bundle's download URL on the GitHub release and its SHA-256, and the Docker
# image tagged with this version.
write_server_json() {
  local hashes='{}' bundle
  for bundle in dist/*.mcpb; do
    hashes=$(jq --arg file "${bundle#dist/}" --arg sha256 "$(sha256 "$bundle")" '.[$file] = $sha256' <<<"$hashes")
  done
  jq --arg version "$VERSION" --argjson hashes "$hashes" '
    .version = $version
    | (.repository.url + "/releases/download/v" + $version) as $downloads
    | .packages |= map(
        if .registryType == "mcpb" then
          (.identifier | split("/") | last) as $file
          | .identifier = "\($downloads)/\($file)"
          | .fileSha256 = ($hashes[$file] // error("server.json lists \($file), but it was not built"))
        elif .registryType == "oci" then
          .identifier |= sub(":[^:]*$"; ":\($version)")
        else . end)
  ' server.json >dist/server.json
}

rm -rf dist
mkdir -p dist
cp LICENSE dist/LICENSE
scripts/notices.sh dist/THIRD_PARTY_NOTICES
for platform in $PLATFORMS; do
  build_binary "$platform"
  build_bundle "$platform"
done
write_server_json
(cd dist && shasum -a 256 planka-mcp-* LICENSE THIRD_PARTY_NOTICES >checksums.txt)
ls -l dist
