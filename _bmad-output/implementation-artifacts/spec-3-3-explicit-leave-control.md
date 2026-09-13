---
title: 'Explicit leave control'
type: 'feature'
created: '2026-09-08'
status: 'done'
review_loop_iteration: 0
baseline_commit: '45a5d7c7ca928cd3bf670df9c8a3da90769fdb8c'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The companion already has a leave key (`esc` / `ctrl+q`, help "my claude's back") that raises `chatui.IntentLeave`, and `run.loop` already tears the chat surface down on that intent — but it never puts a `leave` frame on the wire. Story 3.1 wired `proto.Leave` end-to-end on the backend (`Hub.Leave` → `teardownPair`), so today the tap is silently dropped: the peer only learns the chat ended when this socket eventually drops. Nothing suppresses a peer `chat_msg` already in flight when the user leaves.

**Approach:** Add `wsclient.SendLeave` (one `proto.Leave` frame, mirroring `SendChat`) and call it from the existing `IntentLeave` arm — exactly once, no confirmation, local end state still shown immediately without waiting for the server (the backend notifies only the peer, never echoes `leave` back). Add a `left` latch in `loop` that drops inbound peer lines after a leave until the next `matched`.

## Boundaries & Constraints

**Always:**
- `IntentLeave` sends exactly one `proto.Leave{}` via `client.SendLeave(ctx)` — no confirmation, no modal, no extra keystroke. A send failure (no live connection) is a content-free breadcrumb only; the local end state is still presented and `loop` keeps running.
- The end presentation happens in the `IntentLeave` arm, synchronously, not gated on any server frame: `pm.stopChat(false)`, then mid-think-time (`desired == stateReady`, nothing else owns the pane) → `pm.launchSearch()`; model already back → `pm.showPhase(phaseFor(desired))`.
- After a leave, an inbound peer `proto.ChatMsg` is not shown: the `left` latch gates the `client.ChatMsgs()` arm. It clears when a fresh `matched` opens a new chat.
- `proto.Leave` stays `struct{}` — no new wire type. `SendLeave` mirrors `SendChat` exactly (nil conn → `ErrNotConnected`, no queue, no retry); `wsclient` stays silent (no logs, no key leak).
- One `IntentLeave` arm, no branch on persist state — leaving a chat that outlived the burst is the same path as leaving mid-burst.

**Ask First:**
- If suppressing the late peer line cleanly seems to need `loop` to drain or reorder `client.SessionEnded()` / `client.ChatMsgs()` beyond a boolean gate — stop and confirm.

