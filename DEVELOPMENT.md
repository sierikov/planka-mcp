# Developing planka-mcp

How to build, test and release planka-mcp. To install and use it, see the [README](README.md).

## Requirements

Go 1.26 (go.mod pins the patch release with its `toolchain` line), [golangci-lint](https://golangci-lint.run) v2.12.2 (the version CI runs), `goimports` (for `make fmt`), `jq` and `zip` (for `make dist`), and Docker (for the image and the end-to-end tests).

## Layout

```text
cmd/planka-mcp        entry point, config, stdio and HTTP transports
internal/planka       Planka HTTP client and wire types
internal/service      name and card resolution, search, write rules
internal/tools        MCP tool definitions, compact output, error text
e2e                   end-to-end tests against a real Planka (build tag e2e)
```

The layers are enforced with `depguard` in `.golangci.yml`: `internal/planka` and `internal/service` do not import the MCP SDK, and `internal/tools` does not import `internal/planka` outside tests.

## Make targets

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
| `make dist` | Build every release file into `dist/`, as the release workflow does (`make dist VERSION=0.1.0`) |
| `make docker` | Build the image as `planka-mcp:dev` |
| `make clean` | Remove build outputs |

## End-to-end tests

`make planka-up` starts Planka 2.2.1 and Postgres from `deploy/compose.yml` (throwaway credentials, localhost only). `make e2e` builds the real binary, bootstraps an API key through Planka's default admin and runs it against that Planka:

- both flows from the top of the [README](README.md): filling in a card found by its words (description, a new label, a due date with a time, "me" as assignee, a running stopwatch, a checklist with a task and a linked card), and moving a card through the lists with a comment;
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

## Fixtures

The fixtures in `internal/planka/testdata/` are a frozen snapshot of real Planka 2.2.1 responses. Refresh them only deliberately with `make fixtures`, against a fresh instance (`make planka-down planka-up` first). It re-seeds Planka and changes every ID, so the tests that refer to fixture IDs have to be updated with it. See `internal/planka/testdata/README.md`.

## Tool snapshots

`cmd/planka-mcp/__toolsnaps__/*.snap` hold the schema of every tool as the model sees it, and a test fails when a tool changes. After a deliberate change run `make toolsnaps` and review the diff. `manifest.json` lists the tools too, and `TestManifestListsEveryTool` fails when that list and the registered tools differ.

## Mocks

The service and tools tests use mockery v3 mocks, generated into the consuming package as `mocks_test.go` by `make mocks` (configured in `.mockery.yml`).

## Releasing

Push a tag such as `v0.1.0`. The release workflow runs the tests, builds the binaries and `.mcpb` bundles for macOS, Linux and Windows (amd64 and arm64) with `scripts/release.sh`, pushes the multi-arch image to `ghcr.io/sierikov/planka-mcp`, then creates the GitHub release (with `checksums.txt`, `LICENSE` and the third-party license notices, which every bundle carries too) and publishes `server.json` to the MCP Registry. Each step runs as its own job with only the permissions it needs, every action is pinned to a commit, the image is pushed only after the tests pass, and the `mcp-publisher` download is checked against its published SHA-256. The version comes from the tag alone: it goes into the binaries, the manifest inside each bundle, `server.json` and the image tag, while the committed `manifest.json` and `server.json` keep the placeholder `0.0.0-dev`. `make dist VERSION=0.1.0` builds the same files into `dist/` locally. Before anything is published, the build job checks `server.json` with the registry's own validator, so a file the registry would refuse stops the release. Publishing to the MCP Registry itself is best-effort (`continue-on-error`), so a failure there does not fail the release: check the run log. Only a failed `mcp-publisher` download or checksum turns that last job red, after the release is already out. After the **first** release, make the `ghcr.io/sierikov/planka-mcp` package public in GitHub's package settings, or others cannot pull the image and the registry cannot read it.
