---
title: 'Story 2.5 — Searching and matched transition, with the spin'
type: 'feature'
created: '2026-09-07'
status: 'done'
review_loop_iteration: 0
baseline_commit: '622bef4f4c7a50fd7691c32cff5d38948ff74a78'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-2-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The backend already emits `queued` when a `ready` session is left waiting, and `run.loop` opens the chat surface the instant `matched` arrives — but the companion ignores `queued` entirely (a static status-line phrase covers the wait) and there is no searching→matched flourish. The waiting moment has no spinner and the match has no "roulette landing."

**Approach:** Surface the inbound `queued` frame on a new `wsclient.Queued()` channel. `run.loop` launches a small Bubble Tea searching spinner (new `internal/searchui`) on `queued` and tears it down on `matched`, turn-end, or any terminal path. `matched` then opens the existing chat surface, which now plays a bounded, non-blocking intro "spin" flourish before settling into its steady four-region view — input focused throughout, inbound peer lines still captured while it plays.

## Boundaries & Constraints

**Always:**
- The spinner is driven by the inbound `queued` frame only — not by `ready`/`busy` state. A match that pairs immediately (no `queued` sent) opens the chat surface with no spinner flash.
- The searching pane shows **one calm line plus a spinner and nothing else**: no queue position, no ETA, no "N online" count, no nudge. All copy is `voice.md` — warm, lowercase, never names the machinery ("looking for someone", not "in the queue").
- While the spinner or the chat surface owns the pane, the status line writes nothing (extend the existing `chatActive` suppression to cover `searchActive`).
- On `matched`, `run.loop` stops the spinner and launches the chat surface **within the one select-case** — no other frame is processed between, so `chat_msg` frames buffered in `wsclient` wait rather than drop.
- The spin flourish is bounded (well under ~1s total), non-blocking, and lives entirely in `chatui`'s render path. It never gates key routing: `textarea.Focus()` in `chatui.New` already holds, and `Update` routes `KeyPressMsg` to the input regardless of flourish state.
- A `PeerMsg` (relayed `chat_msg`) that arrives while the flourish is still playing is appended to history normally — the flourish state only affects `View`, not message handling. A second `matched` while the surface is up is still ignored (unchanged Story 2.3 behaviour).
- The spinner disappears immediately when the queue resolves (`matched`) or the burst ends (`desired` → busy): `stopSearch` in the `matched` case and in the debounce `flush`.
- `wsclient` surfacing `queued`: buffered-by-one channel, never closed, non-terminal (serve keeps reading after it), non-blocking send, drop-on-full acceptable (a redelivered `queued` after reconnect is a no-op once the spinner is up).
- No message content, account key, or chat text reaches disk or logs from any new code. `searchui` logs nothing; the new `run.loop` arms add no content-bearing log line.
- `go build`, `go vet`, `go test -race` over the `go.work` set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh` (still only `companion → proto`, `backend → proto`), `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Any `/proto` change → HALT. `proto.Queued` is already defined and registered; nothing on the wire changes.
- Any new third-party dependency → HALT. The spinner is `charm.land/bubbles/v2/spinner` from the already-required `bubbles/v2`.
- If the flourish cannot be made non-blocking without holding a lock or delaying key routing → HALT.
- Any backend change → HALT. The backend already emits `queued` on enqueue (`hub.go`) and `writeFrame` already serialises it.

