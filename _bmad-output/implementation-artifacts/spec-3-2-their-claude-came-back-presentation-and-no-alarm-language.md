---
title: '"Their Claude came back" presentation and no alarm language'
type: 'feature'
created: '2026-09-08'
status: 'done'
review_loop_iteration: 0
baseline_commit: '8d8a145a933613fd789b93690251cf668eef99ca'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
  - '{project-root}/_bmad-output/specs/spec-claudingtin/voice.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Story 3.1 made `session_ended` the one byte-identical frame for every backend-side end (peer leave, peer model-return, peer disconnect, ban, takeover). The bundled companion still treats `session_ended` as terminal — `wsclient.serve` returns `serveSessionEnded`, `Run` returns `ResultSessionEnded`, and `run.loop` exits the process (`run.go:330`). So a peer merely leaving now kills the user's whole think-time-chat presence, and there is no calm "their Claude came back" surface anywhere.

**Approach:** Make an inbound `session_ended` a non-terminal frame that `wsclient` surfaces on a new `SessionEnded()` channel while keeping the socket alive; `run.loop` gets one routing arm that shows a calm, cause-free end state and returns the pane to the searching spinner (still mid-think-time) or to the status line (model is back). A real connection takeover still exits, detected by the server closing the socket within a short `sessionEndedCloseGrace` window after the frame. Audit every companion user-facing string so no end/disconnect copy carries rejection or alarm language.

## Boundaries & Constraints

**Always:**
- One routing point in `run.loop` for every backend-side end: the `client.SessionEnded()` arm. It never branches on *why* the session ended (the frame carries no cause). It tears down the chat surface, then routes by the current `desired` wire state — `stateReady` → searching spinner, `stateBusy` → status line.
- `session_ended` keeps the websocket open (it ends a *chat*, not a *connection*). The companion process exits on `session_ended` only when the server also closes the socket within `sessionEndedCloseGrace` of the frame — that path stays `ResultSessionEnded`, identical to today's takeover exit.
- All companion user-facing copy matches `voice.md`: warm, lowercase-friendly, an easy "catch you later". No end/disconnect string contains "left", "disconnected", "connection lost", "rejected", "blocked", "reported", "are you sure", and no confirm modal is introduced.
- `wsclient` stays silent (no logs, no account-key leak). The `SessionEnded()` channel mirrors `Matched()` / `Queued()`: buffered by one, never closed, non-blocking send.
- `proto.SessionEnded` stays `struct{}` — no new wire type, no cause field.

**Ask First:**
- If routing the pane cleanly seems to need `run.loop` to send any frame on the wire (e.g. a `leave` on the model-back path) — stop. Sending `leave` is Story 3.3; this story only *receives* `session_ended`.

**Never:**
- No reconnect grace window / delayed teardown (Story 3.4). A bare socket drop still tears down / reconnects immediately.
- No silent re-enqueue: the companion does not re-queue itself or synthesise `ready`/`busy` here (Story 3.5, backend-owned). Returning the pane to the spinner is a local pane change only — pre-3.5 that spinner has nothing to resolve it, an accepted interim state.
- No persist toggle and no local-model-return end path (Stories 3.6 / 3.3). Persist ships default-on, so a returning model does not end a chat; AC1's "local model-return with persist off" branch is out of scope until the toggle and `leave` exist. Recorded in `deferred-work.md`.
- No `leave` keybinding or send, no late-peer-`chat_msg` suppression (Story 3.3).
- No `chatui` / `searchui` model changes — the chat pane closes into the status line or the spinner; no in-pane farewell line.
- No ban trigger or `cmd/ban` wiring (Epic 4).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Peer ends, model already back | chat surface up, `desired == stateBusy`; `session_ended` surfaced | chat torn down (resume), status line shows `PhaseClaudeBack`; socket stays open; `loop` keeps running | N/A |
| Peer ends, user still in think-time | chat surface up, `desired == stateReady`; `session_ended` surfaced | chat torn down, searching spinner raised; no rejection/alarm copy; `loop` keeps running | N/A |
| `session_ended` while searching, unmatched | spinner up, `desired == stateReady` | spinner stays up; no flash, nothing on the wire | N/A |
| `session_ended` while idle, no pane | no pane, `desired == stateBusy` | status line shows `PhaseClaudeBack`; `loop` keeps running | N/A |
| Connection takeover / server shutdown | `session_ended`, then server closes the socket within `sessionEndedCloseGrace` | `serve` → `serveSessionEnded` → `Run` → `ResultSessionEnded`; `loop` tears everything down and returns nil (process exits), as today | frame recorded with `endedAt`; a close *after* the grace is an ordinary reconnect |
| Unrelated drop long after a peer end | `session_ended` surfaced, seconds pass, socket drops | ordinary `serveDropped` → `Reconnecting` → re-hello + re-announce; not an exit | grace expired |
| Frame follows the end | `queued` / `matched` arrives after a `session_ended` | delivered normally; `endedAt` cleared so a later drop reconnects | non-blocking sends unchanged |

