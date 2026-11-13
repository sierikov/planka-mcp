# planka-mcp

[![Go](https://github.com/sierikov/planka-mcp/actions/workflows/go.yml/badge.svg)](https://github.com/sierikov/planka-mcp/actions/workflows/go.yml)

A [Model Context Protocol](https://modelcontextprotocol.io) server for [Planka](https://planka.app) kanban boards. It lets Claude (or any MCP client) find, read, edit, comment on and move your cards without you opening Planka.

> **Unofficial.** planka-mcp is a community project, not affiliated with or endorsed by PLANKA Software GmbH. PLANKA is their trademark.

> **Requires Planka v2.0 or later.** Planka v1 has no per-user API keys and is not supported. The end-to-end tests run against Planka 2.2.1.

MCP Registry name: `io.github.sierikov/planka-mcp`

## Why

You talk about a ticket in plain words and the assistant does the rest: it finds the card, reads all of it, does the work, writes back and moves the card along. Every request runs as your own Planka user (through your API key, or your username and password in stdio mode), so Planka's own permissions apply and comments appear under your name.

Two flows it is built for:

1. *"Here's this ticket: the card about the backup script. Research it and fill it in with the details we need."*
   The assistant finds the card from your description, reads it in one call, then writes back a better description, checklist items, a due date and labels, plus a comment with its findings.
2. *"Let's work on the backup script card."*
   The assistant moves the card to *In progress*, does the work, comments with a summary, and moves the card on (for example to *Ready for release* or *Done*).

What makes this server different from the other Planka MCP servers:

- Boards, lists, labels and people are given **by name**, so you never have to look up an ID; they also accept IDs, and boards accept links. Cards can be given by ID, URL or by text ("the card about the backup script"). When a name or a text matches more than one thing (say, two lists called "Done"), the error lists the candidates with their IDs instead of guessing, and the assistant retries with the ID it means.
- One call per intent and compact JSON output, so it is cheap in tokens and round trips.
- **Per-user keys over HTTP:** a self-hosted instance serves several people, each with their own Planka API key.
- A single Go binary, `.mcpb` bundles for Claude Desktop and a multi-arch Docker image.

## Tools

15 tools, all enabled by default. Reads are annotated read-only; creates and adds (`create_card`, `add_comment`, `add_tasks`) are marked non-destructive; updates are marked idempotent; deletes are marked destructive. Clients may still prompt for updates.

| Tool | What it does | Kind |
|---|---|---|
| `list_boards` | List the projects you can see with their boards, optionally filtered by name | read |
| `get_board` | Show a board as a kanban: lists in order, compact card lines, labels and members (no descriptions); the first 10 cards of each list (3 of a closed one), up to 100 with `list` | read |
| `search_cards` | Find cards across boards by words and filters (board, project, list, label, assignee, archived) | read |
| `get_card` | Everything about one card: description (verbatim markdown), labels, assignees, due date, stopwatch, task lists, attachments, custom fields, latest comments | read |
| `list_comments` | Page through a card's older comments, newest first | read |
| `create_card` | Create a card with labels, assignees, a due date and a position; a missing label is created only with `create_labels` | write |
| `move_card` | Move a card to another list or board, or to the archive with `list=archive` | write (idempotent) |
| `update_card` | Edit title, description, due date, labels, assignees or stopwatch; fields left out stay as they are | write (idempotent) |
| `add_comment` | Comment on a card | write |
| `update_comment` | Edit the text of a comment | write (idempotent) |
| `add_tasks` | Append tasks, or links to other cards, to a checklist (created if missing); many per call | write |
| `update_tasks` | Tick, untick or rename tasks, several in one call | write (idempotent) |
| `delete_card` | Delete a card. **Permanent**: it does not go to Planka's trash; use `move_card` with `list=archive` to keep it | destructive |
| `delete_comment` | Delete a comment | destructive |
| `delete_task` | Delete a checklist task | destructive |

Your MCP client decides how to confirm writes. The split lets you auto-allow `move_card` and `add_comment` and still be asked before edits and deletes.

Dates are `YYYY-MM-DD`, or `YYYY-MM-DD HH:MM` when the time of day matters, in the server's time zone (set `TZ` to change it; the Docker image embeds the time zone database).

## Signing in: API key or username and password

Every call runs as one Planka user, chosen in one of two ways:

- **An API key (preferred).** It works in both stdio and HTTP mode, and you can revoke it without changing your password.
- **Your username (or email) and password**, in stdio mode only, for when you cannot get a key.

### Getting an API key

Each person needs their own key for their own Planka user. In Planka 2.1 and 2.2, **creating an API key is admin-only**: the server rejects the request from a non-admin user. So:

- If you are not a Planka admin, **ask your admin to generate an API key for your user** and send it to you over a private channel. Or use your username and password (below).
- If you are an admin, you can generate your own the same way.

Where the admin finds it (checked against Planka's source):

- **Planka 2.2:** user menu → **Administration** → **Users** tab → click the user's name → **API Key** tab → **Create API key**.
- **Planka 2.1:** user menu → **Administration** → **Users** → the **⋯** actions of the user's row → **API Key** → **Create API key**.

Copy the key right away: Planka shows it only once. Generating a new key later replaces the old one, which stops working. Treat the key like a password. It can do whatever its user can do in Planka.

### Username and password (stdio only)

Set `PLANKA_USERNAME` (your username or email) and `PLANKA_PASSWORD` instead of `PLANKA_API_KEY`. If all three are set, the username and password win, and the server logs that the key is ignored.

- **Session handling:** the server logs in on the first tool call and keeps the session token in memory only. When Planka answers 401 (an expired or revoked session) it logs in again and repeats that call once. It logs out when it stops, so sessions do not pile up in your Planka account.
- **A wrong password is tried once.** After Planka rejects the username or password, the server stops sending them and reports the same error until it is restarted with the right ones, so it cannot lock your account with repeated failed logins.
- **Accounts it cannot use:** single sign-on (OIDC) accounts and accounts with two-factor authentication. Those need an API key. If Planka asks your account to accept its terms first, log in once in the browser.
- **Your password sits in your MCP client's configuration in plain text**, so prefer a key when you can get one.
- **Not in HTTP mode:** the shared server refuses a username and password, as it refuses a key, because an exposed port must never act as one person.

## Setup

Pick the setup that fits. Stdio runs on your own machine with your own key or login; HTTP is a server you (or your team) host, where each client sends its own key.

<details>
<summary>Claude Code (stdio)</summary>

Download the binary for your platform from the [latest release](https://github.com/sierikov/planka-mcp/releases/latest) (`planka-mcp-macos-arm64`, `planka-mcp-macos-amd64`, `planka-mcp-linux-amd64`, `planka-mcp-linux-arm64`, `planka-mcp-windows-amd64.exe`, `planka-mcp-windows-arm64.exe`), make it executable, then (on macOS a downloaded, unsigned binary may be quarantined: run `xattr -d com.apple.quarantine ./planka-mcp` if it will not start):

```bash
claude mcp add planka \
  -e PLANKA_URL=https://planka.example.com \
  -e PLANKA_API_KEY=your-api-key \
  -- /path/to/planka-mcp
```

Without a key, use your username and password instead:

```bash
claude mcp add planka \
  -e PLANKA_URL=https://planka.example.com \
  -e PLANKA_USERNAME=your-username \
  -e PLANKA_PASSWORD='your-password' \
  -- /path/to/planka-mcp
```

Use the `https://` address. Over `http://` the password travels unencrypted with the first request, before any redirect could help, so the server warns at startup when it would send a password to an `http://` address on another machine. If `http://` redirects, the server reports the address to use instead of following it.

Or build it yourself with Go 1.26: `go install github.com/sierikov/planka-mcp/cmd/planka-mcp@latest` (or `@v0.1.0` for a specific release; the binary reports that module version).

</details>

<details>
<summary>Claude Code (HTTP)</summary>

Run the server somewhere your client can reach (see the Docker setup below), then add it with your own key in the `Authorization` header:

```bash
claude mcp add --transport http planka https://host/mcp \
  --header "Authorization: Bearer <your-api-key>"
```

The MCP endpoint is `/mcp`. Each person adds the server with their own key. Put TLS in front of it (see Security).

</details>

<details>
<summary>Claude Desktop (.mcpb)</summary>

Download the bundle for your platform from the [latest release](https://github.com/sierikov/planka-mcp/releases/latest), open it (on macOS, if Gatekeeper blocks the bundled binary, it is quarantined as an unsigned download: see the `xattr` note in the Claude Code (stdio) setup), and Claude Desktop asks for your Planka URL and either your API key or your username and password (the key and password fields are marked sensitive).

| Platform | File |
|---|---|
| macOS (Apple Silicon) | `planka-mcp-macos-arm64.mcpb` |
| macOS (Intel) | `planka-mcp-macos-amd64.mcpb` |
| Windows (x64) | `planka-mcp-windows-amd64.mcpb` |
| Windows (arm64) | `planka-mcp-windows-arm64.mcpb` |
| Linux (x64) | `planka-mcp-linux-amd64.mcpb` |
| Linux (arm64) | `planka-mcp-linux-arm64.mcpb` |

The `.mcpb` format is for Claude Desktop. Other clients use the stdio or HTTP setups above.

</details>

<details>
<summary>Other clients (mcpServers JSON)</summary>

Most MCP clients (Cursor, Windsurf, VS Code, Claude Desktop without the bundle and others) take an `mcpServers` JSON block. For stdio, point it at the binary and pass your URL and key in `env`:

```json
{
  "mcpServers": {
    "planka": {
      "command": "/path/to/planka-mcp",
      "env": {
        "PLANKA_URL": "https://planka.example.com",
        "PLANKA_API_KEY": "your-api-key"
      }
    }
  }
}
```

For a self-hosted HTTP server, give the `/mcp` URL and your own key in the `Authorization` header:

```json
{
  "mcpServers": {
    "planka": {
      "type": "http",
      "url": "https://host/mcp",
      "headers": {
        "Authorization": "Bearer <your-api-key>"
      }
    }
  }
}
```

Key names differ a little between clients (some want `"type": "streamable-http"`, or `serverUrl` instead of `url`): check your client's MCP documentation.

</details>

<details>
<summary>Docker Compose, next to Planka (HTTP)</summary>

Add the server as another service in the compose file that runs Planka:

```yaml
services:
  planka:
    image: ghcr.io/plankanban/planka:2.2.1
    # ... your existing Planka service ...

  planka-mcp:
    image: ghcr.io/sierikov/planka-mcp:latest
    restart: unless-stopped
    depends_on:
      - planka
    environment:
      PLANKA_URL: http://planka:1337   # Planka as seen from this container
      # Do NOT set PLANKA_API_KEY here: in HTTP mode the server refuses to start with it.
      # TZ: Europe/Berlin              # optional, the time zone for dates
    ports:
      - "8080:8080"                    # or publish it only through your reverse proxy
```

The image runs the HTTP transport by default and serves `/mcp` and `GET /healthz` on port 8080. It is a distroless, non-root image for linux/amd64 and linux/arm64. Put a reverse proxy with TLS in front of it, then connect clients with the Claude Code (HTTP) setup above.

Without compose:

```bash
docker run -d -p 8080:8080 -e PLANKA_URL=https://planka.example.com ghcr.io/sierikov/planka-mcp:latest
```

</details>

## Configuration

Flags override environment variables.

| Variable | Flag | Stdio | HTTP | Default | Description |
|---|---|---|---|---|---|
| `PLANKA_URL` | `-url` | required | required | none | Base URL of your Planka v2 instance |
| `PLANKA_API_KEY` | none | this or a login | **must not be set** | none | Your personal Planka API key. In HTTP mode the server refuses to start if it is set; each client sends its own key |
| `PLANKA_USERNAME` | none | with `PLANKA_PASSWORD` | **must not be set** | none | Your Planka username or email, for logging in without an API key. Wins over `PLANKA_API_KEY` when both are set |
| `PLANKA_PASSWORD` | none | with `PLANKA_USERNAME` | **must not be set** | none | Your Planka password. A trailing newline is dropped; other spaces are kept |
| `PLANKA_MCP_TRANSPORT` | `-transport` | `stdio` | `http` | `stdio` (the Docker image sets `http`) | `stdio` or `http` |
| `PLANKA_MCP_ADDR` | `-addr` | not used | optional | `:8080` | Listen address in HTTP mode |
| `TZ` | none | optional | optional | system time zone | Time zone used for dates in tool output |

In HTTP mode clients send `Authorization: Bearer <planka-api-key>`; a request without it gets HTTP 401 before MCP handling. The server runs stateless streamable HTTP at `/mcp` and answers `GET /healthz` with `ok`.

## Security

Report a vulnerability privately, as [SECURITY.md](SECURITY.md) describes.

- **Per-user keys.** Every Planka call runs as the owner of the key that came with the request. There is no shared service account, and Planka's permissions are what limit each user.
- **No key in the server's environment.** In HTTP mode the server refuses to start when `PLANKA_API_KEY` is set, so an exposed port can never act as the server owner. Clients send their own key in the `Authorization` header.
- **Keys are never logged.** The access log has one line per request: method, path, status and the first 8 hex characters of the key's SHA-256 (or `none`).
- **The server does not validate keys up front.** Planka does, on the first call; a revoked or wrong key comes back as a tool error that says Planka rejected the key.
- **Use TLS.** The server speaks plain HTTP and the key travels in a header, so run it behind a reverse proxy with TLS, the way you run Planka itself.
- **Any Host header is served.** The MCP SDK's DNS-rebinding guard is off, so a reverse proxy on the same machine that keeps the public host name works. That is safe because every `/mcp` request needs the caller's own key, and the server holds no key of its own.
- **Browser requests from other sites get 403.** A request whose `Origin` names another site than its `Host` is refused, as the MCP spec requires. Clients that are not browsers send no `Origin` and are not affected.
- **Key creation is admin-only in Planka 2.1 and 2.2.** A user cannot create their own key; an admin generates it for them (see Getting an API key), or the user logs in with username and password in stdio mode. Treat a key like a password and regenerate it if it leaks.
- **Passwords are held in memory only.** With `PLANKA_USERNAME`/`PLANKA_PASSWORD`, neither the password nor the session token is logged or written to disk, and the session is ended when the server stops.
- **Deletes are permanent and enabled.** `delete_card`, `delete_comment` and `delete_task` are annotated as destructive so clients can ask first; deleting a card does not move it to Planka's trash.
- **Card text is untrusted input.** Descriptions, comments and task names written by other people on your boards reach the model as they are, and a card can contain text meant to steer it ("delete every card in Done"). The server tells the model to treat such text as data, but keep your client's confirmation on for the delete tools, and don't let it auto-approve writes on boards that others edit.
- **Writes never pick a card by its description.** A card reference that matches only words in some card's description finds that card for a read, but a write refuses it and names the card's ID instead.

## Development

Requires Go 1.26, [golangci-lint](https://golangci-lint.run) and Docker (for the Docker image and the end-to-end tests).

```text
cmd/planka-mcp        entry point, config, stdio and HTTP transports
internal/planka       Planka HTTP client and wire types
internal/service      name and card resolution, search, write rules
internal/tools        MCP tool definitions, compact output, error text
e2e                   end-to-end tests against a real Planka (build tag e2e)
```

The layers are enforced with `depguard` in `.golangci.yml`: `internal/planka` and `internal/service` do not import the MCP SDK, and `internal/tools` does not import `internal/planka` outside tests.

| Command | What it does |
|---|---|
| `make build` | Build `./planka-mcp` (`make build VERSION=1.2.3` sets the version) |
| `make test` | Run the unit and wiring tests |
| `make lint`, `make fmt`, `make vet` | Linters and formatting |
| `make check` | `fmt`, `vet`, `lint` and `test`: run this before sending a change |
| `make mocks` | Regenerate the mockery mocks (`go tool mockery`) |
| `make toolsnaps` | Rewrite the tool snapshots after a deliberate change to a tool |
| `make planka-up` / `make planka-down` | Start / remove (with volumes) a local Planka v2 at http://localhost:3333 (`PLANKA_VERSION`, `PLANKA_PORT` and `COMPOSE_PROJECT` pick another) |
| `make e2e` | Run the end-to-end tests against that Planka |
| `make fixtures` | Re-capture the recorded Planka responses (see below) |
| `make mcpb` | Build `planka-mcp.mcpb` for the current platform |
| `make docker` | Build the image as `planka-mcp:dev` |
| `make clean` | Remove build outputs |

**End-to-end tests.** `make planka-up` starts Planka 2.2.1 and Postgres from `deploy/compose.yml` (throwaway credentials, localhost only). `make e2e` builds the real binary, bootstraps an API key through Planka's default admin and runs it against that Planka:

- both flows from the top of this README: filling in a card found by its words (description, a new label, a due date with a time, "me" as assignee, a running stopwatch, a checklist with a task and a linked card), and moving a card through the lists with a comment;
- creating a card with labels, a due date and an assignee, then ticking and deleting its tasks, editing and deleting its comments, paging the rest, and clearing the due date;
- moving a card to another board, and two lists with the same name (the ambiguity error and the retry by ID);
- archiving (searches see the card only with `include_archived`), then deleting for good;
- custom fields from a board's group and from a project's base group, read back by name;
- a label name the board lacks is refused unless `create_labels` is set;
- a task that links a card cannot be ticked or renamed, and shows the linked card's current title;
- two users with different keys over HTTP.

CI runs them against Planka 2.1.1 and 2.2.1. To run them locally against another version, start it as its own stack next to the default one, then point `make e2e` at its port:

```bash
make planka-up PLANKA_VERSION=2.1.1 PLANKA_PORT=3334 COMPOSE_PROJECT=planka-mcp-2-1
make e2e PLANKA_PORT=3334
```

The server under test runs in a fixed time zone that is not UTC, and the tests check due dates against what Planka itself stored. Without a reachable Planka the tests skip, unless `CI=true`, where they fail. To point them at another instance, set `PLANKA_E2E_URL` and `PLANKA_E2E_API_KEY` (an admin's key; without it they bootstrap one). They never read `PLANKA_URL` or `PLANKA_API_KEY`, so a shell set up for your real Planka cannot send them there.

**Fixtures** in `internal/planka/testdata/` are a frozen snapshot of real Planka 2.2.1 responses. Refresh them only deliberately with `make fixtures`, against a fresh instance (`make planka-down planka-up` first). It re-seeds Planka and changes every ID, so the tests that refer to fixture IDs have to be updated with it. See `internal/planka/testdata/README.md`.

**Tool snapshots.** `cmd/planka-mcp/__toolsnaps__/*.snap` hold the schema of every tool as the model sees it, and a test fails when a tool changes. After a deliberate change run `make toolsnaps` and review the diff. `manifest.json` lists the tools too, and `TestManifestListsEveryTool` fails when that list and the registered tools differ.

**Mocks.** The service and tools tests use mockery v3 mocks, generated into the consuming package as `mocks_test.go` by `make mocks` (configured in `.mockery.yml`).

**Releasing.** Push a tag such as `v0.1.0`. The release workflow runs the tests, builds the binaries and `.mcpb` bundles for macOS, Linux and Windows (amd64 and arm64) with `scripts/release.sh`, pushes the multi-arch image to `ghcr.io/sierikov/planka-mcp`, then creates the GitHub release (with `checksums.txt`, `LICENSE` and the third-party license notices, which every bundle carries too) and publishes `server.json` to the MCP Registry. Each step runs as its own job with only the permissions it needs, every action is pinned to a commit, the image is pushed only after the tests pass, and the `mcp-publisher` download is checked against its published SHA-256. The version comes from the tag alone: it goes into the binaries, the manifest inside each bundle, `server.json` and the image tag, while the committed `manifest.json` and `server.json` keep the placeholder `0.0.0-dev`. `make dist VERSION=0.1.0` builds the same files into `dist/` locally. Before anything is published, the build job checks `server.json` with the registry's own validator, so a file the registry would refuse stops the release. Publishing to the MCP Registry itself is best-effort (`continue-on-error`), so a failure there does not fail the release: check the run log. Only a failed `mcp-publisher` download or checksum turns that last job red, after the release is already out. After the **first** release, make the `ghcr.io/sierikov/planka-mcp` package public in GitHub's package settings, or others cannot pull the image and the registry cannot read it.

## Roadmap

Ideas, in no particular order and without dates:

- OAuth, so that claude.ai, ChatGPT and their phone apps can use a hosted instance with each person's own Planka login.
- Users who sign in to Planka through SSO (Pocket ID and other OIDC providers); until then they use an API key.
- Check the Planka version on `/api/bootstrap` and fail clearly on Planka v1.
- Verified writes and batch operations that report each item's result.
- Opt-in toolsets, and MCP prompts for the two flows above.

## License

MIT
