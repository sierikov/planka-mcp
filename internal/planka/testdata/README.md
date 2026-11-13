# Planka fixtures

Raw JSON responses captured from a real **Planka v2.2.1** (`ghcr.io/plankanban/planka:2.2.1`,
`GET /api/bootstrap` reports `"version":"2.2.1"`). Do not edit them by hand.

Regenerate against a fresh instance:

```sh
make planka-down   # removes the volumes
make planka-up     # Planka at http://localhost:3333
make fixtures      # scripts/capture-fixtures.sh: seeds via scripts/planka-bootstrap.sh, then captures
```

IDs and timestamps change on every capture; tests must not depend on specific values.

## Files

| File | Request |
|---|---|
| `users_me.json` | `GET /api/users/me` |
| `projects.json` | `GET /api/projects` |
| `board.json` | `GET /api/boards/:id` (7 lists: 5 created + `archive` + `trash`; archived cards are not included) |
| `card.json` | `GET /api/cards/:id` ("Write README": label, due date, member, task list with 3 tasks, one linked to another card) |
| `comments.json` | `GET /api/cards/:id/comments` ("Research search API", 2 comments) |
| `list_cards_archive.json` | `GET /api/lists/:archiveListId/cards` |
| `card_created.json` | `POST /api/lists/:listId/cards` |
| `card_updated.json` | `PATCH /api/cards/:id` |
| `label_created.json` | `POST /api/boards/:id/labels` |
| `card_label_created.json` | `POST /api/cards/:id/card-labels` |
| `card_membership_created.json` | `POST /api/cards/:id/card-memberships` |
| `comment_created.json` | `POST /api/cards/:id/comments` |
| `task_list_created.json` | `POST /api/cards/:id/task-lists` |
| `task_created.json` | `POST /api/task-lists/:id/tasks` |
| `task_updated.json` | `PATCH /api/tasks/:id` |
| `error_404.json` | `GET /api/cards/1` -> HTTP 404 `{"code":"E_NOT_FOUND","message":"Card not found"}` |

The write fixtures come from performing each write once on a scratch card (and a scratch label), which the
capture script deletes again.

Seed (see `scripts/planka-bootstrap.sh`): project "Projects"; board "Planka MCP" with active lists Open,
In progress, Ready for release, Done and closed list Dropped; labels `feature`, `research`; cards "Write README"
(Open), "Research search API" (In progress), "Old archived task" (archive list).

## Planka facts verified against v2.2.1

Verified in the Planka source in the container (`/app`) and against the live instance.

| Fact | Result |
|---|---|
| `POSITION_GAP = 65536` | Confirmed (`/app/constants.js`). Lists, cards and tasks created with positions that are multiples of it. |
| `GET /api/users/me` is the current user | Confirmed (`users_me.json`; `{"item": {...}, "included": {...}}`). |
| `DELETE /api/cards/:id` is permanent | Confirmed: the card is gone (`GET` returns 404) and the board's `trash` list stays empty. |
| List types `active`, `closed`, `archive`, `trash`; only `active`/`closed` are kanban lists | Confirmed (`/app/api/models/List.js`). Every board gets an `archive` and a `trash` list automatically; they have `name: null` and `position: null`. `POST /api/boards/:id/lists` accepts only `active` and `closed`. |
| Default label colour is `muddy-grey` | **Needs correction.** There is no server default: `color` is **required** on `POST /api/boards/:id/labels` (omitting it gives `E_MISSING_OR_INVALID_PARAMS`). `muddy-grey` is a valid colour; the client must send it explicitly when it auto-creates labels. |
| Auth via `X-Api-Key` | Confirmed. A missing or wrong key gives HTTP 401 `{"code":"E_UNAUTHORIZED","message":"Access token is missing, invalid or expired"}`. |
| API key via `POST /api/users/:id/api-key` | Confirmed. The key is returned once in `included.apiKey`; the user only keeps an `apiKeyPrefix`. Calling it again replaces the key. |
| Login via `POST /api/access-tokens` with `{emailOrUsername, password}` | Confirmed. Success returns `{"item": "<token>"}`. |

## Other API facts later tasks rely on

- **Terms acceptance.** The default admin must accept the terms on first login. `POST /api/access-tokens`
  answers HTTP 403 `{"code":"E_FORBIDDEN","step":"accept-terms","pendingToken":"...","message":"Terms acceptance required"}`.
  Fetch `GET /api/terms` (public) -> `item.signature` (64 hex chars), then
  `POST /api/access-tokens/accept-terms` with `{pendingToken, signature}` -> `{"item": "<access token>"}`.
- **`GET /api/config` needs authentication** (HTTP 401 without a token) and carries no version. The version
  is in the public `GET /api/bootstrap` -> `{"item": {"version": "2.2.1", "termsLanguages": [...], ...}}`.
  `GET /api/terms` is public too. Use `/api/bootstrap` for readiness and version checks.
- **Board creation is `multipart/form-data`** (`position`, `name`), not JSON. Most other endpoints take JSON.
- **Response envelopes:** single objects are `{"item": ...}`, collections `{"items": [...]}`, both with an
  optional `included` object of sideloaded records (`users`, `lists`, `cards`, `labels`, `cardLabels`,
  `cardMemberships`, `taskLists`, `tasks`, ...). Errors are `{"code": "...", "message": "..."}` (validation errors
  also carry `problems`).
- **IDs are strings** (large integers). `GET /api/boards/:id` returns lists, cards, labels, card-labels,
  card-memberships, task lists and tasks in `included`, but not comments and not archived/trash cards.
  Cards carry no label or member names; join through `cardLabels`/`cardMemberships`.
- **Archived cards** live in the board's `archive` list and are read with `GET /api/lists/:archiveListId/cards`
  (the endpoint also works for `active` lists and returns their cards, paginated; the OpenAPI doc calls it
  "endless lists" only). Moving a card there is `PATCH /api/cards/:id` with `{"listId": <archive list id>, "position": 65536}`;
  afterwards the card has `position: null` and `prevListId` set.
- **Moving a card needs a position:** `PATCH /api/cards/:id` with only `listId` is HTTP 422
  `{"code":"E_UNPROCESSABLE_ENTITY","message":"Position must be present"}`.
- **Due dates** are ISO 8601 date-times (`2026-12-31T12:00:00.000Z`). `PATCH` with `"dueDate": null` clears it;
  `"dueDate": ""` is rejected (HTTP 400, `E_MISSING_OR_INVALID_PARAMS`), so an empty string from the MCP layer
  must be translated to `null`.
- **Cards** have `type` `project` or `story`. Create requires `type` and `name` (pass a `position` as well).
- **Comments** from `GET /api/cards/:id/comments` come newest first. Card responses carry only `commentsTotal`.
- **Tasks** can be plain (`name`) or linked (`linkedCardId`). A linked task comes back with `name` already set to
  the linked card's name (`card.json`, task "Research search API"). Task lists have `showOnFrontOfCard` and `hideCompletedTasks`.
- **Create responses** contain only `{"item": ...}` (no `included`).
