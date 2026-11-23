#!/usr/bin/env bash
# Assembles the npm packages of a release into dist/npm/, in the esbuild layout:
#
#   planka-mcp/            @sierikov/planka-mcp, the package users run with npx:
#                          npm/cli.js, which starts the binary, plus README.md
#                          and LICENSE
#   planka-mcp-<os>-<cpu>/ @sierikov/planka-mcp-<os>-<cpu>, one per platform:
#                          the binary, LICENSE and THIRD_PARTY_NOTICES. The main
#                          package lists them all as optionalDependencies, and
#                          npm installs only the one for the machine it runs on.
#
# Usage: scripts/npm-packages.sh VERSION [DIR]
#
# VERSION has no leading "v". DIR holds the binaries, named as the release
# names them (planka-mcp-macos-arm64, planka-mcp-windows-amd64.exe, ...), with
# LICENSE and THIRD_PARTY_NOTICES next to them. It defaults to dist/, which
# scripts/release.sh fills; a directory of downloaded release assets works too
# (gh release download vVERSION -D DIR). scripts/npm-publish.sh publishes the
# result.
set -euo pipefail

VERSION=${1:?usage: scripts/npm-packages.sh VERSION [DIR]}
SRC=${2:-dist}

if [[ ! $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "npm-packages.sh: VERSION must look like 1.2.3, without a leading v: $VERSION" >&2
  exit 2
fi

# A relative DIR is relative to the caller, so resolve it before moving to the repo root.
if [[ ! -d $SRC ]]; then
  echo "npm-packages.sh: not a directory: $SRC" >&2
  exit 1
fi
SRC=$(cd "$SRC" && pwd)

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

TMPL=npm/package.json.tmpl
OUT=dist/npm

# One platform per line: Node's os and cpu, then the platform as the release
# file names spell it. A new platform also needs a line in scripts/release.sh.
PLATFORMS='darwin arm64 macos-arm64
darwin x64 macos-amd64
linux x64 linux-amd64
linux arm64 linux-arm64
win32 x64 windows-amd64
win32 arm64 windows-arm64'

# exe NODE_OS: the binary's file name on that OS.
exe() {
  if [[ $1 == win32 ]]; then echo planka-mcp.exe; else echo planka-mcp; fi
}

# Check every input before writing anything, and name each one that is missing.
missing=0
for file in "$SRC/LICENSE" "$SRC/THIRD_PARTY_NOTICES" README.md "$TMPL" npm/cli.js; do
  if [[ ! -f $file ]]; then
    echo "npm-packages.sh: not found: $file" >&2
    missing=1
  fi
done
while read -r os _ platform; do
  bin=$SRC/planka-mcp-$platform
  [[ $os == win32 ]] && bin=$bin.exe
  if [[ ! -f $bin ]]; then
    echo "npm-packages.sh: binary not found: $bin (build with make dist VERSION=$VERSION)" >&2
    missing=1
  fi
done <<<"$PLATFORMS"
if ((missing)); then
  exit 1
fi

main=$(jq -r .main.name "$TMPL")
main_dir=$OUT/planka-mcp
rm -rf "$OUT"
mkdir -p "$main_dir"

# The main package: the launcher, with every platform package as an optional dependency.
suffixes=$(awk '{print $1 "-" $2}' <<<"$PLATFORMS")
jq --arg v "$VERSION" --arg suffixes "$suffixes" '
  .main as $m
  | {name: $m.name, version: $v} + $m + .common
  | .optionalDependencies = ([$suffixes | split("\n")[] | select(. != "") | {key: "\($m.name)-\(.)", value: $v}] | from_entries)
' "$TMPL" >"$main_dir/package.json"
install -m 755 npm/cli.js "$main_dir/cli.js"
cp README.md "$SRC/LICENSE" "$main_dir/"
echo "npm-packages.sh: wrote $main_dir ($main $VERSION)" >&2

# One package per platform, holding that platform's binary.
while read -r os cpu platform; do
  name=$(exe "$os")
  bin=$SRC/planka-mcp-$platform
  [[ $os == win32 ]] && bin=$bin.exe
  dir=$main_dir-$os-$cpu
  mkdir -p "$dir/bin"
  jq --arg v "$VERSION" --arg os "$os" --arg cpu "$cpu" --arg exe "$name" '
    {name: "\(.main.name)-\($os)-\($cpu)", version: $v}
    + .platform
    + {os: [$os], cpu: [$cpu], files: ["bin/\($exe)", "LICENSE", "THIRD_PARTY_NOTICES"]}
    + .common
  ' "$TMPL" >"$dir/package.json"
  install -m 755 "$bin" "$dir/bin/$name"
  cp "$SRC/LICENSE" "$SRC/THIRD_PARTY_NOTICES" "$dir/"
  echo "npm-packages.sh: wrote $dir ($main-$os-$cpu $VERSION)" >&2
done <<<"$PLATFORMS"
