---
title: 'Story 1.6 — Companion: launch, transcript watch, ready/busy over the websocket'
type: 'feature'
created: '2026-09-05'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'a8ac6cbe10550bcfbc466587bfde486be4e6f60b'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** `companion/main.go` is a compile-only stub. The pieces exist in isolation — the
Story 1.3 transcript parser, the Story 1.5 account key, the Story 1.4 backend that accepts
`hello` — but nothing wires them into a running process that connects and signals think-time.

**Approach:** Fill in `companion/main.go` to take the three launch arguments
(`transcript-path`, `config-dir`, `server-url`), load the account key, open one `coder/websocket`
connection, send `hello`, tail the transcript via `transcript.Watch`, and translate its
turn boundaries to `ready` / `busy` frames within ~1s — reconnecting with backoff and
re-announcing on any drop. A minimal stdout status line and nothing else for UI.

## Boundaries & Constraints

**Always:**
- `main` takes exactly three positional args, order `transcript-path config-dir server-url`;
  a wrong count prints a usage line to stderr and exits 2. Empty `config-dir` ⇒
  `identity.DefaultConfigDir()`. Empty `server-url` ⇒ `SERVER_URL` env ⇒ compiled default
  `ws://127.0.0.1:8080/ws`. If the resolved URL's path is empty or `/`, append `/ws`.
- The account key comes only from `identity.Load`; it travels in `proto.Hello.AccountKey`
  and appears in no log, error, or status line.
- Turn boundaries come only from `transcript.Watch` over the given path. `TurnStart` ⇒
  `ready`, `TurnEnd` ⇒ `busy`, written as `proto.Encode` text frames within ~1s. No turn
  seen / idle ⇒ `busy`.
- One websocket (`coder/websocket`). The first frame after every (re)connect is `hello`
  with `proto.PROTOCOL_VERSION` + the key; immediately after, the frame for the current
  tracked state is (re)sent. Any read/write failure ⇒ reconnect with exponential backoff +
  full jitter (base ~0.5s, cap ~30s), retried indefinitely, backoff reset after a
  successful `hello`.
- Seeded / bursty events are coalesced with a short debounce (~250ms): a backlog of
  historical turns sends at most the settled current state, never every transition.
- The companion writes only to its own `os.Stdout` (status line — warm copy per the voice
  guide, "waiting", never "queue") and `os.Stderr` (diagnostics). It never writes to a
  Claude Code stream and renders no chat UI.
- `please_update` ⇒ one stderr line, stop reconnecting, process stays alive. `session_ended`
  ⇒ clean shutdown, exit 0. `SIGINT` / `SIGTERM` ⇒ cancel the watch, normal ws close, exit 0.
- stdlib + `proto` + `fsnotify` + `coder/websocket` only. `go build` / `go vet` /
  `go test -race` over `scripts/workspace_modules.sh`, `gofmt -l .`,
  `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and
  `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Any companion dependency beyond `proto` + `fsnotify` + `coder/websocket` (a backoff, UUID,
  or TUI library).
- Any change to `/proto` or to the backend.
- Changing the compiled default `SERVER_URL` off the localhost placeholder (Epic 6).

**Never:**
- No chat UI, no Bubble Tea, no pane placement (Stories 1.8 / 2.3); no first-run / 18+ gate
  (Story 1.9); no `SessionStart` hook logic (Story 1.7).
- No stuck-turn watchdog / forced-`busy` timeout — tracked as deferred work.
- No `heartbeat` send, no post-`hello` read deadline (a later story).
- No account-key enforcement, no persistence.
- The companion never infers or overrides turn state from timers or hook nudges — only the
  transcript parser (AD-6).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| Normal startup | 3 args, backend reachable | `identity.Load`, dial URL, send `hello{PROTOCOL_VERSION, key}`, start `transcript.Watch`, status shows "waiting" | — |
| Turn start / end | parser emits `TurnStart` / `TurnEnd` | `ready` / `busy` frame within ~1s; status line updates | — |
| Seed replay | `Watch` replays a multi-turn existing transcript | events debounced ~250ms; only the current state reaches the wire | — |
| Websocket drop | `Read` / `Write` errors | reconnect with backoff+jitter (unbounded); on reconnect re-send `hello` then the current-state frame | transient; never exits |
| `please_update` | backend rejects the version | one stderr line (no key), stop reconnecting, process stays alive, status shows "update needed" | — |
| `session_ended` | another connection for this key took over | clean shutdown, exit 0 | — |
| Blank `server-url` arg | `""` | `SERVER_URL` env ⇒ `ws://127.0.0.1:8080/ws` | — |
| URL path empty or `/` | `wss://host` | append `/ws` before dialing | — |
| Blank `config-dir` arg | `""` | `identity.DefaultConfigDir()` | wrapped stderr error + exit non-zero if that also fails |
| Wrong arg count | `!= 3` positional | usage to stderr, exit 2 | — |
| `identity.Load` fails | unwritable config dir | stderr error with no key material, exit non-zero | — |
| Fatal transcript watch error | `Watch` `Errors` delivers a fatal (rotation retries exhausted) | stderr line, clean shutdown, exit non-zero | — |
| Output scrub | any row above | no account key, no transcript/message content; nothing written outside the companion's own stdout/stderr | — |

