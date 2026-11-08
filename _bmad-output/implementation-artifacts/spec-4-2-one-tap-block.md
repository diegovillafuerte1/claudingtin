---
title: 'Story 4.2: One-tap block'
type: 'feature'
created: '2026-10-29'
status: 'done'
baseline_commit: '3fe0c32b99f72aa8a38b8a5e8476990b0071f831'
review_loop_iteration: 1
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-4-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** `POST /v1/send` accepts `block` and drops it, so a block does nothing: the pair can match again and the chat continues until a separate `leave`.

**Approach:**
- `block` names the chat it means: `proto.Block{SessionID}` (`session_id`, from that chat's `matched`).
- The hub blocks only that session's counterpart:
  - the current peer when the session is the current pairing;
  - the most recent counterpart when that pairing has just ended (the peer's end crossed the block);
  - an unknown or stale `session_id` is a no-op and never touches a new partner.
- A block of the live pairing ends it through `teardownPair`: the peer gets the ordinary `session_ended`, and the blocker is silently re-enqueued if it still wants a match. The block is written via 4.1's `addBlock`.
- The mod sends `block` with the session ID, with no trailing `leave`.
  - A held chat (Claude back, 3.6) sends `busy` first, then `block`, so the blocker is never re-matched while Claude is back.
  - If the block POST fails, the mod falls back to `leave` so the pair cannot linger.

## Boundaries & Constraints

**Always:**
- Block is keyed to account keys only. The peer never learns of it: its frames are byte-identical to every other end. The blocker gets no frame.
- The hub keeps, per session, the `lastSessionID`/`lastPeerKey` of its most recent pairing: set at match, carried over on resume.
- A block whose `session_id` matches neither the current pairing nor `lastSessionID` does nothing.
- The blocker is re-enqueued only if it was in that live pairing, `WantsMatch`, and isn't grace-held.
- The server clears the handle's `sessionID` after a block, as it does for leave and busy.
- `proto.Block` gains `SessionID string json:"session_id"`, and the golden fixture is regenerated. `PROTOCOL_VERSION` is unchanged (an additive field).
- No profile, preference or userConfig field is touched. Mod hooks never await the network.

**Ask First:**
- any other `/proto` change;
- any change to `leave` or `report` semantics (report is 4.6).

**Never:**
- the block list view or unblock (4.3);
- any frame to the blocked peer beyond `session_ended`;
- blocking by "whoever is current" without a matching `session_id`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Block mid-burst | A paired with B (session S), A `WantsMatch`, `block{S}` | row (A,B), B gets `session_ended`, A silently re-enqueued, A and B never rematch | write error → memory kept, logged |
| Crossed end | B ended S just before A's `block{S}` | (A,B) recorded from `lastPeerKey`, no frame to anyone, A's queue state unchanged | — |
| Already re-matched | A is now paired with C (session T), a late `block{S}` | (A,B) recorded, A–C untouched | — |
| Stale / unknown | `block{X}` with X neither current nor last, or empty | no-op, 204 | — |
| Held chat | Claude back, mod sends `busy` then `block{S}` | busy ends S (A not re-enqueued), block records (A,B) via last | — |
| Many cycles | A blocked B, both ready repeatedly | never paired, either direction | — |
| Restart | block recorded, `serve` reopened | still in effect | — |
| Mod mid-burst | block button or `/claudingtin block` | exactly one `block{session_id}`, no `leave`; chat cleared | POST fails → one `leave` sent |

</frozen-after-approval>

## Code Map

- `proto/messages.go` L72 -- `Block{SessionID string \`json:"session_id"\`}`. Regenerate the fixture with `(cd proto && go test . -run TestGoldenFixtures -update)`. Update the `messages_test.go` sample (L22) to carry a session ID.
- `proto/messages.go` L72 -- `Block{SessionID string \`json:"session_id"\`}`. Regenerate the fixture with `(cd proto && go test . -run TestGoldenFixtures -update)`. The `messages_test.go` sample at L22 carries a session ID.
- `backend/internal/hub/hub.go`:
  - `Session` L49: add hub-owned `lastPeerKey string`, set in `drainQueue` at the match (about L464) and carried over on resume (about L185).
  - New `blockCmd{s}` plus `(h *Hub) Block(s)` next to `Leave` (about L546).
  - The `Run` switch case (next to `leaveCmd`, about L310): target = paired peer's key, else `lastPeerKey`; `policy.addBlock(ctx, s.Key, target)`; `p.teardownPair(s)`; re-enqueue `s` if `WantsMatch && graceTimer == nil`; then `p.drainQueue(eligible)`.
  - `policy.addBlock` is in `policy.go` (4.1).
- `backend/internal/server/v1.go` L592 send switch -- `case proto.Block: a.hub.Block(h.sess); h.sessionID = ""`. Leave `wantsMatch` alone (block is not busy).
- `plugin/hooks/chat.tsx` L310 `leaveChat`:
  - kind `block` sends `[block]`;
  - `report` keeps `[report, leave]` until 4.6;
  - `leave` sends `[leave]`;
  - update the wire-order comment.
- Tests:
  - `backend/internal/hub/policy_test.go`: reuse `startHubWith`, `readyInOrder`, `countingWriter`.
  - `backend/internal/server/v1_test.go`: existing `/v1` harness.
  - `backend/cmd/serve/main_test.go`: `openPolicyHub`, for restart.
  - `plugin/hooks/chat.test.tsx`: the block tests about L397 and the 3.6 held block/report test about L738.

## Tasks & Acceptance

**Execution:**
- [x] `proto/` -- `Block.SessionID`, sample and fixture.
- [x] `backend/internal/hub/hub.go` -- `blockCmd{s, sessionID}`, `Block(s, sessionID)`, `lastPeerKey`/`lastSessionID`, the scoped case.
- [x] `backend/internal/hub/block_test.go`, every matrix row, including already-re-matched and stale cases:
  - block mid-burst (peer frame identical to a leave's, blocker silently re-enqueued and matched with a third key);
  - never rematched over many cycles in both directions;
  - the race via `lastPeerKey`;
  - unpaired and never matched is a no-op;
  - a grace-held blocker is not re-enqueued;
  - the writer gets exactly one row.
- [x] `backend/internal/server/v1.go` + `v1_test.go` (including block then lease lapse: no stray `session_ended`) -- `/v1/send` `block` reaches the hub: peer gets `session_ended`, 204.
- [x] `backend/cmd/serve/main_test.go` -- block through the hub, reopen, still enforced.
- [x] `plugin/hooks/chat.tsx` (+ `link.ts` if needed) + `chat.test.tsx`. Tests:
  - `block{session_id}` alone on the wire, for both button and command;
  - held: `busy` then `block`;
  - a failed block POST falls back to one `leave`.

**Acceptance Criteria:**
- Given `go test -race ./backend/... ./proto/...`, `claude plugin validate --strict plugin` and `claude plugin test plugin`, when run, then all pass.
- Given a block, when the store's `profile` table and the mod's config are inspected, then nothing changed.

## Spec Change Log

- **Loop 1 (2026-11-08), intent_gap:**
  - **Finding:** all three reviewers. An unscoped `block` (empty `Block{}`, "current peer, else `lastPeerKey`") can land on an innocent new partner. This happens when the peer's end crosses the block and the hub has already re-matched the blocker, when a lapsed handle's ready is replayed before the block, or when a POST is retried.
  - **Human decision:** scope blocks to `session_id` (a `/proto` change), use busy-then-block for held chats, and fall back to `leave` when the block POST fails.
  - **Amended:** the frozen Intent, Boundaries and Matrix.
  - **Known-bad state avoided:** blocking whoever is currently paired.
  - **KEEP:**
    - `blockCmd`/`Hub.Block`;
    - teardown through `teardownPair` with the silent blocker re-enqueue;
    - `lastPeerKey` carried over on resume;
    - the server clearing `h.sessionID`;
    - the mod's `[block]` with no trailing leave;
    - the test shapes in the reference patch (hub `block_test.go`, the `/v1` contract test, `main_test` restart, mod wire tests).

## Design Notes

- **Why the mod drops the trailing `leave`:** a `leave` removes its sender from the queue (`leaveCmd`'s `removeFromQueue`). After the hub re-enqueued the blocker, that `leave` would undo the AC's "re-enqueued if busy". A held chat is still right: its later `busy` takes the blocker out, as the model is back.
- **Why `lastPeerKey`:** the person tapped block on someone. A `session_ended` crossing on the wire must not make that tap a no-op.

- **"lastSessionID", made concrete:** the hub keeps the current and the previous pairing per account key (`recentPairs`, `maxRecentPairs = 2`, at most 4096 keys). This covers the matrix's "already re-matched" row. It is keyed by account key, not by `*Session`, so it survives a resume, a takeover and a lapsed handle's re-register. It is in memory only and lost on restart (documented in the README).
- **A block naming an older chat with the same person** also ends a live chat with them (review loop 1, v2): the blocked person never stays in a chat with the blocker.
- **Mod fallbacks (review):**
  - in a held chat, a `busy` that fails is retried by the worker, never marked sent;
  - after a fallback `leave` during a live burst, the worker re-sends `ready`.

## Verification

**Commands:**
- `go test -race ./backend/... ./proto/...` -- expected: all pass.
- `claude plugin validate --strict plugin && claude plugin test plugin` -- expected: all pass.

## Suggested Review Order

**A block names its chat**

- Entry point: block only the counterpart of the named chat; end the live chat only if it's with that person.
  [`hub.go:397`](../../backend/internal/hub/hub.go#L397)

- Recent pairings kept per account key (current plus previous), surviving a resume, takeover or lapse.
  [`hub.go:74`](../../backend/internal/hub/hub.go#L74)

- Recorded at match time, for both sides.
  [`hub.go:559`](../../backend/internal/hub/hub.go#L559)

- The wire: `Block{session_id}`.
  [`messages.go:74`](../../proto/messages.go#L74)

**Server and store**

- `/v1/send` routes it; the handle's session clears only when the block named it.
  [`v1.go:604`](../../backend/internal/server/v1.go#L604)

- A repeat block is not written twice.
  [`policy.go:150`](../../backend/internal/hub/policy.go#L150)

**The mod**

- Block alone, naming the chat; report and leave unchanged.
  [`chat.tsx:335`](../../plugin/hooks/chat.tsx#L335)

- Held: busy first, retried if it fails; a failed block falls back to leave, then ready again.
  [`link.ts:508`](../../plugin/hooks/link.ts#L508)

**Peripherals**

- A late block never touches the new partner.
  [`block_test.go:145`](../../backend/internal/hub/block_test.go#L145)

- A late block, then a lease lapse: the new chat resumes.
  [`v1_test.go:1012`](../../backend/internal/server/v1_test.go#L1012)

- The block survives a restart; no profile row is touched.
  [`main_test.go:123`](../../backend/cmd/serve/main_test.go#L123)

- Wire order for a block in a held chat.
  [`chat.test.tsx:792`](../../plugin/hooks/chat.test.tsx#L792)
