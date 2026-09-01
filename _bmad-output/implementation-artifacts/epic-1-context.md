# Epic 1 Context: Foundation, the think-time signal, and the safety gate

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

This epic stands up the greenfield monorepo and the foundational plumbing every later epic builds on: the shared wire-protocol module, CI that cross-builds the companion and publishes the backend image, the fail-open plugin launch, the companion's transcript-driven think-time signal, a minimal backend that accepts a versioned `hello` and tracks per-key connection state, and a reinstall-stable anonymous identity. All of it is gated behind a local first-run safety and 18+ screen that must be cleared before any network traffic leaves the machine. The through-line: a companion that knows when the model is thinking, never disturbs Claude Code, and connects no one until the risks are acknowledged.

## Stories

- Story 1.1: Monorepo scaffold and the `/proto` wire contract
- Story 1.2: CI cross-build and release skeleton
- Story 1.3: Transcript format spike and defensive parser
- Story 1.4: Minimal backend — `hello`, connection registry, `/status` skeleton
- Story 1.5: Account key at a reinstall-stable path
- Story 1.6: Companion — launch, transcript watch, `ready`/`busy` over the websocket
- Story 1.7: SessionStart hook — fail-open launch
- Story 1.8: Pane placement
- Story 1.9: First-run screen and 18+ gate as a local precondition

## Requirements & Constraints

- **Fail-open is absolute.** The `SessionStart` hook returns in ≤~50ms, spawns the companion detached with a hard timeout, and swallows every failure (spawn error, missing/non-executable binary, unreachable backend, crashed companion) with nothing printed to Claude Code's stderr. Not opted in is a silent no-op. The companion never writes to the Claude Code TUI.
- **Think-time is detected by tailing the Claude Code session transcript**, expressed to the backend only as `ready` / `busy`. Plugin hooks may nudge but are never authoritative; the backend never infers session state from timing. A single user turn — including all its tool calls — is one continuous burst mapping to at most one match. A burst that ends while the user is unmatched removes them from the queue silently, no error surfaced.
- **Account key:** a random UUIDv4 generated once and stored at a stable OS-config path *outside* the plugin's own directory (mode `0600`), so reinstalling/updating the plugin keeps the same key. Reused on every start; a corrupt or empty file is detected and regenerated. It is the user's whole identity to the backend — no Claude-account linkage is available to plugin code (hook input JSON carries no user/account identity). Never appears in any log line.
- **First-run gate, before any network call:** one screen covering what the product is, an honest plain safety warning (you are being connected to strangers; do not share identifying, location, or financial information; screenshots/quoting are possible), how block and report work, and an affirmative "I am 18 or older" control. The websocket does not open until it is cleared. Acceptance is stored locally only — no server round-trip, no attestation sent. Declining leaves the plugin installed but inert (no queue, no chat) and re-presents the screen next run. The screen stays re-accessible from the companion menu.
- All client↔server traffic is TLS/WSS. Logs are structured JSON to stdout with no account keys and no message content. Transient IP data (rate-limiting only, later epics) stays in memory, never persisted or logged. All user-facing copy is warm, light, and human per the voice guide — never name the machinery ("waiting," not "in the queue").
- Success target the epic serves: the spinner reliably resolves inside a think-time burst (median time-to-match under ~15s) — the plumbing here must add no perceptible latency to the host session.

## Technical Decisions