**Never:**
- No queue position, ETA, online count, idle nudge, or any "reward for staying" (Epic 5 owns nudges).
- No change to the `ready` / `busy` / `matched` / `chat_msg` / `session_ended` wire flow, the FIFO scan, the opener rotation, or the eligibility seam.
- No `session_ended` routing, silent re-enqueue, reconnect-grace, or "return to spinner and re-enqueue" semantics — Epic 3. In this story a burst that ends while searching just stops the spinner locally.
- No minigame, no perceptible cost, no flourish that blocks the socket write or the input.
- No SQLite / persistence; no spinner or flourish state written anywhere.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| Enter searching | `ready` sent, backend replies `queued` | spinner pane appears; status line stops writing; one calm line + animated spinner, nothing else | N/A |
| Instant match | `ready` sent, backend replies `matched` (no `queued`) | chat surface opens directly; no spinner ever shown; `stopSearch` is a no-op | N/A |
| Match resolves the wait | spinner up, `matched` arrives | spinner torn down immediately; chat surface opens; flourish plays then settles | N/A |
| Burst ends while searching | spinner up, transcript `TurnEnd` (`desired` → busy) | spinner torn down on the debounce flush; status line returns to the waiting phrase | N/A |
| Redelivered queued after reconnect | spinner up, socket drops and re-`ready`s, backend re-sends `queued` | still one spinner (`launchSearch` guarded by `!searchActive`); no flash | N/A |
| Peer line during the flourish | chat surface up, flourish still playing, `PeerMsg` arrives | line is appended to history in order; nothing lost when the flourish ends | N/A |
| Typing during the flourish | chat surface up, flourish playing, user types + presses Enter | keystrokes go to the focused input; Enter sends as normal — flourish never swallows a key | N/A |
| Flourish is bounded | chat surface up | flourish advances on a fixed tick, ends after a fixed frame count (< ~1s), then no further flourish ticks are scheduled | N/A |
| Terminal path while searching | spinner up, `ctx` cancelled / `please_update` / `session_ended` / watcher dies | `stopSearch(false)` runs beside `stopChat(false)`; loop exits on its existing path | silent |
| Queued arrives after burst ended | `desired` already busy when `queued` lands | no spinner (`launchSearch` guarded by `desired == stateReady`) | N/A |
| searchui inbound key | spinner up, user presses a key | ignored (no quit key, no minigame); ctrl+c still cancels ctx via the process signal handler | N/A |

</frozen-after-approval>

## Code Map

