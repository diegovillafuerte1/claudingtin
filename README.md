# claudingtin

A Claude Code plugin that turns model think-time into a chat roulette: while Claude is working, the companion quietly connects you to another person who is also waiting, and drops the connection the moment your session needs you back. The companion never writes to the Claude Code TUI, and nothing leaves your machine until a local first-run safety and 18+ screen has been cleared.

This repository is a Go workspace (`go.work`) over four modules — `proto`, `backend`, `companion`, `plugin` — with an enforced dependency direction: `proto` is the single definition of every websocket message, and both `backend` and `companion` import it while `plugin` links no sibling at all. The repository root is deliberately not itself a module.

| Module | Path | Import path | Depends on | Role |
|--------|------|-------------|------------|------|
| `proto` | `./proto` | `github.com/diegovillafuerte1/claudingtin/proto` | — (stdlib only) | Wire contract: the `{type, v, ...payload}` envelope, `PROTOCOL_VERSION`, one struct + `snake_case` discriminator per v1 message, and `Encode`/`Decode`. |
| `backend` | `./backend` | `github.com/diegovillafuerte1/claudingtin/backend` | `proto` | Server: `cmd/serve` (websocket + `GET /status`), `cmd/ban`, `cmd/reports`. Minimal in Epic 1. |
| `companion` | `./companion` | `github.com/diegovillafuerte1/claudingtin/companion` | `proto`, `fsnotify`, `coder/websocket` | Local TUI that tails the Claude Code transcript and speaks `ready`/`busy` over the websocket, behind a local first-run 18+/safety gate. Transcript turn-boundary parser + fsnotify tailer live in `internal/transcript`; the format it targets is pinned in [`docs/transcript-format.md`](docs/transcript-format.md). |
| `plugin` | `./plugin` | `github.com/diegovillafuerte1/claudingtin/plugin` | — (execs the companion by path) | `cmd/session-start` fail-open launcher plus the `hooks/session-start.sh` arch-dispatch wrapper, registered by `.claude-plugin/plugin.json` → `hooks/hooks.json` (`SessionStart`, `matcher: startup\|resume`). Inside tmux the launcher drops the companion into an adjacent split pane beside the Claude session; with no tmux it spawns it detached and prints one line on how to open a live view. Committed cross-built binaries — the per-platform `session-start` launcher beside the pinned `companion` — live under `bin/<os>-<arch>/`. |

## Backend

`backend serve` (`./backend/cmd/serve`) is the minimal Epic 1 server. It reads
one environment variable — `PORT`, default `8080` when unset or empty — and
exposes exactly two HTTP routes:

- **`/ws`** — websocket upgrade. The client's first frame must be a `hello`
  (`proto` type) carrying its `protocol_version` and account key. The version is
  gated: `protocol_version >= PROTOCOL_VERSION - 1` is accepted (including a
  version newer than the backend); anything older gets exactly one
  `please_update` frame followed by a normal close. A bad first frame (not
  `hello`, or undecodable) gets one `error` frame (`expected_hello` /
  `bad_frame`) then a close. On a valid `hello` the account key is registered in
  a single-writer connection registry; a later `hello` for a live key takes over
  and the displaced connection receives `session_ended` then a normal close.
  Post-`hello` frames are read and discarded in this epic.
- **`GET /status`** — returns `200` with `application/json` body
  `{"concurrent_users": N}`, where `N` is the number of distinct connected
  account keys. A non-GET `/status` is `405`; every other path is `404`.

Logs are structured JSON (`log/slog`) to stdout and deliberately carry **no
account key and no frame text**. The process shuts down gracefully on `SIGINT` /
`SIGTERM`. TLS is terminated by the operator's proxy, never in-process.

## Companion

### Runtime

The `SessionStart` hook launches the companion once per Claude Code session with
three positional arguments — `transcript-path`, `config-dir`, `server-url`. A
non-empty `transcript-path` is required; an empty `server-url` falls back to
`$SERVER_URL`, then to a compiled-in localhost default; the resolved URL must be
`ws://` or `wss://` with a host, and if it has no path `/ws` is appended. It
loads the account key, opens one websocket to the backend (`coder/websocket`),
sends a version + key `hello`, then tails the transcript and turns each turn
boundary into a `ready` (model thinking) or `busy` (your turn) frame within
about a second. A dropped connection is retried forever with exponentially
backed-off, fully jittered delay, re-sending `hello` and re-announcing the
current state on every reconnect. The companion writes only a one-line status to
its own stdout and diagnostics to stderr — never to the Claude Code TUI — and
the account key appears in no log, error, or status line. `please_update` from
the backend stops the retries and leaves the process running; `session_ended`,
`SIGINT`, or `SIGTERM` shut it down cleanly.

