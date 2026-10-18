---
title: 'Sprint Change Proposal — move claudingtin into Claude Code (mods pivot)'
date: 2026-10-17
author: Diego (with Claude, bmad-correct-course)
mode: batch
status: approved 2026-10-17 — applied
scope: major
trigger: 'Strategic pivot, 2026-10-16: replace the Go companion TUI + mux pane with a Claude Code mod that does everything over HTTP'
inputs:
  - _bmad-output/planning-artifacts/prds/prd-claudingtin-2026-08-31/prd.md
  - _bmad-output/planning-artifacts/epics.md
  - _bmad-output/planning-artifacts/architecture/architecture-claudingtin-2026-09-01/ARCHITECTURE-SPINE.md
  - _bmad-output/specs/spec-claudingtin/SPEC.md (+ glossary.md, voice.md)
  - _bmad-output/planning-artifacts/research/technical-claude-code-plugins-ui-companion-fronten-2026-10-07/research.md
  - _bmad-output/implementation-artifacts/spec-mods-skeleton.md
  - _bmad-output/implementation-artifacts/sprint-status.yaml
  - _bmad-output/implementation-artifacts/deferred-work.md
---

# Sprint Change Proposal — the mods pivot

## 1. Issue summary

**Trigger.** Strategic pivot (category: *strategic pivot / new platform capability*), decided by Diego on 2026-10-16. Claude Code shipped **mods** (v2.1.287): a plugin can draw UI inside the Claude Code process for the whole session, including a docked `Pane`, an `AbovePrompt` band, buttons and inputs. claudingtin's v1 design predates mods. It runs a separate Go companion TUI that tails the session transcript, holds a websocket, and places itself in a tmux / mux split or a new OS window. Diego wants claudingtin to live natively inside Claude Code instead.

**Diego's locked decisions** (2026-10-16, confirmed 2026-10-17):

1. **The mod does everything.** It is TypeScript in `plugin/` and talks to the backend over HTTP using `$.http.fetch`. The Go companion goes away. He chose this over keeping the companion as a headless helper spawned by the mod.
2. **Busy/ready comes from mod turn events,** not from watching the transcript.
3. **Pane placement is removed:** the tmux/windowterm/placement code, the `session-start` launcher, and the companion-launching `SessionStart` hook. He accepts the reach regression: mod UI does not draw in the VS Code chat panel, Remote Control viewers, `claude -p`, the Agent SDK, cloud sessions or Desktop-WSL. The removal lands **in the same PR as the mod chat surface**.
4. **Transport is long-poll** (decided 2026-10-17). The mod holds one `GET /v1/events` open; the backend answers when an event is ready or after about 20 s, and the mod polls again at once.
5. **`/ws` and `companion/` are deleted** in that same chat-slice PR. `/proto` stays as the single source of wire shapes.

**Evidence and current state.**

- The first slice is built. Commit `b76083d` on `feat/mods-skeleton` adds an `AbovePrompt` band that polls `GET /status` (spec: `spec-mods-skeleton.md`, status `done`). `claude plugin validate --strict` and `claude plugin test` both pass.
- The mods API now covers what the 2026-10-07 research doubted. Source: the bundled `claude-code.d.ts`, written by Claude Code 2.1.301.
  - `turn.start` / `turn.complete` fire per main-loop turn (`e.agentId` is undefined for the main loop). That is exactly the think-time burst, including FR3's "tool calls don't split a turn".
  - `$.process.spawn` is now a documented, session-long streaming child. This settles the research's one disputed claim. It is not used under decision 1, but remains a fallback.
  - `$.fs` can read and write absolute paths, so the mod can reuse the companion's existing account-key file.
  - `$.ui.open` reports `isPlaced`, and there is a `session.attach` event. Together they let the mod tell whether its UI is actually on screen.
  - A clipboard write exists (`isCopied`), which FR24 needs.
  - `$.command.register` adds slash commands, used as keyboard fallbacks for leave / block / report.
- Bookkeeping drift found along the way:
  - PR #30 (Story 3.5) is **merged** to `main`, but `sprint-status.yaml` still shows `3-5` as `backlog`.
  - `feat/mods-skeleton` branched off `a3008d5` and is **behind `main`** by #30.
  - The PRD has **two FR6 entries**; the first is stale.

## 2. Impact analysis

### Checklist record