- `companion/internal/searchui/searchui.go` — **new.** A minimal Bubble Tea v2 model: a `spinner.Model` (`spinner.New`, `spinner.MiniDot`) plus a stored pane size. `Init` returns `m.spinner.Tick`; `Update` advances on `spinner.TickMsg` (re-arm), stores `tea.WindowSizeMsg`, ignores `tea.KeyPressMsg`; `View` renders one `voice.md` line + the spinner frame (roughly centred, no counts). All copy is static in-package — no `inert` needed. Logs nothing.
- `companion/internal/searchui/searchui_test.go` — **new.** `Init` returns a non-nil tick cmd; a `spinner.TickMsg` produces a new frame and re-arms; a `KeyPressMsg` is inert; `View` never contains a digit, "queue", "ETA", or "online"; `View` carries no terminal control bytes (mirror `chatui_test.assertNoTerminalControls`).
- `companion/internal/chatui/chatui.go` — **edit, the spin.** `Model` (L167-181) gains `flourishFrame int`, `flourishDone bool`, and a small fixed frame slice (or reuse `spinner.Line.Frames`). `New` (L185-218) leaves the flourish "playing" (`flourishDone=false`). `Init` (L221) → `tea.Batch(textarea.Blink, m.flourishTick())`. `Update` (L224) gains a `flourishTickMsg` case: bump `flourishFrame`; if still under the frame count, return `m, m.flourishTick()`; else set `flourishDone=true`, call `m.relayout()`, return `m, nil`. `render` (L404-412): while `!m.flourishDone`, prepend one fixed flourish row above the header; when done it is gone. `headerHeight`/`relayout` (L327-340, L378-380) budget that one row only while the flourish plays (one-time viewport growth when it clears). Package scope note (L1-17): the searching spinner is `internal/searchui`; the spin is this intro flourish — bounded, non-blocking, never delays input.
- `companion/internal/chatui/chatui_test.go` — **edit.** Helpers at L14-55 (`step`, `viewOf`, `typeString`, `pressEnter`). New: `Init` returns a batch that includes a flourish tick; feeding `flourishTickMsg` the fixed number of times sets `flourishDone` and stops re-arming; the flourish row is present in `viewOf` before completion and absent after; typing + `pressEnter` during the flourish still appends the self line and (with `WithSend`) fires the callback; a `PeerMsg` during the flourish lands in history; `assertNoTerminalControls` on the flourishing view.
- `companion/internal/wsclient/wsclient.go` — **edit.** `Client` (L82-95) gains `queued chan proto.Queued`; `New` (L96-105) `queued: make(chan proto.Queued, 1)`. Add `func (c *Client) Queued() <-chan proto.Queued` beside `Matched()` (L116-124) with the buffered-1 / never-closed / non-terminal / drop-on-full doc. `serve` read switch (L277-303): add `case proto.Queued:` → non-blocking send onto `c.queued`, not terminal, keep serving. Package doc (L1-13): "four inbound frames that matter" → five (add `queued`).
- `companion/internal/wsclient/wsclient_test.go` — **edit.** Server writes `proto.Queued` → client surfaces it on `Queued()` and keeps serving (a later `matched` still arrives); nothing is logged.
- `companion/internal/run/run.go` — **edit, orchestration.** `stateClient` (L125-132) gains `Queued() <-chan proto.Queued`. Add a `searchProgram interface { Run() (tea.Model, error); Quit(); Kill() }` seam and a `newSearchProgram` package var beside `newChatProgram` (L141-169), building `tea.NewProgram(searchui.New(), tea.WithContext(ctx), tea.WithInput(cfg.In), tea.WithOutput(cfg.Out), tea.WithoutSignalHandler())`. `loop` state block (L221-227): add `search searchProgram`, `searchActive bool`, `searchDone chan struct{}`. `showPhase` (L231-236): also bail when `searchActive`. Add `launchSearch` / `stopSearch(resume bool)` mirroring `launchChat` / `stopChat` (L238-298); `stopSearch(true)` hands the pane back via `sl.Show(phaseFor(desired))`. New select arm `case <-client.Queued():` → `if !chatActive && !searchActive && desired == stateReady { launchSearch() }`. `matched` case (L434-440): `stopSearch(false)` before `launchChat(m)`. `flush` (L325-330): after the state push, `if searchActive && desired == stateBusy { stopSearch(true) }`. `ctx.Done()` and every `clientDone` terminal branch (L336-359): add `stopSearch(false)` beside `stopChat(false)`. New `case <-searchDone:` → `stopSearch(ctx.Err() == nil)`. Package doc (L1-12): note the searching spinner and the spin handoff.
- `companion/internal/run/run_test.go` — **edit.** `fakeClient` (L59-83) gains `queued chan proto.Queued`; nil-safe `Queued()` beside `ChatMsgs()` (L140-142); `newFakeClient` (L76-83) makes it buffered. Add `stubSearchProgram` + `fakeSearchProgram` mirroring `stubChatProgram` / `fakeChatProgram` (L1246-1358). New tests: `queued` launches the spinner and suppresses the status line; `matched` after `queued` stops the spinner then launches the chat program (order asserted, no dropped `chat_msg`); `matched` with no `queued` never constructs a search program; a `TurnEnd` while searching stops the spinner and the waiting phrase returns; terminal paths stop the spinner. Existing tests stay green.
- `proto/messages.go` — **read-only.** `Queued` (L106-110) and its `TypeQueued` registry entry (L188) already exist. No change.
- `backend/internal/hub/hub.go`, `backend/internal/server/server.go` — **read-only.** `hub.go` already `deliver(cmd.s, proto.Queued{})` on a session left waiting (L150-155); `server.go` `writeFrame` already serialises any registered frame on `sess.Outbound`. No backend change, no new log line.
- `companion/main.go` — **read-only.** `run.Run` wiring (L97) is unchanged; the spinner program is built inside `loop`.
- `scripts/check_deps.sh` — **read-only.** A new `companion/internal/searchui` importing `charm.land/bubbles/v2/spinner` keeps `companion → proto` the only sibling edge; `bubbles/v2` is already a direct companion dependency.
- `README.md` — **edit.** Companion UI section: while waiting for a match the pane shows a calm searching spinner (no counts); on match a brief non-blocking "spin" flourish plays before the chat view settles.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/searchui/searchui.go` — new spinner model: `Init`/`Update`/`View`, one `voice.md` line + `spinner.MiniDot`, keys inert, no logging
- [x] `companion/internal/searchui/searchui_test.go` — tick advances + re-arms, key inert, no digits/"queue"/"ETA"/"online", no control bytes
- [x] `companion/internal/chatui/chatui.go` — bounded intro flourish: `flourishTickMsg`, frame counter, `render` prepends one row while playing, `relayout` on completion; input focus + message handling untouched
- [x] `companion/internal/chatui/chatui_test.go` — flourish is bounded and clears; typing/Enter and `PeerMsg` during the flourish still work; no control bytes
- [x] `companion/internal/wsclient/wsclient.go` — `queued` channel + `Queued()`; `serve` case; package doc four→five frames
- [x] `companion/internal/wsclient/wsclient_test.go` — inbound `queued` surfaced, serving continues, nothing logged
- [x] `companion/internal/run/run.go` — `searchProgram` seam + `newSearchProgram`; `launchSearch`/`stopSearch`; `Queued()` and `searchDone` arms; `showPhase`/`flush`/`matched`/terminal wiring; package doc
- [x] `companion/internal/run/run_test.go` — `fakeClient.Queued()`, `stubSearchProgram`; spinner launch/suppress, `matched` handoff order, no-`queued` path, turn-end teardown, terminal teardown
- [x] `README.md` — searching spinner + spin flourish notes
- [x] `go work sync` — no diff after commit

**Acceptance Criteria:**
- Given a `ready` session, when the backend sends `queued`, then the companion shows only a searching spinner (one line + animation, no position/ETA/count) and the status line stops writing until the surface is gone.
- Given the spinner is up, when `matched` arrives, then the spinner is torn down before the chat surface opens and no `chat_msg` buffered behind the `matched` is dropped.
- Given a match that pairs immediately (no `queued`), when `matched` arrives, then no spinner is ever constructed.
- Given the chat surface has just opened, when the spin flourish plays, then it advances on a fixed tick, ends after a fixed frame count in well under ~1s, schedules no further flourish ticks, and the input accepts keystrokes and Enter the whole time.
- Given the flourish is still playing, when a peer `chat_msg` arrives, then it is appended to history in order and is still there once the flourish ends.
- Given the user is searching, when their burst ends (`TurnEnd`) or any terminal path fires, then the spinner is removed and the pane returns to the status line (or the process exits on its existing path).
- Given every companion user-facing string added here, when scanned, then the copy matches `voice.md` (warm, lowercase, no machinery, no alarm) and no digit-bearing count appears in the searching pane.
- Given the finished tree, when `gofmt -l .`, `go vet`, `go test -race` over the `go.work` set, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` run, then all pass and `check_deps.sh` still reports only `companion → proto` and `backend → proto`.

