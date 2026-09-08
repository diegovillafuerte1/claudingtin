---
stepsCompleted: [step-01-validate-prerequisites, step-02-design-epics, step-03-create-stories]
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-claudingtin-2026-08-31/prd.md
  - _bmad-output/planning-artifacts/prds/prd-claudingtin-2026-08-31/addendum.md
  - _bmad-output/planning-artifacts/architecture/architecture-claudingtin-2026-09-01/ARCHITECTURE-SPINE.md
  - _bmad-output/specs/spec-claudingtin/SPEC.md
  - _bmad-output/specs/spec-claudingtin/glossary.md
  - _bmad-output/specs/spec-claudingtin/voice.md
---

# claudingtin - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for claudingtin (Claude Think-Time Chat Roulette, v1), decomposing the requirements from the PRD, the Architecture Spine, and the SPEC into implementable stories.

Scope: v1 only — Claude Code plugin + bundled companion TUI + one hosted matching/relay backend. The v2 reconnect webapp is out of scope.

## Requirements Inventory

### Functional Requirements

**F1 — Think-time detection and session lifecycle**

FR1: The chat surface is a bundled companion process (a small TUI) that the plugin launches on `SessionStart`; it holds the websocket to the backend, renders the chat, and takes the user's input.
FR2: Think-time boundaries are determined by the companion watching the Claude Code session transcript — burst begins when the user's turn starts, ends when the assistant's response completes. The `UserPromptSubmit` / `Stop` hooks may provide a secondary signal but are not relied on for timing. On burst begin the user is entered into the queue; on burst end the active chat ends, unless persist is enabled.
FR3: Tool-call boundaries within one turn are treated as the same continuous burst, so a single user turn maps to at most one match. `[ASSUMPTION]`
FR4: If a burst ends while the user is still queued and never matched, the plugin silently removes them from the queue with no error surfaced.
FR5: The companion renders adjacent to the Claude session — automatically as a `tmux` split pane when inside tmux; otherwise the plugin prints a one-line instruction to open it in a split/pane/window the user controls. Inbound messages render in the companion's own pane; nothing is pushed into the Claude Code TUI.
FR6: The plugin is a no-op when the user is not opted in, the companion can't start, or the backend is unreachable — it must never block, delay, or error the Claude Code session. Where no adjacent placement is possible (some IDE terminals), the companion still runs and the user places it manually. `[ASSUMPTION on placement fallback]`

**F2 — FIFO matching and queue**

FR7: The matching server pairs users strictly first-in-first-out: the longest waiter is matched to the next arrival. No scoring, no preference weighting in v1.
FR8: A user is not matched with anyone on their block list (FR32) or within an active auto-cooldown window with them (FR34).
FR9: If the two longest waiters are ineligible for each other (block/cooldown), the server skips to the next eligible pair rather than stalling the queue. `[ASSUMPTION]`
FR10: While queued, the user sees a "searching" spinner and nothing else — no queue position, no ETA, no "N people online."
FR11: On a match, both users are shown the same rotating pre-written opener at the same time.
FR12: Median time-to-match is instrumented server-side and exposed on a maintainer status endpoint (supports M1). `[ASSUMPTION]`

**F3 — The chat surface**

FR13: The chat is text only. No image, file, audio, or video sharing.
FR14: Outbound URLs / links in messages are stripped or rendered inert in v1.
FR15: Chat message content is not persisted to disk on the server or the client. The relay holds messages in memory only for delivery.
FR16: The surface shows at minimum: the other person's pseudonym, their optional profile blurb if set, the message history for the current chat, an input box, and always-visible block, report, and leave ("my Claude came back") controls.
FR17: Typing indicators may be shown; read receipts are not. `[ASSUMPTION]`
FR18: The surface indicates connection state (searching / connected / your Claude is back / disconnected) using the no-rejection language in F4.

**F4 — Exit convention and disconnect handling**

FR19: Every chat-ending event — model returns, user leaves, user blocks, network disconnect, other party leaves — is presented to the remaining user with identical, neutral "their Claude came back" framing. No event ever tells a user they were left, rejected, blocked, or reported.
FR20: On any disconnect or chat end while the user's burst is still active, the plugin returns the user to the spinner and silently re-enters them in the queue.
FR21: There is no "user left" / "user is typing…" / "connection lost" alarm language anywhere in the UI.

**F5 — Persist and the commitment ramp**

FR22: The profile includes a persist toggle. When on, the user's chats continue after their think-time burst ends, until they explicitly leave.
FR23: Persist ships default-on.
FR24: When a persisted chat ends, the user is offered the chat's session ID to copy, with a one-line explanation that it's the only way to find this person again.
FR25: Any reconnect built on a session ID (v2) requires mutual opt-in — both parties took an explicit keep/connect action — never a unilateral one. `[ASSUMPTION / v2]`
FR26: Session IDs are opaque, unguessable, and carry no lookup capability in v1.

**F6 — Tiny profile**

FR27: The profile is small and entirely optional: a display name / pseudonym and a short free-text blurb. Editable from within the plugin. `[ASSUMPTION on blurb]`
FR28: Other users only ever see the pseudonym, the blurb, and (v2) tags — never the account key.
FR29: A single optional "prefer to be matched with" hint may be stored for v2 pull-in; it has no effect on v1 FIFO matching and must be skippable. `[ASSUMPTION]`

**F7 — Openers**

FR30: The plugin shows one opener per match, drawn from a curated rotating set of generic pre-written prompts. Not generated per match in v1.
FR31: The opener set lives in the open-source repo so contributors can extend it. `[ASSUMPTION]`

**F8 — Block, cooldown, and rate limiting**

FR32: A user can block the current chat partner with one action. The block is permanent (until the user removes it) and enforced by the account key (FR41).
FR33: The block primitive is independent of all preference / tag settings: blocking someone must not require or cause any change to the user's tags or match hints.
FR34: The server applies an auto-cooldown between any two users who have just been matched: after ~1 minute together they are auto-blocked from re-matching for ~24h. Exact rules are OQ-5. `[ASSUMPTION]`
FR35: The user can view and remove entries from their block list from within the plugin. Auto-cooldowns expire on their own and need not be listed.
FR36: The server rate-limits per account key: ≤1 new match per ~10s, ≤~5 messages per second, and exactly 1 concurrent chat session. Every limit must have a concrete configured value — none left unbounded. `[ASSUMPTION on values]`

**F9 — Reporting and moderation**

FR37: The chat surface offers a one-tap report on the active chat. Reporting also blocks that user.
FR38: A report captures only the last N (≈20) messages of that chat plus both account keys and a timestamp — nothing from any other conversation. `[ASSUMPTION on N]`
FR39: Reports are delivered to the maintainer(s) out of band and are the only circumstance under which any message content is written to storage, retained only as long as needed to action the report.
FR40: The maintainer can ban an account key, immediately ending that key's ability to queue or chat. A ban persists until the user deliberately resets their local key.
FR41: The account key is a random UUID generated once and stored at a stable path outside the plugin's own directory (an OS config dir), so reinstalling or updating the plugin keeps the same key. It is sent to the backend as the user's identity; the backend never sees anything about the user's Claude account.
FR42: The repo ships published community guidelines and the report/block flow links to them.
FR49: The server applies a keyword filter to messages as part of reactive moderation — blocking or flagging a maintained list of slurs and known abuse patterns. The list and its governance live in the repo (OQ-7). `[ASSUMPTION]`

**F10 — Onboarding, age gate, and safety disclosure**

