---
title: 'Unified session_ended on every backend-side end'
type: 'feature'
created: '2026-09-08'
status: 'done'
review_loop_iteration: 0
baseline_commit: '942545693c903e329b70b471129b64840cc52819'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** A matched chat can end four ways on the backend today (peer `busy`/model-return, peer socket drop, connection takeover, and — once added — peer `leave`), but the termination frame is produced by two different code paths (`hub.teardownPair` and the server `Evict` case). Nothing proves the frame is byte-identical across causes, a taken-over connection that is also someone's peer can receive `session_ended` twice, and the takeover victim's frame can race ahead of its own relay teardown. The client `leave` frame is defined in `/proto` but silently discarded.

**Approach:** Make `hub.teardownPair` the single cause-agnostic emission point for the peer-facing `session_ended`, wire the `leave` frame to it, dedupe the takeover victim's frame to exactly one in the connection handler, reorder the takeover branch so the pairing is gone before the victim is told, and lock all of it with tests that drive every available cause and compare the received bytes.

## Boundaries & Constraints

**Always:**
- `session_ended` stays `proto.SessionEnded struct{}` — no cause, no `session_id`, no timestamp, no field of any kind. On the wire it is exactly `{"type":"session_ended","v":1}`, byte-identical for every cause.
- Every backend-side end routes its peer notification through `pairingState.teardownPair` (the one `deliver(peer, proto.SessionEnded{})` call). The taken-over connection itself is notified by exactly one path (the server `Evict` case).
- The pairing entry (both `p.pairings` directions) is deleted before `session_ended` is emitted, for every cause including takeover. `session_ended` is the last frame the peer receives for that session.
- A connection receives at most one `session_ended` per `matched` session; a fresh `matched` re-arms it.
- Wire shapes live only in `/proto`; no new message type. All hub state mutation stays on the single hub goroutine. Logs stay content-free and key-free.

**Ask First:**
- If wiring `leave` cleanly requires changing the leaver's queue/`ready`/`busy` state (beyond mirroring `busyCmd`'s existing `removeFromQueue` no-op) — stop and confirm. This spec assumes `leave` only tears the pairing down; whether the leaver is re-queued is Story 3.5.

**Never:**
- No reconnect grace window / delayed teardown (Story 3.4). Disconnect still tears down immediately.
- No silent re-enqueue of the still-busy peer (Story 3.5). `teardownPair`'s "neither peer re-enqueued" stays.
- No ban trigger or `cmd/ban` wiring (Epic 4) — only leave the `teardownPair` path cause-agnostic so ban inherits it.
- No companion-side work: leave UI/keybinding, local end state, late-message suppression (Story 3.3).
- Do not make `deliver` blocking or delivery-guaranteed (epic-2-retro item 11). `session_ended` stays best-effort non-blocking on a full/closed `Outbound`, same as every other hub frame — recorded as an accepted v1 deviation.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Peer goes busy | A,B paired; A sends `busy` | B receives exactly one `session_ended` = `{"type":"session_ended","v":1}`; both `pairings` directions cleared before emit; A gets no frame | N/A |
| Peer leaves | A,B paired; A sends `leave` | Identical to busy: B gets one `session_ended`; A gets no backend frame; pairing cleared first | N/A |
| Peer disconnects | A,B paired; A's socket drops | B receives exactly one `session_ended`; pairing cleared; no grace delay | read error → `Unregister` → `teardownPair` |
| Connection takeover | key K paired as `prev`; new `hello` for K | `prev` conn: one `session_ended` then normal close; `prev`'s peer: one `session_ended` on `Outbound`; new conn proceeds to queue | new conn registered after `prev` torn down |
| Takeover races peer end | `prev` paired with A; A ends (busy/drop) AND K taken over, concurrently | `prev` receives exactly ONE `session_ended` (handler dedupe), then close | second emission (Evict or Outbound) suppressed |
| `chat_msg` in flight at end | peer sends `chat_msg` just as the session ends | `chat_msg` arrives before `session_ended` or is dropped — never after | post-teardown `relay` is a silent no-op |
| `leave` while not paired | session queued or idle sends `leave` | silent no-op: no `session_ended`, no `error`, no panic | `teardownPair` returns early when unpaired |
| Stale `leave` after takeover | replaced session's late `leave` frame | ignored; does not evict the replacement or notify anyone | acts on the session pointer; `teardownPair` no-ops |

</frozen-after-approval>

## Code Map

