---
title: 'Reconnect grace window'
type: 'feature'
created: '2026-09-13'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'e38b8eeed162a441667f330fcb1830531194a4bd'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** A bare socket drop while paired tears the pairing down immediately (`server.go`'s deferred `Unregister` → `teardownPair`), so a wifi blip ends the chat — and, once a match-rate limiter exists, would burn a cooldown — even though the same account key could reconnect a second later.

**Approach:** On disconnect, hold a paired session's pairing for `reconnectGraceWindow` (~10s) instead of tearing it down. A reconnect presenting the same account key and that session's own `session_id` in `Hello` repoints the pairing to the new connection with no `session_ended` sent. A timeout, or a reconnect with no/wrong `session_id`, falls through to today's exact teardown.

## Boundaries & Constraints

**Always:**
- `Hello` gains an optional `SessionID` (`omitempty`); a plain connect or fresh re-match omits it, wire form unchanged.
- Grace applies only to a session **paired** at disconnect (`unregisterCmd`). Queued-or-idle-at-disconnect is unchanged: immediate silent removal, no timer.
- `sessions[key]` and both `p.pairings` entries stay untouched for the whole window; a resume repoints them, a timeout runs the same `teardownPair` the immediate path already used (so it inherits Story 3.5's re-enqueue the moment that lands — today the same no-op every other cause has).
- A resume never calls `drainQueue` or mints a `session_id` — structurally exempt from anything that later gates on match-rate budget.
- A `Hello` naming a `session_id` that is not currently held (wrong, or the hold expired) gets one `proto.SessionEnded` on its *own* new connection, resetting a stale local pane via the existing cause-agnostic frame and the companion's existing `client.SessionEnded()` handling — no companion branching added.
- `wsclient` remembers the last `Matched.session_id` and offers it on the very next `Hello` only, clearing it the instant `SessionEnded` is surfaced or `SendLeave` runs.
- `reconnectGraceWindow` is a package var, not a const (matches `sessionEndedCloseGrace` / `backoffBase`), so tests can shrink it.

**Ask First:** buffering `chat_msg` sent to the disconnected side during the outage vs. accepting it's lost like any other v1 relay frame (no ack/retry) — confirm before adding buffering.

**Never:** no new wire type (`session_ended` stays `struct{}`, a successful resume sends nothing to anyone); no change to `leaveCmd`/`busyCmd` timing (stays immediate); no rate-limit code (none exists yet); no re-enqueue logic beyond routing expiry through the existing `teardownPair` (Story 3.5's job).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior |
|----------|--------------|---------------------------|
| Paired, brief drop, quick resume | A-B paired; A drops, reconnects <10s later with matching key + `session_id` | B gets no `session_ended`; A's new connection is linked into the existing pairing; relay works both ways again |
| Paired, drop, no reconnect | A-B paired; A drops; nothing reconnects before expiry | B gets exactly one `session_ended`, byte-identical to today's; A's key is fully unregistered |
| Paired, drop, reconnect with wrong/absent/expired `session_id` | A-B paired; A drops; a new connection for A's key arrives with no id, a stale one, or after expiry | any still-held pairing tears down now (B gets `session_ended`); the new connection also gets one `session_ended` |
| Queued, drop | A queued, never matched; A drops | unchanged: silent queue removal, no timer |

</frozen-after-approval>

## Code Map

- `proto/messages.go:27-30` (`Hello`) -- add `SessionID string \`json:"session_id,omitempty"\``.
- `backend/internal/hub/hub.go`:
  - `Session` struct `:30-35` -- add `SessionID string` (hub-owned: set in `drainQueue` at match, read at disconnect/resume), `ResumeSessionID string` (server-written once from the inbound `Hello` before `Register`, hub-read once at register time), `graceTimer *time.Timer` (non-nil while held). Extend the doc's field-ownership list; add `"time"` import.
  - New var near `maxScan` (`:67`): `var reconnectGraceWindow = 10 * time.Second` (doc like `sessionEndedCloseGrace`, `wsclient.go:44-50`).
  - New command `graceExpiredCmd{s *Session}` + `isCommand()`, grouped at `:39-62`.
  - `drainQueue` `:296-301` -- after minting `m`, set `head.SessionID = m.SessionID; succ.SessionID = m.SessionID`.
  - `case registerCmd:` `:112-134` and `case unregisterCmd:` `:135-151` -- see Design Notes for the full branch; the exact algorithm is spelled out there rather than restated here.
  - New `case graceExpiredCmd:` -- see Design Notes.
- `backend/internal/server/server.go:172-178` -- set `sess.ResumeSessionID = hello.SessionID` before `s.hub.Register(sess)`.
- `backend/internal/hub/hub_test.go` -- add `shrinkGraceWindow(t)` (mirrors `wsclient_test.go`'s `shrinkBackoff`); update `TestUnregisterWhilePairedNotifiesSurvivor` (`:750`) to shrink+wait; add resume-succeeds, mismatched/expired-resume, and queued-drop-unaffected cases.
- `backend/internal/server/server_test.go` -- update `TestPairedDialClosingDeliversSessionEndedToPeer` (`:554`) the same way; add an e2e resume-over-websocket case (extend `helloFor` with a `session_id` param, or add a sibling helper).
- `companion/internal/wsclient/wsclient.go`:
  - `Client` struct `:104-118` -- add `resumeID string` guarded by `mu`.
  - `Run` `:209-212` -- set `SessionID: resumeID` on the outgoing `Hello` when non-empty.
  - `serve`, `case proto.Matched:` `:378-388` -- store `m.SessionID` into `resumeID`.
  - `case proto.SessionEnded:` `:401-409` -- clear `resumeID`.
  - `SendLeave` `:288-296` -- clear `resumeID` before writing.
- `companion/internal/wsclient/wsclient_test.go` -- extend the `TestReconnectResendsHello` pattern: matched-then-drop resends `Hello` with that id; leave-then-drop and ended-then-drop resend `Hello` with none.
- `companion/internal/run` -- no change. A failed resume already arrives as a plain `proto.SessionEnded`, handled today by `run.go:472-495`; a successful resume is invisible to `run.loop` by design.

## Tasks & Acceptance

**Execution:**
- [x] `proto/messages.go` -- add `Hello.SessionID` -- lets a reconnecting client name the session it wants back.
- [x] `backend/internal/hub/hub.go` -- add the three `Session` fields, `ReconnectGraceWindow`, `graceExpiredCmd`; mint `SessionID` in `drainQueue`; grace-hold in `unregisterCmd`; resume/mismatch branching in `registerCmd`; `graceExpiredCmd` handling -- the hold/resume/expiry state machine.
- [x] `backend/internal/server/server.go` -- set `ResumeSessionID` from `hello.SessionID` -- carries the resume request into the hub.
- [x] `backend/internal/hub/hub_test.go` -- `shrinkGraceWindow`; update the affected test; add resume/mismatch/queued cases.
- [x] `backend/internal/server/server_test.go` -- update the affected tests; add an e2e resume case.
- [x] `companion/internal/wsclient/wsclient.go` -- track/clear/offer `resumeID`.
- [x] `companion/internal/wsclient/wsclient_test.go` -- reconnect-with-id / leave-then-reconnect / ended-then-reconnect cases.

**Acceptance Criteria:**
- Given a companion disconnects mid-session, when it reconnects within the grace window presenting the same account key and `session_id`, then the session resumes, relay continues, and the peer received no `session_ended` during the window.
- Given the grace window expires with no reconnect, when the backend times the hold out, then the peer receives `session_ended` exactly once, byte-identical to every other cause.
- Given a resume occurs, when the hub processes it, then no `drainQueue` call and no `session_id` mint happen on that path.
- Given a session was only queued (never paired) when its socket dropped, when the backend processes the disconnect, then behavior is unchanged: silent queue removal, no timer.

## Spec Change Log

- **Finding:** step-03's Matrix Test Audit found the implementing subagent's code (and this spec's own Design Notes sketch) delivered the new connection's `SessionEnded` only when nothing at all was held for the key (the fully-expired case) — not when something *was* held but under a different `session_id` (the still-held-mismatch case). This contradicted the frozen Boundaries bullet ("wrong, **or** the hold expired") and the frozen I/O Matrix row, both of which cover all three sub-cases uniformly.
- **Amended:** the Design Notes code sketch (non-frozen) now sends `SessionEnded` to the new connection whenever a named `session_id` did not resolve to a resume, via a `resumed` flag spanning both the resume and takeover branches. `backend/internal/hub/hub.go`'s `registerCmd` and `backend/internal/hub/hub_test.go`'s `TestReconnectWithWrongSessionIDTearsDownHeldPairing` were corrected to match.
- **Avoids:** a companion that guesses wrong (or races) on its resume attempt while a *different* pairing is still held silently keeping a stale chat pane open forever, with no frame ever telling it otherwise.
- **KEEP:** the quick-resume-within-window and expired-resume-gets-a-frame behaviors were already correct and unchanged; only the still-held-mismatch sub-case was fixed.

- **Finding:** step-04's Blind Hunter and Edge Case Hunter review layers independently caught the same real bug: in the resume branch, `resumed = true` was set unconditionally once `graceTimer != nil` and the `session_id` matched — even when the inner `p.pairings[prev]` lookup then failed. That lookup fails whenever `prev`'s live counterpart already left or went busy while `prev` was grace-held (`teardownPair` deletes both pairing directions but has no reason to touch `prev`'s timer, since it doesn't know `prev` is grace-held). The reconnecting client was then registered as "resumed" with no pairing and no `SessionID`, and got no `SessionEnded` either — silently orphaned until its next `Ready`/`Busy` push.
- **Amended:** `resumed` is now set only inside the successful `if peer, ok := p.pairings[prev]; ok { ... }` block. On failure it falls through to the existing `!resumed && cmd.s.ResumeSessionID != ""` tail, which correctly delivers the reconnecting connection a `SessionEnded` — no other cleanup needed, since the unconditional `sessions[cmd.s.Key] = cmd.s` at the end of `registerCmd` already retires `prev` from the registry either way, and `prev`'s own timer was already stopped just above. Added `TestResumeAfterPeerLeftDuringGraceGetsSessionEnded` (verified it fails against the pre-fix code, passes after).
- **Avoids:** a reconnecting companion whose old chat partner left while it was disconnected being silently registered as if nothing happened, with no pairing and no frame ever telling it the session actually ended.
- **Rejected sibling fix:** the reviewers also suggested having `teardownPair` itself stop and evict a torn-down pairing's *other* side's `graceTimer` when that side happens to be grace-held. Not needed: the `resumed` fix alone makes every path self-heal correctly (a reconnect gets the `SessionEnded` fallback above; a reconnect that never comes still gets cleaned up when the original timer fires, since `graceExpiredCmd`'s `delete(sessions, ...)` runs regardless of `teardownPair`'s no-op). Adding it would touch `teardownPair`'s signature and every call site for no behavioral gain.
- **Deferred, not blocking:** the same reviewers flagged that `Hub.Count()` counts a grace-held (socket-dead) session as connected for up to `ReconnectGraceWindow` — a pre-existing informational-only `/status` metric, not a correctness issue; logged to `deferred-work.md` rather than fixed here.

- **Finding:** the verification-gap review layer found that every resume/mismatch test disconnects `prev` (arming `graceTimer`) before attempting a resume, so `registerCmd`'s condition `prev.graceTimer != nil && ... ResumeSessionID == prev.SessionID` never exercised its first conjunct — the ID match alone would pass every existing test. A regression dropping that conjunct would let a reconnect naming a still-live session's own `session_id` silently steal its pairing with no eviction and no notification to anyone, or (as manual verification confirmed) crash the hub goroutine outright on `prev.graceTimer.Stop()` against a nil timer.
- **Amended:** added `TestResumeAttemptAgainstStillLiveSessionIsOrdinaryTakeover` — `a` stays fully live and paired (never `Unregister`ed) while `a2` reconnects naming `a`'s own current `session_id`; asserts the ordinary-takeover path (Evict closed, B gets `session_ended`, a2 also gets one since its named resume did not resolve) rather than a silent repoint. Verified: passes against the correct code, and crashes with a nil-pointer panic when the `graceTimer != nil` conjunct is manually removed — confirming the guard is load-bearing, not redundant.
- **Avoids:** a future refactor that "simplifies" the resume condition to just the ID check shipping a hub-crashing regression (or, short of a crash, a silent session hijack) with every existing test still green.

## Design Notes

`registerCmd` gains one branch, told apart by the incoming `Hello.SessionID` against whatever `sessions[key]` currently holds. `graceTimer` lives on `Session` itself (not a side table), so the existing `sessions` / `p.pairings` maps stay the single source of truth and the existing stale-command guards (`cur == cmd.s`) protect it for free:

```go
case registerCmd:
    resumed := false
    if prev, ok := sessions[cmd.s.Key]; ok && prev != cmd.s {
        if prev.graceTimer != nil && cmd.s.ResumeSessionID != "" &&
            cmd.s.ResumeSessionID == prev.SessionID {
            prev.graceTimer.Stop()
            prev.graceTimer = nil
            if peer, ok := p.pairings[prev]; ok {
                delete(p.pairings, prev)
                p.pairings[cmd.s] = peer
                p.pairings[peer] = cmd.s
                cmd.s.SessionID = prev.SessionID
                resumed = true
            }
            // peer, ok can fail: prev's live counterpart may have already
            // left/gone busy while prev was grace-held. resumed stays false,
            // falling through to the tail below — prev needs no further
            // cleanup, sessions[cmd.s.Key] = cmd.s retires it either way.
        } else {
            if prev.graceTimer != nil {
                prev.graceTimer.Stop()
                prev.graceTimer = nil
            }
            p.removeFromQueue(prev)
            p.teardownPair(prev)
            close(prev.Evict)
            p.drainQueue(eligible)
        }
    }
    if !resumed && cmd.s.ResumeSessionID != "" {
        deliver(cmd.s, proto.SessionEnded{}) // named resume did not resolve
    }
    sessions[cmd.s.Key] = cmd.s
```

Note the `resumed` flag: a `Hello` naming a `session_id` gets `SessionEnded` on its own connection whenever that name did not resolve to a resume — whether because nothing was held for the key at all (wrong/expired), or because something *was* held but under a different id (the takeover branch tore it down instead). Only a plain takeover with no named `session_id` stays silent to the new connection, exactly as before this story.

`unregisterCmd` only changes for the currently-registered, currently-paired case: instead of `removeFromQueue` + `teardownPair` + `drainQueue`, it just arms `cmd.s.graceTimer = time.AfterFunc(reconnectGraceWindow, func() { h.send(graceExpiredCmd{s: cmd.s}) })`. Every other case (stale registrant, or not paired) is untouched.

`graceExpiredCmd` guards `sessions[cmd.s.Key] == cmd.s && cmd.s.graceTimer != nil` (else stale — already resumed or superseded), then nils the timer and runs `delete(sessions, cmd.s.Key); p.teardownPair(cmd.s); p.drainQueue(eligible)` — the deferred tail of today's `unregisterCmd`.

## Verification

**Commands:**
- `cd backend && go test -race ./...` -- expected: pass, including new grace cases.
- `cd companion && go test -race ./...` -- expected: pass, including new reconnect cases.
- `cd proto && go test ./...` -- expected: pass (round-trip covers the new `Hello` field).
- `go vet ./...` in each of `backend/`, `companion/`, `proto/` -- expected: no diagnostics.
- `gofmt -l backend/ companion/ proto/` -- expected: no output.

## Suggested Review Order

**The hold/resume/expiry state machine (start here)**

- Entry point: the resume-vs-takeover branch, and the `resumed` flag that only becomes true once a pairing is actually repointed
  [`hub.go:139`](../../backend/internal/hub/hub.go#L139)

- Why `resumed` starts false and is set only inside the successful pairing lookup — the fix for the orphaned-resume bug two review layers caught
  [`hub.go:146`](../../backend/internal/hub/hub.go#L146)

- The fallback: any named `session_id` that didn't resolve into a resume gets told so, on its own connection, via the existing cause-agnostic frame
  [`hub.go:204`](../../backend/internal/hub/hub.go#L204)

- Disconnect while paired now arms a hold instead of tearing down immediately — `sessions`/`p.pairings` stay untouched for the whole window
  [`hub.go:217`](../../backend/internal/hub/hub.go#L217)

- Deferred timeout: the exact tail of the old immediate-teardown path, now guarded against staleness
  [`hub.go:253`](../../backend/internal/hub/hub.go#L253)

- `graceTimer` lives on `Session` itself, not a side table, so existing stale-command guards protect it for free
  [`hub.go:48`](../../backend/internal/hub/hub.go#L48)

- The mint-time stamp a resume later compares against
  [`hub.go:414`](../../backend/internal/hub/hub.go#L414)

**The wire and the carrying fields**

- `Hello` gains an optional, `omitempty` field — a plain connect's wire form is unchanged
  [`messages.go:33`](../../proto/messages.go#L33)

- The server carries a resume request from the decoded `Hello` into the hub, before `Register`
  [`server.go:182`](../../backend/internal/server/server.go#L182)

**The companion's side of a resume**

- `offerResumeID` is read on every (re)connect's `Hello` — empty unless a chat is believed active
  [`wsclient.go:213`](../../companion/internal/wsclient/wsclient.go#L213)

- Remembered on `Matched`, cleared on `SendLeave` and on `SessionEnded` — the three lifecycle hooks that keep it honest
  [`wsclient.go:389`](../../companion/internal/wsclient/wsclient.go#L389)
  [`wsclient.go:295`](../../companion/internal/wsclient/wsclient.go#L295)
  [`wsclient.go:419`](../../companion/internal/wsclient/wsclient.go#L419)

**Tests**

- The two bugs two rounds of review caught, each with a test that fails on the buggy code and passes on the fix
  [`hub_test.go:953`](../../backend/internal/hub/hub_test.go#L953)
  [`hub_test.go:1014`](../../backend/internal/hub/hub_test.go#L1014)

- The three matrix rows: quick resume, wrong/expired id, and the e2e companion over a real websocket
  [`hub_test.go:807`](../../backend/internal/hub/hub_test.go#L807)
  [`hub_test.go:859`](../../backend/internal/hub/hub_test.go#L859)
  [`hub_test.go:905`](../../backend/internal/hub/hub_test.go#L905)
  [`server_test.go:600`](../../backend/internal/server/server_test.go#L600)

- The companion-side resume-id lifecycle, mirrored one hook at a time
  [`wsclient_test.go:290`](../../companion/internal/wsclient/wsclient_test.go#L290)
  [`wsclient_test.go:327`](../../companion/internal/wsclient/wsclient_test.go#L327)
  [`wsclient_test.go:376`](../../companion/internal/wsclient/wsclient_test.go#L376)