FR43: On first run the plugin presents, before any network call: what the product is, an honest safety warning (you are being connected to strangers; do not share identifying, location, or financial information; screenshots/quoting are possible), and the block/report explanation.
FR44: The user affirmatively confirms they are 18 or older before being allowed to queue. Self-attestation is the v1 approach — no ID checks, no third-party age estimation.
FR45: The safety screen is re-accessible from the plugin menu at any time.
FR46: Declining the age gate or safety acknowledgement leaves the plugin installed but inert (no queue, no chat). `[ASSUMPTION]`

**F11 — Idle experience (empty / slow queue)**

FR47: While waiting in an empty or slow queue, the surface may show a single lightweight nudge at a time — gentle tooltips only.
FR48: Idle nudges must not include games, activities, streaks, points, or anything that rewards staying. If the queue resolves, the nudge disappears immediately.
FR50: After a threshold wait with no match, the surface offers the user the option to leave a short note for the next person to enter the queue. The next arrival sees the note as their opener and may reply, which starts a normal chat. Notes are ephemeral (consumed by the next arrival or expiring) and subject to the same text-only, link-inert, keyword-filter rules as chat. `[ASSUMPTION]`
FR51: The "leave a note" flow is the v1 seed for the v2 missed-connections board: in v1 it stays local to the queue and is never publicly listed.

### NonFunctional Requirements

