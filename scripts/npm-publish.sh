#!/usr/bin/env bash
# Publishes the npm packages that scripts/npm-packages.sh wrote to dist/npm/.
#
# Usage: scripts/npm-publish.sh VERSION TAG [--dry-run]
#
# VERSION has no leading "v", as the packages hold it. TAG is the npm
# dist-tag: latest for a release, next for a pre-release (npm refuses latest
# for a pre-release version). With --dry-run npm goes through every step but
# the upload.
#
# The platform packages go first and the main package last, because the main
# package lists them as optional dependencies. A package whose NAME@VERSION is
# already on npm is skipped, so a run after a partial or a hand-made publish
# carries on where it stopped. In the release workflow npm signs in through
# trusted publishing (OIDC); by hand, run npm login first.
set -euo pipefail

VERSION=${1:?usage: scripts/npm-publish.sh VERSION TAG [--dry-run]}
TAG=${2:?usage: scripts/npm-publish.sh VERSION TAG [--dry-run]}
DRY_RUN=${3:-}

if [[ -n $DRY_RUN && $DRY_RUN != --dry-run ]]; then
  echo "usage: scripts/npm-publish.sh VERSION TAG [--dry-run]" >&2
  exit 2
fi

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

MAIN=dist/npm/planka-mcp

# Check every package before publishing any, so a stale or missing dist/npm/
# publishes nothing.
dirs=("$MAIN"-* "$MAIN")
for dir in "${dirs[@]}"; do
  if [[ ! -f $dir/package.json ]]; then
    echo "npm-publish.sh: no package in $dir: run scripts/npm-packages.sh $VERSION first" >&2
    exit 1
  fi
  have=$(jq -r .version "$dir/package.json")
  if [[ $have != "$VERSION" ]]; then
    echo "npm-publish.sh: $dir holds version $have, not $VERSION" >&2
    exit 1
  fi
done

for dir in "${dirs[@]}"; do
  name=$(jq -r .name "$dir/package.json")
  if [[ $(npm view "$name@$VERSION" version 2>/dev/null) == "$VERSION" ]]; then
    echo "npm-publish.sh: $name@$VERSION is already on npm, skipping" >&2
    continue
  fi
  (cd "$dir" && npm publish --access public --tag "$TAG" ${DRY_RUN:+"$DRY_RUN"} >&2)
done
