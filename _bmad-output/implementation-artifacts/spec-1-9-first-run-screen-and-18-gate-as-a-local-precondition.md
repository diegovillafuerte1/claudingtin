---
title: 'Story 1.9 — First-run screen and 18+ gate as a local precondition'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: '0128818b0e3e28403e65c0b89ed596779b7ee0ac'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The companion connects the moment it starts (`run.Run` → `transcript.Watch` +
`wsclient`). Nothing presents the honest safety warning or the 18+ self-attestation the
product requires (PRD FR43–FR46, epic AR25), so a first-time user is queued to strangers
before acknowledging any risk.

**Approach:** Add a local, network-free gate at the top of `run.Run`: a new
`companion/internal/safety` package renders one plain screen (what this is, an honest
strangers/no-identifying-info/screenshots warning, how block and report work, an affirmative
"18 or older" prompt) to the companion's own stdout and reads one line from its own stdin.
Accept ⇒ record acknowledgement in a local file and connect as today. Decline / no input ⇒
show a calm inert line and stay running without connecting; nothing recorded, so the screen
returns next run. `CLAUDINGTIN_SAFETY_REVIEW` reprints the screen and exits without
connecting — the re-access path until a companion menu exists.

## Boundaries & Constraints

**Always:**
- The gate runs inside `run.Run` before `transcript.Watch` and before any `wsclient`
  activity. No websocket dial, no transcript handle, until it clears.
- Acknowledgement lives only at `<configDir>/safety-ack`, written mode `0600` via a sibling
  temp file + `fsretry.Rename` (mirror `identity.regenerate`). Contents: the screen version
  and an accepted-at Unix-millis line — no account key, no other identity. No network call,
  no frame, no attestation is ever sent for it.
- `safety.Accepted` is defensive like `identity.Load`: a missing, empty, malformed, or
  older-`ScreenVersion` file ⇒ not accepted, no error. Only a real read failure
  (e.g. permission denied) ⇒ wrapped error, and it carries no key material.
- Accept requires a deliberate affirmative line: after `strings.TrimSpace` + lowercase, one
  of `y` / `yes` / `i am 18 or older`. Anything else — a different line, empty line, or EOF —
  is a decline. The prompt copy states exactly what to type.
- Outcomes: **accepted / already-accepted** ⇒ proceed to the existing watch+client path.
  **declined** ⇒ `statusline` shows a new inert phase, then block on `ctx.Done()` and return
  nil (exit 0); nothing recorded. **aborted** (`ctx` cancelled while waiting for the line) ⇒
  return nil (exit 0). A `safety-ack` write failure ⇒ return a wrapped error (exit 1).
- All new copy lives in `safety` (screen) and `statusline` (the inert phase) — warm, plain,
  lowercase-friendly, never softens the risk, never names the machinery (no "queue", no
  "disconnect"), per `voice.md`. The screen is phrased so a model reading it takes no action.
- `run.Config` gains `In io.Reader` and `ConfigDir string`; `main` wires `os.Stdin` and the
  already-resolved `configDir`. `run.Run` still defaults `Out`/`Err` (and now `In`) when nil.
- `CLAUDINGTIN_SAFETY_REVIEW` non-empty ⇒ `main.run` prints `safety.Screen()` to stdout and
  returns 0 without loading identity, gating, or connecting.
- stdlib + existing `internal/*` only. `go build` / `go vet` / `go test -race` over
  `scripts/workspace_modules.sh`, `gofmt -l .`, `go work sync` + `git diff --exit-code`,
  `bash scripts/check_deps.sh`, `bash scripts/checks_test.sh` all pass;
  `check_deps.sh` still prints exactly `companion → proto` and `backend → proto`.

**Ask First:**
- Any third-party dependency (bubbletea / bubbles / lipgloss / a prompt library) — the
  screen is plain text this story.
- Changing the companion's three-positional-arg contract, `/proto`, or the backend.
- A real companion menu, or changing `CLAUDINGTIN_SAFETY_REVIEW` to an arg/flag.
- Sending anything about acceptance to the backend, or a per-session re-prompt when already
  accepted.

**Never:**
- No chat UI, no TTY probing / raw-mode input, no second config file, no env var beyond
  `CLAUDINGTIN_SAFETY_REVIEW`.
