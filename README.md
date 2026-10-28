# claudingtin

A Claude Code plugin that turns model think-time into a chat roulette: while Claude is working, claudingtin quietly connects you to another person who is also waiting, and drops the connection the moment your session needs you back. The client is a Claude Code mod that draws inside the session itself, and nothing leaves your machine until a local first-run safety and 18+ screen has been cleared.

This repository holds a Go workspace (`go.work`) over two modules — `proto` and `backend` — plus the mod in `plugin/` (TypeScript, not a Go module). The dependency direction is enforced: `proto` is the single definition of every wire message, and `backend` imports it. The repository root is deliberately not itself a module.

| Unit | Path | Depends on | Role |
|------|------|------------|------|
| `proto` | `./proto` (`github.com/diegovillafuerte1/claudingtin/proto`) | — (stdlib only) | Wire contract: the `{type, v, ...payload}` envelope, `PROTOCOL_VERSION`, one struct + `snake_case` discriminator per v1 message, `Encode`/`Decode`, and golden JSON fixtures in `testdata/fixtures/`. |
| `backend` | `./backend` (`github.com/diegovillafuerte1/claudingtin/backend`) | `proto` | Server: `cmd/serve` (the `/v1` long-poll transport + `GET /status`), `cmd/ban`, `cmd/reports`. |
| mod | `./plugin` | — | The Claude Code mod: `.claude-plugin/plugin.json` → `hooks/hooks.json` → `hooks/register.tsx`. Think-time signal, status band, first-run gate and chat pane. |

## Backend

`backend serve` (`./backend/cmd/serve`) reads one environment variable — `PORT`,
default `8080` when unset or empty — and exposes exactly four HTTP routes:

- **`POST /v1/hello`** — body is a `hello` carrying `protocol_version` and the
  account key. `protocol_version >= PROTOCOL_VERSION - 1` is accepted (including
  a version newer than the backend) and answered with `welcome{handle}`; anything
  older gets `please_update` and no handle. The account key travels only here;
  every later request carries just the opaque handle in the
  `X-Claudingtin-Handle` header. A second `hello` for the same key supersedes the
  old handle: its queue gets `session_ended`, then its requests get `410`.
- **`GET /v1/events?after=<seq>`** — held up to ~20s; returns a JSON array of
  `{seq, type, v, ...}` events in hub order (empty on timeout). `after=N`
  acknowledges everything up to `N`. Each handle has one bounded, ordered queue;
  overflow ends the session with `session_ended`, never a silent drop. A handle
  stays connected while a poll is in flight or ~5s after one ends; a lapsed lease
  is a disconnect and starts the ~10s reconnect grace window.
- **`POST /v1/send`** — one client→server message: `ready`, `busy`, `chat_msg`,
  `leave`. `block` and `report` are accepted but not acted on until Epic 4.
- **`GET /status`** — returns `200` with `application/json` body
  `{"concurrent_users": N}`, where `N` is the number of distinct connected
  account keys. A non-GET `/status` is `405`; every other path is `404`.

Logs are structured JSON (`log/slog`) to stdout and deliberately carry **no
account key and no frame text**. The process shuts down gracefully on `SIGINT` /
`SIGTERM`. TLS is terminated by the operator's proxy, never in-process.

A single-writer hub goroutine owns the strict-FIFO wait queue, the pairings,
the `session_id` mint and the opener rotation (`backend/openers.txt`, one opener
per line, embedded at build time; voice guide `backend/openers.md`). All of it is
in memory and dies with the process. A `chat_msg` is relayed to the paired peer
only — never echoed back, never parsed, and never logged.

## The mod

The mod lives in `plugin/hooks/` and runs inside Claude Code; no hook ever
awaits the network.

- **Think-time.** A main-loop `turn.start` sends `ready` and a `turn.complete`
  sends `busy`. Surfaces where the mod draws nothing (`claude -p`, the VS Code
  panel, Remote Control) never send `ready`: no surface, no queue.