| # | Item | Status | Finding |
|---|---|---|---|
| 1.1 | Triggering story | [x] | Not a story failure. A pivot, proven by the mods-skeleton slice (`b76083d`). |
| 1.2 | Core problem | [x] | Strategic pivot. The v1 spoke (companion + pane) is replaced by an in-process mod. |
| 1.3 | Evidence | [x] | Above. Skeleton works; the API facts come from `claude-code.d.ts` 2.1.301. |
| 2.1 | Current epic (3) | [!] | 3.1–3.5 done. 3.6 / 3.7 are client-heavy, so building them in the companion now is wasted work. Pause them until the mod reaches parity. |
| 2.2 | Epic-level changes | [!] | Add **Epic 7: Native in Claude Code** and sequence it next. |
| 2.3 | Remaining epics | [!] | Epics 4–6 need their client-side wording moved from "companion" to "mod". Backend substance is unchanged. |
| 2.4 | Invalidated / new epics | [!] | No future epic is invalidated. Epic 1's companion stories (1.3, 1.6, 1.7, 1.8) are superseded *as built*; they stay as history. |
| 2.5 | Order / priority | [!] | New build order: E7 → 3.6–3.7 → E4 → E5 → E6. |
| 3.1 | PRD conflicts | [!] | §1 Shape; UJ-1; F1 (FR1, FR2, FR5, both FR6s); NFR6, NFR10, NFR12; OQ-1; glossary; assumptions index. MVP *goals* are unchanged. |
| 3.2 | Architecture conflicts | [!] | Units table; AD-3, AD-5, AD-6, AD-7, AD-11, AD-12, AD-13, AD-14, AD-15, AD-16, AD-17; Stack; Structural Seed; Capability map; Open Question. Two new ADs. |
| 3.3 | UX conflicts | [x] | There is no UX spine. `voice.md` holds (one word: "pane"). UX lives in FRs and stories, handled there. |
| 3.4 | Other artifacts | [!] | SPEC.md (CAP-1, CAP-3, CAP-4, CAP-10, CAP-11, CAP-13, CAP-14; Constraints; Assumptions; OQ) and glossary.md. CI/release (no cross-built binaries; add mod validate/test). sprint-status. deferred-work (about 12 companion-only items become obsolete). |
| 4.1 | Direct adjustment | [x] Viable | Add an epic and retarget the stories. Backend hub logic (queue, pairing, grace, re-enqueue, session_ended) carries over unchanged behind a new transport. |
| 4.2 | Rollback | [N/A] | Nothing to revert. Epics 1–3 stay valid until 7.6 deletes the companion. |
| 4.3 | MVP review | [x] | MVP holds. One accepted narrowing: surfaces where mods don't draw (decision 3). |
| 4.4 | Path | [x] | **Direct adjustment, with a major architecture amendment.** |

### Epic impact

- **Epics 1–2 (done):** stay done. The companion-side pieces are superseded by Epic 7: the transcript watcher (1.3, 1.6), the launcher (1.7), placement (1.8), the TUI gate (1.9), the chat TUI (2.3) and the spinner (2.5). The backend pieces carry forward: 1.4, 2.1, 2.2, 2.4. Proto (1.1) and identity (1.5, its on-disk path) also carry forward.
- **Epic 3:** 3.1–3.5 are done; their backend semantics carry forward. 3.6 and 3.7 are **paused** and retargeted to the mod. They are re-sequenced after Epic 7.
- **Epic 7 (new):** six stories, listed in §4.1. 7.1 is the skeleton and is already built.
- **Epics 4–6:** same stories and same backend substance. Client-facing ACs change "companion menu / pane" to "mod pane / slash command / userConfig", "WSS" to "HTTPS", and "SERVER_URL" to `userConfig.backendUrl`.

### Technical impact

- **Backend:** adds a `/v1` HTTP client API (hello, a long-poll events endpoint, send) in front of the **unchanged hub**. Each client gets a handle with an ordered event queue and a lease. `/ws` and `coder/websocket` are removed in 7.6.
- **`/proto`:** drops `heartbeat` (the lease replaces it) and adds `welcome` (`handle`). Golden JSON fixtures emitted by proto tests become the contract the mod's TS types are checked against.
- **Plugin:** turns into pure TS. `cmd/session-start`, `hooks/session-start.sh`, `bin/`, the Go module and the `SessionStart` hook are all removed.
- **CI/release:** drops the companion cross-build matrix and binary attach. Adds `claude plugin validate --strict plugin` and `claude plugin test plugin` (clears a deferred-work item). The Windows companion flakes go away.
- **Bug-class win:** the companion's `select` races (`session_ended` vs `chat_msg` vs `matched`, logged in deferred-work by 3.2 and 3.3) cannot happen with **one seq-ordered event stream**.
- **New risk:** the mods API is early access and changes "between releases without notice". Mitigation: pin a minimum Claude Code version, and re-check `claude-code.d.ts` on each bump (see the new Open Question).

