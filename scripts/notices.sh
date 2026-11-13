#!/usr/bin/env bash
# Writes the license texts of the code compiled into planka-mcp, other than
# planka-mcp's own, to FILE: the Go standard library, then each module. Their
# licenses (BSD, MIT, Apache-2.0) ask that these notices go with every copy of
# the binary, so release.sh ships the file next to the binaries and in every
# bundle, and the Dockerfile puts it in the image.
#
# Usage: scripts/notices.sh FILE
set -euo pipefail

OUT=${1:?usage: scripts/notices.sh FILE}

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

# modules: "path version dir" for each module compiled into the binary on any
# release platform.
modules() {
  local goos
  for goos in darwin linux windows; do
    GOOS=$goos go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/planka-mcp
  done | sort -u
}

# section TITLE DIR: TITLE as a heading, then the license and notice files at
# the top of DIR. A module without one stops the script: its terms are unknown.
section() {
  local files
  files=$(find "$2" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) | sort)
  if [[ -z $files ]]; then
    echo "notices.sh: no license file for $1 in $2" >&2
    exit 1
  fi
  printf '\n==== %s ====\n\n' "$1"
  while IFS= read -r file; do
    cat "$file"
  done <<<"$files"
}

{
  echo "planka-mcp includes the code below, under the licenses that follow it."
  section "The Go standard library and runtime ($(go env GOVERSION))" "$(go env GOROOT)"
  while read -r path version dir; do
    section "$path $version" "$dir"
  done < <(modules)
} >"$OUT"
