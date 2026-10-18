---
title: 'Story 7.4: Think-time from turn events, and "no surface, no queue"'
type: 'feature'
created: '2026-10-18'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'd063d6bb72646cf02629f3f4df0c3d1723919178'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-7-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The mod has a key and a gate, but it never joins the queue. The backend's `/v1` long-poll transport (7.2) has no client.

**Approach:** Add a `/v1` client to the mod. The main-loop `turn.start` and `turn.complete` set a desired state, `ready` or `busy`. One background worker brings the backend in line with it:
- It runs a lazy `hello`.
- It sends `ready` or `busy`.
- During a burst it runs one events long-poll, which holds the lease and acks by `seq`.

`ready` is sent only after the gate clears and only on a surface where the mod's UI draws.

## Boundaries & Constraints

**Always:**
- Hooks call `void work($)` and return `next(e)`. No hook awaits the network, and no throw escapes. Every failure goes quiet: the person sees nothing.
- Only main-loop turns count (`e.agentId === undefined`). A burst sends exactly one `ready`, however many continuation turns or tool calls it has.
- A turn is eligible only when all of these hold:
  - `requireGate($)` is true.
  - `loadAccountKey($)` returns a key.
  - `$.session.surfaces()` is non-empty.
  - The AbovePrompt band hook has been asked to render this session (the "band rendered" signal).
- In `$.state` (`link`, so a hot reload resumes): `handle`, `after`, `want`/`sent` (`ready`|`busy`|null), `quiet`, `outdated`, and a poll `gen`. The key never goes in `$.state`, and the key and handle never reach any log, UI or error.
- Wire format comes from the `/proto` fixtures:
  - hello `{type:'hello',v:1,protocol_version:1,account_key}`.
  - The handle travels in header `X-Claudingtin-Handle`.
  - `/v1/send` answers 204.
  - `/v1/events` returns a bare JSON array of `{seq,type,v,…}`.
- Every request runs under the existing abandon-timer pattern: 5s for hello/send, 25s for events (above the 20s server hold). At most one events poll is in flight.

**Ask First:** a new dependency; any backend or `/proto` change; adding a `session_id` to hello (resume belongs to 7.5).

**Never:**
- Rendering chat, spinner or events, or surfacing any state (7.5).
- `persist` (3.6).
- Removing the companion or `/ws` (7.6).
- Polling `/v1/events` outside a burst.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Burst start | main `turn.start`, eligible, no handle | hello → `welcome` stores handle, `after=0`; then send `ready`; start events poll | hello fails → nothing; retried at next `turn.start` |
| Burst end | main `turn.complete` (any reason) after `ready` sent | send `busy`, stop poll; nothing surfaced | fails → dropped silently |
| Fast burst | start then complete before hello lands | worker sees `want=busy`, `sent=null`: no hello, no ready | — |
| Subagent / second turn | `agentId` set, or main `turn.start` while `sent=ready` | nothing sent | — |
| Not eligible | gate false, no key, empty surfaces, or band never rendered | no hello, no ready, no poll | — |
| Hot reload | `session.start` with handle in `$.state`, `sent=ready` | poll resumes on the same handle, no hello | — |
| Events | `[{seq:3,…},{seq:2,…}]`, after=2 | `after` becomes max seq (3); seq ≤ after ignored | bad JSON → backoff, retry |
| Stale handle | `ready` send → 410 | drop handle, hello once, send `ready` | second 410 → quiet |
| Superseded | events poll → 410 | drop handle, `quiet=true`, stop; no hello until next main `turn.start` clears quiet | — |
| Outdated | hello → `please_update` | `outdated=true`, no more hellos this session | — |
| Poll error | network error, timeout, non-410 4xx/5xx | backoff 1s→2s→…→15s cap while the burst lasts | — |

</frozen-after-approval>

## Code Map

- `plugin/hooks/register.tsx`:
  - The `session.start` poller is the model for the abandon timer and the `gen` guard.
  - Add main-loop `turn.start`/`turn.complete` hooks.
  - Set the band-rendered flag in the AbovePrompt hook, before the `online === null` early return.
  - On `session.start`, resume the worker.
- `plugin/hooks/safety.tsx:106` -- `requireGate($)`. It may open the pane unasked, which is fine.
- `plugin/hooks/identity.ts:53` -- `loadAccountKey($)`. Its module-level `inFlight` single-flight is the precedent for the worker's module-level guard.
- `plugin/types/index.d.ts` -- add the `Link` type and the `link` and `drawn` keys to `PluginState.claudingtin`.
- `plugin/hooks/register.test.tsx` -- test idioms: `mock.clock(on)`, `on('http.fetch', …)` returning `{value:{status,ok,headers,text}}`, `$.session.start`.
- `plugin/.claude-plugin/types/claude-code/index.d.ts` -- API truth:
  - `TurnStartInput` L14038 and `TurnCompleteFields` L13936: `agentId` is absent on the main loop.
  - `$.session.surfaces()` L2872: empty under `-p` and VS Code.
  - `HttpInit` L5336: no abort and no timeout.
  - The test kit starts at L15590. Raise turns via `$.turn.start`/`$.turn.complete`, and mock surfaces with `on('session.surfaces', …)`. Verify both there.