- No change to `wsclient`, `transcript`, `identity`, `proto`, the backend, the plugin
  launcher, `hooks/*`, `.github/workflows/*`, or any committed binary.
- No self-exit / watchdog for a declined companion (it stays running — the pre-existing
  Epic 1 lifecycle posture); no retry loop on a `safety-ack` write failure.
- Do not reorder or renumber existing `statusline.Phase` constants.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| First run, accepts | no `safety-ack`; stdin line `yes` | screen printed to Out; `safety-ack` written `0600` (version + accepted-at, no key); watch + `wsclient` start; `hello` goes out | — |
| First run, `i am 18 or older` | no `safety-ack`; stdin that exact line (any case/space) | same as accept | — |
| First run, declines | no `safety-ack`; stdin line `no` | screen printed; nothing written; inert status line; process stays up; no dial; exit 0 on signal | — |
| First run, no input | no `safety-ack`; stdin at EOF (detached `/dev/null`) | treated as decline: inert line, no dial, no file, stays up | — |
| Signal during prompt | waiting on the line; SIGINT/SIGTERM | `ctx` cancels; gate returns aborted; `Run` returns nil (exit 0) | — |
| Already accepted | `safety-ack` present, `version == ScreenVersion` | no screen printed; straight to watch + client | — |
| Stale screen version | `safety-ack` present, older version | re-prompt exactly like first run | — |
| Corrupt / empty ack | `safety-ack` unparseable | re-prompt like first run | not an error |
| Ack unreadable | `safety-ack` dir/file permission denied | `Run` returns wrapped error, exit 1, no key in it | wrapped error |
| Review env | `CLAUDINGTIN_SAFETY_REVIEW=1` (any prior state) | `main.run` prints `safety.Screen()`, returns 0; no identity load, no gate, no dial | — |
| Output scrub | any row above | no account key and no transcript/message content on stdout or stderr | — |

</frozen-after-approval>

## Code Map

- `companion/internal/safety/safety.go` — **new.** `ScreenVersion` const (`1`); `Screen()
  string` (all copy here). `Accepted(configDir) (bool, error)` — read `<configDir>/safety-ack`
  via `fsretry.Open`, parse `version <n>` / `accepted_at <ms>`; true only when `n ==
  ScreenVersion`; missing/empty/malformed/older ⇒ `false, nil`; real IO error ⇒ wrapped.
  `Record(configDir) error` — temp file, `Chmod(0o600)`, write the two lines, `Sync`,
  `fsretry.Rename` (model: `identity.regenerate`, `identity.go:223`). `Outcome` enum
  (`Accepted`/`AlreadyAccepted`/`Declined`/`Aborted`). `Gate(ctx, in io.Reader, out
  io.Writer, configDir) (Outcome, error)` — `Accepted` short-circuit; else `Fprint(out,
  Screen())` and read one line on a goroutine (`bufio` `ReadString('\n')`) `select`ed against
  `ctx.Done()`; affirmative ⇒ `Record` + accepted; EOF/other ⇒ declined; ctx first ⇒ aborted.
  No logging; the account key is never in scope in this package.
- `companion/internal/safety/safety_test.go` — **new.** Table tests: `Screen()` contains the
  required elements and none of the forbidden machinery words; `Accepted` across
  missing/valid/older/corrupt/unreadable; `Record`→`Accepted` round-trip + `0600` mode +
  no-key content; `Gate` for accept (`yes`, `y`, phrase, padded/mixed-case), decline (`no`,
  empty line, immediate EOF), already-accepted (nothing written to out), and ctx-cancel ⇒
  `OutcomeAborted`.
- `companion/internal/run/run.go` — **edit.** `Config` gains `In io.Reader`, `ConfigDir
  string`; default `In` to `os.Stdin` alongside the existing `Out`/`Err` defaults
  (`run.go:55`). After `ctx, cancel := context.WithCancel(ctx)` (`run.go:66`) and before
  `transcript.Watch` (`run.go:69`): `switch safety.Gate(ctx, cfg.In, cfg.Out,
  cfg.ConfigDir)` — accepted/already ⇒ fall through unchanged; declined ⇒
  `statusline.New(cfg.Out).Show(<inert phase>)`, `<-ctx.Done()`, `return nil`; aborted ⇒
  `return nil`; error ⇒ `return fmt.Errorf("first-run gate: %w", err)`. `loop` and
  `stateClient` unchanged.
