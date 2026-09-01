---
title: "PRD — Claude Think-Time Chat Roulette"
status: final
created: 2026-08-31
updated: 2026-09-01
---

# PRD — Claude Think-Time Chat Roulette

## 1. Overview

**One line.** An open-source Claude Code plugin that pairs you with another Claude user
for a short, ephemeral 1:1 text chat during the dead time while your model is thinking —
so idle think-time becomes human contact instead of waiting.

**Why it exists.** The target user is a lonely, AI-heavy Claude user. They spend much of
their day driving a model that thinks in bursts of seconds to minutes, and that dead time is
frequent and empty. The product's job is to land a small hit of genuine human connection in
that gap. It's a for-fun, open-source project — not a startup, not a growth play. Success is
whether lonely users can reliably find someone to talk to, not time-in-app.

**Shape.** Two halves, built in order:

- **v1 (this PRD's build target):** a Claude Code plugin. FIFO roulette pairing, a
  terminal-adjacent chat surface, and the safety primitives that make stranger chat
  survivable for a small project. **Ephemeral** in the sense that *nothing is stored* — no
  chat logs, client or server (FR15). Chat *duration* is governed by the **persist** toggle,
  which ships default-on (FR23), so by default a chat outlives the think-time burst until
  someone leaves.
- **v2 (roadmap, section 10):** a companion webapp whose **only** job is reconnection — look
  up a past chat's session ID, post to a "missed connections" board, complete a mutual-consent
  connect. Nothing real-time lives there. Interest tags and "pull-in" matching are separate
  later ideas, not part of the webapp.

**Framing decisions already locked** (from brainstorming, 2026-08-28):

- FIFO pairing with **no matching algorithm** — right for short think-time, lowest-effort MVP.
- A light **dating-adjacent element is accepted**, not designed against.
- The **"my Claude came back" exit convention** is load-bearing: every way of leaving a chat
  is framed as your model returning, so bailing never reads as rejection.
- **No minigames, no help/expertise marketplace.** Scope stays on light serendipitous social
  chat. (Rationale in `addendum.md §3`.)

---

## 2. Goals and non-goals

### Goals

- G1. A lonely Claude user can go from "my model is thinking" to "I'm talking to a person" in
  seconds, with near-zero friction.
- G2. Leaving a chat — deliberately or by disconnect — never feels like rejection to either
  side.
- G3. A user who wants more than an ephemeral chat has a voluntary, escalating path to
  actually connect, and is never forced onto it.
- G4. The project is safe enough to run in public as a small open-source effort: known abuse
  vectors have a realistic answer, and the maintainer's legal exposure is deliberately
  minimized.
- G5. The plugin is easy to install and the backend is cheap enough for one person to host
  indefinitely.

### Non-goals

- NG1. **Not** optimizing for engagement, session length, daily actives, or retention. These
  are explicit counter-metrics (section 4).
- NG2. **Not** a matching/recommendation engine in v1. FIFO only.
- NG3. **Not** a help desk, expertise marketplace, or work-collaboration tool.
- NG4. **Not** a general social network — no friend graph, no feed, no persistent DMs in v1.
- NG5. **Not** targeting or accommodating minors. The product is 18+ (section 8).
- NG6. **Not** a claude.ai / Claude Desktop experience in v1 — **Claude Code only.** This is
  a deliberate narrowing of the brainstorm's "integrated with Claude": think-time can only be
  detected via lifecycle hooks, which exist in Claude Code (and possibly Claude Desktop —
  unconfirmed), not on claude.ai (`addendum.md §2`). Broader reach is the v2 webapp's job.
- NG7. **Not** serving match preferences in v1. The primary user often wants an
  opposite-gender match; v1 knowingly does not honor that — FIFO pairs whoever's next, full
  stop. A preference hint is *stored* (FR29) only to seed v2 pull-in. Accepted because any
  preference weighting needs pool depth v1 won't have and adds the legal surface FIFO avoids
  (section 8, OQ-B).

---

## 3. Target users

Personas are carried inline in the journeys (section 5). In summary:

- **The primary user — "the lonely power user."** Uses Claude for hours a day, often solo,
  often late. AI-forward, comfortable in a terminal, socially under-fed. Wants low-stakes
  human contact that fits in the cracks of their workflow. Would prefer an opposite-gender
  match but will take anyone (v1 does not honor this preference — NG7). Wary of anything that
  feels like a dating app pickup, and equally wary of being rejected.
- **The maintainer / self-hoster.** Runs the public instance or their own pool for a
  community. Wants low cost, low moderation burden, and low legal risk.
- **Out of persona scope:** minors; people seeking paid work help; people looking for a
  full-featured dating or friendship app.

---

## 4. Success metrics and counter-metrics

### Primary metric (operational — confirmed by Diego)

- **M1. Liquidity — median time-to-match < ~15s during active hours.** If the spinner
  reliably resolves inside a think-time burst, the product works. If it doesn't, nothing else
  matters. Measured server-side from queue-enter to pairing.

### Outcome signal (north star)

- **M2. People keep using it.** Diego's stance: if lonely users open the plugin during their
  think-time, that *is* the evidence it meets the need — nobody uses a stranger-chat plugin
  unless they want the contact. So the outcome proxy is repeat use and organic spread, **not**
  a satisfaction survey or post-chat rating (deliberately not built). Watch: share of installs
  still queuing weeks later; word-of-mouth growth in the beachhead community; self-hosted
  pools spun up by others.

The line against an engagement product (C1–C2): people coming back to use it is the goal;
people stuck in it for hours, or us designing for daily-actives, is the failure. Repeat use
during think-time = healthy. Rising time-in-app = not.

### Supporting signals (low priority, watch not target)

- M3. Commitment-ramp usage: rate at which chats lead to a persisted session and a shared
  session ID. Signals real value without demanding retention.

### Counter-metrics (if these rise, something is wrong)

- C1. Median or p90 **session length trending up** over weeks — the product is becoming a
  time sink, not a think-time filler.
- C2. **Daily-actives / return-frequency becoming a design focus** — drift toward an
  engagement product. (Users returning is fine; us building for it is not.)
- C3. Reports per 100 chats trending up.

---

## 5. User journeys

### UJ-1 — Marcus, mid-refactor, model is thinking (happy path)

Marcus is knee-deep in a large refactor with Claude Code. He submits a big prompt; the model
starts working. The plugin's hook fires on prompt submission and drops him into the queue; a "searching"
spinner appears in the chat pane. Within a few seconds he's paired and a pre-written opener
appears ("What's the last thing that made you laugh?"). They trade a few messages.
Claude finishes; the `Stop` hook fires; the pane shows "your Claude is back" and the chat
closes (Marcus has persist off). He reads Claude's output and keeps working. Total human
contact: ~90 seconds. He didn't have to decide to end it.

### UJ-2 — Sam wants the chat to keep going (persist)

Sam has **persist** on (the default). She gets matched during a think-time burst and
the conversation is good. Her model returns; the pane notes it quietly but the chat stays
open because persist is on. She keeps talking while starting to read Claude's output. A few
minutes later she's done and taps "my Claude came back" to leave — the same no-rejection
framing as an automatic exit. Before the chat closes she's offered its **session ID** to
copy, in case she wants to find this person again later (FR24).

### UJ-3 — Priya gets a match that's going nowhere

Priya is matched with someone who opens with a crude line. She taps **block**. The chat ends
immediately for both; on the other side it looks identical to any normal "their Claude came
back" exit — no "you were blocked" message, ever. Priya is dropped back to the spinner and
silently re-queued. The blocked pair won't be matched again. If the exchange warranted it she
also taps **report**, which attaches only the last few messages of *that* chat.

### UJ-4 — Empty queue

It's 3 a.m. and nobody else is waiting. Instead of a spinner forever, after a short wait the
pane offers Marcus the option to **leave a short note for the next person** (FR50), or keep
waiting. The note gives the next arrival something to reply to, and is the v1 seed for the v2
missed-connections board (FR51).

### UJ-5 — Lena's first run

Lena installs the plugin. On first invocation she sees: a one-screen explanation of what this
is, an **honest safety warning** (you're being connected to strangers; don't share
identifying or financial info; here's how to block and report), an **18-or-older**
confirmation, and an optional prompt to set a display name. She skips the profile entirely.
Nothing is sent to the server until she finishes this screen.

---

## 6. Features and functional requirements

FRs are globally numbered with stable IDs. Unless marked **[v2]**, an FR is in the v1 build
target.

### F1 — Think-time detection and session lifecycle

Feasibility investigated (OQ-1, `addendum.md §2`): **buildable via a bundled companion
process — not as a pure in-conversation pane.** The FRs below reflect the resolved design.

- **FR1.** The chat surface SHALL be a **bundled companion process** (a small TUI) that the
  plugin launches (on `SessionStart`), holds the websocket to the backend, renders the chat,
  and takes the user's input. Hooks alone cannot hold a UI.
- **FR2.** Think-time boundaries SHALL be determined by the companion **watching the Claude
  Code session transcript** — burst begins when the user's turn starts, ends when the
  assistant's response completes. The `UserPromptSubmit` and `Stop` hooks MAY provide a
  secondary signal but SHALL NOT be relied on for timing (`UserPromptSubmit` caps at 30s;
  `Stop` fires only after the turn). On burst begin the user is entered into the queue; on
  burst end the active chat ends, unless **persist** is enabled for that user (FR22–FR23).
- **FR3.** `[ASSUMPTION]` The plugin SHALL treat tool-call boundaries within one turn as the
  same continuous burst, not new matches, so a single user turn maps to at most one match.
- **FR4.** If a burst ends while the user is still in the queue (never matched), the plugin
  SHALL silently remove them from the queue with no error surfaced.
- **FR5.** The companion SHALL render **adjacent to the Claude session**: automatically as a
  `tmux` split pane when the session is inside tmux; otherwise the plugin SHALL print a
  one-line instruction to open it in a split / pane / window the user controls. Inbound
  messages render in the companion's own pane — nothing is pushed into the Claude Code TUI.
- **FR6.** The plugin SHALL be a no-op when the user is not opted in, the companion can't
  start, or the backend is unreachable — it MUST never block, delay, or error the Claude Code
  session. `[ASSUMPTION]` where no adjacent placement is possible (some IDE-embedded
  terminals), the companion still runs and the user places or focuses it manually.
- **FR6.** The plugin SHALL work as a no-op when the user is not opted in or the backend is
  unreachable — it MUST never block, delay, or error the user's Claude Code session.

### F2 — FIFO matching and queue

- **FR7.** The matching server SHALL pair users strictly first-in-first-out: the longest
  waiter is matched to the next arrival. No scoring, no preference weighting in v1.
- **FR8.** A user SHALL NOT be matched with anyone on their block list (FR32) or within an
  active auto-cooldown window with them (FR34).
- **FR9.** `[ASSUMPTION]` If the two longest waiters are ineligible for each other (block /
  cooldown), the server SHALL skip to the next eligible pair rather than stall the queue.
- **FR10.** While queued, the user SHALL see a "searching" spinner and nothing else — no
  queue position, no ETA, no "N people online."
- **FR11.** On a match, both users SHALL be shown the same rotating pre-written opener
  (F7) at the same time.
- **FR12.** `[ASSUMPTION]` Median time-to-match SHALL be instrumented server-side and exposed
  on a maintainer status endpoint (supports M1).

### F3 — The chat surface

- **FR13.** The chat SHALL be **text only.** No image, file, audio, or video sharing.
- **FR14.** Outbound URLs / links in messages SHALL be stripped or rendered inert in v1
  (abuse-surface reduction, `addendum.md §1`).
- **FR15.** Chat message content SHALL NOT be persisted to disk on the server or the client.
  The relay holds messages in memory only for delivery.
- **FR16.** The surface SHALL show, at minimum: the other person's pseudonym, their optional
  tags/profile blurb if set, the message history for the current chat, an input box, and
  always-visible **block**, **report**, and **leave ("my Claude came back")** controls.
- **FR17.** `[ASSUMPTION]` Typing indicators MAY be shown; read receipts SHALL NOT.
- **FR18.** The surface SHALL indicate connection state (searching / connected / your Claude
  is back / disconnected) using the no-rejection language in F4.

### F4 — Exit convention and disconnect handling

- **FR19.** Every chat-ending event — model returns, user leaves, user blocks, network
  disconnect, other party leaves — SHALL be presented to the *remaining* user with identical,
  neutral "their Claude came back" framing. No event SHALL ever tell a user they were left,
  rejected, blocked, or reported.
- **FR20.** On any disconnect or chat end while the user's burst is still active, the plugin
  SHALL return the user to the spinner and silently re-enter them in the queue.
- **FR21.** There SHALL be no "user left" / "user is typing…" / "connection lost" alarm
  language anywhere in the UI.

### F5 — Persist and the commitment ramp

- **FR22.** The profile SHALL include a **persist** toggle. When on, the user's chats
  continue after their think-time burst ends, until they explicitly leave.
- **FR23.** **Persist SHALL ship default-on.** A new user's chats continue past think-time
  until they leave, unless they turn persist off in the profile. (Diego's call; consistent
  with the brainstorm's "defaultable ON.")
  - `[DESIGN NOTE]` Accepted tradeoff (Diego, confirmed at finalize): with persist default-on
    and no idle auto-close, the *default* user ends every chat with an explicit "my Claude
    came back" tap; the friction-free auto-exit (UJ-1) applies only to users who turn persist
    off. The social fiction still covers the manual exit, so it never reads as rejection (G2
    holds; the "no decision required" wording of G1/UJ-1 is relaxed for the default user).
    Consequence: **C1 (session length) is the primary guardrail against this drifting into a
    time sink** — watch it closely post-launch. Rejected alternatives: idle auto-close after
    N seconds; default-off.
- **FR24.** When a persisted chat ends, the user SHALL be offered the chat's **session ID**
  to copy, with a one-line explanation that it's the only way to find this person again.
- **FR25.** `[ASSUMPTION / research-informed]` Any *reconnect* built on a session ID (v2)
  SHALL require **mutual opt-in** — both parties took an explicit keep/connect action — never
  a unilateral one. Rationale: matches the no-rejection framing and prevents one-sided
  re-contact (`addendum.md §1`).
- **FR26.** Session IDs SHALL be opaque, unguessable, and carry no lookup capability in v1
  (they only become useful with the v2 board).

### F6 — Tiny profile

- **FR27.** The profile SHALL be small and entirely optional: a display name / pseudonym and
  `[ASSUMPTION]` a short free-text blurb. Editable from within the plugin.
- **FR28.** Other users SHALL only ever see the pseudonym, the blurb, and (v2) tags — never
  the account key.
- **FR29.** `[ASSUMPTION]` A single optional "prefer to be matched with" hint (e.g. gender)
  MAY be stored for v2 pull-in; it has no effect on v1 FIFO matching and MUST be skippable.

### F7 — Openers

- **FR30.** The plugin SHALL show one opener per match, drawn from a curated rotating set of
  generic pre-written prompts. Not generated per match in v1 (cost/latency, `addendum.md §3`).
- **FR31.** `[ASSUMPTION]` The opener set SHALL live in the open-source repo so contributors
  can extend it.

### F8 — Block, cooldown, and rate limiting

- **FR32.** A user SHALL be able to **block** the current chat partner with one action. The
  block is permanent (until the user removes it) and enforced by the account key
  (FR41).
- **FR33.** The block primitive SHALL be independent of all preference / tag settings:
  blocking someone MUST NOT require or cause any change to the user's tags or match hints
  (`addendum.md §3`).
- **FR34.** The server SHALL apply an **auto-cooldown** between any two users who have just
  been matched: `[ASSUMPTION]` after ~1 minute together they are auto-blocked from re-matching
  for ~24h, to force variety in a small pool. Exact rules are an open question (OQ-5).
- **FR35.** The user SHALL be able to view and remove entries from their block list from
  within the plugin. Auto-cooldowns expire on their own and need not be listed.
- **FR36.** The server SHALL rate-limit per **account key** to blunt spam/bot
  behavior. `[ASSUMPTION]` starting bounds, tunable: ≤ 1 new match per ~10s, ≤ ~5 messages
  per second, and exactly 1 concurrent chat session. Every limit MUST have a concrete
  configured value — none left unbounded.

### F9 — Reporting and moderation

- **FR37.** The chat surface SHALL offer a one-tap **report** on the active chat. Reporting
  SHALL also block that user.
- **FR38.** A report SHALL capture only the last N `[ASSUMPTION: N≈20]` messages of *that*
  chat plus both account keys and a timestamp — nothing from any other conversation.
- **FR39.** Reports SHALL be delivered to the maintainer(s) out of band (e.g. a moderation
  queue / notification) and SHALL be the only circumstance under which any message content is
  written to storage, retained only as long as needed to action the report.
- **FR40.** The maintainer SHALL be able to **ban** an account key, immediately ending that
  key's ability to queue or chat. A ban persists until the user deliberately resets their
  local key (FR41).
- **FR41.** The **account key** (the identifier all blocks, cooldowns, bans, and rate limits
  are keyed to — see Glossary) SHALL be a **random UUID generated once and stored at a stable
  path outside the plugin's own directory** (e.g. an OS config dir), so that reinstalling or
  updating the plugin keeps the same key. It is sent to the backend as the user's identity;
  the backend never sees anything about the user's Claude account.
  - `[DESIGN NOTE]` Account-linkage was investigated and ruled out: **Claude Code exposes no
    account identifier to plugin code** (hooks get only `session_id` / `transcript_path` /
    `cwd`; the `/status` email is display-only; credential files are undocumented and rotate
    on re-login). See `addendum.md §2`. Consequence: the key survives a plugin reinstall but
    **not** the user deleting the id file, wiping the config dir, moving machines, or running
    a modified (open-source) client — so bans/blocks are evadable by a motivated user.
    Accepted for v1: keyword filter (FR49), rate limits (FR36), and the small pooled
    community are the real backstops. Revisit only if evasion becomes a real problem.
- **FR42.** The repo SHALL ship published **community guidelines** and the report/block flow
  SHALL link to them.
- **FR49.** `[ASSUMPTION]` The server SHALL apply a **keyword filter** to messages as part of
  reactive moderation — blocking or flagging a maintained list of slurs and known abuse
  patterns. The list and its governance live in the repo (OQ-7). This is a coarse safety net,
  not the primary control (block/report is).

### F10 — Onboarding, age gate, and safety disclosure

- **FR43.** On first run the plugin SHALL present, before any network call: what the product
  is, an honest **safety warning** (you are being connected to strangers; do not share
  identifying, location, or financial information; screenshots/quoting are possible), and the
  block/report explanation.
- **FR44.** The user SHALL affirmatively confirm they are **18 or older** (a checkbox /
  one-tap attestation) before being allowed to queue. **Decision (Diego): self-attestation is
  the v1 approach — no ID checks, no third-party age estimation.** The residual regulatory
  risk is acknowledged and documented (section 8, OQ-A), not mitigated by additional gating.
- **FR45.** The safety screen SHALL be re-accessible from the plugin menu at any time.
- **FR46.** `[ASSUMPTION]` Declining the age gate or safety acknowledgement SHALL leave the
  plugin installed but inert (no queue, no chat).

### F11 — Idle experience (empty / slow queue)

- **FR47.** While the user is waiting in an empty or slow queue, the surface MAY show a
  single lightweight nudge at a time — e.g. "while you wait, why not add a line to your
  profile?" or a link to the community guidelines. These are gentle tooltips only.
- **FR48.** Idle nudges SHALL NOT include games, activities, streaks, points, or anything
  that rewards staying. If the queue resolves, the nudge disappears immediately. (Guardrail
  against feature bloat — `addendum.md §3`.)
- **FR50.** `[ASSUMPTION]` After a threshold wait with no match, the surface SHALL offer the
  user the option to **leave a short note for the next person** to enter the queue. The next
  arrival sees the note as their opener instead of a pre-written one and may reply, which
  starts a normal chat. Notes are ephemeral (consumed by the next arrival or expiring) and
  subject to the same text-only, link-inert, keyword-filter rules as chat (FR13–FR14, FR49).
- **FR51.** The "leave a note" flow is the v1 seed for the v2 missed-connections board
  (R2–R3): the note plus its session ID is what a user could later choose to publish. In v1
  it stays local to the queue and is never publicly listed.

---

## 7. Non-functional requirements

### Privacy and data handling

- **NFR1.** No chat message content is persisted anywhere by default (client or server). The
  only exception is report payloads (FR38–FR39), minimized and short-lived.
- **NFR2.** Server-persisted state is limited to: pseudonym + blurb, block lists, cooldown
  timers, bans, and the **account key** (FR41). No IP logs beyond what's transiently
  required for abuse rate-limiting; `[ASSUMPTION]` any such logs auto-expire within 24h.
- **NFR3.** The **account key** SHALL NOT be shown to other users or included in any
  client-visible payload. (It carries no personal data — it's a random UUID, FR41 — but it's
  still the handle for blocks/bans and shouldn't leak.)
- **NFR4.** A user SHALL be able to delete their profile and block list from within the
  plugin; deletion removes all server-side state keyed to them except active bans.
- **NFR5.** The privacy posture (what is and isn't stored) SHALL be documented in the repo in
  plain language.

### Availability and performance

- **NFR6.** `[ASSUMPTION]` A plugin hook SHALL block the Claude Code session for no more than
  ~50ms: it enqueues the queue-enter / chat-end action asynchronously and returns immediately.
  It SHALL fail open (FR6) — any hook error or timeout is swallowed and the Claude session
  proceeds untouched.
- **NFR7.** `[ASSUMPTION]` Message relay round-trip SHALL be < ~500ms p90 under normal load.
- **NFR8.** `[ASSUMPTION]` The backend SHALL target ~99% monthly reachability on a
  best-effort basis — no formal SLA — and SHALL degrade cleanly: an unreachable or erroring
  backend yields a calm "no one around right now" state in the chat surface, never an error
  dump, and never affects the Claude session (FR6).
- **NFR9.** The backend SHALL run within a single small cloud instance's resources for the
  expected pool size (`[ASSUMPTION]` low hundreds of concurrent users).

### Security

- **NFR10.** All client↔server traffic SHALL be encrypted in transit (TLS / WSS).
- **NFR11.** The server SHALL treat all message content as untrusted: no rendering of remote
  markup/HTML, link inertness per FR14, input length caps.
- **NFR12.** The plugin SHALL send its locally stored **account-key UUID** (FR41) with each
  backend request over the encrypted channel (NFR10); the server treats that UUID as the
  identity. No identity derivation, no Claude-account linkage, no separate auth handshake in
  v1. (Consequence — ban/block evasion — is the FR41 `[DESIGN NOTE]`.)

### Cost and operability

- **NFR13.** `[ASSUMPTION]` Target hosting cost for the public instance SHALL be under
  ~$20/month at expected scale.
- **NFR14.** The backend SHALL be packaged for one-command self-hosting (e.g.
  `docker compose up`), defaulting to the public instance's URL when run as a client.
- **NFR15.** The maintainer SHALL have a minimal status/metrics view: concurrent users,
  median time-to-match, reports outstanding, error rate.

### Voice and tone

The emotional core of the product — a roulette *spin*, serendipity, a light warm moment
between two people — has to survive contact with the requirements. These NFRs give it teeth.

- **NFR16.** All user-facing copy SHALL be warm, light, and unpolished-in-a-human-way —
  never corporate, never clinical, never therapized. It should feel like a friend nudged you,
  not like an app onboarded you.
- **NFR17.** The exit / disconnect copy (FR19, FR21) SHALL read as an easy, cheerful
  "catch you later" — the "my Claude came back" fiction carries no apology, no guilt, no
  "are you sure?".
- **NFR18.** The searching → matched transition SHALL have a small moment of delight (the
  "spin" landing) — `[ASSUMPTION]` a brief animation or flourish in the pane — without
  becoming a minigame or costing perceptible time.
- **NFR19.** The curated opener set (FR30–FR31) SHALL be playful and disarming, skewed toward
  low-stakes human curiosity, never survey-like or interview-like. The repo SHALL include a
  one-paragraph voice guide so contributed openers stay on-tone.

### Open-source project health

- **NFR20.** The repo SHALL include: README with install steps, the safety/privacy docs, the
  community guidelines, the opener set + voice guide, a self-host guide, and a documented
  process for handling reports (including the NCMEC path, section 8).
- **NFR21.** License `[ASSUMPTION]` MIT or Apache-2.0.

---

## 8. Trust, safety, and legal posture

This section exists because the product is "adoption-serious" and its closest precedents
(Omegle, Chatroulette) failed on exactly these issues. Detail and citations in
`addendum.md §1`.

### Design stance

- **Text-only, no media, inert links** (FR13–FR14). This is the single highest-leverage
  safety decision: it removes the CSAM-image, nudity, and malware-link surfaces that sank
  video-based competitors, and keeps what a small project must monitor to a tractable size.
- **Ephemeral by default** (FR15, NFR1). Little exists to leak, subpoena, or breach.
- **Reactive moderation** (FR37–FR42, FR49): documented guidelines, in-flow report + block,
  instant disconnect, rate limits, keyword filtering (FR49), fast maintainer bans. Proactive
  scanning is out of scope for a hobby project's resources.
- **18+ only** (FR44). The product does not court minors and explicitly excludes the
  profile-biased "pull-in" feature from any under-18 context (moot while 18+, relevant if age
  policy changes).
- **Honest failure-to-warn mitigation** (FR43): the first-run warning is explicit about risk,
  because "failure to warn" was a live claim against Omegle.

### Known abuse vectors and responses

| Vector | Response |
|---|---|
| Harassment / hate / threats | Instant block; report → maintainer review → ban (FR37–FR40) |
| Sexual content / solicitation (amplified by the dating-adjacent framing) | Keyword filter (FR49); block/report; guidelines set the norm |
| CSAM in text, or grooming | Block/report; **documented NCMEC reporting path on actual knowledge** (18 U.S.C. §2258A); no media channel to carry imagery |
| Spam / bots | Per-key rate limits (FR36); 1 concurrent session; auto-cooldown (FR34) |
| Doxxing / re-contact abuse | No DMs; no directory; mutual-opt-in required for any reconnect (FR25); block is permanent (FR32) and survives a plugin reinstall — though a motivated user can still reset their local key (FR41), which the small pooled community and keyword filter partly offset |
| Small-pool repeat / stalking a thin pool | Auto-cooldown (FR34); block outranks all preferences (FR33) |

### Open legal questions (carried to section 11)

- **OQ-A. Age assurance — DECIDED, residual risk accepted.** v1 uses a self-attested 18+
  checkbox (FR44). Self-attestation is increasingly held inadequate (UK Online Safety Act /
  Ofcom "Highly Effective Age Assurance," in force since 25 July 2025; EU trending), and a
  hobby OSS project cannot run ID checks. Diego's call: accept the risk with a documented
  rationale and an 18+ ToS rather than add gating or geofencing. Revisit if the public
  instance draws a jurisdiction's attention or grows beyond hobby scale. *Owner: Diego
  (revisit trigger above).*
- **OQ-B. Matching-as-defective-product.** *A.M. v. Omegle* let negligent-design claims past
  Section 230 by treating the pairing feature itself as the product. FIFO-with-no-algorithm
  and 18+-only are partly protective; the maintainer should get the risk assessed and keep
  the design rationale documented. *Owner: Diego — get an informal legal read before public
  launch; keep the FIFO / 18+ / text-only rationale written down in the repo.*
- **OQ-C. Operator identity / liability — DECIDED.** The public instance is operated by the
  project maintainer as an **individual** (their GitHub identity), with **no legal entity**
  behind it. Implications to carry forward: the ToS and privacy policy are authored in the
  maintainer's personal name; personal liability is not shielded by an LLC; the honest
  risk-warning (FR43) and minimal-data posture (NFR1–NFR5) are the main protections.
  Self-hosters operate under their own name and exposure, and the repo docs SHALL say so.
  *Consider revisiting (a lightweight entity) only if the instance outgrows hobby scale.*

---

## 9. Cold-start and launch strategy

The pool must feel non-empty during think-time or the core loop fails (M1). Research-backed
plays (`addendum.md §1`):

- **CS1. Launch into one community, not "all lonely Claude users."** Pick a single existing
  Claude / AI space (a specific Discord or subreddit) as the beachhead so concurrency is
  achievable.
- **CS2. Power-hour events.** Promote scheduled windows ("chat night, Thursdays 8pm ET") to
  concentrate a thin pool into the same minutes.
- **CS3. Pre-build the audience.** Use the plugin's own GitHub following and the beachhead
  community before and during launch.
- **CS4. Empty-queue fallback** (FR50, UJ-4): "leave a note for the next person." Doubles as the
  seed for the v2 missed-connections board and as an async liquidity buffer.
- **CS5. FIFO stays the baseline** even as the pool grows; **pull-in** (v2) is the
  concurrency patch for when the live queue is thin but active users exist.

`[ASSUMPTION]` No paid acquisition, no bot-fill of the queue with fake users (fake-user fill
is explicitly rejected on trust grounds).

---

## 10. v2 / post-MVP roadmap

Built on top of v1 once liquidity is proven. Not in the v1 build target.

### The webapp — reconnect, and nothing else

**Decision (Diego): the companion webapp does exactly one job — let two people who had an
ephemeral chat find each other again.** It is not a chat surface, not a real-time UI, not a
general "home" for the product. No live matching happens there.

- **R1. Session-ID lookup.** The v1 opaque session ID (FR24, FR26) becomes searchable on the
  webapp. That's the entry point to everything below.
- **R2. "Missed connections" board.** You can't DM anyone. You post your session ID; the
  other person searches for it, finds the contact info you chose to attach, and a built-in
  connect flow lets you actually connect. **Highest-risk v2 surface** (persistent, public,
  doxxing / harassment vector) — requires pre-publication moderation or heavy filtering.
- **R3. Mutual-consent connect flow.** Reconnection completes only when both people opt in
  (FR25) — the board surfaces the request; it never exposes contact info unilaterally.

### Other post-v1 ideas — not the webapp

These touch the plugin and matching backend, not the reconnect webapp. Parked, unsequenced.

- **R4. Optional interest tags.** Lightly bias matching toward shared tags so a user
  occasionally re-meets a small recurring cast. Tags never override the block primitive.
- **R5. Profile-matched "pull-in."** An active, non-waiting user can be pulled into a match
  if their profile fits a searcher's opt-in criteria (e.g. a/s/l). FIFO stays the default.
  Doubles as the cold-start fix. Needs careful anti-repeat / cooldown design.
- **R6. Anti-repeat / cooldown rules**, properly designed, for pull-in and small pools
  generally (beyond the v1 "~1 minute then day-long auto-block" sketch).

---

## 11. Open questions and assumptions

### Resolved (no phase-blockers remain)

- **OQ-1 (technical, was critical) — RESOLVED: buildable, with constraints.** Findings
  (`addendum.md §2`, doc-cited): (a) there is no thinking-lifecycle hook; `UserPromptSubmit`
  (30s cap) and `Stop` (fires after the turn) are too weak to time or hold a pane, so the
  **companion process watches the session transcript** to detect burst start/end itself
  (FR1–FR2); (b) no mechanism pushes unsolicited text into the Claude Code TUI, so the chat
  lives entirely in the **companion's own pane** and never needs to (FR5); (c) plugins can
  bundle a binary and launch a long-lived unsandboxed process — packaging is fine; (d) `tmux`
  auto-split works; VS Code / JetBrains integrated terminals and native Windows have no tmux,
  so there the user places the pane manually (FR5–FR6). **Verdict: not impossible; ships as a
  plugin-bundled companion TUI. The degradation is "great in tmux / a split-capable terminal,
  manual placement elsewhere" — acceptable for a terminal-comfortable audience.** Residual
  detail for the architect: exact transcript-file location and format stability, turn-parse
  edge cases (interrupts, `SubagentStop`), companion↔plugin handshake.
- ~~OQ-2 (safety/legal). Age assurance stance.~~ **Resolved:** self-attested 18+ checkbox,
  residual risk accepted and documented (FR44, OQ-A).
- ~~OQ-3 (auth). Account-linked identity key.~~ **Resolved — not possible, so scoped down.**
  Investigation (`addendum.md §2`): Claude Code gives plugin code no account identifier, and
  any client-computed key is unverifiable server-side. Decision: the account key is a random
  UUID stored at a reinstall-stable path (FR41, NFR12) — no auth handshake. It survives a
  plugin reinstall; bans/blocks are still evadable by a motivated user (FR41 `[DESIGN NOTE]`).
- ~~OQ-4 (metric). Connection-quality survey metric.~~ **Resolved:** cut. No post-chat
  rating or survey is built. Outcome signal is repeat use + organic spread (M2).

### Non-blocking — decide during build, owner noted

- **OQ-5.** Exact auto-cooldown rules (FR34) — *owner: architect + Diego.*
- ~~OQ-6. Persist default.~~ **Resolved: default-on** (FR23; see the `[DESIGN NOTE]` there
  about the manual-exit tension).
- **OQ-7.** Keyword-filter approach and word-list governance (FR49) — *owner: maintainer.*
- **OQ-8.** Report payload size N (FR38) and moderation-queue mechanism (FR39) — *owner:
  architect.*
- ~~OQ-9. Operator entity.~~ **Resolved: individual maintainer, no entity** (OQ-C). ToS /
  privacy policy authored in the maintainer's personal name — *drafting owner: Diego.*
- **OQ-10.** License choice (NFR21).

### Assumptions index

Every `[ASSUMPTION]`-tagged item in this PRD, plus a few settled-but-still-provisional
decisions. **Load-bearing** ones (bold) would change the design materially if wrong; confirm
those before/with the architect.

| ID | Assumption |
|---|---|
| FR2 | **Watching the session transcript is a reliable way to detect turn start/end** (transcript path/format stable enough). |
| FR3 | **Tool-call boundaries (`PreToolUse`/`PostToolUse`) are treated as one continuous burst → at most one match per user turn.** |
| FR6 | Where no adjacent pane placement is possible (some IDE terminals), the companion still runs and the user places it manually. |
| FR9 | **If the two longest waiters are ineligible (block/cooldown), the server skips to the next eligible pair rather than stalling.** |
| FR12 | Median time-to-match is instrumented server-side and exposed on a maintainer status endpoint. |
| FR17 | Typing indicators may be shown; read receipts never. |
| FR25 | Any v2 reconnect on a session ID requires mutual opt-in, never unilateral. |
| FR27 | The optional profile blurb is short free text. |
| FR29 | A single optional "prefer to be matched with" hint may be stored for v2; no v1 effect; skippable. |
| FR31 | The opener set lives in the repo for contributors to extend. |
| FR34 | **Auto-cooldown ≈ 1 minute together → auto-block ≈ 24h.** Exact rules = OQ-5. |
| FR36 | Rate-limit starting bounds: ≤1 match/~10s, ≤~5 msg/s, exactly 1 concurrent session. |
| FR38 | Report captures last N ≈ 20 messages of that chat only. |
| FR41 | Account key = random UUID at a **reinstall-stable path**; no account-linkage possible; bans/blocks still evadable (accepted). |
| FR46 | Declining the age/safety gate leaves the plugin installed but inert. |
| FR49 | **Keyword filtering is part of reactive moderation** (word list governance = OQ-7). |
| FR50 | Empty-queue "leave a note for the next person" fallback, after a threshold wait. |
| NFR2 | IP logs (if any) auto-expire within 24h. |
| NFR6 | A hook blocks the Claude session ≤ ~50ms; enqueues async. |
| NFR7 | Relay round-trip < ~500ms p90. |
| NFR8 | Backend targets ~99% monthly reachability, best-effort, no SLA. |
| NFR9 | **Hobby-scale: low hundreds of concurrent users, one small cloud instance.** |
| NFR12 | Account-key UUID sent per request over TLS; no auth handshake, no Claude linkage. |
| NFR13 | Hosting cost < ~$20/month at expected scale. |
| NFR18 | The match-landing "spin" delight is a brief animation/flourish. |
| NFR21 | License is MIT or Apache-2.0 (choice = OQ-10). |
| CS (§9) | No paid acquisition; no fake-user queue fill. |

Also foundational (not tagged inline but assumed): **text-only + inert links as the safety
keystone** (FR13–FR14).

---

## 12. Out of scope (v1)

- Voice, video, image, or file sharing.
- Any matching algorithm, preference weighting, or recommendation.
- Friend lists, persistent DMs, feeds, profiles beyond pseudonym + blurb.
- The webapp, the missed-connections board, interest tags, pull-in (all v2).
- Minigames, prompts-as-games, or any "activity" beyond text chat, tooltip nudges, and the
  empty-queue note (FR47–FR51).
- Work-help / consulting / expertise matching.
- claude.ai and Claude Desktop clients.
- Paid tiers, monetization, ads.
- Proactive content scanning / ML moderation.
- Minor-safe mode.

---

## 13. Glossary

| Term | Meaning |
|---|---|
| **Think-time burst** | The stretch from the user submitting a prompt (`UserPromptSubmit`) to the model finishing its response (`Stop`) — the dead time the product fills. Approximate; there is no true "thinking started/stopped" signal (OQ-1). |
| **Account key** | A random UUID generated once and stored at a stable path outside the plugin dir (FR41), so a plugin reinstall/update keeps it. The single identifier every block, auto-cooldown, ban, and rate limit is keyed to. No Claude-account linkage is possible (platform limit — `addendum.md §2`); the backend only ever sees the UUID. Resettable by deleting the id file, so enforcement is best-effort. Never shown to other users or sent in client-visible payloads. |
| **Pseudonym** | The display name other users see. Optional, user-set, unconnected to the account key. |
| **Persist** | A per-user profile toggle (FR22). When on, a chat continues past the think-time burst until the user explicitly leaves. Ships default-on (FR23). |
| **The "my Claude came back" convention** | The social fiction that every chat exit — voluntary, blocked, disconnected, or model-returned — is framed identically as the other person's model returning, so leaving never reads as rejection (FR19, FR21). |
| **Block** | A permanent, user-initiated bar on being re-matched with a specific person (FR32), keyed to the account key, independent of all preference/tag settings (FR33). |
| **Auto-cooldown** | A temporary, automatic bar on re-matching two people who were just paired (FR34) — forces variety in a thin pool. Expires on its own. |
| **Commitment ramp** | The one continuous opt-in escalation: persist → copy session ID → (v2) post to the missed-connections board → mutual-consent reconnect. Each step is voluntary; none is forced. |
| **Session ID** | An opaque, unguessable identifier for a specific chat (FR24, FR26). Inert in v1; becomes searchable on the v2 webapp. |
| **Pull-in (v2)** | Matching a searcher against an active but non-waiting user whose profile fits opt-in criteria (R5). Also the cold-start liquidity patch. Not in v1. |
| **Beachhead** | The single existing community the product launches into for liquidity (CS1), rather than "all lonely Claude users". |
| **Liquidity** | The probability that a user entering the queue finds a match within a think-time burst; the primary success metric (M1). For a real-time pool, driven by concurrency, not total installs. |