- `backend/internal/server/v1.go` -- READ-ONLY reference:
  - `handleHello` L405, `handleEvents` L460 (an `after` beyond the last event gets a 400), `handleSend` L566.
  - Lease 5s, handle TTL 60s.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/hooks/link.ts`:
  - `onTurnStart($)`, `onTurnEnd($)` and `resume($)` each set `want` (and clear `quiet` on start), then `void work($)`.
  - `work` is a single-flight reconcile loop: eligibility → hello → send → poll. It re-reads `want` after each await until `want === sent`.
  - Also here: a `pollLoop` that is `gen`-guarded with backoff, plus the request helpers.
- [x] `plugin/hooks/register.tsx` -- wire the turn hooks (main loop only), the band-rendered flag, and `resume` on `session.start`.
- [x] `plugin/types/index.d.ts` -- add the `Link` state type.
- [x] `plugin/hooks/link.test.ts` -- every matrix row, plus: the fetch log never contains the key except in the hello body, and the 7.3 and band tests stay green.

**Acceptance Criteria:**
- Given a hung backend, when turns start and end, then each `turn.start`/`turn.complete` hook resolves without waiting on any fetch (assert the hook settles while the fetch mock never resolves).
- Given an eligible burst, when it runs, then the fetch log is exactly: hello, send `ready`, events polls carrying the handle header, send `busy`, with no `/v1` traffic before or after.
- Given `claude plugin validate --strict plugin`, when run, then no errors.

## Design Notes

**Why `want`/`sent` instead of queued calls.** Ordering comes from the reconcile loop, not from a promise chain. A fast burst coalesces to no traffic, and `busy` can never overtake `ready`.

**The surface check is a heuristic.** Pairing surfaces with band-rendered keeps out:
- hosts with no surface (`-p`, the SDK, VS Code), and
- hosts that attach a surface but never draw the mod.

A detached terminal that still lists `terminal` will still queue. That is accepted under AD-20's reach note.

**Two sessions on one key** alternate by turn. Each `hello` supersedes the other, but a superseded session stays quiet until its own next `turn.start`. That bounds the churn to one hello per turn.

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all tests pass.

**Manual checks:**
- With a local backend, run a real session. One prompt should log `v1 hello` once and then lease activity. A second prompt should send no hello.

## Suggested Review Order

**The reconcile worker (where turns become traffic)**

- Entry point: one reconcile loop driving `want` vs `sent`; read this first.
  [`link.ts:156`](../../plugin/hooks/link.ts#L156)

- Single-flight worker with an "again" flag; captures `gen` so a reload's old round stops.
  [`link.ts:104`](../../plugin/hooks/link.ts#L104)

- `sent` goes null right before ready, so a timed-out ready still gets its busy.
  [`link.ts:209`](../../plugin/hooks/link.ts#L209)

- Stale handle: 410 on ready re-hellos once, a second 410 goes quiet.
  [`link.ts:218`](../../plugin/hooks/link.ts#L218)

- Busy 410 drops the handle so the next burst says hello again.
  [`link.ts:173`](../../plugin/hooks/link.ts#L173)

**No surface, no queue**

- Eligibility: band drawn, surfaces non-empty, gate, key — abandoned at 5 s.
  [`link.ts:126`](../../plugin/hooks/link.ts#L126)

- Render hooks can't write state, so the drawn flag is deferred to a timer.
  [`link.ts:39`](../../plugin/hooks/link.ts#L39)

**Hook wiring (nothing awaits the network)**

- Main-loop turns only; hooks await a state write, never a fetch.
  [`register.tsx:126`](../../plugin/hooks/register.tsx#L126)

- Band render marks the surface drawn; session.start resumes the link.
  [`register.tsx:164`](../../plugin/hooks/register.tsx#L164)

**Events long-poll**

- One loop per gen + handle; it exits when the handle changes.
  [`link.ts:241`](../../plugin/hooks/link.ts#L241)

- Superseded (410) → quiet until next turn.start; 400 resets `after`.
  [`link.ts:280`](../../plugin/hooks/link.ts#L280)

- Nothing new in a reply → 500 ms pace, so a non-holding proxy can't spin it.
  [`link.ts:303`](../../plugin/hooks/link.ts#L303)

- Fetch can't be cancelled, only abandoned; shared by requests and eligibility.
  [`link.ts:334`](../../plugin/hooks/link.ts#L334)

**Peripherals**

- `Link` state type: kept in `$.state` so hot reload resumes; never holds the key.
  [`index.d.ts:25`](../../plugin/types/index.d.ts#L25)

- Full burst wire log, key-only-in-hello and handle-only-in-header assertions.
  [`link.test.ts:144`](../../plugin/hooks/link.test.ts#L144)

- Race tests: hello after turn end, supersede during eligibility, hung key read.
  [`link.test.ts:406`](../../plugin/hooks/link.test.ts#L406)