- `companion/internal/run/run_test.go` — **edit.** New `Run`-level tests: `In` =`"yes\n"` +
  temp `ConfigDir` ⇒ `hello` observed; `In` = empty reader + temp `ConfigDir` ⇒ no `hello`,
  inert line on Out, `Run` returns nil after cancel; pre-written `safety-ack` ⇒ connects with
  no screen text on Out. Update `TestRunEndToEnd_HelloThenReadyThenBusy` (`:687`),
  `TestRunEndToEnd_ReconnectReannounces` (`:739`), `TestRunTearsDownWatcherOnSessionEnded`
  (`:789`), and any other `Run(ctx, Config{...})` call site to set `ConfigDir` to a temp dir
  with a pre-recorded ack (or `In: strings.NewReader("yes\n")`). `loop`-level tests
  (`newFakeClient`, `:238`+) need no change.
- `companion/internal/statusline/statusline.go` — **edit.** Append one `Phase` constant
  (e.g. `PhaseInert`) after `PhaseUpdateNeeded` (`:33`) — do not reorder — and its `line`
  case (`:37`): a calm, no-guilt line, e.g. `"nothing's connected — you can turn this on
  whenever you like"`.
- `companion/internal/statusline/statusline_test.go` — **edit.** Add the new phase to the
  every-phase table and the repeated-phase no-op check.
- `companion/main.go` — **edit.** `main()` passes `os.Stdin`; `run` signature gains `stdin
  io.Reader` (`main.go:48`). At the very top of `run`, before `resolve` (so it needs no
  valid args): `if getenv("CLAUDINGTIN_SAFETY_REVIEW") != "" { fmt.Fprint(stdout,
  safety.Screen()); return 0 }`. After `configDir` is resolved and `key` loaded, set
  `cfg.In = stdin` and `cfg.ConfigDir = configDir` before `runner.Run`. Refresh the package
  doc comment: name the first-run gate, the review env var, and that a decline exits 0 once
  the process is signalled.
- `companion/main_test.go` — **edit.** Thread the new `stdin` arg through every `run(...)`
  call (`TestRunExitCodes`, `:190`). Add a `CLAUDINGTIN_SAFETY_REVIEW=1` case: `run` prints
  the screen and returns 0 with an unreachable server URL. `resolve` tests unchanged.
- `companion/internal/identity/identity.go`, `.../fsretry/fsretry.go` — **read-only.**
  `fsretry.Open` / `fsretry.Rename` reused; `identity.regenerate` is the temp+rename model.
- `companion/internal/wsclient/*`, `.../transcript/*`, `proto/*` — **read-only.** Untouched;
  the gate sits entirely above them.
- `_bmad-output/specs/spec-claudingtin/voice.md` — **read-only.** First-run + safety copy
  rules the screen and the inert line must follow.
- `README.md` — **edit.** Companion section: a short "First run" note after "Runtime" —
  the local 18+/safety screen gates the connection, acceptance is stored at
  `<configDir>/safety-ack` and never sent, declining leaves it inert and re-prompts, and
  `CLAUDINGTIN_SAFETY_REVIEW=1 companion <transcript> "" ""` reprints it. Add "local
  first-run 18+/safety gate" to the `companion` module-table cell.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/safety/safety.go` — `ScreenVersion`, `Screen()`, `Accepted`,
  `Record` (temp + `fsretry.Rename`, `0600`), `Outcome`, `Gate` (screen + one-line stdin
  read raced against `ctx`).
- [x] `companion/internal/safety/safety_test.go` — the table tests listed in the Code Map;
  assert no account key path is even reachable and no forbidden machinery words in `Screen()`.
- [x] `companion/internal/statusline/statusline.go` + `_test.go` — append `PhaseInert` and
  its copy; extend the phase tables.
- [x] `companion/internal/run/run.go` — `Config.In` / `Config.ConfigDir`, `In` default, the
  gate `switch` before `transcript.Watch`; `loop` untouched.