## 3. Recommended approach

**Direct adjustment.** Add Epic 7, retarget the remaining stories, and amend the PRD, the spine and the SPEC in place.

- **Why not rollback:** nothing shipped is wrong. The hub is transport-agnostic behind its command channel (AD-8), so all of Epic 2–3's backend work survives.
- **Why not an MVP cut:** goals G1–G5 and metrics M1/C1–C3 are unchanged. Only *where the surface lives* changes.
- **Effort:** Epic 7 is about 5 stories of new work (7.2–7.6). 7.2 is a medium backend story; 7.5 is the largest (about the size of 2.3 + 2.5 + the client half of 3.2–3.5 together). 7.5 and 7.6 ship as one PR (decision 3).
- **Risk:** medium. API churn is the top item. Long-poll behaviour behind Fly's proxy needs one real check in 7.2, though the 60 s idle limit is well above the 20 s hold.
- **Timeline:** Epics 4–6 slip by the length of Epic 7. No external date is affected.

## 4. Detailed change proposals

### 4.1 Epics (`epics.md`)

**E-1. Overview › Scope line**

```
OLD: Scope: v1 only — Claude Code plugin + bundled companion TUI + one hosted matching/relay backend.
NEW: Scope: v1 only — a Claude Code plugin whose mod draws the chat inside Claude Code and talks to
     one hosted matching/relay backend over HTTPS. (Mods pivot, 2026-10-17: the bundled Go companion
     TUI and pane placement built in Epics 1–3 are superseded by Epic 7.)
```

**E-2. Requirements Inventory**: mirror the PRD edits in §4.2 for FR1, FR2, FR5, FR6, NFR6, NFR10, NFR12 and NFR14. Mark these ARs `[SUPERSEDED by AR31–AR37, mods pivot]` and keep their text: AR3 (the TUI/websocket/fsnotify stack), AR4, AR5–AR8 (websocket wording), AR21, AR22, AR24, AR25, AR26 (companion half) and AR30. Then append:

- **AR31:** The client is the plugin's mod, TypeScript loaded by Claude Code (no build step). It has no out-of-process component and ships no binaries.
- **AR32:** Client transport is HTTPS JSON:
  - `POST /v1/hello` mints a handle.
  - `GET /v1/events?after=<seq>` is a long-poll held up to about 20 s and returns ordered `{seq, …msg}` events.
  - `POST /v1/send` posts one client→server message.

  The handle travels in a request header. The account key is sent only in `hello`.
- **AR33:** Each handle has an ordered, bounded event queue with monotonic `seq`. The client acknowledges by passing `after`, so delivery is at-least-once and deduplicated by `seq`.
- **AR34:** Connection liveness is a lease. A handle counts as connected while an events poll is in flight or for about 5 s after one ends. A lapsed lease is a disconnect, which starts the AD-17 grace window.
- **AR35:** Think-time comes from main-loop `turn.start` → `ready` and `turn.complete` → `busy`. Subagent turns are ignored.
- **AR36:** No surface, no queue. The mod sends `ready` only while its UI is drawn on an attached surface. Headless, VS Code chat panel and Remote-Control-only sessions never queue.
- **AR37:** The mod reads its configuration from plugin `userConfig` (`backendUrl`, defaulting to the public instance from Epic 6; and `persist`). The backend stays env-only.

**E-3. FR Coverage Map**

```
OLD: FR1: Epic 1 — companion process launched by the plugin on SessionStart
     FR2: Epic 1 — transcript-watch determines think-time boundaries; ready/busy to backend
     FR5: Epic 1 — pane placement (tmux / mux split, new-window fallback, one-line hint last)
     FR6: Epic 1 — plugin is a fail-open no-op ...
NEW: FR1: Epic 7 — the mod is the chat surface (Epic 1 companion superseded)
     FR2: Epic 7 — main-loop turn events determine think-time; ready/busy to backend
     FR5: Epic 7 — chat draws in a mod Pane + AbovePrompt band; no external pane
     FR6: Epic 7 — fail-open; no surface ⇒ no queue
```