</frozen-after-approval>

## Code Map

- `companion/main.go` — **edit.** Replace the stub `main`. Parse the three positional args,
  apply the `config-dir` / `server-url` resolution rules (env, default, `/ws` append),
  `key, err := identity.Load(configDir)`, build `run.Config`, call `run.Run(ctx, cfg)` under
  a `signal.NotifyContext` for `SIGINT`/`SIGTERM`. Thin — no protocol logic here. Extract a
  pure `resolve(args []string, env func(string) string) (run.Config, error)` helper for the
  arg-resolution table test.
- `companion/internal/run/run.go` — **new.** The orchestrator. `Config{TranscriptPath,
  ServerURL, AccountKey string; Out, Err io.Writer}` (writers injected; default
  `os.Stdout` / `os.Stderr`). `Run(ctx, Config) error`: start `transcript.Watch`, start a
  `wsclient.Client`, own a `desired` state (ready/busy) updated by every watcher `Event`,
  a `sent` state, and the ~250ms debounce timer; push `desired` to the client, drive the
  `statusline.Renderer`, and map `please_update` / `session_ended` / fatal-watch / `ctx`
  cancel to the documented exits. Re-push `desired` on every client "connected" signal.
- `companion/internal/wsclient/wsclient.go` — **new.** `Client` around one `coder/websocket`
  conn: connect loop with backoff+jitter (knobs as unexported package vars, `internal/
  transcript` style), `hello` on each connect, `SendState(ctx, msg)` serialised through the
  write path, a "connected" channel/callback so `run` re-pushes state, and decoded
  recognition of `please_update` / `session_ended`; all other inbound frames discarded.
  `proto.Encode` / `proto.Decode` for every frame. Reads on their own goroutine (mirror
  `server.serveConn`). No key in any log.
- `companion/internal/statusline/statusline.go` — **new.** `Renderer` over an `io.Writer`;
  `Show(phase)` writes one human line only when `phase` changes. Phases: connecting,
  waiting (busy), free-to-chat (ready), reconnecting, update-needed. Copy centralised here,
  voice-guide compliant.
- `companion/go.mod` / `companion/go.sum` — **edit.** Add `require
  github.com/coder/websocket v1.8.15` (zero transitive deps — see `backend/go.sum`). Run
  `go work sync`; commit the result (expect only these two files change; no `go.work.sum`).
- `companion/internal/transcript/{parser,watch}.go` — **read-only.** `transcript.Watch(ctx,
  path) (*Watcher, error)`; `Watcher.Events` of `Event{Kind EventKind, At time.Time}` with
  `TurnStart` / `TurnEnd`; `Watcher.Errors` delivers one fatal error before both channels
  close. Do not modify.
- `companion/internal/identity/identity.go` — **read-only.** `Load(configDir) (string,
  error)`, `DefaultConfigDir() (string, error)`.
- `proto/messages.go`, `proto/protocol.go` — **read-only.** `Hello{AccountKey,
  ProtocolVersion}`, `Ready{}`, `Busy{}`, `PleaseUpdate{}`, `SessionEnded{}`, `Encode` /
  `Decode`, `PROTOCOL_VERSION`.
- `backend/internal/server/server.go` — **read-only reference.** Server side of the same
  handshake: `/ws`, hello-first, version gate (`pv < PROTOCOL_VERSION-1` ⇒ `please_update`
  + close), takeover ⇒ `session_ended`. Mirror its framing and close codes.
- `.github/workflows/ci.yml` — **edit.** Add `macos-latest` to the `test` job's `matrix.os`;
  drop the header comment and the "No macOS test runner yet" wording that defer the macOS
  runner to Story 1.6 (deferred-work.md tracks this).
- `README.md` — **edit.** Companion section: short "Runtime" note (three launch args,
  `ready` / `busy` over WSS via `coder/websocket`, reconnect with backoff, minimal stdout
  status line, never writes to the Claude Code TUI). Module table: add `coder/websocket` to
  `companion`'s `Depends on`. CI section: update the "No macOS runner yet" sentence.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/statusline/statusline.go` + `statusline_test.go` — phase→copy
  renderer; table test over every phase plus a repeated-phase no-op.