Exit codes: **0** — clean exit (`SIGINT`/`SIGTERM` on its own, after the peer
ended the session, after `please_update`, or after the first-run screen was
declined and the process was then signalled — or `CLAUDINGTIN_SAFETY_REVIEW` was
set); **2** — wrong number of positional arguments; **1** — any other startup
failure (empty `transcript-path`, an unusable `server-url`, config-dir
resolution, account-key load, an unreadable `safety-ack`) or a fatal runtime
error such as the transcript watch dying.

### First run

Before the transcript is watched or any websocket is opened, the companion
prints one plain, local screen to its own stdout — what this is, an honest
warning that you are put in a room with a stranger and should share nothing
identifying, how block and report work, and an affirmative "18 or older"
prompt — and reads one line from its own stdin. Type `yes` (or `y`, or
`i am 18 or older`) and it records the acknowledgement at
`<config-dir>/safety-ack` (mode `0600`, holding only the screen version and an
accepted-at timestamp — no account key, nothing is ever sent to the backend)
and connects as usual. Anything else, a blank line, or no input at all is a
decline: nothing is recorded, the status line goes quiet and inert, the process
keeps running without connecting, and the screen returns on the next run. A
material change to the screen copy bumps its version so an already-accepted user
sees it once more.

When the companion is first launched headless or detached — no visible
terminal, stdin wired to `/dev/null` — the gate cannot be cleared there and it
stays inert. Run the companion once in a visible terminal to accept; the
recorded acknowledgement then applies to every later launch.

`CLAUDINGTIN_SAFETY_REVIEW=1 companion` reprints the screen and exits `0`
without parsing arguments, loading the account key, gating, or connecting — the
re-access path until the companion grows a menu.

### Account key

The companion's whole identity to the backend is a single random UUIDv4. It is
generated on first run and reused on every start — but a key file that is
missing, empty, whitespace-only, or corrupt is detected and regenerated, so it
is not guaranteed to be the *same* value forever, only stable as long as the
file survives. It is stored — via `os.UserConfigDir()` — at:

| OS | Path |
|----|------|
| Linux | `$XDG_CONFIG_HOME/claudingtin/account-key`, else `~/.config/claudingtin/account-key` |
| macOS | `~/Library/Application Support/claudingtin/account-key` |
| Windows | `%AppData%\claudingtin\account-key` |

The key file is written with mode `0600` and its directory created with `0700`
(on Windows these bits are advisory, not OS-enforced). The location is
deliberately *outside* the plugin's own directory, so reinstalling or updating
the plugin keeps the same key. This file is managed by the companion — don't
hand-edit it; a hand-written non-canonical or uppercase UUID is treated as
corrupt and replaced. The package writes no logs and the key appears in no
returned error or panic message. `internal/identity` owns this. The
`session-start` launcher passes the companion an empty config-dir argument, so
the companion resolves this path itself via `identity.DefaultConfigDir()`.

## Development