- `proto/messages.go:129-134` — `SessionEnded struct{}` (already payload-free; only the "…land in a later epic" comment changes). `proto.Leave` at `:57-61`, registered `:182`.
- `backend/internal/hub/hub.go:222-233` `teardownPair` — THE chokepoint; keep its `deliver(peer, proto.SessionEnded{})`, make the doc comment name it the sole cause-agnostic emission point (ban=Epic 4, grace-expiry=Story 3.4 route here too).
- `backend/internal/hub/hub.go:156-164` `busyCmd` + `:349-353` `Busy` + `:37-59` command list — template for the new `leaveCmd` / `Leave` / `leaveCmd struct{ s *Session }`.
- `backend/internal/hub/hub.go:109-128` `registerCmd` takeover branch — reorder: `removeFromQueue(prev)` + `teardownPair(prev)` before `close(prev.Evict)`.
- `backend/internal/server/server.go:202-213` read-loop switch — add `case proto.Leave: s.hub.Leave(sess)`.
- `backend/internal/server/server.go:216-245` `serveConn` select — add the `sentEnd` bool (see Design Notes).
- `backend/internal/hub/hub_test.go` — helpers `newSession`, `recvOutbound`, `expectNoOutbound`, `startHub`, `matchedFrame`; model on `TestBusyWhilePairedTearsDownAndNotifiesPeer` (`:541`).
- `backend/internal/server/server_test.go` — helpers `newHarness`, `runHub`, `dial`, `writeMsg`, `readMsg`, `readMatched`, `matchedPair`, `expectCloseNoData`, `expectNoFrame`; model on `TestBusyWhilePairedDeliversSessionEndedToPeer` (`:562`), `TestPairedDialClosingDeliversSessionEndedToPeer` (`:541`).

## Tasks & Acceptance

**Execution:**
- [x] `proto/messages.go` -- rewrite the `SessionEnded` doc comment so cause-agnostic/payload-free is the normative contract (no shape change; stays `struct{}`) -- keeps the wire contract self-documenting for the Epic 4 / Story 3.4 callers that will route through the same frame.
- [x] `backend/internal/hub/hub.go` -- add `leaveCmd` + exported `Leave(*Session)` mirroring `busyCmd`/`Busy` (route teardown through `teardownPair`); reorder the `registerCmd` takeover branch so `removeFromQueue(prev)`+`teardownPair(prev)` precede `close(prev.Evict)`; update the `teardownPair` comment to name it the sole cause-agnostic `session_ended` emission point -- one chokepoint, deterministic relay-then-notify ordering, and a `leave` path that reuses it.
- [x] `backend/internal/server/server.go` -- add `case proto.Leave: s.hub.Leave(sess)` to the read-loop switch; in `serveConn` add the `sentEnd` bool (set on `Outbound` `SessionEnded`, cleared on `Matched`, gates the `Evict`-case write) -- delivers the client `leave` to the hub and makes "exactly one `session_ended`" a handler-level guarantee for the takeover victim.
- [x] `backend/internal/hub/hub_test.go` -- add unit tests for `Leave` while paired (peer gets `session_ended`, both `pairings` directions cleared), `Leave` while unpaired/queued (silent no-op), and the takeover-branch reorder (peer still notified; pairing cleared before `close(Evict)`) -- covers the I/O matrix rows the hub owns.
- [x] `backend/internal/server/server_test.go` -- add end-to-end tests: (a) `session_ended` bytes are identical across busy, peer-disconnect, leave, and takeover, and equal the canonical no-`session_id`/no-`cause` frame; (b) `session_ended` is the final frame for busy, disconnect, and leave with a `chat_msg` raced against the end (never arrives after); (c) a takeover victim that is also a peer of a concurrently-ending session receives exactly one `session_ended` then a clean close -- covers AC1/AC2 and the dedupe race.

**Acceptance Criteria:**
- Given any of the four exercisable end causes (peer `busy`, peer `leave`, peer disconnect, connection takeover), when the backend ends the session, then the peer receives exactly one `session_ended` frame whose bytes are identical across all four causes and contain no cause field and no `session_id`.
- Given a session is ending by any cause, when `session_ended` is emitted, then that session's `p.pairings` entries are already deleted and no further `chat_msg` for it can be relayed, so `session_ended` is the last frame the peer receives for that session.
- Given a client sends `leave` while paired, when the hub processes it, then the pairing is torn down through the same `teardownPair` path as `busy` and the peer's frame is byte-identical to the `busy` case.
- Given a client sends `leave` while queued or idle, when the hub processes it, then nothing is sent to anyone and no error is raised.
- Given a connection is taken over while it is also the peer of a session ending concurrently, when both teardown signals reach its handler, then it writes exactly one `session_ended` and then closes.