**Never:**
- No confirmation dialog, "are you sure?", or undo affordance.
- No persist toggle, no local model-return → auto-`leave` path (Stories 3.6 / 3.2's deferred branch). This story sends `leave` only on the explicit key.
- No reconnect grace window (Story 3.4), no self re-enqueue / synthesised `ready`/`busy` (Story 3.5, backend-owned). A mid-burst leave raises the local spinner only; pre-3.5 nothing resolves it — the same accepted interim state Story 3.2 took.
- No new user-facing copy. The leave key, its help text, and the "leave requested from the chat surface" operator breadcrumb already exist and pass Story 3.2's no-alarm-language audit. Do not rename them.
- No `chatui` change — the surface already raises `IntentLeave` and returns `tea.Quit`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior |
|----------|--------------|---------------------------|
| Leave, model back | chat up, `desired == stateBusy`; user hits leave | exactly one `proto.Leave{}` on the wire; chat torn down; `phaseFor(desired)` line shown at once; `loop` keeps running |
| Leave, still mid-burst | chat up, `desired == stateReady`; user hits leave | one `leave` frame; chat torn down; searching spinner raised (Story 3.5 owns re-enqueue); `loop` keeps running |
| Late peer line after leave | user has left; `proto.ChatMsg` arrives on `client.ChatMsgs()` | dropped by the `left` gate — not forwarded, nothing to `cfg.Out` |
| Leave while socket down | chat up; no live connection | `SendLeave` → `ErrNotConnected`; content-free breadcrumb; end state still shown; `loop` keeps running |
| New match after leave | `left == true`; later `matched` then peer `chat_msg` | new chat opens, `left` cleared, the peer line forwarded normally |
| Repeat leave key | chat already torn down by the first leave | impossible — `stopChat` nils `chatIntents`, so `pm.intents()` is dormant; no second `leave` frame |

</frozen-after-approval>

## Code Map

- `companion/internal/wsclient/wsclient.go`
  - `SendChat` `:264-277` — template. Add `SendLeave(ctx context.Context) error`: lock, read `c.conn`, nil → `return ErrNotConnected`, else `return c.writeFrame(ctx, conn, proto.Leave{})`. Doc: one frame, no queue/retry, backend notifies only the peer.
  - package doc `:1-21` — "carries ready/busy state frames and outbound chat_msg frames out": add `leave`.
- `companion/internal/run/run.go`
  - `stateClient` interface `:151-160` — add `SendLeave(ctx context.Context) error`.
  - `loop` locals `:272-274` — add `left := false`.
  - `chatui.IntentLeave` arm `:494-498` — after `pm.stopChat(false)`: `left = true`; `if err := client.SendLeave(ctx); err != nil { fmt.Fprintln(cfg.Err, "companion: leave could not be sent") }`; keep the existing breadcrumb; replace `pm.showPhase(phaseFor(desired))` with `if desired == stateReady && !pm.anyActive() { pm.launchSearch() } else { pm.showPhase(phaseFor(desired)) }`.
  - `client.ChatMsgs()` arm `:448-452` — prepend `if left { continue }` before `pm.sendPeer(...)`.
  - `client.Matched()` arm `:437-446` — inside `if !pm.chatActive() { ... }`, add `left = false`.
  - package doc `:1-36` — one line: an explicit leave sends `proto.Leave` and suppresses later peer lines via `left` until the next match.
- `companion/internal/run/panemanager.go` — no change; `sendPeer` `:235-239` is already `chatActive`-guarded, so the latch is belt-and-braces for a same-pass select race.
- `companion/internal/wsclient/wsclient_test.go` — `TestSendChatFrameArrives` `:629` / `TestSendChatWithoutConnection` `:620` are the templates for `TestSendLeaveFrameArrives` / `TestSendLeaveWithoutConnection`.
- `companion/internal/run/run_test.go` — `fakeClient` `:59-75` + accessors `:160-176`: add `leaveCount int` (mutex-guarded), `SendLeave(ctx) error` (honours a `sendLeaveErr`, like `sendChatErr`), `setSendLeaveErr`, `leavesSent() int`. `TestLeaveIntentReturnsToStatusLine` `:1433` and `TestTeardownStopsInboundForwarding` `:1792` stay valid (extend the first with a leave-count assertion).
- `proto/messages.go:57-61` — `Leave struct{}`, registered `:188`. Read-only.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/wsclient/wsclient.go` -- add `SendLeave(ctx) error` mirroring `SendChat` (nil conn → `ErrNotConnected`, else one `proto.Leave{}` frame); extend the package doc's outbound-frame list -- gives `run.loop` a single-frame leave send with no queue/retry.
- [x] `companion/internal/run/run.go` -- add `SendLeave` to `stateClient`; add the `left` local; in the `IntentLeave` arm send exactly one `leave`, set `left`, and route the pane (spinner mid-burst / `phaseFor(desired)` model-back) without waiting on the server; gate `client.ChatMsgs()` on `!left`; clear `left` on a fresh `matched`; refresh the package doc -- wires the tap to the wire and suppresses late peer lines.
- [x] `companion/internal/wsclient/wsclient_test.go` -- `TestSendLeaveFrameArrives` (live conn → exactly one `leave` frame on the wire) and `TestSendLeaveWithoutConnection` (→ `ErrNotConnected`, nothing written).
- [x] `companion/internal/run/run_test.go` -- extend `fakeClient` with `SendLeave` + count + error hook; add: exactly one `leave` per `IntentLeave`; leave mid-burst raises the spinner and `loop` stays up; leave model-back shows `phaseFor(desired)` and `loop` stays up; a peer `chat_msg` after leave is dropped; `SendLeave` error still shows the end state and `loop` stays up; a `matched` after leave clears `left` so the next peer line is forwarded.

**Acceptance Criteria:**
- Given an active chat, when the user activates the leave control, then exactly one `proto.Leave{}` frame is written, no confirmation prompt or modal is shown, and the chat surface is torn down with the local end state presented in the same `loop` turn — not gated on any server frame.
- Given the user has left, when a peer `chat_msg` subsequently arrives on `client.ChatMsgs()`, then it is not forwarded to any surface; once a new `matched` opens a chat, later peer lines are forwarded again.
- Given the companion has no live connection, when the user activates the leave control, then `SendLeave` returns `ErrNotConnected`, a content-free breadcrumb is emitted, the local end state is still shown, and `loop` keeps running.
- Given there is one `IntentLeave` arm and no persist toggle yet (Story 3.6), when the user leaves a chat whose model has already returned, then it is handled by the exact same code path as any other model-back leave — there is no separate "persisted chat" branch to diverge from it. (This is the "Leave, model back" matrix row; it is not a distinct visible state from leaving mid-burst, which shows the spinner instead.)

## Design Notes

The leaver gets nothing back from the backend — `teardownPair` delivers `session_ended` to the *peer* only (Story 3.1). So the `IntentLeave` arm is the sole place the leaver's end state is decided and it must not wait for a frame. It deliberately does **not** show `PhaseClaudeBack`: that line reads "looks like their claude's back", the peer-protection fiction for the *remaining* user. The leaver did the leaving, so they get the honest `phaseFor(desired)` line (`PhaseWaiting` — "you're back with claude"), or the spinner if still in think-time. Both calm; neither is alarm language.

`left` vs. the existing `sendPeer` `chatActive` guard: `stopChat` runs synchronously in the same arm as `left = true`, so on review the two are always in lockstep at every point `client.ChatMsgs()` can observe them — `left` does not, on its own, close any race `chatActive` was not already closing. It is kept as defense-in-depth documentation of intent (the frozen boundary above names it explicitly) and as a guard that stays correct if a future refactor ever separates the two conditions; it is not a claim that today's one-pass select race (both `IntentLeave` and a buffered `ChatMsgs()` ready, peer arm picked first) is closed — that race is accepted, unchanged, the same way Story 2.3 accepted "buffered behind matched waits rather than drops." Clearing `left` on `matched` only — a new chat is the one thing that should re-enable peer forwarding.

```go
case chatui.IntentLeave:
    pm.stopChat(false)
    left = true
    if err := client.SendLeave(ctx); err != nil {
        fmt.Fprintln(cfg.Err, "companion: leave could not be sent")
    }
    fmt.Fprintln(cfg.Err, "companion: leave requested from the chat surface")
    if desired == stateReady && !pm.anyActive() {
        pm.launchSearch() // Story 3.5 owns the backend re-enqueue
    } else {
        pm.showPhase(phaseFor(desired))
    }
```

## Verification

**Commands:**
- `cd companion && go test -race ./...` -- expected: all packages pass, including the new wsclient and run cases.
- `cd companion && go vet ./...` -- expected: no diagnostics.
- `gofmt -l companion/` -- expected: no output.
- `cd proto && go test ./...` -- expected: pass (no proto change; sanity only).

## Suggested Review Order

**The wire send (start here)**

- Entry point: exactly one `leave` frame, no confirmation, local end state routed the moment it's sent — never gated on a server reply
  [`run.go:513`](../../companion/internal/run/run.go#L513)
- `SendLeave` mirrors `SendChat`: nil conn → `ErrNotConnected`, no queue, no retry, no log
  [`wsclient.go:288`](../../companion/internal/wsclient/wsclient.go#L288)
- Why no retry is safe here: the leaver gets nothing back from the backend either way — `leave` notifies only the peer
  [`wsclient.go:85`](../../companion/internal/wsclient/wsclient.go#L85)

**Suppressing late peer lines**

- `left` local: latches on leave, clears only on a fresh `matched`
  [`run.go:286`](../../companion/internal/run/run.go#L286)
- The gate itself — dropped before `pm.sendPeer` ever sees it
  [`run.go:467`](../../companion/internal/run/run.go#L467)
- Cleared on the one thing that should re-enable forwarding: a new chat
  [`run.go:458`](../../companion/internal/run/run.go#L458)

**Tests**

- Wire-level: exactly one `leave` frame, exact frame count pinned
  [`wsclient_test.go:660`](../../companion/internal/wsclient/wsclient_test.go#L660)
- No connection: `ErrNotConnected`, nothing written
  [`wsclient_test.go:651`](../../companion/internal/wsclient/wsclient_test.go#L651)
- Model-back leave shows the honest waiting line, never the peer-protection `PhaseClaudeBack`
  [`run_test.go:1854`](../../companion/internal/run/run_test.go#L1854)
- Mid-burst leave raises the spinner instead
  [`run_test.go:1887`](../../companion/internal/run/run_test.go#L1887)
- Mid-burst leave, then a fresh match tears the spinner down and clears the late-line gate
  [`run_test.go:1989`](../../companion/internal/run/run_test.go#L1989)
- The "repeat leave key" matrix row: two buffered notifies still send exactly one frame
  [`run_test.go:2096`](../../companion/internal/run/run_test.go#L2096)