</frozen-after-approval>

## Code Map

- `companion/internal/wsclient/wsclient.go`
  - package doc `:1-16` and `ResultSessionEnded` doc `:44-51` — "the last two end Run" is now: `please_update` ends Run; `session_ended` is surfaced and ends Run only when the socket closes right after (takeover / shutdown).
  - knobs `:33-38` — add `sessionEndedCloseGrace = 2 * time.Second` (package var, tests shrink it).
  - `Client` struct `:84-97` + `New` `:100-109` — add `sessionEnded chan proto.SessionEnded`, `make(..., 1)`.
  - `Matched()` `:127-135` / `Queued()` `:119-125` — template for a `SessionEnded() <-chan proto.SessionEnded` accessor + doc.
  - `serve` read goroutine `:276-334` — `case proto.SessionEnded:` (currently `:327` `terminal <- serveSessionEnded; return`) → non-blocking send on `c.sessionEnded`, record `endedAt = time.Now()`, keep reading. Read-error branch `:283-289` → if `!endedAt.IsZero() && time.Since(endedAt) < sessionEndedCloseGrace` send `serveSessionEnded`, else existing `readErr`. Clear `endedAt` in the `proto.Queued` / `proto.Matched` cases.
  - `serveResult` / `serveSessionEnded` `:260-268` and `Run` switch `:201-219` — unchanged; `serveSessionEnded` is still the takeover/shutdown path.
- `companion/internal/run/run.go`
  - `stateClient` interface `:141-149` — add `SessionEnded() <-chan proto.SessionEnded`.
  - `loop` select `:313-477` — new arm `case <-client.SessionEnded():` after the `ChatMsgs()` arm. Tear down the chat surface if up, then: `desired == stateReady` → `if !pm.anyActive() { pm.launchSearch() }`; `desired == stateBusy` → `pm.showPhase(statusline.PhaseClaudeBack)`. Model on the `IntentLeave` arm `:454-458` (`pm.stopChat(false)` → repaint).
  - `clientDone` switch, `wsclient.ResultSessionEnded` `:330-334` — keep `pm.stopAll(); return nil`; update the comment to "server closed the socket right after a session_ended (takeover / shutdown)", distinct from the in-band `SessionEnded()` arm.
  - package doc `:1-27` — note the `session_ended` end-presentation routing (was "maps … terminal conditions … another connection took over").
- `companion/internal/statusline/statusline.go`
  - `Phase` consts `:21-41` — add `PhaseClaudeBack` appended **after** `PhaseInert` (keep existing values, per the standing comment).
  - `line()` `:44-61` — add the calm phrase, e.g. `"looks like their claude's back — catch you later"` (no "left", no apology).
