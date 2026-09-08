---
title: 'Epic 2 retro item 10 — extract paneManager from run.loop'
type: 'refactor'
created: '2026-09-08'
status: 'done'
review_loop_iteration: 0
context: []
baseline_commit: '6e96bf773aeedf345b8f8cd865e306cbf7e9aaae'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Epic 2 retro F4 — `run.loop` is one ~370-line function that every epic
grows — and F5 — `launchChat`/`stopChat` and `launchSearch`/`stopSearch` are
near-verbatim copies. Epic 3 is about to fold `session_ended` routing, silent
re-enqueue, and reconnect-grace into this same function; the retro asks for the
pane lifecycle to be lifted out first (action item 10, owner Diego, `open`).

**Approach:** Move the chat + searching-spinner pane state and its
`launchChat`/`stopChat`/`launchSearch`/`stopSearch`/`showPhase`/`chatLog`
closures out of `loop` into a new `paneManager` type in
`companion/internal/run/panemanager.go`, backed by one shared launch helper and
one shared teardown helper (the F5 fold). `loop` keeps its `for { select }`
structure and delegates to `pm.*`. Pure refactor: no observable behaviour
changes, and the existing `run_test.go` behaviour suite passes with zero edits as
the regression guard.

## Boundaries & Constraints

**Always:**
- Pure refactor. `go test ./...` and `go test -race ./...` stay green at the repo
  root **with `companion/internal/run/run_test.go` unmodified**.
- `loop` keeps its single `for { select { ... } }`, every `select` case in the
  same order, and every observable behaviour byte-for-byte: same wire frames,
  same status-line phases, same content-free breadcrumbs, same teardown ordering
  (spinner stop + chat launch stay in the one `matched` case).
- `paneManager` lives only in the new `companion/internal/run/panemanager.go`
  (package `run`). The `chat*` / `search*` pane state and the seven named
  closures move onto it and nowhere else.
- One shared launch helper and one shared teardown helper back both chat and
  search (F5). The teardown helper keeps the exact escalation used today:
  `Quit()` → wait `done` or `chatQuitGrace` → `Kill()` → bounded second wait →
  proceed regardless.
- The per-launch `notify`/`send` closures still capture the freshly-made channel
  instance (not the `paneManager` field), so a stale chat program's callback
  after a relaunch writes a dead channel and its non-blocking `select`/`default`
  drops it — unchanged.
- Channels `loop`'s `select` reads (`chatDone`, `searchDone`, `chatIntents`,
  `chatSends`) are exposed as methods that just return the underlying field, nil
  when the pane is down, preserving the dormant-case behaviour.
- `chatQuitGrace`, `newChatProgram`, `newChatModel`, `newSearchProgram` stay
  package vars with unchanged signatures (run_test.go stubs them). The
  `chatProgram` / `searchProgram` interfaces stay; both already expose
  `Quit()` / `Kill()`.
- New `panemanager_test.go` unit-tests the manager directly, reusing the existing
  `fakeChatProgram` / `fakeSearchProgram` / `stubChatProgram` / `stubSearchProgram`
  / `lockedBuffer` helpers.
- `gofmt -l .`, `go vet ./...`, `bash scripts/check_deps.sh` clean; dependency
  direction (`companion → proto` only) unchanged.

**Ask First:**
- If exact behaviour preservation forces any observable change (frame ordering,
  a status-line phase, a breadcrumb, teardown timing) — HALT and confirm before
  diverging.
- Moving `pushState` / `flush` / `arm` / the debounce timer onto `paneManager` —
  the plan leaves them in `loop` (they drive the ready/busy wire send, not the
  pane). If the split feels wrong mid-implementation, confirm before widening it.

**Never:**
- No behaviour change of any kind — not the F6 `chatLog` / `chat.Send` routing,
  not the non-blocking `notify` / `send`, not the `matched`-case ordering, not
  the resume-phase repaint.
- No changes to `chatui`, `searchui`, `statusline`, `wsclient`, `transcript`,
  `proto`, or any file outside `companion/internal/run/`.