- **Band and chat.** A band above the prompt shows backend status, then a
  searching spinner (no position, ETA or count) while queued. On a match a chat
  pane opens with the peer's header, the opener, the history, an input, and
  always-visible leave / block / report buttons. Peer text is drawn inert as
  plain `Text`. `/claudingtin chat | leave | block | report | safety` mirrors the
  controls.
- **First run.** A focused pane explains the product, warns about strangers,
  explains block and report, and asks for "I am 18 or older". Acceptance is
  recorded locally in `safety-ack`, beside the account key below; declining
  leaves the mod inert.
- **Configuration.** Plugin `userConfig.backendUrl` (default
  `http://127.0.0.1:8080`).

### Account key

Your whole identity to the backend is a single random UUIDv4, generated on first
run and reused after that. A missing, empty or corrupt key file is regenerated.
It lives outside the plugin directory, so reinstalling keeps it. These are the
paths the retired Go companion used, so an existing key and first-run
acknowledgement carry over:

| OS | Path |
|----|------|
| Linux | `$XDG_CONFIG_HOME/claudingtin/account-key`, else `~/.config/claudingtin/account-key` |
| macOS | `~/Library/Application Support/claudingtin/account-key` |
| Windows | `%AppData%\claudingtin\account-key` |

The file is mode `0600` (advisory on Windows). Don't hand-edit it; a non-canonical UUID is treated as
corrupt and replaced. The key appears in no log or UI.

## Development

Requires Go 1.27.x and Claude Code (for the mod). `proto` is stdlib-only by
rule (`scripts/check_deps.sh`); `backend` currently has no third-party
dependencies either, so building and testing needs no module downloads.

From the repository root (Go's `./...` does not span workspace modules on its own, so name them):

```sh
go build ./backend/... ./proto/...      # compile every module
go test  ./backend/... ./proto/...      # incl. the proto wire round-trip over every v1 message
go vet   ./backend/... ./proto/...      # static checks
go work sync                            # keep the workspace build list in sync
bash scripts/check_deps.sh              # assert the dependency direction
gofmt -l .                              # formatting (expect no output)
claude plugin validate --strict plugin  # the mod's manifest and hooks module
claude plugin test plugin               # the mod's tests
```

The module list above is also emitted by `scripts/workspace_modules.sh` (so
`go build $(bash scripts/workspace_modules.sh)` is equivalent). CI and
`scripts/check_deps.sh` both enumerate the module set from `go.work` via that
helper, so `go.work` is the single source of truth for *which* modules exist.
`scripts/check_deps.sh` still needs a dependency-direction rule written for each
module by name — a module added to `go.work` under an unknown name makes it fail
loudly until rules are added. `scripts/workspace_modules.sh --check` fails if a
`go.work` entry has no `go.mod`, or a top-level module is missing from `go.work`.

## CI & releases

Two GitHub Actions workflows live in `.github/workflows/`.

**`ci.yml`** runs on every pull request and on push to `main`:

- **test** — `go build` / `go vet` / `go test -race` the `go.work`-derived module
  set on `ubuntu-latest`, `macos-latest`, and `windows-latest`.
- **lint** — `gofmt -l .`, `go work sync` must be a no-op, `shellcheck` over
  `scripts/*.sh`, `scripts/check_deps.sh` (dependency direction),
  `scripts/workspace_modules.sh --check`, and the guards' self-tests.
- **plugin** — installs a pinned Claude Code, checks `hooks/hooks.json` lists
  only the mod module, then runs `claude plugin validate --strict plugin` and
  `claude plugin test plugin`.
- **backend-image** — builds `deploy/Dockerfile`. On push to `main` it is pushed
  to `ghcr.io/diegovillafuerte1/claudingtin-backend:edge`; on a pull request it
  is built only — no registry login, no push — so fork PRs need no secrets.

**`release.yml`** runs on a `v*` tag (e.g. `git push origin v0.1.0`) and pushes
the backend image to `ghcr.io/diegovillafuerte1/claudingtin-backend` tagged with
the bare version (`v0.1.0` → `0.1.0`) and `latest`. There are no client
binaries: the mod ships as source.