- [x] `companion/internal/wsclient/wsclient.go` + `wsclient_test.go` — connect / backoff /
  `hello` / send / reconnect / `please_update` / `session_ended`. Tests run a real
  `httptest` + `coder/websocket` server: first frame is `hello` with version + key; a
  server close triggers reconnect + a fresh `hello`; `ready` / `busy` frames arrive;
  shrunk backoff knobs for the timing assertion; `please_update` stops retries; a
  `session_ended` ends the client.
- [x] `companion/internal/run/run.go` + `run_test.go` — orchestration, debounce, state
  mapping, exit translation. Tests inject a fake watcher channel and a fake client:
  `TurnStart`→`ready`, `TurnEnd`→`busy`, a replayed burst coalesces to the current state,
  a reconnect re-pushes the current state, `ctx` cancel returns cleanly. Also a
  300-iteration race regression (`TestContextCancelBeatsWatcherClose`) and the output-scrub
  assertion (`TestOutputScrub`).
- [x] `companion/main.go` — arg parse + `resolve` helper + `identity.Load` + `run.Run`;
  table-test `resolve` (arg count, blank `config-dir` / `server-url` fallbacks, `/ws`
  path append).
- [x] `companion/go.mod` / `companion/go.sum` — add `coder/websocket v1.8.15`;
  `go work sync` (no-op; no `go.work.sum`).
- [x] `.github/workflows/ci.yml` — add `macos-latest` to the test matrix; refresh the two
  stale comments.
- [x] `README.md` — companion runtime note, dependency-table row, CI-section sentence.

**Acceptance Criteria:**
- Given the companion started with a temp transcript file, a temp config dir, and an
  `httptest` `coder/websocket` server URL, when a human-prompt line then an assistant
  `end_turn` line are appended to the transcript, then the server observes `hello` (carrying
  `PROTOCOL_VERSION` and the `identity.Load` key) then `ready` then `busy`, each within ~1s
  of its transcript write.
- Given an established connection, when the server closes the socket and a replacement
  `httptest` server accepts, then the client reconnects, re-sends `hello`, and re-sends the
  frame for the current transcript state with no operator action.
- Given `go test -race` over the `scripts/workspace_modules.sh` set, `gofmt -l .`,
  `go vet`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and
  `bash scripts/checks_test.sh` on the finished tree, when they run, then all pass and
  `check_deps.sh` still prints exactly `companion → proto` and `backend → proto`.
- Given `ci.yml`, when the `test` matrix is read, then it lists `macos-latest` alongside
  `ubuntu-latest` and `windows-latest` and no comment still defers the macOS runner to
  Story 1.6.
- Given any diagnostic or status output from the above, when inspected, then it contains
  neither the account key nor transcript content, and nothing is written outside the
  companion's own stdout/stderr.

## Spec Change Log

## Design Notes

- **State, not events, is what the backend needs.** `run` keeps one `desired` state updated
  by every parser event and a `sent` state; the ~250ms debounce coalesces a seed replay or
  a fast burst so only the settled state reaches the wire. After every successful `hello`
  the current `desired` is re-sent unconditionally — that is the whole of "resume from the
  current transcript state" (AD-1: a spoke recovers by reconnect + re-announce).
- **Backoff:** `min(cap, base*2^n)` with full jitter, `base≈500ms`, `cap≈30s`, reset after a
  `hello` write succeeds; retries never stop — fail-open means the companion keeps trying
  quietly and never touches the host session. `please_update` is the sole stop condition and
  it leaves the process running.
- **coder/websocket usage mirrors the backend:** `websocket.Dial`, text frames,
  `proto.Encode` / `Decode`, `Close(StatusNormalClosure, "")` on a clean exit, `CloseNow` on
  the drop path. Reads run on their own goroutine so a write and an inbound `session_ended`
  cannot deadlock (same shape as `server.serveConn`).
- **`ws://127.0.0.1:8080/ws` is a placeholder** until Epic 6 provisions the public instance;
  moving it is Ask-First.

## Verification