- No new proto frames, no new package, no new third-party dependency.
- Do not rename `loop` or `Run`, do not touch `Config`, do not reorder `select`
  cases, do not "improve" the grace / Kill-escalation model while moving it.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Chat launch on `matched` | no pane active, `matched` frame | `pm.launchChat` builds via the `newChatProgram` seam; `chatActive()` true; `chatDone()` / `intents()` / `sends()` non-nil; any spinner torn down first, same select-case | n/a |
| Chat clean teardown | chat active, `stopChat(false)` | `Quit()` called, waits `chatDone`, no `Kill()`; `chat` / `chatIntents` / `chatSends` nil; `chatActive()` false; no status-line write | program ignores `Quit()` → `Kill()` after `chatQuitGrace`, bounded second wait, teardown completes regardless |
| Chat teardown with resume | chat active, `stopChat(true)` | after teardown, `sl.Show(resumePhase())` — the phase for `loop`'s current `desired` | n/a |
| `showPhase` while a pane owns the screen | `chatActive()` or `searchActive()` true | `showPhase(p)` writes nothing | n/a |
| `showPhase` with no pane up | both inactive | `sl.Show(p)` writes the line | n/a |
| Operator breadcrumb, surface up | chat active, `pm.log(line)` | `chat.Send(chatui.LogLine{line})`; nothing to `cfg.Err` | surface just torn down → line dropped (its frame is gone) |
| Operator breadcrumb, no surface | chat inactive, `pm.log(line)` | `fmt.Fprintln(cfg.Err, line)` | n/a |
| Inbound peer line after teardown | chat inactive, `pm.sendPeer(id, text)` | no-op, no panic, nothing to `cfg.Out` | n/a |
| Spinner launch / teardown | `queued` while ready+unmatched / burst end / terminal path | mirrors chat through the shared launch + teardown helpers; `searchActive()` tracks it; resume repaints when asked | same `Quit()` → grace → `Kill()` escalation as chat |
| Terminal path | `ctx.Done()` or any `clientDone` result | `pm.stopAll()` tears down whichever pane is up with `resume:false` | safe no-op when neither is active |
| Stale chat program callback | old program's `notify` / `send` fires after a relaunch | write lands on the captured old channel, never the current `chatIntents` / `chatSends` | non-blocking `select` / `default` drops it |

</frozen-after-approval>

## Code Map

- `companion/internal/run/run.go` — `loop` (currently ~L241–615). Moves out to the
  new file: the pane state var block (~L260–275), `showPhase` (~L280–285),
  `launchChat` (~L287–319), `stopChat` (~L323–347), `chatLog` (~L356–362),
  `launchSearch` (~L368–376), `stopSearch` (~L381–401). `loop` keeps `pushState`
  / `flush` / `arm` / the debounce timer and every `select` case, rewired to
  `pm.*`. The `matched` case (~L557–566) keeps spinner-stop + chat-launch
  together. Seam vars `newChatProgram` / `newChatModel` / `newSearchProgram`
  (~L170–212), `chatQuitGrace` (~L187), interfaces `chatProgram` / `searchProgram`
  (~L155–198) stay put — both interfaces already expose `Quit()` / `Kill()` so
  each satisfies the new `teardownable` with no churn.
- `companion/internal/run/panemanager.go` — NEW. `pane` struct (`active bool`,
  `done chan struct{}`); `teardownable interface { Quit(); Kill() }`;
  `pane.start(run func())` (set active, make `done`, `go func(){ run(); close(done) }()`);
  `pane.teardown(prog teardownable) bool` (the F5 escalation; returns whether it
  tore anything down); `paneManager` struct holding `ctx` / `cfg` / `sl` /
  `resumePhase func() statusline.Phase` plus `chat` + `chatPane` + `chatIntents`
  + `chatSends` and `search` + `searchPane`; `newPaneManager(ctx, cfg, sl, resumePhase)`;
  methods `chatActive` / `searchActive` / `anyActive`, `showPhase`, `launchChat` /
  `stopChat`, `launchSearch` / `stopSearch`, `log`, `sendPeer`, the
  `chatDone` / `searchDone` / `intents` / `sends` accessors, `stopAll`.