## Design Notes

**Why `queued`-driven, not state-driven.** The backend distinguishes "enqueued and waiting" (`queued` frame) from "matched on the spot" (`matched`, no `queued`). Keying the spinner to the frame means an instant pairing never flashes a spinner, and the companion needs no timer to guess. `run.loop` already owns the `matched` channel; `Queued()` is the exact mirror.

**Why the spin lives in `chatui`, not `searchui`.** Handing `searchui` the `matched` payload, playing the flourish there, then signalling `run.loop` to swap programs adds a done-signal and a payload path. Making the flourish `chatui`'s intro instead keeps the handoff to a single atomic `stopSearch(false); launchChat(m)` in one select-case, so buffered `chat_msg` frames wait rather than drop, and "nothing lost / input not delayed" fall out of the surface already being up with its textarea focused.

**Flourish shape (illustrative).** ~6 frames on a ~90ms tick ≈ 540ms, then done — a fixed count, not a wall-clock timer, so tests drive it deterministically by feeding `flourishTickMsg`. The row is a fixed single line above the header; the one-time viewport growth when it clears reads as "the chat view appears."

**Teardown symmetry.** `stopSearch` mirrors `stopChat` (Quit → grace → Kill) and is called from exactly the places `stopChat` is, plus the debounce `flush` for the burst-ends-while-searching case. Epic 3 will replace that local teardown with real return-to-spinner + re-enqueue semantics.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` and `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass, incl. new `searchui`, the `chatui` flourish tests, `wsclient` inbound `queued`, and the `run` spinner-lifecycle tests
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — clean after commit
- `bash scripts/check_deps.sh` — prints only `companion → proto`, `backend → proto`; `bash scripts/checks_test.sh` — exit 0

**Manual check:**
- `PORT=8080 go run ./backend/cmd/serve`; start one companion (or a scripted ws client) and send `hello` then `ready` — the pane shows a calm searching spinner, no numbers. Start a second, match the pair — the first pane drops the spinner at once, plays a brief flourish, then settles into the chat view with the opener at the top; typing during the flourish is not swallowed. Send `busy` from a lone searching client — the spinner disappears and the waiting line returns. Grep the backend stdout — no chat text, no account key.