- [x] `companion/internal/run/run_test.go` — new gate tests (accept / decline / pre-accepted)
  and fix the existing `Run`-level end-to-end tests to satisfy the gate.
- [x] `companion/main.go` + `companion/main_test.go` — `os.Stdin` wiring, `stdin` param,
  `CLAUDINGTIN_SAFETY_REVIEW` short-circuit, `cfg.In` / `cfg.ConfigDir`, doc comment; thread
  `stdin` through the tests and add the review-env case.
- [x] `README.md` — Companion "First run" note + module-table cell.

**Acceptance Criteria:**
- Given a temp config dir with no `safety-ack`, a temp transcript file, an `httptest`
  `coder/websocket` server, and `Config.In` delivering `"yes\n"`, when `run.Run` executes,
  then the screen text reaches `Out`, `<configDir>/safety-ack` exists mode `0600` containing
  the screen version and an accepted-at millis line and no account key, and the server then
  observes `hello`.
- Given the same setup but `Config.In` at EOF, when `run.Run` executes, then no connection is
  dialed, `Out` shows the inert status line, `safety-ack` is not created, and `Run` returns
  nil after the context is cancelled.
- Given `<configDir>/safety-ack` already recording the current screen version, when `run.Run`
  executes, then no screen text is written to `Out` and `hello` is sent without any stdin
  input.
- Given `CLAUDINGTIN_SAFETY_REVIEW=1` and a deliberately unreachable server URL, when
  `main.run` executes, then it prints `safety.Screen()` to stdout and returns 0 without
  loading the account key or dialing.
- Given the finished tree, when `go build` / `go vet` / `go test -race` over
  `scripts/workspace_modules.sh`, `gofmt -l .`, `go work sync` + `git diff --exit-code`,
  `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` run, then all pass and
  `check_deps.sh` still prints exactly `companion → proto` and `backend → proto`.
- Given any stdout/stderr from the above, when inspected, then it contains neither the
  account key nor transcript/message content.

## Spec Change Log

## Design Notes

- **Gate in `run.Run`, not `main`.** `Run` owns the cancellable context and the
  terminal-condition mapping already; the gate belongs with it, and the existing `run` test
  rig can drive accept/decline/abort. `main` keeps only the `CLAUDINGTIN_SAFETY_REVIEW`
  short-circuit — a launch-surface concern like arg parsing, needing no identity or context.
- **No TTY probing.** `Screen()` to the companion's own stdout is harmless on the detached
  path (`/dev/null`) and visible in the tmux pane (a real PTY, Story 1.8). The one-line stdin
  read blocks in the pane until the user answers — that *is* "does not open the websocket
  until the screen is cleared" — and hits EOF at once on `/dev/null`, which we treat as a
  decline. One code path, no platform branch. A no-tmux user clears the gate once in a
  manually run instance (the Story 1.8 hint); the recorded ack then frees every later
  detached instance to connect.
- **Declined stays running** per the epic AC. On the detached path that is an idle process
  per un-accepted session until the session ends — consistent with Epic 1 having no companion
  self-exit watchdog (Story 1.6 deferred it). Noted so review does not re-flag it.
- **`ctx` vs. a blocked stdin read.** `os.Stdin.Read` is not context-cancellable, so the read
  runs on a goroutine feeding a channel that `Gate` selects against `ctx.Done()`; on abort
  the goroutine leaks until stdin closes — fine for a one-shot at a startup that is exiting.
- **`ScreenVersion`** re-prompts already-accepted users after a material copy change:
  `Accepted` is false when the stored version is lower. Bump it in the same change as the copy.
- **`safety-ack`** is two lenient plain lines (`version 1` / `accepted_at <ms>`); any
  deviation re-prompts, never errors. Written temp + `fsretry.Rename`, mode `0600`, exactly
  like `identity.regenerate`.

## Verification