- `companion/internal/run/run_test.go` — READ-ONLY. 40+ behaviour tests over
  `loop` via `startLoop`, `stubChatProgram` / `stubSearchProgram`, `waitChat` /
  `waitSearch`, `readyThenQueued`. The regression guard: must pass with zero
  edits. Helpers reused by the new test file (same package): `fakeChatProgram`
  (~L1256), `fakeSearchProgram` (~L1818), `stubChatProgram` (~L1346),
  `stubSearchProgram` (~L1880), `lockedBuffer` (~L192).
- `companion/internal/run/panemanager_test.go` — NEW. Direct `paneManager` unit
  tests — one per Matrix row plus a table test proving chat and search share the
  one teardown path.
- `companion/internal/chatui/chatui.go` — READ-ONLY reference: `Intent`,
  `OutboundMsg` (L122), `PeerMsg` (L113), `LogLine` (L134), `WithNotify` /
  `WithSend`.
- `companion/internal/statusline/statusline.go` — READ-ONLY: `Renderer`, `Phase`,
  `New(io.Writer)`, `(*Renderer).Show(Phase)`.
- `_bmad-output/implementation-artifacts/epic-2-retro-2026-09-07.md` — F4 / F5 +
  action-items table row 2. Reconciliation target: append a "Resolved" note and
  flip the row.