**E-4. Epic List**: add after Epic 6, plus a build-order line:

```
### Epic 7: Native in Claude Code (mods pivot)
Replace the companion TUI and pane placement with a Claude Code mod that talks to the backend over a
long-poll HTTPS API: the backend /v1 transport in front of the unchanged hub, the account key and
first-run gate in the mod, think-time from turn events with a "no surface, no queue" rule, the full
chat surface (spinner + spin, chat pane, leave/block/report, end presentation, silent re-enqueue,
grace resume) at parity with Epics 1–3, and deletion of the companion, /ws, the launcher and the
binary release pipeline.
**FRs covered:** FR1, FR2, FR3, FR4, FR5, FR6 (re-delivered), FR10, FR16, FR18–FR21 (client side, re-delivered), FR43–FR46 (re-delivered)

**Build order (2026-10-17):** E1 → E2 → E3 (3.1–3.5) → **E7** → E3 (3.6–3.7) → E4 → E5 → E6.
```

**E-5. New Epic 7 stories**

```
### Story 7.1: Mods skeleton — backend-status band   [DONE: b76083d, spec-mods-skeleton.md]
(as built: AbovePrompt band polling GET /status; userConfig.backendUrl; fail-open; 4 s timeout)

### Story 7.2: Backend HTTP long-poll transport
As a mod client, I want an HTTPS API to the hub, so that I can queue, chat and leave without a socket.
- Given POST /v1/hello {v, account_key, session_id?} with a supported version, then 200 `welcome`
  {handle}; an unsupported version gets `please_update`; a second hello for the same key supersedes
  the old handle (AD-16): the old handle's queue gets `session_ended`, then its requests get 410.
- Given GET /v1/events?after=N, when events with seq > N exist, then they return at once in seq
  order; otherwise the request is held ≤ ~20 s and returns an empty list. Events ≤ N are dropped
  from the queue (ack). The queue is bounded; overflow ends the session with `session_ended`.
- Given POST /v1/send with any client→server proto message, then it reaches the hub exactly as the
  /ws handler's frames do today; protocol errors return an `error` body, never a 5xx.
- Given no events poll for longer than the lease (~poll cap + 5 s), then the hub sees a disconnect and
  the AD-17 grace window runs; a poll on the same handle (or hello with key + session_id) inside
  grace resumes with no `session_ended` to the peer.
- The hub, its tests and /ws are untouched; a contract test drives two HTTP clients through
  match → chat → leave → re-enqueue. Logs carry no handle, key or content. Proto gains `welcome`,
  loses `heartbeat`, and emits golden JSON fixtures for every message type.
- Closes epic-2-retro-item-11 (deliver() reports success) for the HTTP path.

### Story 7.3: Account key and first-run gate in the mod
- The mod reads or creates the account key at os.UserConfigDir()/claudingtin/account-key (the
  companion's path; existing keys carry over) via $.fs, 0600, regenerating a corrupt or empty file;
  the key never appears in $.ui.log, toasts or state visible to other plugins.
- Before any request other than GET /status, the mod opens a focused Pane with the first-run
  screen (what it is, honest safety warning, block/report explainer, "I am 18 or older"). Accept is
  stored locally only; decline leaves the mod inert and re-asks next session; /claudingtin safety
  re-opens it. Copy per voice.md.

### Story 7.4: Think-time from turn events, and "no surface, no queue"
- Main-loop turn.start (e.agentId undefined) → `ready`; turn.complete → `busy`; subagent turns and
  tool calls never produce a second `ready` (FR3). Hooks return next(e) without awaiting the network.
- The mod sends `ready` only when its UI is drawn on an attached surface (band rendered / pane
  isPlaced); under `claude -p`, the VS Code chat panel or a detached session it never queues (FR6).
- Hello lazily on the first eligible `ready`; the handle lives in $.state so a hot reload resumes
  rather than re-hellos; a superseded handle (410) goes quiet until the next turn.start — no
  hello ping-pong between two Claude Code sessions sharing one key.
- A burst that ends while queued removes the user silently (FR4).

### Story 7.5: Mod chat surface (parity with Epics 2–3)
- Searching: the band shows the spinner only (FR10); matched plays a brief bounded flourish
  (NFR18) and opens the chat Pane without delaying input readiness.
- Chat Pane: peer pseudonym + blurb, the opener once, current-chat history, an Input, and
  always-visible leave / block / report Buttons, mirrored as /claudingtin leave|block|report.
  Peer text renders only through Text with control characters stripped — never Markdown, Link or
  Code (AD-15). Outbound is optimistic, keyed by client_msg_id; input length capped client-side.
- Ends: every `session_ended` → the same "their Claude came back" state (3.2 copy); leave sends one
  `leave`, no confirm (3.3); late peer lines after leave are not shown; if still in the burst, back
  to the spinner (3.5 re-enqueue is the backend's); grace resume on a long-poll gap is invisible.
- Narrow terminals (<110 cols) fall back to inline pane mode with the controls still reachable via
  the slash commands. Ships in one PR with 7.6.

### Story 7.6: Remove the companion and pane placement
- Delete companion/, backend /ws (+ coder/websocket), plugin/cmd/session-start,
  plugin/hooks/session-start.sh, plugin/bin/, the plugin Go module and the SessionStart hook;
  go.work and scripts/workspace_modules.sh shrink to proto + backend.
- CI: drop the companion cross-build and binary attach from ci.yml / release.yml; add
  `claude plugin validate --strict plugin` and `claude plugin test plugin`.
- Add a manifest test that hooks.json lists the mod module and that userConfig defaults match code.
- Mark the deferred-work rows that only concern the companion as obsolete.
```

