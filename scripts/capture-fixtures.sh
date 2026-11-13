#!/usr/bin/env bash
# Captures raw Planka v2 responses into internal/planka/testdata/*.json.
# Requires a running Planka (make planka-up). Runs the bootstrap first (seed + API key).
# Write responses come from performing each write once on a scratch card, which
# (together with the scratch label) is deleted again at the end.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/internal/planka/testdata"
mkdir -p "$OUT"

ENV_LINES=$("$ROOT/scripts/planka-bootstrap.sh")
PLANKA_URL=$(sed -n 's/^PLANKA_URL=//p' <<<"$ENV_LINES")
PLANKA_API_KEY=$(sed -n 's/^PLANKA_API_KEY=//p' <<<"$ENV_LINES")

# get PATH FILE: GET /api/PATH, saves the raw body, fails on HTTP >= 400.
# send METHOD PATH FILE JSON: same for writes. Both print the saved file's path.
save() {
  local method=$1 path=$2 file=$3
  shift 3
  local code
  code=$(curl -sS -X "$method" -o "$OUT/$file" -w '%{http_code}' \
    -H "X-Api-Key: $PLANKA_API_KEY" "$@" "$PLANKA_URL/api$path")
  if [ "$code" -ge 400 ]; then
    echo "capture: $method $path -> HTTP $code: $(cat "$OUT/$file")" >&2
    exit 1
  fi
  echo "captured $file ($method $path -> $code)" >&2
}

get() { save GET "$1" "$2"; }
send() { save "$1" "$2" "$3" -H 'Content-Type: application/json' -d "$4"; }

# id_of FILE JQ-FILTER
id_of() { jq -r "$2" "$OUT/$1"; }

get /users/me users_me.json
get /projects projects.json

BOARD_ID=$(jq -r '.included.boards[] | select(.name=="Planka MCP") | .id' "$OUT/projects.json")
get "/boards/$BOARD_ID" board.json

OPEN_LIST=$(id_of board.json '.included.lists[] | select(.name=="Open") | .id')
ARCHIVE_LIST=$(id_of board.json '.included.lists[] | select(.type=="archive") | .id')
README_CARD=$(id_of board.json '.included.cards[] | select(.name=="Write README") | .id')
RESEARCH_CARD=$(id_of board.json '.included.cards[] | select(.name=="Research search API") | .id')
ME_ID=$(id_of users_me.json '.item.id')

get "/cards/$README_CARD" card.json
get "/cards/$RESEARCH_CARD/comments" comments.json
get "/lists/$ARCHIVE_LIST/cards" list_cards_archive.json

# error_404.json: a card id that cannot exist.
code=$(curl -sS -o "$OUT/error_404.json" -w '%{http_code}' -H "X-Api-Key: $PLANKA_API_KEY" "$PLANKA_URL/api/cards/1")
if [ "$code" != 404 ]; then
  echo "capture: GET /api/cards/1 -> HTTP $code, expected 404" >&2
  exit 1
fi
echo "captured error_404.json (GET /cards/1 -> 404)" >&2

# Writes, once each, on a scratch card.
send POST "/lists/$OPEN_LIST/cards" card_created.json \
  '{"type":"project","position":524288,"name":"Scratch card","description":"scratch"}'
SCRATCH=$(id_of card_created.json '.item.id')

send PATCH "/cards/$SCRATCH" card_updated.json \
  '{"name":"Scratch card renamed","description":"updated","dueDate":"2026-12-31T12:00:00.000Z"}'

send POST "/boards/$BOARD_ID/labels" label_created.json \
  '{"position":524288,"name":"scratch-label","color":"muddy-grey"}'
SCRATCH_LABEL=$(id_of label_created.json '.item.id')

send POST "/cards/$SCRATCH/card-labels" card_label_created.json "{\"labelId\":\"$SCRATCH_LABEL\"}"
send POST "/cards/$SCRATCH/card-memberships" card_membership_created.json "{\"userId\":\"$ME_ID\"}"
send POST "/cards/$SCRATCH/comments" comment_created.json '{"text":"scratch comment"}'

send POST "/cards/$SCRATCH/task-lists" task_list_created.json '{"position":65536,"name":"Scratch tasks"}'
SCRATCH_TASK_LIST=$(id_of task_list_created.json '.item.id')

send POST "/task-lists/$SCRATCH_TASK_LIST/tasks" task_created.json '{"position":65536,"name":"Scratch task"}'
SCRATCH_TASK=$(id_of task_created.json '.item.id')

send PATCH "/tasks/$SCRATCH_TASK" task_updated.json '{"isCompleted":true}'

# Clean up so the seeded board is unchanged and the script can be re-run.
curl -sS -f -o /dev/null -X DELETE -H "X-Api-Key: $PLANKA_API_KEY" "$PLANKA_URL/api/cards/$SCRATCH"
curl -sS -f -o /dev/null -X DELETE -H "X-Api-Key: $PLANKA_API_KEY" "$PLANKA_URL/api/labels/$SCRATCH_LABEL"

for f in "$OUT"/*.json; do
  jq . "$f" >/dev/null || { echo "capture: invalid JSON in $f" >&2; exit 1; }
done
echo "captured fixtures into $OUT" >&2