- `_bmad-output/implementation-artifacts/sprint-status.yaml` —
  `epic-2-retro-item-10-f4-f5-…` `open` → `done` with a `ref` to this spec; bump
  `last_updated`.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/run/panemanager.go` — NEW. Add `pane`, `teardownable`,
  `pane.start`, `pane.teardown`, `paneManager` + `newPaneManager`, and all
  pane-lifecycle methods, moved behaviour-preserving from `loop`'s closures. One
  shared launch helper (`pane.start`) and one shared teardown helper
  (`pane.teardown`, holding the `Quit()` → `chatQuitGrace` → `Kill()` →
  bounded-wait escalation) back both chat and search — the F5 fold. Package and
  method doc comments explain the F4 / F5 origin and the nil-channel accessor
  contract.
- [x] `companion/internal/run/run.go` — delete the moved var block + seven
  closures from `loop`; construct
  `pm := newPaneManager(ctx, cfg, sl, func() statusline.Phase { return phaseFor(desired) })`;
  rewire every `select` case and `flush` to `pm.*` (`showPhase` → `pm.showPhase`,
  `searchActive` → `pm.searchActive()`, `!chatActive && !searchActive` →
  `!pm.anyActive()`, `!chatActive` → `!pm.chatActive()`, `chatDone` /
  `searchDone` / `chatIntents` / `chatSends` receives → `pm.chatDone()` /
  `pm.searchDone()` / `pm.intents()` / `pm.sends()`, direct `chat.Send(PeerMsg)`
  → `pm.sendPeer`, `stopSearch(false); stopChat(false)` pairs → `pm.stopAll()`).
  No case reordered, no observable change. Update `loop`'s and the package doc
  comment to point pane ownership at `paneManager`.
- [x] `companion/internal/run/panemanager_test.go` — NEW. Unit-test every Matrix
  row against a directly-constructed `paneManager`, reusing `stubChatProgram` /
  `stubSearchProgram` / `fakeChatProgram` / `fakeSearchProgram` / `lockedBuffer`.
  Include a table test asserting chat and search run through the one
  `pane.teardown` (clean-Quit path and Quit→grace→Kill path both).
- [x] `_bmad-output/implementation-artifacts/epic-2-retro-2026-09-07.md` — F4 and
  F5: append a "Resolved (2026-09-08)" line each; action-items table row 2 →
  done, pointing at this spec.
- [x] `_bmad-output/implementation-artifacts/sprint-status.yaml` —
  `epic-2-retro-item-10-f4-f5-…` `open` → `done` with a `ref` to this spec; bump
  `last_updated`.

**Acceptance Criteria:**
- Given `companion/internal/run/run_test.go` unchanged, when `go test -race ./...`
  runs at the repo root, then every package passes both with and without `-race`
  — proving no behaviour changed.
- Given `loop` after the change, when it is read, then it holds no chat/search
  pane state variables and none of the `launchChat` / `stopChat` / `launchSearch`
  / `stopSearch` / `showPhase` / `chatLog` closures — they exist only on
  `paneManager` in `panemanager.go`.
- Given the chat and searching-spinner surfaces, when either is torn down, then
  both go through the one shared `pane.teardown` (`Quit()` → `chatQuitGrace` →
  `Kill()` → bounded wait), and both launches go through the one shared
  `pane.start`.
- Given `panemanager_test.go`, when `go test ./companion/internal/run/` runs, then
  the new direct `paneManager` tests pass alongside the untouched `loop` tests.
- Given `go build ./...`, `go vet ./...`, `gofmt -l .`, `bash scripts/check_deps.sh`,
  then all are clean and the dependency direction (`companion → proto` only) is
  unchanged.

## Design Notes

**Why `paneManager` and nothing more.** F4 (loop size) and F5 (launch/stop
duplication) share one fix: lift the two pane surfaces behind one type.
`pushState` / `flush` / `arm` / the debounce timer stay in `loop` — they drive
the ready/busy wire send, not the pane. The retro names exactly the chat+search
launch/stop/teardown, `showPhase`, and the `*Done` arms; that is the cut line.

**The teardown fold (F5).** Both `stopChat` and `stopSearch` today are: bail if
inactive → `Quit()` → wait `done` or `chatQuitGrace` → `Kill()` → bounded second
wait → clear state → optional resume repaint. The only real differences are the
program's static type and that chat also nils two channels. `pane.teardown(prog
teardownable) bool` holds the escalation and the active-guard; each `stop*`
method calls it, then nils its own handle (and, for chat, `chatIntents` /
`chatSends`) and repaints on resume. `chatProgram` and `searchProgram` already
have `Quit()` / `Kill()`, so both satisfy `teardownable` with no interface
change.

```go
func (p *pane) teardown(prog teardownable) bool {
	if !p.active {
		return false
	}
	prog.Quit()
	select {
	case <-p.done:
	case <-time.After(chatQuitGrace):
		prog.Kill()
		select {
		case <-p.done:
		case <-time.After(chatQuitGrace):
		}
	}
	p.active = false
	p.done = nil
	return true
}
```

**nil-channel accessors.** `loop`'s `select` must stay dormant on `chatDone` /
`searchDone` / `chatIntents` / `chatSends` while no pane is up. The manager keeps
them as fields, zeroed on teardown, and exposes them via methods that return the
field verbatim — a nil-channel receive, exactly as today. No behaviour rides on
the method call itself; it is re-evaluated each `select` pass just as the bare
variable was.

**resumePhase indirection.** `stopChat` / `stopSearch` repaint
`phaseFor(desired)` on resume, and `desired` is `loop`'s mutable local. The
manager takes a `func() statusline.Phase` set once at construction
(`func() statusline.Phase { return phaseFor(desired) }`) and calls it at teardown
time so it reads the current `desired`. This matches the current direct
`sl.Show(phaseFor(desired))` in `stopChat` (ungated by `showPhase`, because the
pane it belonged to is already down).

**Stale-program safety is preserved verbatim.** `launchChat` still builds
`notify` / `send` closing over the freshly-made `ci` / `cs` locals, not
`pm.chatIntents` / `pm.chatSends`, so a lagging previous program that fires a
callback after a relaunch writes a dead channel and the non-blocking
`select` / `default` drops it.

## Verification

**Commands:**
- `cd companion && go test ./internal/run/` — expected: all pass, incl. new
  `panemanager_test.go`, with `run_test.go` unedited.
- `go test -race ./...` (repo root) — expected: every module green.
- `go build ./... && go vet ./... && gofmt -l .` (repo root) — expected: clean,
  no `gofmt -l` output.
- `bash scripts/check_deps.sh` — expected: OK; `companion → proto` /
  `backend → proto` only.
- `git diff --stat companion/internal/run/run_test.go` — expected: empty (the
  regression guard is untouched).

## Spec Change Log

_No spec loopback. Adversarial review (blind-hunter / edge-case-hunter /
verification-gap) found no intent_gap or bad_spec — verification-gap returned "No
verification gaps found" and confirmed the extraction is behaviour-neutral._

**Review patches applied (additive only, no behaviour change, `run_test.go`
untouched):** (1) a doc comment on the `paneManager.ctx` field explaining it is
carried from construction for the `newChatProgram(pm.ctx, …)` /
`newSearchProgram(pm.ctx, …)` seam calls; (2) `TestPaneManagerResumePhaseThunkEvaluatedAtTeardown`
pinning the thunk's late evaluation; (3) `TestPaneManagerStopWithResumeOnIdleIsNoop`
covering the `stopChat(true)` / `stopSearch(true)` idle no-op path.

**Deferred (not caused by this change):** `TestIntentRacingChatProgramExitDoesNotWedgeLoop`
in the untouched `run_test.go` is a pre-existing `-race` timing flake (~2/6
full-suite, passes in isolation; reproduced on `origin/main` with the diff
stashed). Logged in `deferred-work.md`.

## Suggested Review Order

**Design intent — what moved and why**

- Entry point: `loop` now constructs one `paneManager` and delegates; the thunk closes over `loop`'s live `desired`.
  [`run.go:271`](../../companion/internal/run/run.go#L271)
- The type that absorbed the seven closures + the chat/search state.
  [`panemanager.go:92`](../../companion/internal/run/panemanager.go#L92)

**The F5 duplication fold**

- One launch helper: set active, make `done`, run on a goroutine that closes the captured `done`.
  [`panemanager.go:51`](../../companion/internal/run/panemanager.go#L51)
- One teardown helper for both surfaces: the exact `Quit → chatQuitGrace → Kill → bounded wait` escalation, lifted verbatim.
  [`panemanager.go:68`](../../companion/internal/run/panemanager.go#L68)
- `chatProgram` / `searchProgram` both already expose `Quit()` / `Kill()`, so one interface serves both.
  [`panemanager.go:34`](../../companion/internal/run/panemanager.go#L34)

**Behaviour-preservation seams**

- `stopChat` / `stopSearch`: shared teardown, then nil the handle (and, for chat, the two channels) and repaint on resume — ungated, as before.
  [`panemanager.go:175`](../../companion/internal/run/panemanager.go#L175)
- `showPhase` gate: unchanged `chatActive || searchActive` semantics, now `anyActive()`.
  [`panemanager.go:131`](../../companion/internal/run/panemanager.go#L131)
- nil-channel accessors: return the field verbatim so `loop`'s `select` stays dormant exactly as the bare var did.
  [`panemanager.go:246`](../../companion/internal/run/panemanager.go#L246)
- The `matched` case keeps spinner-stop + chat-launch in one select-case — buffered `chat_msg` waits, not drops.
  [`run.go:422`](../../companion/internal/run/run.go#L422)

**Tests (supporting)**

- Both surfaces run through the one `pane.teardown` — clean-Quit and Quit→grace→Kill branches.
  [`panemanager_test.go:350`](../../companion/internal/run/panemanager_test.go#L350)
- The `resumePhase` thunk is evaluated at teardown time, not captured at construction.
  [`panemanager_test.go:98`](../../companion/internal/run/panemanager_test.go#L98)
- A stale program's callback lands on the channel instance it captured at launch, never the current one.
  [`panemanager_test.go:319`](../../companion/internal/run/panemanager_test.go#L319)
