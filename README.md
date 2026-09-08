# claudingtin

A Claude Code plugin that turns model think-time into a chat roulette: while Claude is working, the companion quietly connects you to another person who is also waiting, and drops the connection the moment your session needs you back. The companion never writes to the Claude Code TUI, and nothing leaves your machine until a local first-run safety and 18+ screen has been cleared.

This repository is a Go workspace (`go.work`) over four modules — `proto`, `backend`, `companion`, `plugin` — with an enforced dependency direction: `proto` is the single definition of every websocket message, and both `backend` and `companion` import it while `plugin` links no sibling at all. The repository root is deliberately not itself a module.

| Module | Path | Import path | Depends on | Role |
|--------|------|-------------|------------|------|
| `proto` | `./proto` | `github.com/diegovillafuerte1/claudingtin/proto` | — (stdlib only) | Wire contract: the `{type, v, ...payload}` envelope, `PROTOCOL_VERSION`, one struct + `snake_case` discriminator per v1 message, and `Encode`/`Decode`. |
| `backend` | `./backend` | `github.com/diegovillafuerte1/claudingtin/backend` | `proto` | Server: `cmd/serve` (websocket + `GET /status`), `cmd/ban`, `cmd/reports`. Minimal in Epic 1. |
| `companion` | `./companion` | `github.com/diegovillafuerte1/claudingtin/companion` | `proto`, `fsnotify`, `coder/websocket` | Local TUI that tails the Claude Code transcript and speaks `ready`/`busy` over the websocket, behind a local first-run 18+/safety gate. Transcript turn-boundary parser + fsnotify tailer live in `internal/transcript`; the format it targets is pinned in [`docs/transcript-format.md`](docs/transcript-format.md). |
| `plugin` | `./plugin` | `github.com/diegovillafuerte1/claudingtin/plugin` | — (execs the companion by path) | `cmd/session-start` fail-open launcher plus the `hooks/session-start.sh` arch-dispatch wrapper, registered by `.claude-plugin/plugin.json` → `hooks/hooks.json` (`SessionStart`, `matcher: startup\|resume`). Inside tmux — or WezTerm / Zellij / Kitty / Windows Terminal — the launcher drops the companion into an adjacent split pane beside the Claude session; in a bare terminal or an editor-integrated terminal it opens the companion in a new OS window (real PTY, so the first-run screen works); only when no window can be opened does it spawn detached and print one line on how to open a live view (`CLAUDINGTIN_NO_WINDOW` forces that fallback). Committed cross-built binaries — the per-platform `session-start` launcher beside the pinned `companion` — live under `bin/<os>-<arch>/`. |

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
  Post-`hello`, `ready` and `busy` frames now drive an in-memory strict-FIFO
  wait queue and a single-writer pairing loop owned by the same hub goroutine:
  `ready` enqueues the account key, `busy` removes it. A waiting key that is not
  paired at once gets a `queued` frame; when the queue head pairs with the first
  eligible successor both peers get a `matched` frame carrying a backend-minted
  opaque `session_id` and a byte-identical `opener` — a pre-written conversation
  opener the hub rotates through a curated set with a round-robin cursor, so the
  same opener is never used for two consecutive matches (`pseudonym` / `blurb`
  stay empty until later stories). The set is `backend/openers.txt`
  (contributor-editable, one opener per line, embedded at build time and
  format-bound by `backend/openers_test.go`); the voice guide is
  `backend/openers.md`. Leaving the queue unmatched — via `busy`, a disconnect,
  or a takeover — is silent. Tearing down an active pairing (a peer's `busy` or
  disconnect) sends the surviving peer a bare `session_ended` with no re-enqueue.
  All queue, pairing, `session_id`, and opener-rotation-cursor state is in-memory
  and dies with the process. A post-`hello` `chat_msg` from a paired connection
  is relayed in-memory to that pairing's other peer only — never echoed back to
  the sender, never parsed, trimmed, or length-checked, and never logged (not
  even a content-free line, which would still leak chat volume and timing). A
  `chat_msg` from a connection that is not paired — never matched, or the pairing
  already ended — is dropped silently with no `error` frame. Text is UTF-8;
  `Encode`/`Decode` replace any invalid bytes with U+FFFD in transit. Relay is
  best-effort and in-order single-hop: v1 has no acknowledgement or retry, so a
  line can be dropped if the peer's delivery buffer is full (the sender's
  optimistic local echo is always shown regardless). Other post-`hello` frames
  (`leave`, undecodable) are still read and discarded in this epic.
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