**E-6. Remaining stories: retarget wording** (the substance is unchanged)

| Story | Change |
|---|---|
| 3.6 Persist toggle | "fresh install … read" → `userConfig.persist` (boolean, default `true`) via `/config`. "companion menu" → `/config` or `/claudingtin persist`. "companion shows / sends `leave`" → "mod". |
| 3.7 Session-ID keepsake | "companion offers … to copy" → "the mod's end state offers a Copy button (clipboard write; shows the ID as text if no clipboard is reachable)". |
| 4.3 Block-list view | "companion requests / companion menu" → "mod requests / `/claudingtin blocks` opens a Pane". |
| 4.8 Keyword filter | "companion's report/block UI links" → "mod's report/block UI links (our own `Link`, never peer text)". |
| 5.1 Profile | "companion sends `profile_put`" → "mod sends `profile_put` from a `/claudingtin profile` Pane". |
| 5.3 forget_me | "companion processes … local profile draft, persist preference, safety ack" → "mod clears `$.store` and the local ack; `persist` resets to its userConfig default; the account-key file is kept unless explicitly reset". |
| 5.4 / 5.5 | "pane / companion" → "band / mod". |
| 6.1 Self-host | "a companion with no `SERVER_URL`" → "the mod with no `backendUrl` set connects to the public instance by default" (and the plugin.json default flips from `http://127.0.0.1:8080` to the public HTTPS URL). |
| 6.2 Fly | "reachable over WSS" → "reachable over HTTPS; long-poll held ≤20 s stays under the Fly proxy idle timeout". |
| 6.3 Status | Unchanged. Also resolves the deferred `Count()`-during-grace note: count live leases and held sessions separately. |

**E-7.** Above Epic 1's story list, add: *"Stories 1.3, 1.6, 1.7, 1.8 and the companion half of 1.9 / 2.3 / 2.5 / 3.2–3.5 were built for the Go companion and are superseded by Epic 7 (mods pivot, 2026-10-17). They are kept as the historical record."*

### 4.2 PRD (`prd.md`): amended in place, `updated: 2026-10-17`

**P-1. §1 Shape, v1 bullet**

```
OLD: a terminal-adjacent chat surface
NEW: an in-session chat surface drawn by the plugin's Claude Code mod
```

**P-2. UJ-1**

```
OLD: The plugin's hook fires on prompt submission and drops him into the queue; a "searching"
     spinner appears in the chat pane. … Claude finishes; the `Stop` hook fires; the pane shows
NEW: The plugin sees the turn start and drops him into the queue; a "searching" spinner appears
     in the band above his prompt. … Claude finishes; the turn completes; the pane shows
```

**P-3. F1 (replace the preamble and FR1, FR2, FR5, and both FR6s)**

