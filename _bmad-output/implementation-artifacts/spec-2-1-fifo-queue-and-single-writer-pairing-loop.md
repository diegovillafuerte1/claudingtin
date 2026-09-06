---
title: 'Story 2.1 — FIFO queue and single-writer pairing loop'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: '8ab71a8e95945d06393477b50a6aebd406384ff9'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-2-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The backend hub is only an account-key connection registry (Story 1.4). It never reads `ready`/`busy`, holds no queue, and makes no matches — so two waiting Claude users are never paired.

**Approach:** Extend the AD-8 single-writer hub goroutine to also own a strict-FIFO wait queue, active pairings, `session_id` minting, and pairing teardown. Connection handlers translate inbound `ready`/`busy` into commands on that goroutine's channel; the goroutine pairs the longest waiter with the first eligible successor and hands each connection handler a `queued` or `matched` frame to write. Everything is in-memory and dies with the process.

## Boundaries & Constraints

**Always:**
- Exactly one goroutine mutates the queue, the pairings map, the `session_id` mint, and the opener-rotation cursor — the existing `hub.Hub.Run` select loop, extended (AD-8). Every other goroutine touches that state only by sending a command on the hub channel. No other code path mutates or reads it for a matching decision.
- The hub goroutine performs **zero network I/O**. `queued` / `matched` / `session_ended` reach a client only by a non-blocking send on a new buffered per-`Session` outbound channel that the connection handler drains — the same pattern as `Evict`.
- Exactly one goroutine ever writes frames to a given websocket connection (the connection handler's main `select`, not its read goroutine and not the hub).
- Matching is strict FIFO with no scoring: pair the queue head with the **first eligible successor** found scanning from the head within a bounded window; if none, the head waits. Eligibility is one predicate seam that in this story returns true for every non-self successor — block / cooldown / ban checks must slot into that seam in Epic 4 without reshaping the scan or the loop.
- An ineligible or unmatchable head never stalls the queue — the loop advances to the next candidate pair.
- Every wire frame is a `/proto` type via `proto.Encode` / `proto.Decode`. The only `/proto` change permitted here is adding payload fields to the existing `Matched` struct: `session_id`, `pseudonym`, `blurb`, `opener` (all `snake_case` json tags).
- `matched` carries a backend-minted opaque, unguessable `session_id` (≥128 bits from `crypto/rand`, URL-safe text) — exactly one per pairing, byte-identical for both peers. `pseudonym` / `blurb` come from the backend canonical profile, which is empty this epic, so both are `""`. `opener` is `""` in this story (Story 2.2 fills selection). A `matched` payload never contains an account key.
- `queued` is sent to a client that enters the queue and is not immediately matched.
- Leaving the queue while unmatched is silent: a `busy` frame, a disconnect, or a takeover-eviction removes the key from the queue with no `error` frame and no client-visible event of any kind.
- The hub goroutine writes no logs (preserve its I/O-free purity). Any match/teardown logging is done by the connection handler in structured JSON with no account key and no message content.
- `go build` / `go vet` / `go test -race` over the `go.work` module set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh` (still only `backend → proto`), and `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Adding any third-party dependency. `crypto/rand` + `encoding/base64` are stdlib and fine; anything else → HALT.
- Any `/proto` change beyond adding the four `Matched` payload fields (new message type, envelope/timestamp changes, a `Decode` variant) → HALT.
- If the connection handler cannot deliver outbound frames without either blocking the hub goroutine or introducing a second writer per connection → HALT and discuss before working around it.

**Never:**
- No opener *selection* logic and no opener file — Story 2.2. `opener` ships `""`.
- No message relay, no `chat_msg` handling — Story 2.4. `chat_msg` frames stay ignored.
- No `leave` handling (Story 3.3), no reconnect grace / silent re-enqueue (Epic 3). A torn-down pairing's surviving peer is **not** auto-re-enqueued here.
- No block / cooldown / ban / rate-limit / keyword eligibility beyond the self-match exclusion — Epic 4.
- No SQLite / persistence — queue, pairings, `session_id`s, and the opener cursor are in-memory only (AD-10).
- No companion changes. The Epic 1 companion already ignores unrecognized inbound frames (`wsclient.go:247`); it must stay untouched.
- No new HTTP routes; `/ws` and `GET /status` only (AD-13).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| Two ready → match | conns A and B each send `ready`; queue otherwise empty | both receive `matched` with a byte-identical, non-empty, opaque `session_id`; `pseudonym` / `blurb` / `opener` are `""`; a `queued` frame may or may not precede `matched` for either | N/A |
| First waiter | conn A sends `ready`; no other waiter | A receives `queued` and stays queued | N/A |
| FIFO order, 3+ | A, then B, then C send `ready` | A pairs with B (longest waiter + first eligible successor); C receives `queued` and waits | N/A |
| Self not eligible | key K already queued; same conn sends `ready` again | no self-match; K is queued once; no additional frame beyond a `queued` | idempotent, no error |
| busy while queued | conn A is queued, sends `busy` | A is removed from the queue; A receives no `error` and no frame; other waiters are undisturbed | silent |
| disconnect while queued | conn A is queued; socket drops | A is removed from the queue on unregister; nothing is sent to any client | silent, no panic |
| takeover while queued | key K queued on conn A; conn B sends `hello` for K | A is evicted (existing `session_ended` + close); K's queue entry is removed; conn B is a fresh connection and is **not** auto-queued | no dangling queue entry |
| busy while paired | A and B are paired; A sends `busy` | the pairing is torn down; B receives a bare `session_ended` (no cause, no `session_id`); neither peer is re-enqueued | N/A |
| disconnect while paired | A and B are paired; A's socket drops | the pairing is torn down on unregister; B receives a bare `session_ended`; B is not re-enqueued | no panic |
| unknown / malformed post-hello frame | a queued or paired conn sends an undecodable frame, or `chat_msg` / `leave` | ignored — no state change, no `error` frame (matches the v1 discard behavior) | silent |
| session_id opacity | many matches on one running backend | every `session_id` is unique, URL-safe, ≥128 bits of entropy, and not derived from any account key | N/A |
| log scrub | a full ready → match → teardown lifecycle using known account-key and would-be message constants | captured JSON logs contain no account key and no message text | hub logs nothing |

</frozen-after-approval>

## Code Map

- `proto/messages.go` — **edit.** `Matched` struct L117 is empty ("Payload fields land in a later epic"). Add `SessionID string \`json:"session_id"\``, `Pseudonym string \`json:"pseudonym"\``, `Blurb string \`json:"blurb"\``, `Opener string \`json:"opener"\``. No `registry` (L166-185) change — adding fields to an existing type needs none. Envelope (`protocol.go:25-28`) untouched; there is still no timestamp field and this story adds none.
- `proto/messages_test.go` — **edit.** `roundTripSamples` L14-35 has a bare `Matched{}` at L29 — populate all four fields with non-zero values so the round-trip and `TestPayloadFieldTagsAreSnakeCaseAndUnreserved` (L222-249) exercise them. `wireNames` (L39-58) and the registry-coverage tests are unaffected ("matched" is unchanged).
- `backend/internal/hub/hub.go` — **edit, the core of the story.** Today: `Session{Key, Conn, Evict}` L19-23; sealed `command` union L25 with `registerCmd` / `unregisterCmd` / `countCmd` L27-31; `Run(ctx)` L55-86 owns `sessions map[string]*Session` and a type switch; `send` L90-97; public `Register` / `Unregister` / `Count` L101-113.
  - Add to `Session`: `Outbound chan any` (buffered, cap ~4) — the handler-drained delivery channel.
  - Add commands `readyCmd{s *Session}` and `busyCmd{s *Session}`; add public `Ready(*Session)` / `Busy(*Session)` mirroring `Register`.
  - `Run` gains hub-owned state: an ordered `queue []*Session` and a `pairings map[*Session]*Session` (peer lookup, both directions). The type switch gains `readyCmd` (enqueue if not already queued/paired, then drain) and `busyCmd` (dequeue silently, or tear down a pairing and notify the peer).
  - Extend the `unregisterCmd` arm: after the existing identity-checked map delete, also remove the session from the queue and tear down any pairing (peer gets a bare `proto.SessionEnded{}` via its `Outbound`). Covers disconnect and takeover for free — both end the handler, which already `defer`s `Unregister`.
  - `drainQueue()`: while `len(queue) >= 2`, take the head and call `firstEligibleSuccessor`; on a hit, remove both, mint a `session_id`, deliver `matched` to each `Outbound`, record the pairing; on a miss for the head within the bound, stop.
  - `firstEligibleSuccessor(queue []*Session, eligible func(a, b *Session) bool) (idx int, ok bool)` scanning `queue[1:]` up to a bounded window (`const maxScan = 64` — the AD-8 bounded head scan; a `const`, not a mutable global, since the hub goroutine reads it). This story passes `func(a, b *Session) bool { return a.Key != b.Key }`.
  - Deliver to `Outbound` with a non-blocking `select { case s.Outbound <- msg: default: }` — hub never blocks; a full/absent channel means the handler is gone.
- `backend/internal/hub/id.go` — **new.** `newSessionID() string`: `crypto/rand` → 16 bytes → `base64.RawURLEncoding.EncodeToString` (~22 chars, URL-safe). Stdlib only. The backend cannot import the companion's `newUUIDv4` (`identity.go:303`) — dependency-direction guard — and `session_id` needs only "opaque, unguessable", not UUID shape (spine conventions / FR26).
- `backend/internal/hub/hub_test.go` — **edit.** Existing helpers: `newSession(key)` L12-14 (nil `Conn`), `startHub(t)` L17-30, `waitCount` L32-45, `isClosed` L47-54, `TestConcurrentRegisterAndCountRaceClean` L160-197. Add: `newSession` gives each fake session a buffered `Outbound`; FIFO order with 3+ sessions; two `Ready` → both `Outbound` get `matched` with equal, non-empty `session_id`; a lone `Ready` gets `queued`; `Busy` and stale `Unregister` remove from the queue with nothing sent; paired-peer `Busy` / `Unregister` sends the survivor a bare `session_ended`; a `-race` stress mixing `Register` / `Ready` / `Busy` / `Unregister` / `Count`.
- `backend/internal/hub/id_test.go` — **new.** `newSessionID` uniqueness across many calls, URL-safe charset, length/entropy floor.
- `backend/internal/server/server.go` — **edit.** `serveConn` L113-204: after `hub.Register` (L169), the post-hello read goroutine L179-187 currently discards every frame — change it to `proto.Decode` each frame and, on `proto.Ready` → `s.hub.Ready(sess)`, on `proto.Busy` → `s.hub.Busy(sess)`, everything else (incl. decode error) `continue`. The main `select` L189-203 gains a `case msg := <-sess.Outbound:` that calls `writeFrame(ctx, conn, msg)` (L208) — keeping the handler the sole writer for that conn. `sess.Outbound` is created alongside `sess.Evict` at L168.
- `backend/internal/server/server_test.go` — **edit.** Harness at L42-84, ws helpers `dial` / `writeMsg` / `readMsg` L97-141, `helloFor` L239, log-capture pattern `TestLogsCarryNoAccountKeyOrFrameText` L543-593. Add: two dials each send `hello` then `ready` → both read a `matched` with equal `session_id`; a third dial reads `queued`; a queued dial closing produces no `error` frame at the peers and `concurrent_users` behaves; a paired dial closing delivers `session_ended` to its peer; extend the log-scrub lifecycle through a match + teardown.
- `README.md` — **edit.** `## Backend` section (added in Story 1.4 at L14): note that `ready` / `busy` now drive an in-memory FIFO queue and pairing loop, that `queued` and `matched` frames are emitted, and that queue/pairing state dies with the process.
- `backend/cmd/serve/main.go` — **read-only.** `hub.New()` L42 + `go h.Run(hubCtx)` L43 are unchanged; the extended hub keeps the same constructor and `Run` signature.
- `.github/workflows/ci.yml` — **read-only.** `test` job already runs `go test -race` on the matrix (L55-61, added for AD-8 per retro F1).

## Tasks & Acceptance

**Execution:**
- [x] `proto/messages.go` — add the four `Matched` payload fields with `snake_case` json tags
- [x] `proto/messages_test.go` — populate the `Matched{}` round-trip sample with non-zero field values
- [x] `backend/internal/hub/id.go` — `newSessionID()` via `crypto/rand` + `base64.RawURLEncoding`
- [x] `backend/internal/hub/id_test.go` — uniqueness, charset, entropy-floor tests
- [x] `backend/internal/hub/hub.go` — add `Session.Outbound`; `readyCmd` / `busyCmd` + `Ready` / `Busy`; hub-owned `queue` + `pairings`; extend `unregisterCmd`; `drainQueue` + `firstEligibleSuccessor(pred)` with a bounded `maxScan`; non-blocking `Outbound` delivery; mint `session_id` in the goroutine
- [x] `backend/internal/hub/hub_test.go` — FIFO order, two-ready match with equal `session_id`, queued-until-matched, silent queue removal on `busy` / disconnect / takeover, paired-teardown `session_ended` to the survivor, `-race` stress over the mixed command set
- [x] `backend/internal/server/server.go` — decode `ready` / `busy` in the read goroutine and forward to the hub; drain `sess.Outbound` in the main `select` and write frames; create `sess.Outbound` beside `sess.Evict`
- [x] `backend/internal/server/server_test.go` — end-to-end match over two ws dials, third dial `queued`, queued-close produces no `error`, paired-close delivers `session_ended`, extended log-scrub
- [x] `README.md` — Backend section: FIFO queue + pairing loop, `queued` / `matched`, in-memory / dies with process
- [x] `go work sync` — no diff after commit

**Acceptance Criteria:**
- Given the running `backend serve` process, when queue, pairing, `session_id`, or teardown state is mutated, then every mutation happens inside the single `hub.Hub.Run` select loop and no other code path mutates it — verified by `go test -race` staying clean under concurrent `Register` / `Ready` / `Busy` / `Unregister` / `Count` and by inspection that those fields are only referenced within `Run`.
- Given the FIFO scan, when successor eligibility is evaluated, then it goes through one predicate function that currently returns true for every non-self successor, and adding block / cooldown / ban conditions to that function requires no change to the scan or the surrounding loop.
- Given the Epic 1 companion, unchanged, when the backend sends it `queued` or `matched`, then its websocket stays open and no error surfaces (the frames are ignored until Story 2.5).
- Given `gofmt -l .`, `go vet`, `go test -race` over the `go.work` module set, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` on the finished tree, when they run, then all pass and `check_deps.sh` still reports only `backend → proto`.

## Spec Change Log

## Design Notes

**Why extend the hub goroutine rather than add a second one.** AD-8 binds queue + pairing + live policy to *one* owner, and Epic 4's block/cooldown/ban reads happen in that same goroutine "for a matching decision". The pairing step also needs the key→conn relationship the hub already holds. A second single-writer goroutine would need a synchronized view of that map or a request/reply per match. So `readyCmd` / `busyCmd` join the existing command union, and `unregisterCmd` grows queue + pairing cleanup so disconnect and takeover are handled without new plumbing.

**Keeping the hub I/O-free.** `hub.go`'s standing rule is no network calls on the registry goroutine (takeover just `close`s `Evict`). Same here: on a match or teardown the goroutine does a non-blocking send of the ready-to-write `proto` value onto each `Session.Outbound`; the connection handler's `select` — already the only writer for that conn — drains it and calls `writeFrame`. A full or absent `Outbound` means the handler has gone; dropping is correct.

**The eligibility seam.** Shape the scan as:
```go
func firstEligibleSuccessor(queue []*Session, eligible func(a, b *Session) bool) (idx int, ok bool) {
    head := queue[0]
    for i := 1; i < len(queue) && i <= maxScan; i++ {
        if eligible(head, queue[i]) {
            return i, true
        }
    }
    return 0, false
}
```
Story 2.1 calls it with `func(a, b *Session) bool { return a.Key != b.Key }`. Epic 4 ANDs `!blocked(a,b) && !onCooldown(a,b) && !banned(b)` into that closure — the loop is untouched.

**`session_id`.** `crypto/rand` → 16 bytes → `base64.RawURLEncoding.EncodeToString` → ~22 URL-safe chars. Not UUID-shaped on purpose: the spine only requires "opaque, unguessable" (FR26). Minted inside the hub goroutine, one per pairing, identical for both peers.

**Paired-peer teardown.** Emits a bare `proto.SessionEnded{}` — already exactly AD-4-shaped (no cause, no `session_id`). No re-enqueue: Epic 3 Story 3.1 consolidates every end path and Story 3.5 adds silent re-enqueue-while-busy. This is a deliberate seam, not an omission.

**`Matched` payload is finalized here.** All four fields land in `/proto` now so the wire shape settles once. Story 2.2 adds only opener *selection* + the opener file; Story 2.5 renders the `queued` → `matched` states. `opener` is `""` from 2.1; `pseudonym` / `blurb` stay `""` until Epic 5's profile store exists.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` and `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `hub` pairing/FIFO tests, `id` tests, and `server` match tests green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no changes after commit
- `bash scripts/check_deps.sh` — still prints only `backend → proto`; `bash scripts/checks_test.sh` — exit 0

**Manual check:**
- `PORT=8080 go run ./backend/cmd/serve`; with two scripted ws clients on `/ws` each sending `hello` then `ready`, both receive a `matched` frame carrying the same `session_id`; a third client sending `hello` then `ready` receives `queued`; closing the third client leaves the first two unaffected and prints no error frame.

## Suggested Review Order

**The single-writer pairing loop (the heart of the change)**

- Entry point: one goroutine now owns the queue, pairings, and `session_id` mint alongside the registry — read the command switch first.
  [`hub.go:78`](../../backend/internal/hub/hub.go#L78)
- `readyCmd`: enqueue, drain, and only `queued` if still waiting — a session that matches on arrival never sees `queued` first.
  [`hub.go:132`](../../backend/internal/hub/hub.go#L132)
- `drainQueue`: pair the head with its first eligible successor while ≥2 wait; one `session_id` per pair, delivered byte-identical.
  [`hub.go:217`](../../backend/internal/hub/hub.go#L217)
- The eligibility seam — `a.Key != b.Key` today; Epic 4 ANDs block/cooldown/ban here without touching the scan.
  [`hub.go:87`](../../backend/internal/hub/hub.go#L87)
- Bounded head scan (AD-8), now a `const` since the hub goroutine reads it.
  [`hub.go:247`](../../backend/internal/hub/hub.go#L247)

**Departures and teardown**

- `busyCmd` / `unregisterCmd`: silent queue removal or pairing teardown (survivor gets a bare `session_ended`), then a re-drain — the Epic-4 seam where a departing head can unblock a waiting pair.
  [`hub.go:142`](../../backend/internal/hub/hub.go#L142)
- Takeover now evicts the displaced session from the queue/pairing immediately, closing the race where it could still be matched during its unwind.
  [`hub.go:95`](../../backend/internal/hub/hub.go#L95)
- `teardownPair` deletes both directions and notifies only the survivor — no re-enqueue (Epic 3 owns that).
  [`hub.go:202`](../../backend/internal/hub/hub.go#L202)

**Hub stays I/O-free**

- `deliver`: non-blocking send onto `Session.Outbound`; a full/nil channel means the handler is gone and dropping is correct.
  [`hub.go:260`](../../backend/internal/hub/hub.go#L260)
- `session_id` minter: `crypto/rand` → `base64.RawURLEncoding`, its own backend helper (can't reach the companion's UUID across the dep guard).
  [`id.go:18`](../../backend/internal/hub/id.go#L18)

**Connection handler wiring**

- Read goroutine decodes each post-hello frame; only `ready` / `busy` act, everything else (incl. decode error) is ignored.
  [`server.go:198`](../../backend/internal/server/server.go#L198)
- The handler's `select` drains `sess.Outbound` and is the sole writer for the socket; a later `session_ended` after a `matched` still lands.
  [`server.go:218`](../../backend/internal/server/server.go#L218)

**Wire contract**

- `Matched` gains `session_id` / `pseudonym` / `blurb` / `opener` (last three empty until Stories 2.2 / Epic 5).
  [`messages.go:120`](../../proto/messages.go#L120)
- Backend README section: FIFO queue + pairing loop, `queued` / `matched`, silent unmatched departure, in-memory / dies with the process.
  [`README.md:29`](../../README.md#L29)

**Tests (peripherals)**

- Hub unit tests: FIFO order, equal `session_id`, silent queue removal on `busy` / disconnect / takeover, paired teardown, `-race` stress.
  [`hub_test.go:277`](../../backend/internal/hub/hub_test.go#L277)
- Over-the-wire: two dials match, third gets `queued`, queued-close is silent, paired-close and `busy`-while-paired deliver `session_ended`.
  [`server_test.go:462`](../../backend/internal/server/server_test.go#L462)
- `session_id` opacity: URL-safe, 128-bit floor, 100k-unique.
  [`id_test.go:8`](../../backend/internal/hub/id_test.go#L8)
