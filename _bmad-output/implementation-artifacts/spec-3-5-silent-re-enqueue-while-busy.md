---
title: 'Silent re-enqueue while busy'
type: 'feature'
created: '2026-09-15'
status: 'done'
review_loop_iteration: 0
baseline_commit: '144195fdbb5d764eeb37279448a1c3e5fe43d8ec'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Tearing down a pairing (peer busy, peer leave, disconnect past the Story 3.4 grace window, or connection takeover) only ever sends the survivor a `session_ended`; `teardownPair`'s own doc comment says "Neither peer is re-enqueued." A survivor still mid-think-time is stranded instead of getting another match.

**Approach:** Track each session's most recent `Ready`/`Busy` signal on `Session`. When `teardownPair` ends a pairing, silently re-enqueue the surviving peer (no `queued` frame) iff its last signal was `Ready` and it isn't itself grace-held. Every `teardownPair` caller already re-drains the queue right after, so a match forms in the same tick when one is available. This also surfaces a companion-side ordering hazard that must close for the feature to actually work: a same-tick re-pair means `session_ended` and the new `matched` can both be sitting ready on their separate wsclient channels, and nothing today orders them for `run.loop`'s `select`.

## Boundaries & Constraints

**Always:**
- `Session.WantsMatch bool` (hub-owned): set `true` in `case readyCmd`, `false` in `case busyCmd`, unconditionally, before that case's existing enqueue/teardown logic.
- `teardownPair` re-enqueues its looked-up `peer` (via `p.enqueue`, never emitting `Queued`) iff `peer.WantsMatch && peer.graceTimer == nil`. The `graceTimer == nil` guard is load-bearing: a grace-held peer's `SessionID` must never be overwritten by a fresh pairing while it might still complete a resume (Story 3.4).
- The caller of `teardownPair(s)` (`s` itself — busy or leave) is never re-enqueued; only `s`'s surviving peer is a candidate. Unchanged from today.
- `run.loop` must handle an in-band `session_ended` before a same-tick `matched` that followed it on the wire. wsclient's read goroutine already dispatches frames in true wire order (decode-then-forward, one frame at a time), so add a non-blocking priority check for `client.SessionEnded()` at the top of `loop`'s `for`, before the main `select` — Go's `select` has no ordering guarantee between two channels that are both already ready, so relying on the main select alone can service the new `matched` first.
- No new wire types or fields; `session_ended` and `matched` are unchanged on the wire.

**Never:** no rate-limiter code (none exists yet — a future limiter must gate the public `Ready()`/`readyCmd` entrypoint, not `p.enqueue`, so this silent path stays structurally exempt from match-rate budget); no change to grace-window (3.4), leave (3.3), or takeover semantics beyond adding the one re-enqueue call.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior |
|---|---|---|
| Peer goes busy, survivor still `Ready` | A-B paired, both `WantsMatch=true`; A goes `Busy` | B gets one `session_ended`, no `queued`; B silently rejoins the queue tail; a later `Ready` elsewhere pairs with B |
| Peer leaves, survivor still `Ready` | A-B paired; A sends `leave` | Same as above for B |
| Grace window expires, survivor still `Ready` | A-B paired; A disconnects, grace expires with no resume | B silently re-enqueued the instant expiry fires |
| Takeover displaces one side, survivor still `Ready` | A-B paired under key k; a new connection for k registers | B silently re-enqueued |
| Survivor's own last signal was `Busy` | A-B paired; B's `WantsMatch=false` when its chat ends for any cause | B gets `session_ended`, is never re-enqueued |
| Survivor is itself grace-held | B is grace-held (disconnected, mid-window); something tears down A's side of the pairing B is still holding | B is not enqueued while `graceTimer != nil`; B's `SessionID` is untouched for its own pending resume |

</frozen-after-approval>

## Code Map