## Suggested Review Order

**The `queued` → spinner → `matched` handoff (start here)**

- Entry point — an inbound `queued` raises the spinner only when ready, unmatched, and nothing else owns the pane.
  [`run.go:531`](../../companion/internal/run/run.go#L531)
- On `matched` the spinner is stopped and the chat surface opened in one select-case, so a `chat_msg` buffered behind it waits rather than drops.
  [`run.go:547`](../../companion/internal/run/run.go#L547)
- `launchSearch` / `stopSearch` mirror `launchChat` / `stopChat` exactly — goroutine `Run`, then Quit → grace → Kill → grace.
  [`run.go:351`](../../companion/internal/run/run.go#L351)
- A burst that ends while searching drops the spinner on the debounce flush and hands the pane back to the status line.
  [`run.go:418`](../../companion/internal/run/run.go#L418)
- `stopSearch(false)` is added beside every `stopChat(false)` on the terminal paths (ctx cancel, all three `clientDone` results).
  [`run.go:429`](../../companion/internal/run/run.go#L429)
- The spinner exiting on its own hands the pane back unless the process is shutting down.
  [`run.go:598`](../../companion/internal/run/run.go#L598)
- The `newSearchProgram` seam — a package var so the loop tests substitute a fake and never stand up a terminal.
  [`run.go:202`](../../companion/internal/run/run.go#L202)

**The searching pane**

- `View`: one calm line plus the spinner frame, centred with `lipgloss.Place`; no count, position, or ETA.
  [`searchui.go:69`](../../companion/internal/searchui/searchui.go#L69)
- `Update`: the spinner advances on its own tick, the pane size is recorded, every key is inert (no quit key, no minigame).
  [`searchui.go:48`](../../companion/internal/searchui/searchui.go#L48)
- The one line of copy, kept in-package so the voice stays in one place.
  [`searchui.go:25`](../../companion/internal/searchui/searchui.go#L25)

**The "spin" flourish (chatui intro)**

- `flourishTickMsg`: a fixed frame counter, re-armed until spent, then `flourishDone` latches and a single `relayout` reclaims the row.
  [`chatui.go:316`](../../companion/internal/chatui/chatui.go#L316)
- `render` prepends the flourish row above the header only while it plays.
  [`chatui.go:481`](../../companion/internal/chatui/chatui.go#L481)
- `flourishHeight` is budgeted into the viewport height in `relayout` — one row while playing, zero once done (the one-time growth).
  [`chatui.go:391`](../../companion/internal/chatui/chatui.go#L391)
- `Init` batches the flourish tick alongside the cursor blink; key routing and message handling are untouched by flourish state.
  [`chatui.go:256`](../../companion/internal/chatui/chatui.go#L256)

**Wire: surface `queued`**

- `serve` hands a `proto.Queued` to the caller with a non-blocking send (non-terminal, drop-on-full — a redelivered `queued` after reconnect is a no-op).
  [`wsclient.go:295`](../../companion/internal/wsclient/wsclient.go#L295)
- `Queued()` — the buffered-by-one, never-closed accessor, mirroring `Matched()`.
  [`wsclient.go:125`](../../companion/internal/wsclient/wsclient.go#L125)

**Tests & docs (peripherals)**

- Spinner lifecycle: launch/suppress, atomic `matched` handoff with a pre-parked `chat_msg`, burst-end, redelivered `queued`, and every terminal path (incl. `please_update`).
  [`run_test.go:1887`](../../companion/internal/run/run_test.go#L1887)
- Flourish: bounded frame count, the one-row viewport reclaim, a mid-flight resize, and typing / peer lines landing while it plays.
  [`chatui_test.go:636`](../../companion/internal/chatui/chatui_test.go#L636)
- `queued` surfaced and serving continues; a full cap-1 buffer does not wedge the read loop.
  [`wsclient_test.go:397`](../../companion/internal/wsclient/wsclient_test.go#L397)
- searchui: tick advances and re-arms, keys inert, the view carries no digits / machinery words / control bytes.
  [`searchui_test.go:80`](../../companion/internal/searchui/searchui_test.go#L80)
- README — the companion "Chat surface" section now describes the spinner and the flourish.
  [`README.md:116`](../../README.md#L116)