### Chat surface

On a `matched` frame the companion opens a text chat surface (Bubble Tea v2) in
its own pane, in place of the status line, and hands the pane back when the
session ends. It shows, all at once: a header with the peer's pseudonym and
optional blurb (both blank until profiles land, so a neutral placeholder name
renders), a scrollable history whose first entry is the backend's opener, a
length-capped input box (2000 runes client-side; keystrokes past the cap are
refused and a paste is truncated), and an always-visible block / report / leave
("my Claude came back") affordance. There is no file, image, audio, or
attachment control anywhere, and no read-receipt state is ever shown. Peer and
opener text is rendered inert — printable characters and newlines only; every
ESC / control byte is dropped so no ANSI, CSI, or OSC sequence reaches the
terminal, and markup and links are shown as the literal characters typed, never
interpreted. Pressing Enter appends your own line to the history optimistically
(keyed by a fresh `client_msg_id`) and then puts one `chat_msg` on the wire; the
backend relays it to your peer only and never echoes it back, so the optimistic
line is the sole local copy. A whitespace-only Enter stays local and sends
nothing. An inbound peer `chat_msg` is rendered inert as a new history line; if
it arrives with no chat surface up it is dropped. If a line cannot be sent (the
socket is down) the companion prints one content-free "a chat line could not be
sent" notice through the surface and carries on. block / report / leave only
raise a content-free intent the companion logs.

While a `ready` session is left waiting for a match (the backend's `queued`
frame), the pane shows a calm searching spinner — one warm line and an
animation, no queue position, ETA, or "N online" count — and the status line
stays quiet until it clears. A match that pairs immediately (no `queued`) skips
it entirely. On `matched` the spinner is torn down and the chat surface opens
with a brief, bounded, non-blocking "spin" flourish (a fixed row above the
header for well under a second, then gone with a one-time viewport growth); the
input is focused and peer lines land in history the whole time it plays. A burst
that ends while searching just drops the spinner locally and the waiting line
returns — real return-to-spinner and re-enqueue are a later epic.

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

Requires Go 1.27.x. Third-party dependencies are `github.com/fsnotify/fsnotify` (pinned `v1.10.1`, the companion's transcript tailer), `github.com/coder/websocket` (pinned `v1.8.15`, the companion↔backend websocket, also used by `backend`), and the Bubble Tea v2 stack — `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2` — for the companion chat surface; `proto` and `plugin` stay stdlib-only. The first build needs module downloads (or a primed module cache) to fetch them; each module's `go.sum` keeps that reproducible. After that, no network access is needed to build or test.

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
`companion <transcript-path> "" ""`. Inside another scriptable multiplexer it
does the equivalent through that tool's own CLI — WezTerm (`$WEZTERM_PANE`),
Zellij (`$ZELLIJ`), Kitty with remote control on (`$KITTY_LISTEN_ON`), or
Windows Terminal (`$WT_SESSION`). With none of those (a bare terminal, a
VS Code / JetBrains integrated terminal), or if every split attempt fails, it
opens the companion in a new OS window — macOS Terminal via a self-deleting
`.command` script, a probed Linux terminal emulator, or `cmd /c start` on
Windows — so it still gets a real PTY and the first-run 18+/safety screen
works. Only when no window can be opened at all (no usable terminal emulator, or
a headless / API-only context) does it spawn that same command detached without
waiting and print exactly one line telling you how to open a split and run it
yourself. Two opt-out knobs take a truthy value — `1`, `true`, `yes`, or `on`
(case-insensitive): `CLAUDINGTIN_NO_WINDOW` forces the detached-spawn fallback
instead of a new window, and `CLAUDINGTIN_DISABLE` skips the launch entirely.
`CLAUDINGTIN_DISABLE` is checked first, so it wins if both are set.
Every other path — opt-out, malformed input, missing or unusable binary, spawn
error, even a panic — still exits `0` (that one-line fallback hint is the only
thing ever written to stdout), so a broken or absent companion never blocks the
Claude Code session.