- `backend/internal/hub/hub.go:40-49` (`Session`) -- add `WantsMatch bool`, hub-owned.
- `backend/internal/hub/hub.go:268-286` (`case readyCmd` / `case busyCmd`) -- set `cmd.s.WantsMatch` before each case's existing logic.
- `backend/internal/hub/hub.go:357-375` (`teardownPair`) -- after `deliver(peer, proto.SessionEnded{})`, add `if peer.WantsMatch && peer.graceTimer == nil { p.enqueue(peer) }`; update its doc comment (drops the "Neither peer is re-enqueued" line).
- `backend/internal/hub/hub_test.go` -- new cases per the I/O matrix (busy/leave/grace-expiry/takeover re-enqueue; busy-survivor and grace-held-survivor do not).
- `companion/internal/run/run.go:336-337` (`loop`'s `for { select {`) -- add the non-blocking `client.SessionEnded()` priority check described above.
- `companion/internal/run/run_test.go` -- new case proving the same-tick reorder is handled (see Tasks).

## Tasks & Acceptance

**Execution:**
- [x] `backend/internal/hub/hub.go` -- add `WantsMatch`, set it in `readyCmd`/`busyCmd`, re-enqueue in `teardownPair` -- the core silent-requeue mechanism.
- [x] `backend/internal/hub/hub_test.go` -- cover the I/O matrix: still-`Ready` survivors of busy/leave/grace-expiry/takeover get silently re-enqueued (proven by pairing them with a fresh third session); a `Busy` survivor and a grace-held survivor never are.
- [x] `companion/internal/run/run.go` -- priority-check `client.SessionEnded()` ahead of the main `select` -- stops a same-tick `matched` from being serviced (and silently dropped by the existing "ignore a second matched while chat is active" guard) before the `session_ended` it followed.
- [x] `companion/internal/run/run_test.go` -- queue an in-band `session_ended` then a new `matched` back-to-back before `loop` reads either; assert the old chat tears down and the new one launches rather than leaving the spinner stuck.

**Acceptance Criteria:**
- Given a session ends while the surviving user's last signal was `Ready`, when the backend processes the end, then that user is placed on the queue tail within the same tick, with no `queued` frame sent.
- Given a session ends after the surviving user's last signal was `Busy`, when the backend processes the end, then that user is not re-enqueued.
- Given a peer is grace-held (Story 3.4) when its held pairing is torn down from the other side, when the backend processes that teardown, then the grace-held peer is not enqueued and its `SessionID` is left untouched.

## Spec Change Log

- **Finding:** the new silent re-enqueue changed behavior three existing `hub_test.go` tests relied on implicitly — each paired A/B, tore down A's side, then readied a fresh third session expecting it to land only a bare `queued` (proving the queue was empty). With B now silently re-enqueued in that same teardown, the fresh session instead paired immediately with B, so `recvOutbound` type-asserted a `proto.Matched` where a `proto.Queued` was expected and failed.
- **Amended:** `TestUnregisterWhilePairedNotifiesSurvivor`, `TestReconnectWithWrongSessionIDTearsDownHeldPairing`, and `TestReconnectAfterGraceExpiryGetsSessionEndedAndCanRematch` each gained one `h.Busy(b)` right after confirming B's `session_ended`, draining B back out of the queue so the rest of each test's original assertion (about a *different* pairing) is not entangled with Story 3.5's own re-enqueue — which now has its own dedicated tests.
- **Finding:** the I/O-matrix row "Survivor's own last signal was Busy" describes a state — a peer sitting in `p.pairings` with `WantsMatch` already `false` — that cannot be reached through the public Hub API: `busyCmd` always tears down its own caller's pairing in the same command that flips `WantsMatch` to `false`, so nothing can observe "paired AND WantsMatch==false" from outside the hub package.
- **Amended:** `TestTeardownPairDoesNotReenqueuePeerWhoseLastSignalWasBusy` constructs a `pairingState` directly and calls `teardownPair` on it (this file is `package hub`) instead of driving it through `Hub`'s command loop — the only way to exercise that guard in isolation.
- **Avoids:** silently regressing three pre-existing teardown/resume tests when a later change touches queue timing, and leaving the one truly-unreachable matrix row without any direct coverage of its guard.
- **Finding (review):** the successful-resume branch of `registerCmd` (Story 3.4) repointed a held pairing onto the new `*Session` and carried over `SessionID`, but not `WantsMatch` — a brand-new `*Session` defaults `WantsMatch` to `false` regardless of what the grace-held `prev`'s was, so a session resumed mid-chat (reconnected, no fresh `Ready`/`Busy` of its own yet) silently lost Story 3.5 eligibility until its next state frame — exactly the case this story exists for.
- **Amended:** `cmd.s.WantsMatch = prev.WantsMatch` added alongside the existing `cmd.s.SessionID = prev.SessionID` carry-over. New test `TestResumedSessionInheritsWantsMatchAndIsSilentlyReenqueued` covers it (verified to fail without the fix).
- **Finding (review):** `TestSameTickSessionEndedBeforeMatchedLaunchesNewChat` was empirically weak — with the companion-side priority-check fix manually removed it still passed ~65% of the time (Go's `select` sometimes services `session_ended` first by chance alone), contradicting its own doc comment's claim to prove the fix rather than scheduler luck.
- **Amended:** the test now runs the same race+assertion as 20 independent subtests, each with its own fresh harness, requiring every one to pass — driving the false-negative probability to statistically negligible. Verified: with the fix removed, every one of several full runs failed; with the fix restored, 20/20 subtests pass across 15+ repeated full runs.
- **Amended (doc-only):** reworded three comments that referred to Story 3.5 as future/open work now that it has landed and settled the question: the `IntentLeave` case in `companion/internal/run/run.go` (the local spinner it raises is not backed by any backend re-enqueue — the backend never re-enqueues an explicit leaver), and the `leaveCmd` case comment plus the `Hub.Leave` doc comment in `backend/internal/hub/hub.go` (both now state definitively that `teardownPair` only ever considers the peer, never the leaver, as a re-enqueue candidate).

## Design Notes

`teardownPair` is already documented as "the single cause-agnostic emission point" for every teardown cause (busy, leave, disconnect-past-grace, takeover), so it is the one place the re-enqueue belongs; every caller already runs `p.drainQueue(eligible)` immediately after, which is what turns a silent re-enqueue into an immediate rematch when one is available — no caller needs its own re-enqueue logic.

The companion-side fix is not defensive polish, it is required for the backend change to work end-to-end: `wsclient.go` dispatches onto `matched`/`sessionEnded` in true wire order via non-blocking sends (`select { case ch <- m: default: }`), but once a same-tick re-pair means both channels can be ready at once, `run.loop`'s plain `select` can service `matched` first. At that instant `pm.chatActive()` is still true (the old chat hasn't been torn down yet), so the existing "ignore a stray second matched while chat is active" guard would silently drop the real new match — the user would be stuck showing the searching spinner while the backend believes they are already paired again, with no retry to recover it.

## Verification

**Commands:**
- `cd backend && go test -race ./...` -- expected: pass, including the new re-enqueue/guard cases.
- `cd companion && go test -race ./...` -- expected: pass, including the new ordering case.
- `go vet ./...` in `backend/` and `companion/` -- expected: no diagnostics.
- `gofmt -l backend/ companion/` -- expected: no output.

## Suggested Review Order

**The re-enqueue decision (start here)**

- `teardownPair` is the single cause-agnostic point every teardown routes through; this is where the silent re-enqueue lives
  [`hub.go:408`](../../backend/internal/hub/hub.go#L408)

- `readyCmd`/`busyCmd` unconditionally record the session's own latest signal before their existing logic runs
  [`hub.go:289`](../../backend/internal/hub/hub.go#L289)
  [`hub.go:300`](../../backend/internal/hub/hub.go#L300)

- `WantsMatch` field and its write/read contract, alongside the other hub-owned `Session` fields
  [`hub.go:53`](../../backend/internal/hub/hub.go#L53)

**The resume interaction (review-caught bug)**

- A resumed session is a brand-new `*Session`; without this line it silently loses re-enqueue eligibility until its own next Ready/Busy
  [`hub.go:188`](../../backend/internal/hub/hub.go#L188)

- The leaver/busy caller is never itself a re-enqueue candidate — only its peer, both in the doc comment and at the call sites
  [`hub.go:309`](../../backend/internal/hub/hub.go#L309)
  [`hub.go:549`](../../backend/internal/hub/hub.go#L549)

**The companion-side ordering fix**

- The non-blocking priority check that drains a pending `session_ended` ahead of the main select, closing the same-tick reorder hazard
  [`run.go:380`](../../companion/internal/run/run.go#L380)

- `handleSessionEnded` extracted so the priority path and the main select's own case share identical logic
  [`run.go:338`](../../companion/internal/run/run.go#L338)

**Tests**

- The review-caught resume gap, with a test that fails on the pre-fix code and passes on the fix
  [`hub_test.go:866`](../../backend/internal/hub/hub_test.go#L866)

- The four I/O-matrix re-enqueue rows (busy, leave, grace-expiry, takeover), all following the same shape
  [`hub_test.go:1163`](../../backend/internal/hub/hub_test.go#L1163)

- The two guard rows: a grace-held survivor is never touched, and a `WantsMatch=false` survivor is never re-enqueued
  [`hub_test.go:1333`](../../backend/internal/hub/hub_test.go#L1333)
  [`hub_test.go:1384`](../../backend/internal/hub/hub_test.go#L1384)

- The same-tick ordering race, run as 20 independent subtests after review found a single run only ~65% reliable
  [`run_test.go:2619`](../../companion/internal/run/run_test.go#L2619)
