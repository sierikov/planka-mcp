#!/usr/bin/env bash
# Bootstraps a local Planka v2 (deploy/compose.yml): logs in as the default admin,
# accepts the terms if asked, creates an API key and seeds a small board.
#
# stdout: exactly two lines, PLANKA_URL=... and PLANKA_API_KEY=...
# stderr: progress.
#
# Seeding is skipped when the project "Projects" already exists, so the script can
# be re-run against a live instance (it then only issues a fresh API key). For a
# clean slate run `make planka-down planka-up` first.
set -euo pipefail

PLANKA_URL="${PLANKA_URL:-http://localhost:3333}"
ADMIN_LOGIN="${PLANKA_ADMIN_LOGIN:-admin}"
ADMIN_PASSWORD="${PLANKA_ADMIN_PASSWORD:-admin-password-123}"
GAP=65536

log() { echo "bootstrap: $*" >&2; }

AUTH_HEADER=()

# api METHOD PATH [curl args...] prints the response body; fails with the body on stderr for HTTP >= 400.
api() {
  local method=$1 path=$2
  shift 2
  local out code
  out=$(curl --max-time 10 -sS -X "$method" -w '\n%{http_code}' "${AUTH_HEADER[@]}" "$@" "$PLANKA_URL/api$path")
  code=${out##*$'\n'}
  out=${out%$'\n'*}
  if [ "$code" -ge 400 ]; then
    echo "bootstrap: $method $path -> HTTP $code: $out" >&2
    return 1
  fi
  printf '%s' "$out"
}

# json METHOD PATH JSON
json() {
  local method=$1 path=$2 body=$3
  api "$method" "$path" -H 'Content-Type: application/json' -d "$body"
}

wait_ready() {
  # GET /api/config needs auth in Planka 2.2.1; GET /api/bootstrap is public (and carries the version).
  log "waiting for Planka at $PLANKA_URL"
  for _ in $(seq 1 90); do
    if curl --max-time 10 -sf -o /dev/null "$PLANKA_URL/api/bootstrap"; then
      return 0
    fi
    sleep 2
  done
  echo "bootstrap: Planka did not become ready" >&2
  exit 1
}

login() {
  local resp token
  resp=$(curl --max-time 10 -sS -X POST "$PLANKA_URL/api/access-tokens" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --arg u "$ADMIN_LOGIN" --arg p "$ADMIN_PASSWORD" '{emailOrUsername:$u,password:$p}')")
  token=$(jq -r '.item // empty' <<<"$resp")
  if [ -z "$token" ]; then
    local step pending
    step=$(jq -r '.step // empty' <<<"$resp")
    pending=$(jq -r '.pendingToken // empty' <<<"$resp")
    if [ "$step" != "accept-terms" ] || [ -z "$pending" ]; then
      echo "bootstrap: login failed: $resp" >&2
      exit 1
    fi
    log "accepting terms"
    local signature
    signature=$(curl --max-time 10 -sS "$PLANKA_URL/api/terms" | jq -r '.item.signature')
    resp=$(curl --max-time 10 -sS -X POST "$PLANKA_URL/api/access-tokens/accept-terms" \
      -H 'Content-Type: application/json' \
      -d "$(jq -n --arg t "$pending" --arg s "$signature" '{pendingToken:$t,signature:$s}')")
    token=$(jq -r '.item // empty' <<<"$resp")
    if [ -z "$token" ]; then
      echo "bootstrap: accept-terms failed: $resp" >&2
      exit 1
    fi
  fi
  AUTH_HEADER=(-H "Authorization: Bearer $token")
}

create_api_key() {
  local me_id key
  me_id=$(api GET /users/me | jq -r '.item.id')
  key=$(api POST "/users/$me_id/api-key" | jq -r '.included.apiKey')
  AUTH_HEADER=(-H "X-Api-Key: $key")
  API_KEY=$key
  ME_ID=$me_id
}

seed() {
  local existing
  existing=$(api GET /projects | jq -r '.items[] | select(.name=="Projects") | .id' | head -n1)
  if [ -n "$existing" ]; then
    log "project \"Projects\" already exists, skipping seed"
    return 0
  fi

  log "seeding"
  local project board
  project=$(json POST /projects '{"type":"shared","name":"Projects"}' | jq -r '.item.id')
  board=$(api POST "/projects/$project/boards" -F "position=$GAP" -F 'name=Planka MCP' | jq -r '.item.id')

  local open progress ready done dropped
  open=$(json POST "/boards/$board/lists" "{\"type\":\"active\",\"position\":$GAP,\"name\":\"Open\"}" | jq -r '.item.id')
  progress=$(json POST "/boards/$board/lists" "{\"type\":\"active\",\"position\":$((GAP * 2)),\"name\":\"In progress\"}" | jq -r '.item.id')
  json POST "/boards/$board/lists" "{\"type\":\"active\",\"position\":$((GAP * 3)),\"name\":\"Ready for release\"}" >/dev/null
  json POST "/boards/$board/lists" "{\"type\":\"active\",\"position\":$((GAP * 4)),\"name\":\"Done\"}" >/dev/null
  json POST "/boards/$board/lists" "{\"type\":\"closed\",\"position\":$((GAP * 5)),\"name\":\"Dropped\"}" >/dev/null

  local feature
  feature=$(json POST "/boards/$board/labels" "{\"position\":$GAP,\"name\":\"feature\",\"color\":\"muddy-grey\"}" | jq -r '.item.id')
  json POST "/boards/$board/labels" "{\"position\":$((GAP * 2)),\"name\":\"research\",\"color\":\"muddy-grey\"}" >/dev/null

  local due readme research
  due=$(date -u -v+7d +%Y-%m-%dT12:00:00.000Z 2>/dev/null || date -u -d '+7 days' +%Y-%m-%dT12:00:00.000Z)
  readme=$(json POST "/lists/$open/cards" "$(jq -n --arg d "$due" \
    '{type:"project",position:65536,name:"Write README",description:"Document how to install and configure planka-mcp.",dueDate:$d}')" | jq -r '.item.id')
  research=$(json POST "/lists/$progress/cards" \
    '{"type":"project","position":65536,"name":"Research search API"}' | jq -r '.item.id')

  json POST "/cards/$readme/card-labels" "{\"labelId\":\"$feature\"}" >/dev/null
  json POST "/cards/$readme/card-memberships" "{\"userId\":\"$ME_ID\"}" >/dev/null

  local tasklist
  tasklist=$(json POST "/cards/$readme/task-lists" "{\"position\":$GAP,\"name\":\"Tasks\"}" | jq -r '.item.id')
  json POST "/task-lists/$tasklist/tasks" "{\"position\":$GAP,\"name\":\"Draft the install section\",\"isCompleted\":true}" >/dev/null
  json POST "/task-lists/$tasklist/tasks" "{\"position\":$((GAP * 2)),\"name\":\"Add client setup snippets\"}" >/dev/null
  json POST "/task-lists/$tasklist/tasks" "{\"position\":$((GAP * 3)),\"linkedCardId\":\"$research\"}" >/dev/null

  json POST "/cards/$research/comments" '{"text":"Planka has a card search endpoint on lists."}' >/dev/null
  json POST "/cards/$research/comments" '{"text":"Board-level search is client side for now."}' >/dev/null

  local archive_list old_card
  archive_list=$(api GET "/boards/$board" | jq -r '.included.lists[] | select(.type=="archive") | .id')
  old_card=$(json POST "/lists/$open/cards" '{"type":"project","position":131072,"name":"Old archived task"}' | jq -r '.item.id')
  json PATCH "/cards/$old_card" "{\"listId\":\"$archive_list\",\"position\":$GAP}" >/dev/null

  log "seeded project $project, board $board"
}

wait_ready
login
create_api_key
seed

echo "PLANKA_URL=$PLANKA_URL"
echo "PLANKA_API_KEY=$API_KEY"