## Spec Change Log

## Design Notes

The `sentEnd` guard lives in the connection handler because that is the only place that sees both emission paths: the takeover victim's `session_ended` comes from the `Evict` case (it deliberately bypasses `Outbound`), while the same connection can also receive `session_ended` on `Outbound` as a normal teardown peer. The flag is per-session, not per-connection — a companion keeps one socket across many matches — so a `proto.Matched` clears it.

```go
// serveConn select loop, sketch
case msg := <-sess.Outbound:
    switch msg.(type) {
    case proto.Matched:
        sentEnd = false
    case proto.SessionEnded:
        if sentEnd { continue } // already ended this session
        sentEnd = true
    }
    s.writeFrame(ctx, conn, msg)   // keep existing matched / session-ended logging
case <-sess.Evict:
    if !sentEnd {
        s.writeFrame(ctx, conn, proto.SessionEnded{})
    }
    _ = conn.Close(websocket.StatusNormalClosure, "")
    cancel(); <-readDone; return
```

`leaveCmd` is `busyCmd` renamed: `removeFromQueue(cmd.s)` (no-op when paired) → `teardownPair(cmd.s)` → `drainQueue(eligible)`. No new teardown logic.

## Verification

**Commands:**
- `cd backend && go test -race ./...` -- expected: all packages pass, including the new hub and server cases.
- `cd proto && go test ./...` -- expected: pass; round-trip and registry tests unaffected.
- `cd backend && go vet ./... && cd ../proto && go vet ./...` -- expected: no diagnostics.
- `gofmt -l backend/ proto/` -- expected: no output.

## Suggested Review Order

**The one cause-agnostic frame (start here)**

- Entry point: the frame's contract — payload-free, byte-identical for every end cause
  [`messages.go:132`](../../proto/messages.go#L132)
- THE single emission point: pairing deleted first, then exactly one bare `SessionEnded` to the peer
  [`hub.go:251`](../../backend/internal/hub/hub.go#L251)

**Wiring `leave` as a termination cause**

- New `leave` command handler — structurally `busyCmd`, routed through `teardownPair`; re-enqueue stays Story 3.5
  [`hub.go:171`](../../backend/internal/hub/hub.go#L171)
- Public `Hub.Leave` mirroring `Hub.Busy`
  [`hub.go:385`](../../backend/internal/hub/hub.go#L385)
- The `leave` frame reaches the hub — previously decoded and discarded
  [`server.go:207`](../../backend/internal/server/server.go#L207)

**Exactly one `session_ended` per matched session**

- `sentEnd` latch: dedupes the hub-teardown frame against the Evict-case frame; re-armed by a fresh `Matched`
  [`server.go:229`](../../backend/internal/server/server.go#L229)
- Duplicate `SessionEnded` on `Outbound` is dropped — no write, no log
  [`server.go:247`](../../backend/internal/server/server.go#L247)
- Evict case now writes `session_ended` only if the teardown path didn't already
  [`server.go:265`](../../backend/internal/server/server.go#L265)

**Deterministic relay-then-notify ordering on takeover**

- Reorder: displaced session's queue/pairing torn down before `close(prev.Evict)`, so the pairing is gone before the victim is signalled
  [`hub.go:125`](../../backend/internal/hub/hub.go#L125)

**Tests**

- Byte-identical `session_ended` across busy / disconnect / leave / takeover
  [`server_test.go:603`](../../backend/internal/server/server_test.go#L603)
- `session_ended` is the final frame — raced `chat_msg` never lands after it
  [`server_test.go:661`](../../backend/internal/server/server_test.go#L661)
- Takeover victim that is also a peer of a concurrently-ending session gets exactly one frame
  [`server_test.go:711`](../../backend/internal/server/server_test.go#L711)
- `sentEnd` guard re-arms on re-match (pins the `case proto.Matched` reset)
  [`server_test.go:749`](../../backend/internal/server/server_test.go#L749)
- Hub-level: leave tears down + notifies peer; leave frame == busy frame; leave while unpaired is silent
  [`hub_test.go:562`](../../backend/internal/hub/hub_test.go#L562)
- Takeover branch tears down the pairing before Evict; stale `leave` after takeover is a no-op
  [`hub_test.go:658`](../../backend/internal/hub/hub_test.go#L658)
