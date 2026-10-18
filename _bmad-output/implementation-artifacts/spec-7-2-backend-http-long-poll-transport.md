---
title: 'Story 7.2: Backend HTTP long-poll transport'
type: 'feature'
created: '2026-10-18'
status: 'done'
review_loop_iteration: 0
baseline_commit: '4bfa3e0ae278978da640b262bb23004ab1211612'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-7-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The mod can't hold a websocket; it needs an HTTPS API to the hub to queue, chat and leave.

**Approach:** Add `/v1/hello`, `/v1/events` (long-poll) and `/v1/send` in a new `server` file in front of the **unchanged** hub. Each handle owns one hub `Session` at a time, a pump draining its `Outbound` into one bounded seq'd event queue (AD-19), and a lease whose lapse calls `hub.Unregister` so the existing grace window runs (AD-17). `/proto` gains `welcome`, drops `heartbeat`, and writes golden fixtures.

## Boundaries & Constraints

**Always:** `hub.go` and the `/ws` handler code stay byte-identical; only `routes()` and the package doc change in `server.go`. Every response body is a `/proto` value or an event list. Client errors return 400 with an `error` body, never a 5xx; the exceptions are `please_update` (200) and 410 for a dead handle. Logs carry no handle, key or content. Handle-registry mutation happens under one mutex in the server package; queue/pairing state stays in the hub.

**Ask First:** changing hub code or `/ws` behaviour; any new dependency.

**Never:** mod/TS work (7.3+); deleting `/ws` (7.6); rate limits or content caps beyond a request-body size cap (Epic 4); persisting anything.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Hello OK | `{type:hello,v,account_key,protocol_version}` | 200 `welcome{handle}`; lease timer starts | — |
| Hello too old | `protocol_version < PROTOCOL_VERSION-1` | 200 `please_update`, no handle minted | — |
| Bad hello | undecodable / not hello / empty key | 400 `error{bad_frame|expected_hello}` | no handle |
| Supersede | 2nd hello for key with live handle H1 | H1 queue gets `session_ended` (unless its last event already is one), then H1 polls with nothing pending and all H1 sends get 410 | — |
| Events ready | `after=N`, events `seq>N` queued | 200 JSON array in seq order, each `{seq, type, v, ...}`; events ≤N dropped | — |
| Events idle | nothing `>N` | held ≤20s, then `[]` | bad `after` → 400 `error` |
| Overflow | queue at cap (256) and a new event arrives | `hub.Leave(sess)` once; a single `session_ended` is appended past the cap; later relays until next `matched` are dropped | — |
| Send | ready/busy/leave/chat_msg | 204, forwarded to hub as `/ws` does | — |
| Send other | other client types | 204, ignored (as `/ws`) | server→client type or undecodable → 400 `error` |
| Lease lapse | no poll in flight for >5s | `hub.Unregister(sess)`; handle kept for 60s | — |
| Resume by poll | poll/send on lapsed handle | new hub `Session` registered with `ResumeSessionID` = last `matched.session_id` seen; then `Ready` replayed if last intent was ready | grace expired → hub delivers `session_ended` |
| Resume by hello | hello with key + `session_id` inside grace | new handle; pairing resumes, peer gets nothing | — |
| Unknown handle | missing/unknown/GC'd header | 410 `error{handle_gone}` | — |

</frozen-after-approval>

## Code Map

- `backend/internal/hub/hub.go` -- READ-ONLY. `Session{Key,Conn,Evict,Outbound,ResumeSessionID}`; `Register/Unregister/Ready/Busy/Leave/Relay`. `deliver` drops on a full `Outbound` (cap set by caller). Resume needs a **new** `*Session` naming the held `SessionID`. `SessionID`/`WantsMatch` are hub-owned, so don't read them; track session_id from `matched` events instead. Resume does NOT close the old session's `Evict`.
- `backend/internal/server/server.go` -- `routes()` (add the `/v1/*` routes) and package doc; `serveConn` (L130–314) is the reference for hello gating, `sentEnd`, and ignored frames; `remoteHost`.
- `backend/internal/server/server_test.go` -- `/ws` tests; L485 writes `proto.Heartbeat{}` → switch to a raw `{"type":"heartbeat","v":1}` frame (same "ignored" assertion). The log-redaction test is at L1146.
- `backend/cmd/serve/main.go:56-61` -- only `ReadHeaderTimeout` is set, so a 20s hold is safe; no change.
- `proto/messages.go`, `proto/protocol.go`, `proto/messages_test.go` -- registry, samples, `wireNames`.

## Tasks & Acceptance