NFR1: No chat message content is persisted anywhere by default (client or server). The only exception is report payloads (FR38–FR39), minimized and short-lived.
NFR2: Server-persisted state is limited to: pseudonym + blurb, block lists, cooldown timers, bans, and the account key. No IP logs beyond what's transiently required for abuse rate-limiting; any such logs auto-expire within 24h. `[ASSUMPTION]`
NFR3: The account key is not shown to other users or included in any client-visible payload.
NFR4: A user can delete their profile and block list from within the plugin; deletion removes all server-side state keyed to them except active bans.
NFR5: The privacy posture (what is and isn't stored) is documented in the repo in plain language.
NFR6: A plugin hook blocks the Claude Code session for no more than ~50ms: it enqueues the action asynchronously and returns immediately. It fails open — any hook error or timeout is swallowed and the Claude session proceeds untouched. `[ASSUMPTION]`
NFR7: Message relay round-trip is < ~500ms p90 under normal load. `[ASSUMPTION]`
NFR8: The backend targets ~99% monthly reachability on a best-effort basis — no formal SLA — and degrades cleanly: an unreachable or erroring backend yields a calm "no one around right now" state, never an error dump, and never affects the Claude session. `[ASSUMPTION]`
NFR9: The backend runs within a single small cloud instance's resources for the expected pool size (low hundreds of concurrent users). `[ASSUMPTION]`
NFR10: All client↔server traffic is encrypted in transit (TLS / WSS).
NFR11: The server treats all message content as untrusted: no rendering of remote markup/HTML, link inertness per FR14, input length caps.
NFR12: The plugin sends its locally stored account-key UUID with each backend request over the encrypted channel; the server treats that UUID as the identity. No identity derivation, no Claude-account linkage, no separate auth handshake in v1.
NFR13: Target hosting cost for the public instance is under ~$20/month at expected scale. `[ASSUMPTION]`
NFR14: The backend is packaged for one-command self-hosting (e.g. `docker compose up`), defaulting to the public instance's URL when run as a client.
NFR15: The maintainer has a minimal status/metrics view: concurrent users, median time-to-match, reports outstanding, error rate.
NFR16: All user-facing copy is warm, light, and unpolished-in-a-human-way — never corporate, never clinical, never therapized.
NFR17: The exit / disconnect copy reads as an easy, cheerful "catch you later" — the "my Claude came back" fiction carries no apology, no guilt, no "are you sure?".
NFR18: The searching → matched transition has a small moment of delight — a brief animation or flourish in the pane — without becoming a minigame or costing perceptible time. `[ASSUMPTION]`
NFR19: The curated opener set is playful and disarming, skewed toward low-stakes human curiosity, never survey-like. The repo includes a one-paragraph voice guide so contributed openers stay on-tone.
NFR20: The repo includes: README with install steps, the safety/privacy docs, the community guidelines, the opener set + voice guide, a self-host guide, and a documented process for handling reports (including the NCMEC path).
NFR21: License is MIT or Apache-2.0. `[ASSUMPTION — OQ-10]`

### Additional Requirements

_From the Architecture Spine (AD-1 … AD-18), Stack, and Structural Seed. `[ADOPTED]` decisions are inherited verbatim from the PRD._

**Greenfield / scaffolding**

- AR1: No starter template. Greenfield monorepo built from scratch: `/proto` (shared Go module: wire types + `PROTOCOL_VERSION`), `/backend` (`cmd/` for `serve` / `ban` / `reports`; `migrations/` embedded, ordered, forward-only), `/companion` (Go TUI), `/plugin` (`bin/<os>-<arch>/` committed cross-built binaries; `hooks/` SessionStart launcher), `/deploy` (Dockerfile, docker-compose.yml, fly.toml, keywords.txt), `/.github/workflows/` (cross-build + release).
- AR2: Dependency direction is enforced: `plugin → companion`; `companion → proto`; `backend → proto`. Nothing depends on `plugin`; `proto` depends on nothing.
- AR3: Stack, pinned at init: Go 1.27.x; charmbracelet/bubbletea v2.0.x + bubbles v2.2.x + lipgloss v2.0.x; coder/websocket v1.8.15; fsnotify/fsnotify v1.10.1 (atomic-rename / rotation handling in the transcript watcher); modernc.org/sqlite v1.57.0 (pure-Go, no cgo).
- AR4: Release flow: CI cross-builds companion binaries (macOS arm64/x64, Linux, Windows), publishes the backend container image, attaches binaries to a GitHub release. Cross-built binaries are committed under `plugin/bin/<os>-<arch>/`; the hook resolves them via `${CLAUDE_PLUGIN_ROOT}`. Each plugin release pins one companion binary version.

**Wire protocol (AD-3, AD-5)**

- AR5: Every websocket message is JSON matching a type declared in `/proto`; both units import `/proto`; no message shape exists elsewhere. Envelope: `{ "type": <snake_case>, "v": <int>, ...payload }`.
- AR6: v1 message set — client→server: `hello` (protocol version, account key), `ready`, `busy`, `chat_msg` (`client_msg_id`, `text`), `leave`, `block`, `report` (`last_n`), `profile_put` (`pseudonym`, `blurb`; empty = clear), `forget_me`, `note_put` (`text`), `heartbeat`. server→client: `queued`, `matched` (`session_id`, peer `pseudonym`/`blurb`, `opener` text), `chat_msg` (`text`), `session_ended`, `blocklist`, `profile_ack`, `error` (`code`, `msg`), `please_update`.
- AR7: `chat_msg` is not echoed to the sender; the companion renders its own outbound optimistically keyed by `client_msg_id`. `please_update` is its own type, sent before the socket closes, never an `error` code.
- AR8: Protocol version negotiated on connect: the companion sends its version in `hello`; the backend accepts the current and previous minor; older gets a single `please_update` then close. Any breaking wire change bumps the minor.

**Backend architecture (AD-1, AD-2, AD-8, AD-10, AD-13, AD-15, AD-16, AD-17, AD-18)**

- AR9: Hub-and-spoke: all queue / pairing / active-session state lives in the backend and only there. Companions hold no authoritative state and never communicate companion-to-companion; a companion's only recovery is to reconnect and re-announce.
- AR10: Single-writer state: exactly one goroutine mutates queue, pairing, and in-memory policy, driven by a channel; all connection handlers interact with it only by sending on that channel. On startup it loads every block, cooldown, and ban from SQLite; thereafter SQLite is a write-through durable mirror and no other code path reads policy for a matching decision. It owns pairing teardown.
- AR11: Matching (FR7–FR9): scan from the queue head, pair the head with the first eligible successor (not blocked / on cooldown / banned / self); if none within a bounded scan, the head waits.
- AR12: Cooldown write rule: a cooldown row is written only after a pairing lasts ~1 minute or both sides send ≥1 message — never at pairing time, and never for a pairing torn down inside the AD-17 grace window with no exchange.
- AR13: Data-at-rest: message content is never written to disk or logs. Sole exception: a `report` row (the last N messages of that one chat), deleted once actioned. Persisted data is limited to: profiles, block lists, cooldown rows, bans, account-key rows, report rows, and a `schema_version` row. Empty-queue notes and rate-limit counters live in memory only.
- AR14: Entities (SQLite): `ACCOUNT` (account_key PK), `PROFILE` (account_key PK, pseudonym, blurb), `BLOCK` (blocker_key, blocked_key, created_ms), `COOLDOWN` (key_a, key_b, expires_ms), `BAN` (account_key, created_ms, note), `REPORT` (id PK, reporter_key, reported_key, created_ms, last_messages).
- AR15: No admin HTTP surface. The only HTTP endpoints are the websocket upgrade and `GET /status`. Maintainer actions (`backend ban`, `backend reports`, …) are CLI subcommands writing the shared SQLite file. `serve` re-reads bans on a ~10s interval and on `SIGHUP`; `backend ban` writes the row and sends `SIGHUP` via pidfile when present. SQLite opened WAL + `busy_timeout` for two-process access.
- AR16: Content sanitization has one owner per direction: the backend is the single point that inerts/strips links, enforces message length caps, and rejects oversized or non-text frames on ingest — before relay and before the keyword filter. The companion renders all peer text as literal (no remote markup, no control sequences, no link activation).
- AR17: The backend owns queue membership and the FR20 requeue: the companion sends only `ready` / `busy`. One active connection per account key — a second `hello` for a connected key takes over; the prior connection gets `session_ended` + close. This is how FR36 "exactly one concurrent session" is met.
- AR18: Reconnect grace window (~10s): on spoke disconnect the backend holds the pairing; a reconnect presenting the same account key + `session_id` resumes it. On expiry the peer gets `session_ended` and both sides are re-enqueued if still busy. Grace resumes and involuntary requeues are exempt from rate limiting.
- AR19: Forward-only embedded schema migrations: the backend embeds ordered SQL migrations and applies all pending ones on startup, gated by the `schema_version` row. No down-migrations in v1.

**Session identity & lifecycle (AD-4, AD-6, AD-7, AD-9, AD-11, AD-14)**

- AR20: One session ID per chat: the backend mints exactly one `session_id` at pairing and returns it in `matched`. Every exit terminates with a single `session_ended` carrying no cause field and no `session_id`. On `leave` / burst-end the backend stops relaying at once — messages already received are delivered, later ones dropped, `session_ended` is the peer's last message.
- AR21: The companion alone determines think-time boundaries by tailing the Claude Code session transcript file, expressed to the backend solely as `ready` / `busy`. Plugin hooks may nudge but are not authoritative. The backend never infers session state from timing.
- AR22: Plugin → companion is launch-only: the `SessionStart` hook spawns the companion once per Claude Code session, detached, with three arguments — transcript path, config-dir path, server URL. After spawn the plugin holds no channel to the companion. Any failure (spawn error, missing binary, unreachable backend, crashed companion) is swallowed with a hard timeout; Claude Code proceeds untouched and no error surfaces.
- AR23: Profile: the companion edits a local draft and sends `profile_put` on change (full replace, not a patch). The backend stores the canonical profile and is the only origin of the pseudonym / blurb delivered to a peer in `matched`. `forget_me` drops all rows keyed to the account key.
- AR24: Account key: the companion reads or creates a random UUID at a stable OS-config path outside the plugin directory and sends it in `hello`. The backend treats it as opaque and authoritative with no verification.
- AR25: The first-run 18+ / safety gate (FR43–44) is a local precondition in the companion — no server round-trip, no server-side attestation record; the companion simply does not connect until it is cleared.

**Configuration & conventions (AD-12, Consistency Conventions)**

- AR26: Configuration is environment variables only — no config files. Companion reads `SERVER_URL` (default: the public instance). Backend reads `PORT`, `DB_PATH`, `KEYWORDS_PATH`, optional `REPORT_WEBHOOK`.
- AR27: Conventions: Go packages lowercase no underscores; wire types are `PascalCase` structs in `/proto` with a `snake_case` `type` discriminator; SQLite tables `snake_case` plural. IDs are UUIDv4 strings; `session_id` is an opaque unguessable string minted by the backend. Timestamps are Unix epoch milliseconds (`int64` wire, `INTEGER` SQLite). Client-facing errors are an `error` message, never a transport close, except `please_update`. Logs are structured JSON to stdout — no message content, no account keys. Transient IP data (rate-limiting only) is in memory, never persisted or logged.

**Deployment (Structural Seed)**

- AR28: Public instance: one Fly.io machine (`shared-cpu-1x`) + one volume; TLS at Fly; single-instance by design (one SQLite file, no horizontal scale in v1); free shared IPv4. Volume snapshots are the backup posture.
- AR29: Self-host: identical image via `docker compose up`; operator supplies a volume mount, env vars, and their own TLS reverse proxy.

**Known implementation risk**

- AR30: The Claude Code session-transcript file location and JSONL schema are not a documented, stable contract, yet the companion depends on both. An implementation spike must pin the current format, build a defensive parser, and CI-test the full cross-build matrix (folds together with the `modernc.org/sqlite` Windows check).

### UX Design Requirements

No standalone UX design contract (`bmad-ux` spine) exists for this project. The companion's UX is specified as prose across:

- **PRD §5 (User Journeys UJ-1 … UJ-5)** — the happy path, persist, block-and-report, empty queue, and first run.
- **PRD FRs** — FR5 (pane placement), FR10 (spinner only), FR16–FR18 (surface contents, connection state), FR19–FR21 (no-rejection framing, no alarm language), FR24 (session-ID copy offer), FR27 (in-plugin profile editing), FR35 (block-list management view), FR43–FR46 (first-run onboarding screen), FR47–FR48 (idle nudges), FR50 (leave-a-note flow).
- **PRD NFR16–NFR19** and **SPEC companion `voice.md`** — voice and tone, exit copy, the searching→matched "spin" flourish, opener voice guide.

These are already captured as FRs / NFRs above; no separate UX-DR list is maintained. Story acceptance criteria will cite `voice.md` for copy tone and the PRD journeys for flow.

### FR Coverage Map

FR1: Epic 1 — companion process launched by the plugin on SessionStart
FR2: Epic 1 — transcript-watch determines think-time boundaries; ready/busy to backend
FR3: Epic 1 — tool-call boundaries within a turn = one burst = at most one match
FR4: Epic 1 — burst ends while still queued → silent queue removal
FR5: Epic 1 — pane placement (tmux auto-split / manual elsewhere)
FR6: Epic 1 — plugin is a fail-open no-op when not opted in / companion can't start / backend unreachable
FR7: Epic 2 — strict FIFO pairing, no scoring
FR8: Epic 4 — no match against block list or active cooldown
FR9: Epic 2 — skip to next eligible pair rather than stall the queue
FR10: Epic 2 — "searching" spinner only, no queue position / ETA / count
FR11: Epic 2 — both users shown the same opener at the same time
FR12: Epic 6 — median time-to-match instrumented and exposed on the status endpoint
FR13: Epic 2 — text only, no media
FR14: Epic 4 — outbound links stripped / inert
FR15: Epic 2 — no chat content persisted; in-memory relay only
FR16: Epic 2 — surface shows pseudonym, blurb, current-chat history, input, block/report/leave controls
FR17: Epic 2 — no read receipts (delivered). Typing indicator is "may show" / optional and is NOT implemented in v1: no `typing` wire type, no indicator in the chat surface (spec-2-3 descopes it explicitly). Revisit only if a v1 typing indicator is ever decided in scope. See epic-2-retro-2026-09-07 finding F3.
FR18: Epic 3 — connection-state indication in no-rejection language
FR19: Epic 3 — every chat-end presented identically as "their Claude came back"
FR20: Epic 3 — disconnect / end while busy → spinner + silent re-enqueue
FR21: Epic 3 — no alarm language anywhere in the UI
FR22: Epic 3 — persist toggle keeps chats alive past the burst
FR23: Epic 3 — persist ships default-on
FR24: Epic 3 — session-ID copy offered when a persisted chat ends
FR25: Deferred (v2) — mutual-opt-in reconnect; out of v1 scope
FR26: Epic 3 — session IDs opaque, unguessable, no v1 lookup
FR27: Epic 5 — small, entirely optional profile (pseudonym + blurb), in-plugin editable
FR28: Epic 5 — peers only ever see pseudonym / blurb, never the account key
FR29: Epic 5 — one optional skippable "prefer to be matched with" hint, no v1 effect
FR30: Epic 2 — one curated rotating pre-written opener per match
FR31: Epic 2 — opener set lives in the repo for contributors
FR32: Epic 4 — one-action permanent block, enforced by account key
FR33: Epic 4 — block primitive independent of all preference / tag settings
FR34: Epic 4 — auto-cooldown between two just-matched users (~1 min → ~24h)
FR35: Epic 4 — view / remove block-list entries in-plugin
FR36: Epic 4 — per-account-key rate limits, every limit a concrete value
FR37: Epic 4 — one-tap report on the active chat, also blocks
FR38: Epic 4 — report captures only last N (≈20) messages of that chat + both keys + timestamp
FR39: Epic 4 — reports delivered out of band; only path that writes message content, short-lived
FR40: Epic 4 — maintainer ban of an account key, immediate
FR41: Epic 1 (key creation) + Epic 4 (enforcement) — random UUID at a reinstall-stable path, sent as identity
FR42: Epic 4 — published community guidelines, linked from report/block flow
FR43: Epic 1 — first-run pre-network screen: what it is + honest safety warning + block/report explainer
FR44: Epic 1 — affirmative 18+ self-attestation before queueing
FR45: Epic 1 — safety screen re-accessible from the plugin menu
FR46: Epic 1 — declining the gate leaves the plugin installed but inert
FR47: Epic 5 — single lightweight idle nudge at a time, tooltips only
FR48: Epic 5 — no games / streaks / points in idle nudges; nudge clears on match
FR49: Epic 4 — backend keyword filter over messages (repo-governed list)
FR50: Epic 5 — empty-queue "leave a note for the next person"
FR51: Epic 5 — the note is the v1 seed for the v2 board; stays local, never publicly listed

**NFR coverage:** NFR1 → E2/E6; NFR2 → E4; NFR3 → E4; NFR4 → E5; NFR5 → E5/E6; NFR6 → E1; NFR7 → E2; NFR8 → E1; NFR9 → E6; NFR10 → E1; NFR11 → E2; NFR12 → E1; NFR13 → E6; NFR14 → E6; NFR15 → E6; NFR16 → E2; NFR17 → E3; NFR18 → E2; NFR19 → E2; NFR20 → E6; NFR21 → E6.

**Additional-requirement coverage:** AR1–AR8, AR21, AR22, AR24, AR25, AR30 → E1; AR9, AR10, AR11, AR16, AR17, AR20 → E2; AR18, AR20 → E3; AR12, AR13, AR14, AR15, AR16 → E4; AR23 → E5; AR15, AR19, AR26, AR27, AR28, AR29 → E6.

## Epic List

### Epic 1: Foundation, the think-time signal, and the safety gate
Stand up the greenfield monorepo (`/proto`, `/backend`, `/companion`, `/plugin`, `/deploy`, CI cross-build/release), the versioned-JSON wire contract with version negotiation, the `SessionStart` hook that launches the companion detached and fail-open, the companion's transcript watcher that maps a Claude Code turn to `ready`/`busy`, a minimal backend that accepts `hello` and tracks per-key connection state, and the account-key UUID at a reinstall-stable path. All gated behind the pre-network first-run screen — product explainer, honest safety warning, block/report explainer, affirmative 18+ self-attestation — where declining leaves the plugin installed but inert. Includes the AR30 spike to pin the transcript file location and JSONL schema and build a defensive parser plus a CI cross-build matrix.
**FRs covered:** FR1, FR2, FR3, FR4, FR5, FR6, FR41 (key creation), FR43, FR44, FR45, FR46

### Epic 2: Get matched and chat
The FIFO queue and single-writer pairing loop, the `matched` message carrying a curated rotating pre-written opener shown to both users at once, the text-only chat surface in the companion (peer pseudonym + blurb, current-chat history, input box, block/report/leave controls present), the in-memory-only message relay, connection-state display, and the small searching→matched "spin" flourish.
**FRs covered:** FR7, FR9, FR10, FR11, FR13, FR15, FR16, FR17, FR30, FR31

### Epic 3: Leaving without rejection
Route every chat-ending event — model returns, explicit leave, network disconnect, reconnect-grace expiry, peer-initiated end — through one identical neutral "their Claude came back" presentation with no cause leaked and no alarm language; silently re-enqueue a still-busy user; the reconnect grace window; the persist toggle shipping default-on; and the session-ID copy offer when a persisted chat ends (IDs opaque, unguessable, inert in v1).
**FRs covered:** FR18, FR19, FR20, FR21, FR22, FR23, FR24, FR26

### Epic 4: Staying safe with strangers
One-action permanent block keyed to the account key and independent of any preference/tag setting, plus an in-plugin block-list view; automatic re-match cooldown between two just-paired users; per-account-key rate limits with concrete configured values; one-tap report that also blocks and captures only the last ~20 messages of that one chat; backend-side link inerting, length caps, non-text-frame rejection, and a repo-governed keyword filter; the maintainer `ban` CLI subcommand; and the published community guidelines linked from the flow.
**FRs covered:** FR8, FR14, FR32, FR33, FR34, FR35, FR36, FR37, FR38, FR39, FR40, FR41 (enforcement), FR42, FR49

### Epic 5: Profile and the idle queue
The small, entirely optional profile — pseudonym plus short blurb, in-plugin editable, full-replace to the canonical backend copy, with the single skippable "prefer to be matched with" hint that has no v1 effect and a `forget_me` that clears all keyed state; the idle-queue experience of at most one lightweight tooltip nudge at a time with no gamification; and the empty-queue "leave a note for the next person" flow whose note the next arrival sees as their opener.
**FRs covered:** FR27, FR28, FR29, FR47, FR48, FR50, FR51

### Epic 6: Run it in public
One-command self-hosting from a single image with env-var-only configuration defaulting to the public instance URL, the Fly.io single-machine + volume deployment with WAL SQLite and forward-only migrations, the `GET /status` maintainer view (concurrent users, median time-to-match, reports outstanding, error rate) with the time-to-match instrumentation behind it, and the repo documentation set — README + install, plain-language privacy posture, self-host guide, report-handling process including the NCMEC path, and the license.
**FRs covered:** FR12

---

## Epic 1: Foundation, the think-time signal, and the safety gate

Stand up the greenfield monorepo, the versioned wire contract, the fail-open plugin launch, the transcript-driven think-time signal, the reinstall-stable account key, and the local first-run 18+/safety gate — a companion that knows when the model is thinking, never disturbs Claude Code, and connects no one until the risks are acknowledged.

### Story 1.1: Monorepo scaffold and the `/proto` wire contract

As a developer on claudingtin,
I want the monorepo laid out with a shared `/proto` module defining every v1 wire message,
So that the companion and backend build against one enforced protocol contract from day one.

**Acceptance Criteria:**

**Given** a fresh clone
**When** `go build ./...` and `go test ./...` run
**Then** the `proto`, `backend`, `companion`, and `plugin` modules build green
**And** `proto` exports the envelope `{ "type": <snake_case>, "v": <int>, ...payload }` and a `PROTOCOL_VERSION` constant.

**Given** the `proto` package
**When** its tests run
**Then** every client→server type (`hello`, `ready`, `busy`, `chat_msg`, `leave`, `block`, `report`, `profile_put`, `forget_me`, `note_put`, `heartbeat`) and every server→client type (`queued`, `matched`, `chat_msg`, `session_ended`, `blocklist`, `profile_ack`, `error`, `please_update`) round-trips through JSON marshal/unmarshal with its `snake_case` discriminator intact.

**Given** the module layout
**When** the import graph is checked in CI
**Then** `plugin → companion`, `companion → proto`, `backend → proto` hold, nothing imports `plugin`, and `proto` imports none of them.

### Story 1.2: CI cross-build and release skeleton

As the maintainer,
I want CI that cross-builds the companion for every target OS and publishes the backend image,
So that a tagged release produces the binaries the plugin ships and a runnable server image.

**Acceptance Criteria:**

**Given** a pull request
**When** the workflow runs
**Then** the companion compiles for macOS arm64, macOS x64, Linux x64, and Windows x64, the backend container image builds, and all tests run.

> _Deferred:_ the `modernc.org/sqlite` build+open check on Windows named in the original AC is deferred to the story that first imports `modernc.org/sqlite` (Story 1.4 / Epic 4) — the dependency does not exist in Epic 1. See `spec-1-2-ci-cross-build-and-release-skeleton.md` and `epic-1-retro-2026-09-06.md` (finding F2).

**Given** a version tag
**When** the release workflow runs
**Then** the four companion binaries are attached to a GitHub release and the backend image is published
**And** the cross-built binaries are placed under `plugin/bin/<os>-<arch>/` with the commit/refresh step documented.

**Given** the hook resolves a binary
**When** it runs in any supported environment
**Then** it locates the correct `plugin/bin/<os>-<arch>/` binary via `${CLAUDE_PLUGIN_ROOT}`.

### Story 1.3: Transcript format spike and defensive parser

As a developer,
I want the Claude Code session-transcript format pinned and a tolerant parser that emits turn boundaries,
So that think-time detection rests on a documented, tested contract rather than a guess.

**Acceptance Criteria:**

**Given** the investigation is done
**When** its findings are committed
**Then** a repo note records the transcript file location, the JSONL line schema, the version observed, and known stability caveats.

**Given** a normal-turn fixture transcript
**When** the parser tails it
**Then** it emits exactly one turn-start and one turn-end event.

**Given** a turn fixture containing N tool calls
**When** the parser tails it
**Then** it still emits exactly one turn-start and one turn-end (tool-call boundaries do not split the burst).

**Given** a transcript file that is truncated mid-line, atomically renamed, or rotated
**When** the parser is watching it
**Then** the watcher recovers without crashing and resumes emitting events, and unknown line types are skipped.

### Story 1.4: Minimal backend — `hello`, connection registry, `/status` skeleton

As a companion client,
I want a backend that accepts my versioned `hello` and tracks my connection,
So that there is a hub to announce readiness to before any matching exists.

**Acceptance Criteria:**

**Given** `backend serve` with `PORT` set
**When** a client opens the websocket and sends `hello` with a supported protocol version
**Then** the connection is accepted with no `please_update` and the key is registered as connected.

**Given** a client sends `hello` with a protocol version older than current-minus-one
**When** the backend processes it
**Then** the client receives exactly one `please_update` and the socket is then closed.

**Given** a key already has a live connection
**When** a second `hello` arrives for that key
**Then** the prior connection receives `session_ended` and is closed, and the new one becomes the active connection.

**Given** the server is running
**When** `GET /status` is called
**Then** it returns JSON with at least `concurrent_users`, and structured stdout logs contain no account key and no message content.

### Story 1.5: Account key at a reinstall-stable path

As a user,
I want a stable anonymous identity created once on my machine,
So that blocks and bans keyed to me survive a plugin reinstall without linking to my Claude account.

**Acceptance Criteria:**

**Given** no key file exists
**When** the companion starts
**Then** it generates a random UUIDv4 and writes it to a stable OS-config path outside the plugin directory with mode `0600`.

**Given** a key file exists
**When** the companion starts again
**Then** it reads and reuses the same UUID, and removing/reinstalling the plugin directory does not change it.

**Given** the companion runs
**When** its stdout/stderr are inspected
**Then** the account key never appears in any log line
**And** a corrupt or empty key file is detected and regenerated.

### Story 1.6: Companion — launch, transcript watch, `ready`/`busy` over the websocket

As a user,
I want the companion to watch my session and tell the backend when my model starts and stops thinking,
So that I am queued during think-time and removed when it ends.

**Acceptance Criteria:**

**Given** the companion is started with `transcript-path`, `config-dir`, and `server-url` arguments
**When** it comes up
**Then** it opens a websocket to `server-url`, sends `hello` with its protocol version and account key, and begins tailing the transcript via the Story 1.3 parser.

**Given** a turn starts in the transcript
**When** the parser emits turn-start
**Then** the companion sends `ready` within ~1s; on turn-end it sends `busy` within ~1s.

**Given** the websocket drops
**When** the companion reconnects with backoff
**Then** it re-sends `hello` and resumes sending `ready`/`busy` from the current transcript state.

**Given** no chat feature exists yet
**When** the companion is running
**Then** it renders only a minimal status line and never writes to the Claude Code TUI.

### Story 1.7: SessionStart hook — fail-open launch

As a user,
I want the plugin to start the companion without ever slowing or breaking Claude Code,
So that installing this plugin carries no risk to my normal workflow.

**Acceptance Criteria:**

**Given** a Claude Code session starts
**When** the `SessionStart` hook runs
**Then** it resolves the platform companion binary via `${CLAUDE_PLUGIN_ROOT}`, spawns it detached with the three arguments and a hard timeout, and returns in under 50ms.

**Given** the companion binary is missing, non-executable, or fails to spawn
**When** the hook runs
**Then** the failure is swallowed, nothing is printed to Claude Code's stderr, and the session proceeds untouched.

**Given** the hook has spawned the companion
**When** the hook process exits
**Then** the companion keeps running, and it is spawned exactly once per Claude Code session.

**Given** an opt-out marker is present
**When** the hook runs
**Then** it does nothing.

### Story 1.8: Pane placement

As a user,
I want the chat pane placed next to my session automatically where possible,
So that I can see it without arranging windows myself.

**Acceptance Criteria:**

**Given** the session is running inside `tmux` (`$TMUX` set)
**When** the companion starts
**Then** it is placed as a `tmux` split pane adjacent to the Claude session.

**Given** the session is not inside tmux
**When** the companion starts
**Then** the plugin prints exactly one line instructing the user how to open/focus the companion pane, and nothing blocks.

**Given** either placement
**When** inbound messages arrive
**Then** they render only in the companion's own pane.

### Story 1.9: First-run screen and 18+ gate as a local precondition

As a first-time user,
I want an honest explanation and an 18+ check before anything connects,
So that I know what I am opting into and no one under 18 is queued.

**Acceptance Criteria:**

**Given** a fresh install
**When** the companion starts
**Then** it shows a one-screen: what the product is, an honest safety warning (strangers; do not share identifying, location, or financial information; screenshots/quoting are possible), how block and report work, and an affirmative "I am 18 or older" control — and it does not open the websocket until the screen is cleared.

**Given** the user accepts
**When** the acknowledgement is recorded
**Then** it is stored locally only — no server round-trip and no attestation is sent — and the companion proceeds to connect.

**Given** the user declines
**When** the screen closes
**Then** the companion stays running but never connects (no queue, no chat) and re-presents the screen on the next run.

**Given** the user has previously accepted
**When** they open the companion menu
**Then** the safety screen is reachable again, and all copy matches the `voice.md` tone rules.

---

## Epic 2: Get matched and chat

The FIFO queue and pairing loop, the shared pre-written opener, the text-only chat surface, the in-memory relay, and the searching→matched "spin" — the core loop that turns think-time into a conversation.

### Story 2.1: FIFO queue and single-writer pairing loop

As a waiting user,
I want to be paired with the next available person in arrival order,
So that I get into a conversation quickly and fairly.

**Acceptance Criteria:**

**Given** the backend `serve` process
**When** queue and pairing state is mutated
**Then** all mutation happens in exactly one channel-driven goroutine; no other code path mutates the queue, pairings, or in-memory policy.

**Given** two clients have sent `ready`
**When** the pairing loop runs
**Then** both receive `matched` carrying the same opaque `session_id`, and each `queued` client receives `queued` until matched.

**Given** three or more clients are waiting
**When** pairings are made
**Then** the longest-waiting client is matched first (strict FIFO), scanning from the head to the first eligible successor (not self; block/cooldown predicates are added in Epic 4).

**Given** a client sends `busy` or disconnects before being matched
**When** the loop next runs
**Then** it is removed from the queue silently with no error and no client-visible event.

### Story 2.2: Curated opener set and per-match selection

As a newly matched user,
I want a light pre-written opener to react to,
So that the first message is easy and never feels like a cold pickup.

**Acceptance Criteria:**

**Given** a match is made
**When** the backend builds `matched`
**Then** it includes a non-empty `opener` string chosen from a curated set, and both peers receive the identical opener.

**Given** consecutive matches on one backend
**When** openers are selected
**Then** the same opener is not used back-to-back (rotating, not random-with-repeats).

**Given** the opener set
**When** a contributor wants to extend it
**Then** it is a plain file in the repo they can append to, and a length-bound check keeps entries on-format; the `voice.md` opener guide is present.

### Story 2.3: Chat surface — render and input

As a matched user,
I want a clean text pane showing who I am talking to and our messages,
So that I can hold a conversation without any unsafe surface.

**Acceptance Criteria:**

**Given** a chat is active
**When** the companion renders it
**Then** it shows the peer's pseudonym and blurb, a scrollable current-chat history, an input box, and always-visible block, report, and leave controls.

**Given** a peer message contains ANSI/control bytes, HTML, or markdown
**When** it is rendered
**Then** it appears as inert literal text with no markup interpretation and no link activation.

**Given** the chat surface
**When** the user looks for a way to send a file, image, or audio
**Then** there is no such affordance anywhere, and input beyond the length cap is prevented client-side.

**Given** the peer is typing
**When** an indicator is shown
**Then** it is a typing indicator only; no read receipts are ever shown.

### Story 2.4: In-memory message relay

As a user in a chat,
I want my messages delivered to my partner and nowhere else,
So that the conversation is real-time and leaves no trace.

**Acceptance Criteria:**

**Given** A and B share a session
**When** A sends `chat_msg`
**Then** B receives it, A does not receive it back, and A's companion has already shown it optimistically keyed by `client_msg_id`.

**Given** any message flows through the relay
**When** disk and logs are inspected
**Then** no message content is ever written to disk or appears in logs (verified by test).

**Given** a session ends (`leave` or burst-end)
**When** the peer sends a later `chat_msg`
**Then** the backend drops it; already-received messages were delivered.

**Given** normal local load
**When** round-trip latency is measured
**Then** it is under ~500ms p90.

### Story 2.5: Searching and matched transition, with the spin

As a waiting user,
I want a calm spinner and then a small moment of delight when I match,
So that waiting is quiet and matching feels like a roulette landing.

**Acceptance Criteria:**

**Given** the user is `queued`
**When** the pane renders
**Then** it shows only a "searching" spinner — no queue position, no ETA, no online count.

**Given** `matched` arrives
**When** the transition plays
**Then** a brief non-blocking flourish runs for a bounded short duration, then the chat view appears with the opener shown once at the top; input readiness is not delayed by the flourish.

**Given** `matched` arrives while the flourish is still playing
**When** the transition completes
**Then** no message or state is lost, and all copy matches `voice.md`.

---

## Epic 3: Leaving without rejection

Every way a chat can end — routed through one identical gentle framing — plus the persist toggle and the session-ID keepsake.

### Story 3.1: Unified `session_ended` on every backend-side end

As the system,
I want one identical termination message for every cause,
So that a peer can never tell whether they were left, blocked, reported, or timed out.

**Acceptance Criteria:**

**Given** any termination cause (peer `leave`, peer `busy`/model-return, peer disconnect past grace, ban, connection takeover)
**When** the backend ends the session
**Then** it emits exactly one `session_ended` frame with no cause field and no `session_id`, byte-identical across all causes.

**Given** a session is ending
**When** `session_ended` is sent
**Then** the relay for that session is already closed, and `session_ended` is the last frame the peer receives on it.

### Story 3.2: "Their Claude came back" presentation and no alarm language

As a user whose chat just ended,
I want a calm, cheerful "catch you later" every time,
So that leaving or being left never stings.

**Acceptance Criteria:**

**Given** any end path (`session_ended` received, or local model-return with persist off)
**When** the companion updates
**Then** it shows the same "your Claude is back" state with cheerful copy — no apology, no "are you sure?", no confirm modal.

**Given** the companion's user-facing strings
**When** they are scanned
**Then** no end/disconnect copy contains "left", "disconnected", "connection lost", "rejected", "blocked", or "reported", and the tone matches `voice.md`.

**Given** the end state is shown
**When** the user is still in a think-time burst
**Then** the pane returns to the spinner; if the model is back, the pane closes.

### Story 3.3: Explicit leave control

As a user,
I want a one-tap "my Claude came back" that just works,
So that I can step away without ceremony or guilt.

**Acceptance Criteria:**

**Given** an active chat
**When** the user activates the leave control
**Then** exactly one `leave` is sent, with no confirmation prompt, and the local end state is shown immediately without waiting for the server.

**Given** the user has left
**When** a late peer `chat_msg` arrives
**Then** it is not shown.

**Given** the chat was persisted past the burst
**When** the user leaves
**Then** the flow is identical to leaving during the burst.

### Story 3.4: Reconnect grace window

As a user with a flaky connection,
I want a brief drop to not kill my chat,
So that a wifi blip does not end a good conversation or burn a cooldown.

**Acceptance Criteria:**

**Given** a companion disconnects mid-session
**When** it reconnects within ~10s presenting the same account key and `session_id`
**Then** the session resumes, relay continues, and the peer received no `session_ended` during the window.

**Given** the grace window expires with no reconnect
**When** the backend times out
**Then** the peer receives `session_ended` and both sides are re-enqueued if still busy.

**Given** a resume occurs
**When** rate limits are evaluated
**Then** the resume does not consume match-rate budget.

### Story 3.5: Silent re-enqueue while busy

As a user still mid-think-time when a chat ends,
I want to be quietly put back in line,
So that I get another match without a jarring "you were requeued" message.

**Acceptance Criteria:**

**Given** a session ends while the user's last signal was `ready` (still busy)
**When** the backend processes the end
**Then** the user is placed back on the queue tail within one tick with no client-visible requeue event, and the companion shows the spinner again.

**Given** a session ends after the user has gone `busy` (model back)
**When** the backend processes the end
**Then** the user is not re-enqueued.

**Given** a silent re-enqueue happens
**When** rate limits are evaluated
**Then** it does not count against the match-rate limit.

### Story 3.6: Persist toggle, default-on

As a user,
I want my chats to outlive the think-time burst by default,
So that a good conversation is not cut off the instant my model returns.

**Acceptance Criteria:**

**Given** a fresh install
**When** the persist setting is read
**Then** it is on.

**Given** persist is on
**When** the model returns (`busy`/turn-end)
**Then** the chat stays open and the companion shows only a quiet note that the model is back.

**Given** persist is off
**When** the model returns
**Then** the companion shows the end state and sends `leave` to the server (the UJ-1 friction-free exit).

**Given** the companion menu
**When** the user toggles persist
**Then** the change takes effect on the next chat.

### Story 3.7: Session-ID keepsake on persisted-chat end

As a user who just had a good persisted chat,
I want the chance to keep its ID,
So that I have the one thread that could let me find this person again later.

**Acceptance Criteria:**

**Given** a chat that was persisted past the burst
**When** it ends
**Then** the companion offers the `session_id` (from `matched`) to copy, with a one-line note that it is the only way to find this person again.

**Given** a chat that was not persisted
**When** it ends
**Then** no copy-ID affordance is shown.

**Given** the offer is shown
**When** the user dismisses it
**Then** nothing is blocked, and the decision to offer came from local persist state, never from `session_ended` contents.

---

## Epic 4: Staying safe with strangers

Block, cooldown, rate limits, report, content sanitization, keyword filter, and the maintainer ban CLI — the safety floor that makes stranger chat survivable for a small project.

### Story 4.1: Persisted policy store and single-writer load

As the backend,
I want blocks, cooldowns, and bans durable and loaded into memory at startup,
So that every matching decision is made from fast in-memory state that survives restarts.

**Acceptance Criteria:**

**Given** a fresh `DB_PATH`
**When** `backend serve` starts
**Then** it applies all embedded forward-only migrations, records `schema_version`, and creates `account`, `profile`, `block`, `cooldown`, `ban`, and `report` tables; SQLite is opened WAL with `busy_timeout`.

**Given** a database with some migrations already applied
**When** the backend starts
**Then** only pending migrations run, and no down-migration path exists.

**Given** the backend is running
**When** the single writer makes a matching decision
**Then** it reads policy only from memory; writes are write-through to SQLite.

**Given** `serve` is running
**When** a CLI subcommand opens the same database
**Then** both access it concurrently without "database is locked".

### Story 4.2: One-tap block

As a user in a bad match,
I want to block my partner in one action,
So that I never see them again and they get no signal that I blocked them.

**Acceptance Criteria:**

**Given** an active chat
**When** the user activates block
**Then** a `block` row keyed to the account key is written write-through, the in-memory predicate is updated, and the chat ends via an ordinary `session_ended` to the peer.

**Given** A has blocked B
**When** the pairing loop runs over many queue cycles
**Then** A and B are never paired again.

**Given** a backend restart
**When** the policy store reloads
**Then** the block is still in effect.

**Given** a block occurs
**When** the user's profile and preference fields are inspected
**Then** nothing in them changed, and A is returned to the spinner and re-enqueued if busy.

### Story 4.3: Block-list view and unblock

As a user,
I want to see and undo my blocks,
So that a block is a considered choice I stay in control of.

**Acceptance Criteria:**

**Given** the companion requests the block list
**When** the backend responds with `blocklist`
**Then** it contains every active manual block and no cooldowns.

**Given** the user removes an entry
**When** the backend processes it
**Then** the `block` row is deleted write-through, the in-memory predicate is cleared, and future pairing between the two is allowed again.

**Given** the block list is empty
**When** it renders
**Then** it shows a calm empty state per `voice.md`, reachable from the companion menu.

### Story 4.4: Auto-cooldown between just-matched pairs

As a user in a thin pool,
I want a delay before I re-meet the same person,
So that the roulette keeps feeling varied.

**Acceptance Criteria:**

**Given** a pairing that lasted at least the configured duration OR had at least one message each way
**When** it ends
**Then** the backend writes a `cooldown` row (default ~24h) and adds the predicate.

**Given** a pairing torn down inside the reconnect grace window with no messages exchanged
**When** it ends
**Then** no cooldown row is written and the pair can be re-matched immediately.

**Given** a cooldown has expired
**When** the pairing loop runs
**Then** it no longer blocks the pair, and expired rows are pruned.

**Given** the deployment
**When** cooldown timing is configured
**Then** the threshold-together and cooldown-length come from config with documented defaults (OQ-5).

### Story 4.5: Per-account-key rate limits

As the maintainer,
I want concrete per-key limits on matches, messages, and concurrent chats,
So that spam and bot behavior are blunted without unbounded surface.

**Acceptance Criteria:**

**Given** a client sends more than ~5 `chat_msg`/s
**When** the backend processes them
**Then** excess messages are dropped with an `error` and the socket is not closed.

**Given** a client is already in a session
**When** the pairing loop runs
**Then** it cannot be placed in a second concurrent session.

**Given** a client flaps `ready`/`busy` rapidly
**When** matches are attempted
**Then** it is matched at most once per ~10s.

**Given** a grace resume or a silent re-enqueue
**When** the match-rate limit is evaluated
**Then** neither counts against it, and every limit has a configured value with no unbounded case.

### Story 4.6: One-tap report

As a user facing something serious,
I want to report the chat in one tap,
So that the maintainer gets just enough context and nothing from my other chats.

**Acceptance Criteria:**

**Given** an active chat
**When** the user reports
**Then** the peer is also blocked (Story 4.2 behavior) and exactly one `report` row is written: the last ~N (default 20, configurable) messages of that session only, both account keys, and a timestamp.

**Given** the stored report
**When** it is inspected
**Then** `last_messages` holds at most N messages, all from that session and none from any other conversation.

**Given** `REPORT_WEBHOOK` is set
**When** a report is filed
**Then** a POST with the payload fires; when it is unset the row still persists for CLI review.

**Given** the whole codebase
**When** message-content writes are audited
**Then** the `report` row is the only path that persists message content.

### Story 4.7: Backend content sanitization — links, length, frame type

As the system,
I want links inerted and oversized or non-text frames rejected on ingest,
So that the highest-leverage abuse surfaces are closed before relay.

**Acceptance Criteria:**

**Given** a `chat_msg` or `note_put` containing a URL
**When** the backend ingests it
**Then** the URL is stripped or rendered inert per a documented rule before relay.

**Given** a message longer than the configured cap
**When** it is ingested
**Then** it is rejected with an `error` and not relayed.

**Given** an oversized or non-text websocket frame
**When** it arrives
**Then** it is rejected with an `error`, and the same sanitization rules demonstrably apply to empty-queue notes.

### Story 4.8: Keyword filter

As the maintainer,
I want a repo-governed keyword filter as a coarse safety net,
So that known slurs and abuse patterns are caught even without a report.

**Acceptance Criteria:**

**Given** `KEYWORDS_PATH` points at the repo-maintained list
**When** the backend starts or receives `SIGHUP`
**Then** the list is loaded/reloaded.

**Given** a message matches a listed term or pattern
**When** it is processed (after sanitization, before relay)
**Then** it is blocked or flagged per config and an `error`/notice is returned to the sender.

**Given** the repo
**When** a contributor looks for the list
**Then** the list file and a governance note exist, its format is documented, and the companion's report/block UI links to the published community guidelines.

### Story 4.9: Maintainer ban CLI

As the maintainer,
I want CLI subcommands to ban a key and work through reports,
So that I can remove a bad actor fast without standing up an admin web surface.

**Acceptance Criteria:**

**Given** `backend ban <key> [--note]`
**When** it runs
**Then** it writes a `ban` row and signals the running `serve` via `SIGHUP` (pidfile); `serve` also re-reads bans on a ~10s interval.

**Given** a key has been banned
**When** it sends `hello` or is mid-session
**Then** within ~10s (or immediately on `SIGHUP`) it cannot queue or chat, and any active session ends with an ordinary `session_ended`.

**Given** `backend reports`
**When** it runs
**Then** it lists report rows and can mark one actioned, which deletes that row's stored messages; the CLI and `serve` share the database without corruption, and bans persist across restart.

---

## Epic 5: Profile and the idle queue

The small optional profile, the skippable v2 preference hint, data deletion, idle nudges, and the empty-queue note.

### Story 5.1: Profile edit and canonical backend copy

As a user,
I want an optional pseudonym and blurb I can edit in the plugin,
So that a match sees a little of me if I want, and nothing if I do not.

**Acceptance Criteria:**

**Given** no profile is set
**When** the user queues and matches
**Then** it works, and the peer sees a default/blank pseudonym.

**Given** the user edits the profile and saves
**When** the companion sends `profile_put` (full replace; empty = clear)
**Then** the backend stores it, returns `profile_ack`, and it is the only source of the pseudonym/blurb delivered to a peer in the next `matched`.

**Given** any `matched` payload a peer receives
**When** it is inspected
**Then** it never contains the account key.

### Story 5.2: "Prefer to be matched with" hint — skippable, inert in v1

As a user,
I want to optionally record a match preference,
So that a future version could use it, without it changing anything now.

**Acceptance Criteria:**

**Given** the profile UI
**When** the user reaches the preference hint
**Then** it can be left unset with a single skip.

**Given** the hint is set
**When** the v1 pairing loop runs
**Then** match outcomes are identical to the hint being unset (no effect on FIFO), and the hint is not shown to peers.

**Given** the hint is set
**When** it is stored
**Then** it is retained for later v2 use only.

### Story 5.3: `forget_me` and local data deletion

As a user,
I want to delete my data from within the plugin,
So that I can walk away cleanly.

**Acceptance Criteria:**

**Given** the user triggers `forget_me`
**When** the backend processes it
**Then** it deletes every row keyed to the account key except an active `ban`, and acknowledges the deletion.

**Given** the deletion has run
**When** the user matches again
**Then** the peer-facing pseudonym is back to default, and a follow-up `hello` still works because the account-key file is unchanged unless the user also explicitly reset it.

**Given** the user also asks to clear local state
**When** the companion processes it
**Then** the local profile draft, persist preference, and safety acknowledgement are cleared, but the account-key file is kept unless explicitly reset.

### Story 5.4: Idle nudges

As a waiting user,
I want at most one gentle nudge while the queue is slow,
So that waiting stays calm and never turns into a game.

**Acceptance Criteria:**

**Given** the user has been `queued` past a short threshold
**When** a nudge is shown
**Then** at most one tooltip-style nudge is visible at a time (e.g. "add a line to your profile", a guidelines link), rotating slowly.

**Given** any nudge
**When** it renders
**Then** it is text/link only with no streaks, points, counters, or interactive reward mechanic.

**Given** `matched` arrives
**When** the chat renders
**Then** the nudge is already gone, and nudges never appear outside the `queued` state; copy matches `voice.md`.

### Story 5.5: Empty-queue "leave a note for the next person"

As a user alone in the queue at 3 a.m.,
I want to leave a short note for whoever arrives next,
So that the empty queue still leads somewhere.

**Acceptance Criteria:**

**Given** the user has waited past a longer threshold with no match
**When** the offer appears and the user submits a note
**Then** the companion sends `note_put` and the note is attached to the user's queue entry, held in backend memory only (never disk or logs).

**Given** the note's author is still waiting
**When** the next client is paired with them
**Then** the note replaces the curated opener for that match, and replying starts a normal chat.

**Given** the note's author leaves before being paired
**When** the queue entry is removed
**Then** the note is discarded.

**Given** a note is submitted
**When** it is processed
**Then** it passes the same sanitization and keyword filter as chat, it expires after a bounded time, and nothing about notes appears on `/status` or any list.

---

## Epic 6: Run it in public

Self-host packaging, the Fly.io deployment, the maintainer status view, and the repository documentation set.

### Story 6.1: Env-only configuration and self-host image

As a self-hoster,
I want to run the backend with one command and only environment variables,
So that standing up my own pool is trivial and drift-free.

**Acceptance Criteria:**

**Given** the repo
**When** `docker compose up` is run against `deploy/docker-compose.yml`
**Then** a working backend starts that a companion can connect to, with a volume mounted at `DB_PATH`.

**Given** the backend
**When** its configuration is inspected
**Then** every setting (`PORT`, `DB_PATH`, `KEYWORDS_PATH`, optional `REPORT_WEBHOOK`) is an environment variable and there is no config file anywhere.

**Given** a companion with no `SERVER_URL` set
**When** it starts
**Then** it connects to the public instance URL by default; the compose file documents the volume and the TLS-reverse-proxy expectation.

### Story 6.2: Fly.io deployment

As the maintainer,
I want a pinned single-machine Fly deployment,
So that the public instance runs cheaply and predictably.

**Acceptance Criteria:**

**Given** `deploy/fly.toml`
**When** `fly deploy` runs
**Then** one `shared-cpu-1x` machine comes up with a volume mounted at `DB_PATH`, TLS terminated at Fly, free shared IPv4, and reachable over WSS.

**Given** the Fly configuration
**When** scaling is inspected
**Then** it is pinned to exactly one instance (no horizontal scale).

**Given** the deployment
**When** the maintainer needs to operate it
**Then** a runbook covers deploy, logs, and volume snapshot/restore, and notes the projected ~$5–10/month cost.

### Story 6.3: `GET /status` and time-to-match instrumentation

As the maintainer,
I want a minimal live view of the pool's health,
So that I can tell at a glance whether liquidity (M1) is holding.

**Acceptance Criteria:**

**Given** pairings are happening
**When** the backend records each queue-enter→pairing duration
**Then** it maintains a rolling median.

**Given** `GET /status`
**When** it is called
**Then** it returns concurrent users, median time-to-match, reports outstanding (unactioned `report` rows), and error rate, as JSON plus a minimal HTML view.

**Given** `/status`
**When** it is served
**Then** it requires no auth and exposes no account keys and no message content.

### Story 6.4: Privacy posture and open-source documentation

As a prospective user or self-hoster,
I want plain-language docs on what is stored and how reports are handled,
So that I can trust the project and run it responsibly.

**Acceptance Criteria:**

**Given** the repo
**When** the docs are reviewed
**Then** a README with install steps, `PRIVACY.md`, `SELF-HOSTING.md`, and `MODERATION.md` all exist and are linked from the README.

**Given** `PRIVACY.md`
**When** it is checked against the code
**Then** its "what is and isn't stored" list matches the persisted entities (`account`, `profile`, `block`, `cooldown`, `ban`, `report`, `schema_version`) and states that no chat content is stored except short-lived reports.

**Given** `MODERATION.md`
**When** it is reviewed
**Then** it documents the report-handling process including the NCMEC reporting path on actual knowledge, and states the instance is operated by the maintainer as an individual in their personal name; the opener voice guide is discoverable from `CONTRIBUTING`.

### Story 6.5: License

As a contributor,
I want a clear open-source license,
So that I know the terms before contributing or self-hosting.

**Acceptance Criteria:**

**Given** the repo root
**When** it is inspected
**Then** a `LICENSE` file is present containing MIT or Apache-2.0 (the maintainer's choice, resolving OQ-10), and the README states the license.

**Given** the repo convention for source headers
**When** CI runs
**Then** there are no license-check failures.