**Commands** (repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `safety` suite green; updated `run` / `statusline` /
  `main` suites green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no diff (no new module)
- `bash scripts/check_deps.sh` — exit 0, prints exactly `companion → proto` and
  `backend → proto`
- `bash scripts/checks_test.sh` — exit 0
- `shellcheck $(git ls-files '*.sh')` — no output (no shell changes)
- `git diff --stat 0128818b0e3e28403e65c0b89ed596779b7ee0ac -- proto backend plugin .github/workflows`
  — empty

**Manual checks:**
- Read `companion/internal/safety/safety.go`: `Screen()` covers what-it-is, the honest
  strangers / no identifying-location-financial / screenshots-and-quoting warning, how block
  and report work, and the affirmative 18+ prompt; tone matches `voice.md`; no account key
  referenced anywhere in the package.
- Skim `README.md`: Companion "First run" note present; module-table cell updated.

## Suggested Review Order

**The gate — how the screen becomes a connect / no-connect decision**

- Entry point: the whole first-run policy in one linear function — `Accepted` short-circuit, print, one stdin line raced against `ctx`.
  [`safety.go:230`](../../companion/internal/safety/safety.go#L230)
- Fail-closed on entry: an already-cancelled context returns `OutcomeAborted` before the consent screen is ever printed.
  [`safety.go:233`](../../companion/internal/safety/safety.go#L233)
- The four outcomes and their contract — accepted / already-accepted / declined / aborted.
  [`safety.go:91`](../../companion/internal/safety/safety.go#L91)
- The affirmative set: `y` / `yes` / `i am 18 or older`, trimmed + lowercased; everything else declines.
  [`safety.go:212`](../../companion/internal/safety/safety.go#L212)

**Wiring the gate into the run**

- The gate runs before `transcript.Watch` and any dial; the `switch` is explicit and `default`s to a fail-closed error.
  [`run.go:87`](../../companion/internal/run/run.go#L87)
- `ConfigDir` is a hard requirement — an empty one returns an error rather than touching a cwd-relative `safety-ack`.
  [`run.go:74`](../../companion/internal/run/run.go#L74)
- Declined ⇒ one calm inert line, then block until the session ends; nothing recorded, so the screen returns next run.
  [`run.go:92`](../../companion/internal/run/run.go#L92)

**Local persistence — acknowledgement, never an attestation**

- `Accepted` is lenient like `identity.Load`: missing / empty / malformed / older-version ⇒ `(false, nil)`; only a real IO error is wrapped.
  [`safety.go:110`](../../companion/internal/safety/safety.go#L110)
- `Record` writes two plain lines via temp-file + `fsretry.Rename`, mode `0600`, no key material.
  [`safety.go:163`](../../companion/internal/safety/safety.go#L163)
- `ScreenVersion` — bump it with any material copy change so an already-accepted user re-sees the screen once.
  [`safety.go:42`](../../companion/internal/safety/safety.go#L42)

**Launch surface**

- `CLAUDINGTIN_SAFETY_REVIEW` reprints the screen and exits 0 before arg-parsing / identity / gate / network — the re-access path until a menu exists.
  [`main.go:65`](../../companion/main.go#L65)
- `main` wires `os.Stdin` and the resolved `configDir` into `run.Config`.
  [`main.go:94`](../../companion/main.go#L94)

**Status line**

- One appended phase, `PhaseInert`, with no-guilt copy; earlier constants keep their values.
  [`statusline.go:38`](../../companion/internal/statusline/statusline.go#L38)

**The screen copy**

- All first-run copy in one place — warm, plain, never softens the risk, never names the machinery (per `voice.md`).
  [`safety.go:64`](../../companion/internal/safety/safety.go#L64)

**Peripherals — tests, docs**

- `safety` suite: accept variants, decline variants, already-accepted, both abort paths, unreadable-dir + `Record`-failure errors, screen-content + no-key asserts.
  [`safety_test.go:188`](../../companion/internal/safety/safety_test.go#L188)
- `run` suite: accept-connects, decline-stays-inert, pre-accepted-silent, gate-error-propagates, abort-returns-nil.
  [`run_test.go:949`](../../companion/internal/run/run_test.go#L949)
- `main` suite: full accept writes the ack end-to-end; `CLAUDINGTIN_SAFETY_REVIEW` works with no args.
  [`main_test.go:303`](../../companion/main_test.go#L303)
- `statusline` suite: the new phase in the every-phase table + a repeat-no-op.
  [`statusline_test.go:19`](../../companion/internal/statusline/statusline_test.go#L19)
- README: Companion "First run" section, module-table cell, exit-code wording.
  [`README.md:66`](../../README.md#L66)
