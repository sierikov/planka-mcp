#!/usr/bin/env bash
# End-to-end test of the npm packages: builds a release with scripts/release.sh
# (which replaces dist/), assembles the packages with scripts/npm-packages.sh,
# packs them, installs the main package and this machine's platform package
# into a throwaway project, and runs the server through the launcher.
#
# Usage: scripts/npm-test.sh, or make npm-test. Needs go, jq, zip, node and npm.
# Prints "ok: <case>" per case and stops at the first failure.
set -euo pipefail

VERSION=0.0.0-test

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

ok() { echo "ok: $*"; }
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

scripts/release.sh "$VERSION" >/dev/null
scripts/npm-packages.sh "$VERSION" 2>/dev/null

# Case: every platform package holds the binary the release ships for it.
while read -r target asset; do
  want=dist/planka-mcp-$asset
  got=dist/npm/planka-mcp-$target/bin/planka-mcp
  if [[ $target == win32-* ]]; then
    want=$want.exe
    got=$got.exe
  fi
  cmp -s "$want" "$got" || fail "planka-mcp-$target does not hold $want"
done <<'EOF'
darwin-arm64 macos-arm64
darwin-x64 macos-amd64
linux-x64 linux-amd64
linux-arm64 linux-arm64
win32-x64 windows-amd64
win32-arm64 windows-arm64
EOF
ok "each platform package holds its release binary"

# Case: a directory of release assets, as gh release download leaves them, works as well.
assets=$tmp/assets
mkdir "$assets"
cp dist/planka-mcp-* dist/LICENSE dist/THIRD_PARTY_NOTICES "$assets/"
rm "$assets"/*.mcpb
cp -R dist/npm "$tmp/npm-from-dist"
scripts/npm-packages.sh "$VERSION" "$assets" 2>/dev/null
diff -r "$tmp/npm-from-dist" dist/npm >/dev/null || fail "packages from release assets differ from those from dist/"
ok "packages from downloaded release assets"

# Case: metadata of the generated packages.
main=dist/npm/planka-mcp/package.json
jq -e --arg v "$VERSION" '
  .name == "@sierikov/planka-mcp" and .version == $v and .mcpName == "io.github.sierikov/planka-mcp"
  and .bin == {"planka-mcp": "cli.js"}
  and (.optionalDependencies | keys) == [
    "@sierikov/planka-mcp-darwin-arm64", "@sierikov/planka-mcp-darwin-x64", "@sierikov/planka-mcp-linux-arm64",
    "@sierikov/planka-mcp-linux-x64", "@sierikov/planka-mcp-win32-arm64", "@sierikov/planka-mcp-win32-x64"]
  and ([.optionalDependencies[]] | all(. == $v))
' "$main" >/dev/null || fail "$main"
for dir in dist/npm/planka-mcp-*; do
  jq -e --arg v "$VERSION" '
    .version == $v and (.os | length) == 1 and (.cpu | length) == 1
    and .bin == null and .scripts == null and .optionalDependencies == null
    and .repository.url == "git+https://github.com/sierikov/planka-mcp.git"
  ' "$dir/package.json" >/dev/null || fail "$dir/package.json"
done
[[ $(find dist/npm -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ') == 7 ]] || fail "dist/npm does not hold seven packages"
ok "metadata"

# Pack every package, and install the main one with this machine's into a throwaway project.
tgz=$tmp/tgz
proj=$tmp/proj
mkdir "$tgz" "$proj"
for dir in dist/npm/*; do
  (cd "$dir" && npm pack --silent --pack-destination "$tgz" >/dev/null)
done

# Case: each package holds exactly the files it should.
contents() { tar -tzf "$1" | LC_ALL=C sort | tr '\n' ' '; }
[[ $(contents "$tgz/sierikov-planka-mcp-$VERSION.tgz") == "package/LICENSE package/README.md package/cli.js package/package.json " ]] ||
  fail "the main package does not hold exactly LICENSE, README.md, cli.js and package.json"
for tarball in "$tgz"/sierikov-planka-mcp-*-"$VERSION".tgz; do
  case $(contents "$tarball") in
    "package/LICENSE package/THIRD_PARTY_NOTICES package/bin/planka-mcp package/package.json " | \
      "package/LICENSE package/THIRD_PARTY_NOTICES package/bin/planka-mcp.exe package/package.json ") ;;
    *) fail "$tarball holds: $(contents "$tarball")" ;;
  esac
done
ok "package contents"

here=$(node -p 'process.platform + "-" + process.arch')
printf '{"private": true}\n' >"$proj/package.json"
(cd "$proj" && npm install --no-audit --no-fund --silent \
  "$tgz/sierikov-planka-mcp-$VERSION.tgz" "$tgz/sierikov-planka-mcp-$here-$VERSION.tgz" >&2)
launcher=$proj/node_modules/@sierikov/planka-mcp/cli.js

# Case: the version, through the bin link npx uses.
got=$(cd "$proj" && npx --no-install planka-mcp version)
[[ $got == "$VERSION" ]] || fail "version: got '$got'"
ok "version"

# Case: stdout carries nothing but the MCP stream. jq fails on any line that is
# not JSON, so a banner from the launcher would fail this.
initialize='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"0"}}}'
got=$( (
  printf '%s\n' "$initialize"
  sleep 1
) | PLANKA_URL=https://planka.example.com PLANKA_API_KEY=x node "$launcher" 2>/dev/null |
  jq -r 'select(.id == 1).result.serverInfo.version')
[[ $got == "$VERSION" ]] || fail "initialize: serverInfo.version is '$got'"
ok "stdout is clean for MCP"

# Case: the binary's exit code comes through (no PLANKA_URL: exit 2).
code=0
env -u PLANKA_URL node "$launcher" >/dev/null 2>&1 || code=$?
[[ $code == 2 ]] || fail "exit code: got $code, want 2"
ok "exit code passes through"

# Case: an unsupported platform fails on stderr alone.
code=0
err=$(node -e 'Object.defineProperty(process, "platform", {value: "freebsd"}); require(process.argv[1])' "$launcher" 2>&1 >"$tmp/stdout") || code=$?
[[ $code == 1 && ! -s $tmp/stdout && $err == *"no build for freebsd"* ]] || fail "unsupported platform: code $code, stderr '$err'"
ok "unsupported platform"

# Case: a supported platform whose package is not installed gets its own
# message. The launcher, copied with its package.json to a directory with no
# node_modules, cannot find the platform package, as after --omit=optional.
bare=$tmp/bare
mkdir "$bare"
cp "$launcher" "$(dirname "$launcher")/package.json" "$bare/"
code=0
err=$(cd "$bare" && NODE_PATH='' node cli.js 2>&1 >"$tmp/stdout") || code=$?
[[ $code == 1 && ! -s $tmp/stdout && $err == *"@sierikov/planka-mcp-$here is missing. Reinstall without --omit=optional"* ]] ||
  fail "missing package: code $code, stderr '$err'"
ok "missing platform package"

# Case: the registry entry names this version of the npm package.
jq -e --arg v "$VERSION" '
  [.packages[] | select(.registryType == "npm")] | length == 1 and .[0].identifier == "@sierikov/planka-mcp"
  and .[0].version == $v and .[0].runtimeHint == "npx" and .[0].transport == {type: "stdio"}
' dist/server.json >/dev/null || fail "dist/server.json: npm package"
ok "server.json"