```
Feasibility (OQ-1, revised 2026-10-17): buildable as a Claude Code **mod** — in-process plugin UI
(v2.1.287+) — with no companion process.

FR1. The chat surface SHALL be the plugin's mod: it draws inside Claude Code (an AbovePrompt band
     and a docked Pane), holds the client side of the backend connection over HTTPS, renders the
     chat and takes the user's input. Nothing runs outside the Claude Code process.
FR2. Think-time boundaries SHALL come from the mod's main-loop turn events: burst begins at
     `turn.start`, ends at `turn.complete`; subagent turns are ignored. On burst begin the user is
     entered into the queue; on burst end the active chat ends unless persist is enabled (FR22–FR23).
FR3. (unchanged; now satisfied natively — one main-loop turn spans all its tool calls)
FR5. The chat SHALL render inside the Claude Code session, in the mod's band and pane. There is no
     external pane, split or window.
FR6. The plugin SHALL be a no-op when the user is not opted in or the backend is unreachable, and
     SHALL never block, delay, or error the Claude Code session. Where mod UI cannot draw (VS Code
     extension chat panel, Remote Control viewers, `claude -p`, Agent SDK, cloud sessions, Desktop
     WSL) the plugin SHALL never enter the queue — no surface, no queue. [ACCEPTED LIMITATION,
     Diego 2026-10-16]
```

**P-4. NFRs**

```
NFR6  OLD: A plugin hook SHALL block the Claude Code session for no more than ~50ms: it enqueues …
      NEW: No mod hook SHALL await the network or block the Claude Code session; render and turn
           hooks return promptly and all backend I/O runs in background tasks. Any error is
           swallowed into a calm offline state (FR6).
NFR10 OLD: (TLS / WSS).            NEW: (TLS / HTTPS).
NFR12 OLD: … sends its locally stored account-key UUID with each backend request …
      NEW: … sends its locally stored account-key UUID in `hello` over TLS; later requests carry
           only the opaque handle minted for it. No identity derivation, no Claude-account
           linkage, no separate auth handshake in v1.
```

**P-5. §11 OQ-1** gets a new paragraph:

> **Revised 2026-10-17 (mods pivot):** Claude Code mods (v2.1.287+) supersede findings (a) to (d). Turn events replace transcript watching, and in-process UI replaces the companion pane. The trade is reach: no mod UI on the VS Code chat panel, Remote Control, or headless/cloud runs (FR6).

**P-6. Assumptions index:**
- FR2 becomes "**Main-loop `turn.start`/`turn.complete` reliably bracket a user turn.**"
- FR6 becomes "No surface ⇒ no queue; accepted reach loss on non-drawing surfaces."
- Add a row: "**The mods API (early access) stays compatible enough to track per release.**"

**P-7. Glossary:** "Think-time burst" becomes "the stretch from a main-loop `turn.start` to its `turn.complete`".

### 4.3 Architecture spine: new revision, `updated: 2026-10-17`, `binds: [plugin, backend, proto]`

**A-1. Units:** `plugin` becomes "Claude Code plugin whose TS mod is the spoke: think-time signal, HTTPS client, chat UI, account key, first-run gate". **Remove** the `companion` row. Diagram: `plugin (mod) <-> HTTPS JSON <-> backend`; `backend → proto`; the mod's TS types are checked against proto golden fixtures. Dependency rule: `backend → proto`; `proto` imports nothing; nothing imports `plugin`.

**A-2. AD changes**