- `companion/internal/run/panemanager.go` — reuse `stopChat`, `launchSearch`, `anyActive`, `showPhase` as-is; an optional thin `pm.routeAfterEnd(ready bool)` wrapper is fine if it keeps `loop` readable and gets a `panemanager_test.go` case.
- `companion/internal/run/run_test.go` — `fakeClient` (`:58-181`): add `sessionEnded chan proto.SessionEnded`, `SessionEnded()` accessor (nil-safe like `Queued`), and a `pushSessionEnded()` helper. Existing `endRunWith(wsclient.ResultSessionEnded)` tests (`TestSessionEndedReturnsNil` `:464`, `TestSessionEndedTearsDownChatSurface` `:1558`, `TestSessionEndedTearsDownSpinner` `:2070`, `:1543`, `:2161`) keep their meaning as the takeover/shutdown exit — adjust comments only.
- `companion/internal/run/run_test.go` `TestRunTearsDownWatcherOnSessionEnded` and `companion/main_test.go` `TestRunFullAcceptWritesAck` — both drove a clean `run()` exit via a socket-open `session_ended`; each now has its recorder `Close` the socket right after the frame (the takeover/shutdown pairing) so the exit-0 assertions still hold.
- `companion/internal/wsclient/wsclient_test.go` — existing serve/reconnect harness; model new cases on the current `session_ended` and reconnect tests.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/wsclient/wsclient.go` -- add the `sessionEnded` channel + `SessionEnded()` accessor + `sessionEndedCloseGrace`; in `serve`, surface `session_ended` non-blocking and keep reading, record `endedAt`, and only return `serveSessionEnded` when a read error follows within the grace; clear `endedAt` on `queued`/`matched`; refresh the package + `ResultSessionEnded` docs -- keeps the socket alive for a peer-end while preserving the takeover exit.
- [x] `companion/internal/statusline/statusline.go` -- add `PhaseClaudeBack` (appended last) and its calm, apology-free line -- the shared "their Claude came back" state for the model-back end path.
- [x] `companion/internal/run/run.go` -- add `SessionEnded()` to `stateClient`; add the single `client.SessionEnded()` routing arm (tear down chat, then spinner if `stateReady` else `PhaseClaudeBack`); re-comment the `ResultSessionEnded` clientDone case and the package doc -- one cause-free end presentation, pane routed by think-time state.
- [x] `companion/internal/wsclient/wsclient_test.go` -- cover: `session_ended` surfaced on `SessionEnded()` and serving continues; a `queued`/`matched` after it still delivered; socket close within grace → `ResultSessionEnded`; socket close after grace → `Reconnecting` + re-hello -- pins the terminal/non-terminal split.
- [x] `companion/internal/run/run_test.go` -- extend `fakeClient` with `SessionEnded()`; add: peer-end while `desired == stateBusy` (chat down, `PhaseClaudeBack` written, `loop` still running); peer-end while `desired == stateReady` (chat down, spinner up, `loop` still running); `session_ended` while searching (spinner stays); assert every `statusline` phase line is free of the banned substrings -- covers AC1–AC3 and the copy rule.
- [x] `companion/internal/statusline/statusline_test.go` -- `line(PhaseClaudeBack)` is non-empty and, with every other phase, contains none of "left", "disconnect", "connection lost", "rejected", "blocked", "reported", "are you sure" -- the no-alarm-language guard.
- [x] `_bmad-output/implementation-artifacts/deferred-work.md` -- append one entry: the local-model-return end path (AC1 branch b) is deferred to Stories 3.6 (persist toggle) + 3.3 (`leave`).

**Acceptance Criteria:**
- Given an inbound `session_ended` on a live connection, when `run.loop` handles it, then the same calm state is shown regardless of end cause — no apology, no "are you sure?", no confirm modal — and the websocket stays connected.
- Given `session_ended` is handled while the user's model is still thinking (`desired == stateReady`), when the pane is routed, then the chat surface is torn down and the searching spinner is shown; when the model is back (`desired == stateBusy`), the chat surface is torn down and the status line shows `PhaseClaudeBack`.
- Given `session_ended` is immediately followed by the server closing the socket (within `sessionEndedCloseGrace`), when `wsclient` observes the close, then `Run` returns `ResultSessionEnded` and the companion process exits exactly as it does for a takeover today.
- Given every companion user-facing string, when scanned, then no end/disconnect copy contains "left", "disconnected", "connection lost", "rejected", "blocked", or "reported", and the tone matches `voice.md`.
- Given a `queued` or `matched` frame arrives after a `session_ended`, when `wsclient` processes it, then it is delivered on its channel and a later unrelated socket drop reconnects rather than exiting.

## Spec Change Log

## Design Notes

Why the grace window: the `session_ended` frame is byte-identical for a peer leaving (socket stays open, companion must keep running) and for this connection being taken over (server sends the frame, then closes). The only signal that separates them is whether the close follows the frame promptly. `sessionEndedCloseGrace` (~2s) is generous for the server's synchronous `writeFrame` → `conn.Close` on the `Evict` path yet short enough that a genuine later network blip after a peer-end reconnects normally. Story 3.1's `deferred-work.md` entry (an idle displaced connection getting a bare close with no final frame) becomes reachable once the socket survives a `session_ended`; the grace window contains it — that case reconnects instead of exiting, which is acceptable pre-3.5.

`run.loop` sketch (new arm, after the `ChatMsgs()` case):

```go
case <-client.SessionEnded():
    // One cause-free end presentation. The frame carries no reason; do not
    // branch on one. Socket stays up — a peer left, not the connection.
    pm.stopChat(false)
    if desired == stateReady {
        if !pm.anyActive() {
            pm.launchSearch() // back to looking; 3.5 owns the re-enqueue
        }
    } else {
        pm.showPhase(statusline.PhaseClaudeBack)
    }