**Execution:**
- [x] `proto/messages.go` -- add `Welcome{Handle string}` (`welcome`), remove `Heartbeat`, and add `EncodeEvent(seq int64, msg any)` that adds `"seq"` to the flat form. Update `protocol.go` doc to say transport-neutral/HTTPS.
- [x] `proto/messages_test.go` -- update samples, and add a golden test that compares each sample's `Encode` (plus one `EncodeEvent`) with `testdata/fixtures/<type>.json`. `-update` regenerates; commit the fixtures.
- [x] `backend/internal/server/v1.go` -- new file: handle registry, hello/events/send handlers, per-handle pump, lease and GC timers. Hold, lease, TTL and cap are `server` fields so tests can shrink them. Handle header: `X-Claudingtin-Handle`. 32-byte crypto/rand base64url handle. 64 KiB body cap.
- [x] `backend/internal/server/server.go` -- register the routes (POST-only hello/send, GET-only events, otherwise 405) and update the package doc.
- [x] `backend/internal/server/v1_test.go` -- one test per I/O-matrix row, plus a two-client contract test (hello → ready → matched with equal session_id → chat both ways, never echoed → A leave → B `session_ended` → A ready → rematched), and a no-handle/key/text log test.
- [x] `backend/internal/server/server_test.go` -- the Heartbeat line only.

**Acceptance Criteria:**
- Given the change, when `go test -race ./...` runs in `proto` and `backend`, then all tests pass and `git diff main -- backend/internal/hub` is empty.
- Given a paired handle whose lease lapses, when its peer sends chat during grace and the handle then polls, then the chat lines are delivered in order (the old session's pump keeps draining) and the peer never sees `session_ended`.

## Design Notes

Pump ordering: the hub delivers to the old session only until it processes the new `Register`. So when the pump takes a message from the new `Outbound`, it first drains the old one non-blocking, which keeps hub order. Supersede is decided in the HTTP layer at hello time; an `Evict` on the current session (a `/ws` takeover of the same key) is treated as supersede too. A dead handle (superseded with nothing pending, or unknown) gets 410. "Never two `session_ended` in a row" replaces `/ws`'s `sentEnd` latch, and it resolves the deferred 3.1 idle-displaced row: an HTTP takeover always ends with `session_ended`, then 410.

Review refinements (iteration 0, patch-level):
- Only `chat_msg` counts against the cap. State events (`queued`, `matched`, `session_ended`) are always appended, so a fresh match is never lost to a full queue.
- `busy`, `leave` and overflow clear the replayed-ready intent. `busy` and `leave` also clear the tracked `session_id`.
- `after > lastSeq` is a 400.
- `reregister` stops the pump two generations back, so the pump chain stays bounded.

## Verification

**Commands:**
- `export PATH="/opt/homebrew/bin:$PATH"; (cd proto && go test -race ./...) && (cd backend && go test -race ./...) && go vet ./proto/... ./backend/...` -- expected: green.

**Manual checks:**
- After deploy, run a 20s held `/v1/events` through Fly's proxy and confirm it returns `[]` with no 502 (needs Diego).

## Suggested Review Order

**Handle lifecycle (hello, supersede, lease)**

- Entry point: mint a handle, retire any older handle for the key, register with the hub.
  [`v1.go:405`](../../backend/internal/server/v1.go#L405)

- AD-16 supersede is decided here, before the hub's takeover runs.
  [`v1.go:443`](../../backend/internal/server/v1.go#L443)

- Lease lapse calls Unregister under the lock, so it can't be reordered after a resume.
  [`v1.go:332`](../../backend/internal/server/v1.go#L332)

- Re-register names the last session_id, so the hub's grace window resumes the pairing.
  [`v1.go:354`](../../backend/internal/server/v1.go#L354)

- Retire: terminal session_ended, key freed, then 410 once drained.
  [`v1.go:288`](../../backend/internal/server/v1.go#L288)

**Ordered event queue (AD-19)**

- Pump handoff: finish the predecessor before the first push keeps hub order.
  [`v1.go:174`](../../backend/internal/server/v1.go#L174)

- Overflow is counted on chat only, and ends the session with one session_ended.
  [`v1.go:220`](../../backend/internal/server/v1.go#L220)

- Never two session_ended in a row; replaces /ws's sentEnd latch.
  [`v1.go:259`](../../backend/internal/server/v1.go#L259)

- Long-poll: ack, hold, wake on notify; an `after` past the last seq is rejected.
  [`v1.go:460`](../../backend/internal/server/v1.go#L460)

**Send path**

- Intent recorded before re-register; same hub calls as a /ws frame.
  [`v1.go:559`](../../backend/internal/server/v1.go#L559)

**Wire contract**

- `welcome` added; `EncodeEvent` stamps `seq` onto the flat form.
  [`messages.go:269`](../../proto/messages.go#L269)

- Golden fixtures the mod's TS types will round-trip.
  [`fixtures_test.go:46`](../../proto/fixtures_test.go#L46)

- Production wiring: one line in routes().
  [`server.go:53`](../../backend/internal/server/server.go#L53)

**Tests**

- Two-client contract: match → chat → leave → rematch.
  [`v1_test.go:712`](../../backend/internal/server/v1_test.go#L712)

- Resume inside grace keeps the chat sent while the handle was lapsed.
  [`v1_test.go:595`](../../backend/internal/server/v1_test.go#L595)

- Real New() wiring plus a /ws takeover of a /v1 handle.
  [`v1_test.go:801`](../../backend/internal/server/v1_test.go#L801)