- **Monorepo, built from scratch, no starter template.** Layout: `/proto` (shared Go module: wire types + `PROTOCOL_VERSION`), `/backend` (`cmd/` for `serve` / `ban` / `reports`; `migrations/` embedded, ordered, forward-only), `/companion` (Go TUI), `/plugin` (`bin/<os>-<arch>/` committed cross-built binaries; `hooks/` SessionStart launcher), `/deploy` (Dockerfile, docker-compose.yml, fly.toml, keywords.txt), `/.github/workflows/`.
- **Dependency direction, enforced by a CI import-graph check:** `plugin → companion`, `companion → proto`, `backend → proto`. Nothing imports `plugin`; `proto` imports nothing.
- **Stack, pinned at init:** Go 1.27.x; charmbracelet bubbletea v2.0.x + bubbles v2.2.x + lipgloss v2.0.x; coder/websocket v1.8.15; fsnotify/fsnotify v1.10.1 (handles atomic-rename / rotation in the transcript watcher); modernc.org/sqlite v1.57.0 (pure-Go, no cgo).
- **Wire protocol:** every websocket message is JSON matching a type declared once in `/proto`; both companion and backend import it; no message shape exists elsewhere. Envelope `{ "type": <snake_case>, "v": <int>, ...payload }`. Wire types are `PascalCase` structs with a `snake_case` `type` discriminator; every type must round-trip marshal/unmarshal with its discriminator intact. v1 set — client→server: `hello` (protocol version, account key), `ready`, `busy`, `chat_msg` (`client_msg_id`, `text`), `leave`, `block`, `report` (`last_n`), `profile_put`, `forget_me`, `note_put`, `heartbeat`; server→client: `queued`, `matched`, `chat_msg`, `session_ended`, `blocklist`, `profile_ack`, `error` (`code`, `msg`), `please_update`. `chat_msg` is never echoed to its sender. `please_update` is its own type, sent before the socket closes, never an `error` code. Client-facing errors are an `error` message, never a transport close (except `please_update`).
- **Version negotiation:** the companion sends its version in `hello`; the backend accepts the current and previous minor; anything older gets exactly one `please_update`, then the socket closes. Any breaking wire change bumps the minor.
- **Backend (this epic = minimal):** a single channel-driven goroutine owns connection-registry mutation. `serve` reads `PORT`. On valid `hello` the key is registered as connected. One active connection per account key — a second `hello` for a connected key takes over; the prior connection gets `session_ended` then close. Only two HTTP endpoints ever exist: the websocket upgrade and `GET /status` (returns JSON with at least `concurrent_users`). No admin HTTP surface.
- **Plugin → companion is launch-only.** `SessionStart` spawns the companion once per Claude Code session, detached, with three arguments: transcript path, config-dir path, server URL. After spawn the plugin holds no channel to it. The hook resolves the platform binary via `${CLAUDE_PLUGIN_ROOT}` → `plugin/bin/<os>-<arch>/`. An opt-out marker makes the hook do nothing.
- **Transcript parser (Story 1.3, AR30):** the transcript file location and JSONL schema are *not* a documented stable contract. Commit a repo note pinning the current location, line schema, observed version, and stability caveats. The parser emits exactly one turn-start and one turn-end per turn — N tool calls inside a turn do not split it. It recovers without crashing from a file truncated mid-line, atomically renamed, or rotated, and skips unknown line types. The companion sends `ready` within ~1s of turn-start, `busy` within ~1s of turn-end; on websocket drop it reconnects with backoff, re-sends `hello`, and resumes from current transcript state.
- **CI / release:** PR workflow compiles the companion for macOS arm64, macOS x64, Linux x64, Windows x64, builds the backend container image, and runs all tests — including a `modernc.org/sqlite` build-and-open check on Windows. A version tag attaches the four binaries to a GitHub release, publishes the backend image, and refreshes `plugin/bin/<os>-<arch>/` (commit step documented). Each plugin release pins one companion binary version.
- **Configuration is environment variables only, no config files.** Companion reads `SERVER_URL` (default: the public instance). Backend reads `PORT`, `DB_PATH`, `KEYWORDS_PATH`, optional `REPORT_WEBHOOK`.
- **Conventions:** Go packages lowercase, no underscores. IDs are UUIDv4 strings. Timestamps are Unix epoch milliseconds (`int64` wire, `INTEGER` SQLite). SQLite tables `snake_case` plural (schema itself lands in Epic 4).

## UX & Interaction Patterns

- **Pane placement.** Inside tmux (`$TMUX` set): the companion is placed as a tmux split pane adjacent to the Claude session automatically. Not inside tmux: the plugin prints exactly one line telling the user how to open/focus the companion pane, and nothing blocks. VS Code / JetBrains integrated terminals and native Windows PowerShell have no tmux — this manual placement is the accepted degradation. Inbound messages only ever render in the companion's own pane.
- **Companion in this epic** renders only a minimal status line — no chat UI yet.
- **First-run screen** copy is honest and plain but warmly delivered; it does not soften the risk. The exit/"your Claude is back" framing and all other companion copy follow the repo voice guide (short sentences, lowercase-friendly, never corporate or clinical, never name the machinery).

## Cross-Story Dependencies

- Story 1.1 (`/proto` contract) must land before Story 1.4 and Story 1.6 — both depend on the shared message types and `PROTOCOL_VERSION`.
- Story 1.3 (defensive transcript parser) is consumed directly by Story 1.6 (companion transcript watch).
- Story 1.5 (account key) feeds Story 1.6 (companion sends it in `hello`) and Story 1.4 (backend registers connections by key).
- Story 1.7 (SessionStart hook) launches the Story 1.6 companion with the three arguments; Story 1.2 produces and commits the binaries the hook resolves via `${CLAUDE_PLUGIN_ROOT}`.
- Story 1.8 (pane placement) is part of the companion/hook launch path (Stories 1.6 / 1.7).
- Story 1.9 (first-run gate) blocks the Story 1.6 websocket connect until the screen is cleared.
- Downstream: enforcement of the account key (blocks, cooldowns, bans) and the SQLite schema arrive in Epic 4 — this epic only creates the key and the connection registry. FIFO matching, the chat surface, and unified `session_ended` semantics arrive in Epics 2–3; here the backend does nothing past accepting `hello` and tracking connection state.