| AD | Change |
|---|---|
| AD-1 | "Companions" → "mod clients"; recovery = re-poll on the same handle or re-`hello`. |
| AD-2 | "companion" → "mod". The gate carve-out stays. |
| AD-3 | Transport is HTTPS JSON (AR32). The envelope and message set are unchanged except `heartbeat` is removed and server→client gains `welcome` (`handle`). Every response body or event is a `/proto` type. The mod's TS declarations must round-trip proto's golden fixtures in `claude plugin test`. |
| AD-5 | The version goes in the `hello` body, and `please_update` is the `hello` response. "then close" becomes "no handle issued". |
| AD-6 | **Rewritten.** The mod alone decides busy vs back, from main-loop `turn.start` / `turn.complete`, sent as `ready` / `busy`. The backend never infers session state from timing. `[ADOPTED, amended 2026-10-17]` |
| AD-7 | **Replaced.** "The plugin is the whole client: no spawned process, no shipped binary. Its mod runs for the session's life; state that must survive hot reload lives in `$.state`." |
| AD-11 | "companion" → "mod (via `$.fs`)". Same path, `os.UserConfigDir()/claudingtin/account-key`. |
| AD-12 | Backend: env only (unchanged). Client: plugin `userConfig` only (`backendUrl`, `persist`). There are still no config files of our own. |
| AD-13 | The HTTP surface is `/v1/hello`, `/v1/events`, `/v1/send` and `GET /status`. Still no admin surface. |
| AD-14 | Rewritten to NFR6 (new text): hooks never await the network or throw; background tasks only; any failure shows a calm offline state. |
| AD-15 | Client rule: peer text renders only through `Text` with control characters stripped, never through `Markdown` / `Link` / `Code`. |
| AD-16 | "one active connection" → "one active handle per key". A new `hello` supersedes: the old handle gets `session_ended`, then 410. A superseded mod stays quiet until its next `turn.start` (no ping-pong). |
| AD-17 | "disconnect" = lease lapse (AR34). Resume = the same handle polls inside grace, or `hello` with key + `session_id`. |
| **AD-19 (new)** | **Ordered event delivery.** Each handle has one bounded event queue with monotonic `seq`. Delivery is at-least-once, acknowledged by `after`, deduplicated by `seq`. All server→client messages for a handle flow through it in hub order. Overflow ends the session (`session_ended`) rather than dropping silently. *Prevents:* the ordering races of separate channels, and silent relay loss. |
| **AD-20 (new)** | **No surface, no queue.** The mod sends `ready` only while its UI is drawn on an attached surface. *Prevents:* ghost matches, where a peer is paired with someone who can't see the chat. |

**A-3. Stack:**
- **Remove** bubbletea, bubbles, lipgloss, coder/websocket and fsnotify.
- **Add** Claude Code ≥ 2.1.301 (mods API, `claude-code.d.ts` as the authority) and TypeScript loaded directly by Claude Code (no build step).
- Keep Go 1.27, modernc.org/sqlite, Fly and Docker.

**A-4. Structural Seed:**
- The dev-machine box becomes "Claude Code session → plugin mod (in-process)", with `HTTPS` to the backend.
- Release flow: the backend image only. The plugin ships as source, with no binaries.
- Source tree: remove `companion/`, `plugin/bin`, `plugin/hooks` launcher. Add `plugin/hooks/register.tsx` (and siblings), `plugin/types/`, `plugin/.claude-plugin/plugin.json`.

**A-5. Capability map:**
- F1, F3, F5's toggle and F10 now live in `plugin` (mod).
- F1 is governed by AD-6, AD-7, AD-14 and AD-20.
- F4 gains AD-19.

**A-6. Open Questions:**
- **Remove** the transcript-contract question; it is obsolete.
- **Add:** "The mods API is early access and may change without notice. Pin a minimum Claude Code version in the README. On each version bump, diff `claude-code.d.ts` for the surfaces we use (`turn.*`, `ui.*`, `http.fetch`, `fs`, `store`, `command.register`), and keep `claude plugin test` in CI."

### 4.4 SPEC (`SPEC.md` + `glossary.md`)

| Where | Change |
|---|---|
| CAP-1 intent | "the companion detects … by tailing the Claude Code session transcript" → "the plugin's mod detects … from Claude Code's main-loop turn events". |
| CAP-3 intent | "in the companion's own pane, placed adjacent to the Claude session (tmux auto-split …)" → "in a pane and band the plugin's mod draws inside the Claude Code session". |
| CAP-4, CAP-10, CAP-11 | "companion" → "mod". CAP-11's "plugin menu" → "`/claudingtin safety`". |
| CAP-13 | Rewritten: "The mod never blocks, delays, or errors the host session. Hooks don't await the network, and every failure becomes a calm offline state. Where mod UI cannot draw, the plugin never queues." Success line: "no mod hook awaits the network; backend down ⇒ calm offline; headless / VS Code panel / Remote Control ⇒ never queued". |
| CAP-14 success | "the only HTTP endpoints are the websocket upgrade and `GET /status`" → "the only HTTP endpoints are the `/v1` client API (hello, events, send) and `GET /status`". |
| Constraints | "Claude Code only" line: "transcript and hooks" → "mod turn events". "TLS/WSS" → "TLS/HTTPS". "Both companion and backend import `/proto`" → "the backend imports `/proto`; the mod's TS types are fixture-checked against it". Stack line: drop "Bubble Tea v2 companion TUI, `coder/websocket`"; the monorepo becomes `/plugin`, `/backend`, `/proto`; "18 architecture decisions" → "20". Hub-and-spoke and identity lines: "companion(s)" → "mod client(s)". |
| Assumptions | The transcript line becomes "Main-loop turn events bracket a user turn reliably". "a hook blocks ≤~50ms" → "no mod hook awaits the network". |
| Open Questions | Replace the transcript spike with the mods-API-churn question (A-6). |
| glossary | **Companion**: replaced by **Mod**: "The TypeScript module inside the claudingtin plugin that Claude Code runs in-process for the session. It detects think-time from turn events, talks to the backend over HTTPS, and draws the chat in a band and a pane. It holds no authoritative state." "Think-time burst": as in P-7. "Backend / hub": "Companions are spokes" → "Mod clients are spokes". |