```

`PhaseClaudeBack` is appended after `PhaseInert` so the existing constant values are untouched (the statusline file already carries that instruction).

## Verification

**Commands:**
- `cd companion && go test -race ./...` -- expected: all packages pass, including the new wsclient, run, and statusline cases.
- `cd companion && go vet ./...` -- expected: no diagnostics.
- `gofmt -l companion/` -- expected: no output.
- `cd proto && go test ./...` -- expected: pass (no proto change; sanity only).

## Suggested Review Order

**The one cause-free end presentation (start here)**

- Entry point: the single routing arm — tear down the chat, then spinner (still thinking) or the calm line (model back); never branches on why
  [`run.go:454`](../../companion/internal/run/run.go#L454)
- The calm state itself: one line, every cause, no "left" / apology / confirm
  [`statusline.go:66`](../../companion/internal/statusline/statusline.go#L66)
- `PhaseClaudeBack` appended last so existing phase values are untouched
  [`statusline.go:47`](../../companion/internal/statusline/statusline.go#L47)
- Package doc: session_ended is a chat-end, not a connection-end; only a takeover ends loop
  [`run.go:8`](../../companion/internal/run/run.go#L8)

**session_ended stops being terminal, without losing the takeover exit**

- The frame is surfaced and serving continues (was: `return serveSessionEnded`)
  [`wsclient.go:382`](../../companion/internal/wsclient/wsclient.go#L382)
- Takeover still exits: a socket close within the grace after the frame → `serveSessionEnded`; a ctx cancel takes the clean path
  [`wsclient.go:330`](../../companion/internal/wsclient/wsclient.go#L330)
- Any later frame (decodable or not) disarms the takeover window — the socket proved live
  [`wsclient.go:344`](../../companion/internal/wsclient/wsclient.go#L344)
- The grace knob, generous for the backend's synchronous write+close, tight enough that a later blip reconnects
  [`wsclient.go:50`](../../companion/internal/wsclient/wsclient.go#L50)
- New non-blocking, never-closed channel + accessor, mirroring `Matched()` / `Queued()`
  [`wsclient.go:173`](../../companion/internal/wsclient/wsclient.go#L173)
- `ResultSessionEnded` now names the takeover / shutdown close explicitly
  [`run.go:341`](../../companion/internal/run/run.go#L341)

**Seam**

- `stateClient` gains `SessionEnded()`
  [`run.go:158`](../../companion/internal/run/run.go#L158)

**Tests**

- Routing: model back → `PhaseClaudeBack`; idle; mid-burst → spinner; mid-search → spinner untouched; loop keeps running in every case
  [`run_test.go:2197`](../../companion/internal/run/run_test.go#L2197)
- wsclient: surfaced + non-terminal; then-close → `ResultSessionEnded` + no reconnect; a frame between clears the arm (matched & queued); a late drop reconnects
  [`wsclient_test.go:346`](../../companion/internal/wsclient/wsclient_test.go#L346)
- No phase line carries rejection / alarm vocabulary
  [`statusline_test.go:89`](../../companion/internal/statusline/statusline_test.go#L89)