Requires Go 1.27.x. Third-party dependencies are `github.com/fsnotify/fsnotify` (pinned `v1.10.1`, the companion's transcript tailer) and `github.com/coder/websocket` (pinned `v1.8.15`, the companion↔backend websocket, also used by `backend`); `proto` and `plugin` stay stdlib-only. The first build needs module downloads (or a primed module cache) to fetch them; each module's `go.sum` keeps that reproducible. After that, no network access is needed to build or test.

From the repository root (Go's `./...` does not span workspace modules on its own, so name them):

```sh
go build ./proto/... ./backend/... ./companion/... ./plugin/...   # compile every module
go test  ./proto/... ./backend/... ./companion/... ./plugin/...   # incl. the proto wire round-trip over every v1 message
go vet   ./proto/... ./backend/... ./companion/... ./plugin/...   # static checks
go work sync                                                      # keep the workspace build list in sync
bash scripts/check_deps.sh                                        # assert the dependency direction
gofmt -l .                                                        # formatting (expect no output)
```

The module list above is also emitted by `scripts/workspace_modules.sh` (so
`go build $(bash scripts/workspace_modules.sh)` is equivalent). CI and
`scripts/check_deps.sh` both enumerate the module set from `go.work` via that
helper, so `go.work` is the single source of truth for *which* modules exist:
`go build` / `go vet` / `go test` in CI pick up a new module automatically.
`scripts/check_deps.sh` still needs a dependency-direction rule written for each
module by name — a module added to `go.work` under an unknown name makes it fail
loudly until rules are added. `scripts/workspace_modules.sh --check` fails if a
`go.work` entry has no `go.mod`, or a top-level module is missing from `go.work`.

## CI & releases

Two GitHub Actions workflows live in `.github/workflows/`.

**`ci.yml`** runs on every pull request and on push to `main`:

- **test** — `go build` / `go vet` / `go test` the `go.work`-derived module set
  on `ubuntu-latest`, `macos-latest`, and `windows-latest`. The companion ships
  for all three and has OS-sensitive runtime paths (the fsnotify transcript
  tailer and its symlink handling, process signalling); running the suite on
  every OS is standing regression insurance.
- **lint** — `gofmt -l .`, `go work sync` must be a no-op, `shellcheck` over
  `scripts/*.sh` and `plugin/hooks/*.sh`, `scripts/check_deps.sh`
  (dependency direction), and `scripts/workspace_modules.sh --check`
  (every `go.work` entry has a `go.mod`, every top-level module is wired in).
- **cross-build** — plain `go build` (`CGO_ENABLED=0`) of the companion **and the
  `session-start` launcher** for the four targets `darwin/arm64`, `darwin/amd64`,
  `linux/amd64`, `windows/amd64`, each uploaded as a build artifact
  (`companion-<os>-<arch>` and `session-start-<os>-<arch>`).
- **backend-image** — builds `deploy/Dockerfile`. On push to `main` it is pushed
  to `ghcr.io/diegovillafuerte1/claudingtin-backend:edge`; on a pull request it
  is built only — no registry login, no push — so fork PRs need no secrets.

**`release.yml`** runs on a `v*` tag (e.g. `git push origin v0.1.0`):

- attaches the four companion binaries — `companion-darwin-arm64`,
  `companion-darwin-amd64`, `companion-linux-amd64`,
  `companion-windows-amd64.exe` — and the four matching `session-start-<os>-<arch>`
  launcher binaries to the GitHub Release for the tag;
- pushes the backend image to `ghcr.io/diegovillafuerte1/claudingtin-backend`
  tagged with the bare version (`v0.1.0` → `0.1.0`) and `latest`.

### Manual step: refreshing `plugin/bin/`

CI never commits binaries. After a release, copy the freshly built binaries into
`plugin/bin/<os>-<arch>/` — the `session-start` launcher beside the `companion`
for each target — and commit them as a separate reviewed change. Each plugin
release pins exactly one version of both binaries.

The `SessionStart` hook chain is: Claude Code reads
`plugin/.claude-plugin/plugin.json` → `hooks/hooks.json` (one `SessionStart`
entry, `matcher: "startup|resume"`, `timeout: 10`) → runs
`hooks/session-start.sh`, which maps the host to `<os>-<arch>` and runs
`bin/<os>-<arch>/session-start`. The hook fires only on session `startup` and
`resume`; `clear` and `compact` keep using the companion already launched for
that session. The launcher reads the hook's stdin JSON, takes a per-`session_id`
lock in the temp dir (one companion per session), and then places the companion
beside your Claude session: inside tmux (`$TMUX` set) it opens an adjacent
`tmux split-window` pane — side by side, no focus stolen from Claude — running
`companion <transcript-path> "" ""`; with no tmux (VS Code / JetBrains
terminals, native PowerShell), or if the split fails, it falls back to spawning
that same command detached without waiting and prints exactly one line telling
you how to open a split and run it yourself. Set `CLAUDINGTIN_DISABLE` to
`1`/`true`/`yes`/`on` to disable the launch entirely. Every other path — opt-out,
malformed input, missing or unusable binary, spawn error, even a panic — still
exits `0` (that one-line hint on the no-tmux fallback is the only thing ever
written to stdout), so a broken or absent companion never blocks the Claude Code
session.