### 4.5 `sprint-status.yaml`

```
  3-5-silent-re-enqueue-while-busy: done            # was backlog; PR #30 merged 2026-09-16
  # Epic 3 paused after 3-5; 3-6/3-7 resume after epic-7 (mods pivot 2026-10-17)

  epic-7: in-progress                                # build order: runs before 3-6
  7-1-mods-skeleton-backend-status-band: review      # b76083d on feat/mods-skeleton, no PR yet
  7-2-backend-http-long-poll-transport: backlog
  7-3-account-key-and-first-run-gate-in-the-mod: backlog
  7-4-think-time-from-turn-events-and-no-surface-no-queue: backlog
  7-5-mod-chat-surface: backlog                     # ships in one PR with 7-6
  7-6-remove-the-companion-and-pane-placement: backlog
  epic-7-retrospective: optional
```

Action items:
- `epic-1-retro-item-7` (F4 atomicfile) → `done`, "obsolete: companion removed in 7-6".
- `epic-1-retro-item-3` (F3) is retargeted: "warn vs refuse on `http://` `backendUrl` to a non-loopback host in the mod". It stays open, expiring at the Epic 6 deploy.
- `epic-2-retro-item-11` (F7) stays open, with "owned by 7-2 (AD-19 overflow ⇒ session_ended)".

### 4.6 `deferred-work.md`

Append a dated "mods pivot" note marking the companion-only rows **obsolete at 7.6**:
- the Windows watcher/identity flakes
- the run.loop Println hang
- the windowterm Windows/Linux rows
- the `TestIntentRacing…` flake
- the 3.2 takeover flash, last-`chat_msg` drop, `session_ended`/`matched` race, wsclient half-open and close-code rows
- the 3.3 untagged-`ChatMsg` row (AD-19 removes the class)
- the pane-placement-removal row (now Story 7.6)

These move to 7.2 or Epic 6:
- the 3.1 idle-displaced `sentEnd` row (7.2 defines takeover for HTTP)
- the `Count()`-during-grace row (6.3)
- the mod CI row (7.6)
- the manifest-test row (7.6)
- the band-composition and hung-fetch rows (7.4 / 7.5)

## 5. Implementation handoff

**Scope: major.** It changes the architecture's spoke and transport. Diego has already made every product and architecture call, so no separate PM or Architect session is needed. The edits above are pre-drafted.

| Step | Who | Deliverable |
|---|---|---|
| 1 | This workflow, on approval | Apply §4.1–4.6 to `epics.md`, `prd.md`, `ARCHITECTURE-SPINE.md`, `SPEC.md`, `glossary.md`, `sprint-status.yaml` and `deferred-work.md`. Update memory. |
| 2 | Diego | Rebase `feat/mods-skeleton` onto `main` (picks up #30), push, and open a PR for 7.1. Merge, then mark `7-1` done. |
| 3 | `bmad-build`, fresh context per story | 7.2 → 7.3 → 7.4 → (7.5 + 7.6 as one PR). Each on `story/7-N-slug`, the usual PR flow. |
| 4 | `bmad-retrospective` | Epic 7 retro: check parity with Epics 1–3 and API-churn exposure. |
| 5 | `bmad-build` | Resume 3.6 → 3.7 → Epic 4 … per the build order. |

**Success criteria:**
- Two Claude Code sessions on one machine, each with its own test key, against a local backend:
  - Both see the band.
  - Submitting prompts in both matches them, and they chat in the mod pane.
  - Leave, block and report all show "their Claude came back" on the other side.
  - A 5 s network blip mid-chat resumes silently.
  - `claude -p` never queues.
- `companion/`, `/ws` and every placement path are gone.
- CI is green with `claude plugin validate --strict` and `claude plugin test`.
- The PRD, spine, SPEC and epics carry no live "companion", "transcript" or "tmux" requirement.