**Commands** (repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `run` / `wsclient` / `statusline` suites green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no diff after commit
- `bash scripts/check_deps.sh` — exit 0, edges unchanged (`companion → proto`,
  `backend → proto`)
- `bash scripts/checks_test.sh` — exit 0
- `shellcheck $(git ls-files '*.sh')` — no output
- `go list -m all` in `companion/` — adds only `github.com/coder/websocket`; no other new
  module

**Manual checks:**
- Read `ci.yml`: `macos-latest` in the `test` matrix; the "deferred to Story 1.6" comments
  are gone.
- Skim `README.md`: companion runtime note present; dependency table lists `coder/websocket`.

## Suggested Review Order

**The orchestrator — how a turn boundary becomes a wire frame**

- Entry point: wiring + the derived context that tears the watcher down on every terminal path.
  [`run.go:54`](../../companion/internal/run/run.go#L54)
- The event core: one select over transcript events, watch errors, the debounce timer, and client lifecycle.
  [`run.go:168`](../../companion/internal/run/run.go#L168)
- `arm` — a burst / seed replay coalesces to one flush at `debounceInterval` (250ms).
  [`run.go:137`](../../companion/internal/run/run.go#L137)
- `pushState` — `sent` advances only on a successful write, so a failed send is retried on the next reconnect.
  [`run.go:152`](../../companion/internal/run/run.go#L152)
- `Connected` handler: unconditional re-announce of `desired` after every (re)connect (AD-1).
  [`run.go:248`](../../companion/internal/run/run.go#L248)

**Watch-error handling — transient vs fatal**

- Events-closed branch: a clean context cancel exits 0; a real watcher death returns the wrapped error.
  [`run.go:189`](../../companion/internal/run/run.go#L189)
- A healthy event clears `lastWatchErr` so a recovered-from hiccup is never reported as the fatal cause.
  [`run.go:220`](../../companion/internal/run/run.go#L220)

**The websocket client**

- Connect / hello-first / serve / reconnect loop; `attempt` resets only after the hello write lands.
  [`wsclient.go:120`](../../companion/internal/wsclient/wsclient.go#L120)
- `emit` blocks until `run.loop` takes the event (or ctx cancels) — a `Connected` is never dropped.
  [`wsclient.go:109`](../../companion/internal/wsclient/wsclient.go#L109)
- `serve` reads on its own goroutine; recognises `please_update` / `session_ended`, discards the rest.
  [`wsclient.go:222`](../../companion/internal/wsclient/wsclient.go#L222)
- `backoffDelay` — `min(cap, base·2ⁿ)` with full jitter.
  [`wsclient.go:300`](../../companion/internal/wsclient/wsclient.go#L300)

**Launch surface**

- `run` seam: all arg / identity / exit-code logic, testable; `os.Exit` stays in `main`.
  [`main.go:48`](../../companion/main.go#L48)
- `resolve` — required transcript-path, `ws`/`wss` scheme + host validation, no raw URL in errors.
  [`main.go:100`](../../companion/main.go#L100)

**Status line**

- All user-facing copy in one place — five phases, voice-guide compliant, repeat-suppressed.
  [`statusline.go:37`](../../companion/internal/statusline/statusline.go#L37)

**Peripherals — tests, CI, docs**

- End-to-end: real `Watch` + real `wsclient` over `httptest` — `hello` → `ready` → `busy` on transcript writes.
  [`run_test.go:687`](../../companion/internal/run/run_test.go#L687)
- Regression: a delayed `Events()` drain still gets the post-reconnect re-announce.
  [`run_test.go:290`](../../companion/internal/run/run_test.go#L290)
- Regression: 300 iterations proving a context cancel racing the watcher close always exits 0.
  [`run_test.go:527`](../../companion/internal/run/run_test.go#L527)
- Regression: `attempt` resets after `hello` — inter-hello gaps stay near `backoffBase` across repeated drops.
  [`wsclient_test.go:426`](../../companion/internal/wsclient/wsclient_test.go#L426)
- Regression: 10 lifecycle events over a buffer of 8 with a slow drain — no `Connected` lost.
  [`wsclient_test.go:454`](../../companion/internal/wsclient/wsclient_test.go#L454)
- Output scrub: the account key and transcript content reach neither stdout nor stderr.
  [`run_test.go:841`](../../companion/internal/run/run_test.go#L841)
- Exit codes: wrong arg count → 2, invalid URL → 1, `identity.Load` failure → non-zero with a scrubbed message.
  [`main_test.go:190`](../../companion/main_test.go#L190)
- `resolve` table: env fallback, `/ws` append, scheme/host rejection, empty transcript-path.
  [`main_test.go:13`](../../companion/main_test.go#L13)
- CI `test` matrix gains `macos-latest`; header reframed as standing regression coverage.
  [`ci.yml:30`](../../.github/workflows/ci.yml#L30)
- README Companion "Runtime" note: three launch args, reconnect behaviour, exit codes, key never logged.
  [`README.md:40`](../../README.md#L40)
