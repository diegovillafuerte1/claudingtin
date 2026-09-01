---
id: SPEC-claudingtin
companions:
  - glossary.md
  - voice.md
  - ../planning-artifacts/architecture/architecture-claudingtin-2026-09-01/ARCHITECTURE-SPINE.md
sources:
  - ../planning-artifacts/prds/prd-claudingtin-2026-08-31/prd.md
  - ../planning-artifacts/prds/prd-claudingtin-2026-08-31/addendum.md
---

> **Canonical contract.** This SPEC and the files in `companions:` are the complete, preservation-validated contract for what to build, test, and validate. Source documents listed in frontmatter are for traceability — consult them only if you need narrative rationale or prose color this contract intentionally omits.

# Claude Think-Time Chat Roulette — v1

## Why

**A vision to realize, for a specific person to reach.** The target user is a lonely, AI-heavy Claude Code user who spends hours a day driving a model that thinks in bursts of seconds to minutes. That dead time is frequent and empty. This puts a small hit of genuine human contact in the gap: while your model works, you are paired FIFO with another Claude user for an ephemeral 1:1 text chat, and every way of leaving is framed as your model returning so it never reads as rejection. It is a for-fun open-source project, not a startup and not a growth play. It succeeds if lonely users can reliably find someone to talk to during think-time; it fails if it becomes a time sink or gets designed around daily-actives. v1 is a Claude Code plugin plus a bundled companion TUI plus one hosted matching/relay backend; the v2 reconnect webapp is out of scope.

## Capabilities

- **CAP-1 — Think-time detection and auto-enqueue**
  - **intent:** While the user's model is working, the companion detects the think-time burst by tailing the Claude Code session transcript and enters the user into the match queue with no action from them; when the burst ends the active chat ends unless persist is on for that user.
  - **success:** Submitting a prompt in Claude Code places the user in the queue within that burst; a single user turn including its tool calls maps to at most one match; a burst that ends while the user is still unmatched removes them from the queue silently with no error surfaced.

- **CAP-2 — FIFO pairing**
  - **intent:** The backend pairs waiting users strictly first-in-first-out with no scoring and no preference weighting, scanning from the queue head and skipping any successor that is block-, cooldown-, ban-, or self-ineligible.
  - **success:** The longest waiter is matched to the first eligible arrival; median time-to-match is instrumented server-side (queue-enter to pairing) and runs under ~15s during active hours; an ineligible queue head never stalls the queue; both matched users receive the same opener at the same time.

- **CAP-3 — Ephemeral text chat surface**
  - **intent:** Paired users exchange text-only messages in the companion's own pane, placed adjacent to the Claude session (tmux auto-split where available, manual placement otherwise), showing the peer's pseudonym and optional blurb, the current chat's history, an input box, and always-visible block / report / leave controls.
  - **success:** Messages relay in-memory only with no disk write on client or server; no image, file, audio, or video can be sent; URLs are inert; the surface shows connection state using the no-rejection language of CAP-4; message relay round-trip is under ~500ms p90 at expected load.

- **CAP-4 — No-rejection exit convention**
  - **intent:** Every chat-ending event — model returns, peer leaves, block, report, network disconnect — is presented to the remaining user with one identical neutral "their Claude came back" framing.
  - **success:** No UI path ever tells a user they were left, rejected, blocked, or reported; the wire `session_ended` message carries no cause field and no session ID; a disconnect or chat-end while the user's burst is still active returns them to the spinner and silently re-enqueues them; the companion decides whether to offer "copy session ID" from its own local persist state, never from message contents.

- **CAP-5 — Persist toggle and session-ID keepsake**
  - **intent:** A per-user "persist" toggle, shipping default-on, keeps a chat alive past the end of the think-time burst until the user explicitly leaves; when a persisted chat ends the user is offered that chat's opaque session ID to copy, with a one-line note that it is the only way to find this person again.
  - **success:** With persist on, the model returning does not close the chat and the user leaves via "my Claude came back"; with persist off, burst-end closes the chat automatically; the session ID offered is opaque, unguessable, and carries no lookup capability in v1.

- **CAP-6 — Tiny optional profile**
  - **intent:** An entirely optional, in-plugin-editable profile of a pseudonym plus a short free-text blurb, with the backend copy canonical (full-replace on change); plus a single optional, skippable "prefer to be matched with" hint that is stored but has no effect on v1 matching.
  - **success:** A user can enter the queue with no profile set; peers only ever see the pseudonym, the blurb, and nothing else — never the account key; a "forget me" action deletes every server-side row keyed to the account key except an active ban.

- **CAP-7 — Curated rotating openers**
  - **intent:** Each match shows both users the same opener drawn from a curated, rotating set of pre-written prompts that lives in the open-source repo.
  - **success:** Exactly one opener per match, identical on both sides, selected by the backend and delivered in the `matched` message; openers are not generated per match; a contributor can add openers by editing the repo set.

- **CAP-8 — Block, auto-cooldown, and rate limiting**
  - **intent:** A user can permanently block the current chat partner in one action, keyed to the account key and independent of any preference or tag setting; the backend applies an automatic ~24h cooldown between two users after a pairing that actually happened; and it rate-limits per account key on match rate, message rate, and concurrent chats.
  - **success:** A blocked pair is never matched again; the block list is viewable and removable from within the plugin; a cooldown row is written only after a pairing lasts ~1 minute or both sides send at least one message, never at pairing time and never for a pairing torn down inside the reconnect grace window with no exchange; every rate limit has a concrete configured value (starting bounds: ≤1 new match per ~10s, ≤~5 messages/s, exactly 1 concurrent chat); involuntary re-enqueues and grace resumes do not count against the match-rate limit.

- **CAP-9 — Reporting and maintainer moderation**
  - **intent:** A one-tap report on the active chat, which also blocks that user, captures only the last ~20 messages of that one chat plus both account keys and a timestamp and delivers it to the maintainer out of band; the maintainer can ban an account key from a CLI on the backend binary.
  - **success:** A report row is the only circumstance under which any message content is written to storage, and it is deleted once actioned; a banned key cannot queue or chat, taking effect within ~10s of the ban command; the repo ships community guidelines and the report/block flow links to them.

- **CAP-10 — Content sanitization and keyword filter**
  - **intent:** The backend is the single point that strips or inerts links, enforces message length caps, rejects oversized or non-text frames on ingest, and applies a repo-maintained keyword filter — all before relay; the companion renders all peer text as literal.
  - **success:** No active link reaches a peer; slurs and known abuse patterns on the maintained list are blocked or flagged; the companion renders no remote markup, HTML, or control sequences from peer text; empty-queue notes are subject to the same rules.

- **CAP-11 — First-run onboarding and 18+ gate**
  - **intent:** Before any network call, the companion presents what the product is, an honest safety warning (you are being connected to strangers; do not share identifying, location, or financial information; screenshots and quoting are possible), the block/report explanation, and an affirmative "I am 18 or older" attestation; the safety screen stays re-accessible from the plugin menu.
  - **success:** No traffic leaves the machine until the screen is cleared; declining the age or safety acknowledgement leaves the plugin installed but inert (no queue, no chat); attestation is self-declared only, with no ID check, no age estimation, and no server-side attestation record.

- **CAP-12 — Empty-queue "leave a note"**
  - **intent:** After a threshold wait with no match, the surface offers the user the option to leave a short note for the next person to enter the queue, who sees the note as their opener and may reply to start a normal chat.
  - **success:** Notes are ephemeral — consumed by the next arrival or expiring — and held in backend memory only; they are subject to the same text-only, link-inert, and keyword-filter rules as chat; in v1 a note is never publicly listed.

- **CAP-13 — Fail-open host integration**
  - **intent:** The plugin's `SessionStart` hook spawns the companion once per Claude Code session, detached, with a hard timeout, and returns immediately; every failure path is swallowed.
  - **success:** A hook blocks the Claude Code session for no more than ~50ms; a spawn error, missing binary, unreachable backend, or crashed companion produces no error, delay, or block in the Claude Code UI; not being opted in is a silent no-op.

- **CAP-14 — Self-hostable backend and maintainer status**
  - **intent:** The backend ships as a single image that self-hosts with one command and, run as a client, defaults to the public instance URL; it is configured through environment variables only and exposes a minimal `GET /status` view.
  - **success:** `docker compose up` runs a working pool; `GET /status` reports concurrent users, median time-to-match, reports outstanding, and error rate; the only HTTP endpoints are the websocket upgrade and `GET /status` (maintainer actions are CLI subcommands on the same binary); the public instance fits one small cloud instance at low-hundreds concurrency for under ~$20/month.

- **CAP-15 — Open-source project documentation**
  - **intent:** The repo ships the documentation a self-hoster and a report-handler need to operate the product responsibly.
  - **success:** The repo contains a README with install steps, the safety and privacy docs, the community guidelines, the opener set plus its voice guide, a self-host guide, and a documented report-handling process including the NCMEC path.

## Constraints

- **Claude Code only in v1.** Think-time is detectable only via the Claude Code session transcript and hooks; no claude.ai, no Claude Desktop, no web client. The v2 reconnect webapp is a separate initiative and is reconnect-only, never a real-time UI.
- **Nothing about chat content is persisted anywhere**, client or server; the relay holds messages in memory only for delivery. Sole exception: a report payload (the last ~20 messages of one chat), deleted once actioned.
- **The backend is the single authority for all matching and moderation policy** — identity acceptance, eligibility (block / cooldown / ban), rate limiting, keyword filtering. The open-source companion renders outcomes and never gates on policy locally. Carve-out: the first-run 18+/safety gate is a local companion precondition with no server round-trip and no attestation record.
- **Hub-and-spoke only.** All queue, pairing, and session state lives in the backend and only there; companions hold no authoritative state and never communicate companion-to-companion. A companion's only recovery action is to reconnect and re-announce.
- **Identity is a presented, unverified random UUID.** The companion reads or creates it once at a stable OS-config path outside the plugin directory and sends it in `hello`; the backend trusts it with no verification. No Claude-account linkage is possible — the platform exposes none to plugin code. Ban and block evasion by a motivated user (deleting the id file, modified client) is accepted for v1.
- **The plugin must never block, delay, or error the host Claude Code session.** Every failure path fails open; a hook returns in ≤~50ms.
- **Text only, no media, links inert** — the single highest-leverage safety decision, removing the CSAM-image, nudity, and malware-link surfaces that sank video-based precedents.
- **18+ only, self-attested.** No ID checks, no third-party age estimation, no minor-safe mode. Residual regulatory risk is accepted and documented, not mitigated by additional gating.
- **Single backend instance in v1** — one SQLite file, no horizontal scale. Sized for low hundreds of concurrent users on one small cloud instance; hosting target under ~$20/month (~$5–10/month real Fly.io bill).
- **FIFO with no matching algorithm and no preference weighting.** The optional "prefer to be matched with" hint is stored only to seed v2 and has zero effect on v1 pairing.
- **Reactive moderation only** — reports, block, rate limits, keyword filter, fast bans. No proactive or ML content scanning. A documented NCMEC reporting path applies on actual knowledge of CSAM.
- **The public instance is operated by the maintainer as an individual**, with no legal entity behind it; the ToS and privacy policy are authored in the maintainer's personal name. Self-hosters operate under their own name, and the repo docs say so.
- **The wire protocol is versioned JSON defined once in `/proto`.** Both companion and backend import `/proto`; no message shape exists elsewhere. The backend accepts the current and previous minor version; an older companion gets a single `please_update` then a close.
- **All client↔server traffic is TLS/WSS**, and the server treats all message content as untrusted: no rendering of remote markup or HTML, input length caps, link inertness.
- **The implementation stack and topology are fixed by the adopted architecture spine** — Go 1.27, a Bubble Tea v2 companion TUI, `coder/websocket`, pure-Go `modernc.org/sqlite`, a single Fly.io machine plus one volume, in a monorepo of `/plugin`, `/companion`, `/backend`, `/proto`. See `ARCHITECTURE-SPINE.md` for the 18 architecture decisions, wire message set, entity model, and source tree.
- **User-facing copy is warm, light, and human** — never corporate, clinical, or therapized. Exit copy is a cheerful "catch you later" with no apology and no "are you sure?"; the searching→matched transition has a brief non-blocking flourish. See `voice.md`.
- **No paid acquisition and no bot or fake-user queue fill.** Launch into a single beachhead community, optionally concentrated with scheduled "power-hour" windows.

## Non-goals

- Not optimizing for engagement, session length, daily actives, or retention — these are explicit counter-metrics.
- Not a matching, recommendation, or preference-weighting engine in v1. FIFO only.
- Not a help desk, expertise/consulting marketplace, or work-collaboration tool.
- Not a general social network — no friend graph, no feed, no persistent DMs.
- Not targeting or accommodating minors; no minor-safe mode.
- Not a claude.ai or Claude Desktop experience in v1.
- Not the v2 reconnect webapp, the missed-connections board, interest tags, or pull-in matching.
- No voice, video, image, or file sharing.
- No proactive content scanning or ML moderation.
- No paid tiers, monetization, or ads.
- No post-chat rating or satisfaction survey — deliberately not built; the outcome signal is repeat use and organic spread.

## Success signal

Primary (M1): median time-to-match under ~15s during active hours, measured server-side from queue-enter to pairing — if the spinner reliably resolves inside a think-time burst, the product works, and if it does not, nothing else matters. Outcome proxy (M2): people keep using it and it spreads on its own — the share of installs still queuing weeks later, word-of-mouth growth in the beachhead community, and self-hosted pools spun up by others. Failure indicators (C1–C3): median or p90 session length trending up over weeks, daily-actives or return-frequency becoming a design focus, or reports per 100 chats trending up.

## Assumptions

- Watching the Claude Code session transcript is a reliable way to detect turn start and end — the transcript path and format are stable enough. (Also an open question — see below.)
- Tool-call boundaries within one turn are one continuous burst, so a user turn maps to at most one match.
- If the two longest waiters are ineligible for each other, the backend skips to the next eligible pair rather than stalling the queue.
- Auto-cooldown is roughly: ~1 minute paired, or ≥1 message each way, then a ~24h bar on re-matching. Exact rules are OQ-5.
- Rate-limit starting bounds: ≤1 new match per ~10s, ≤~5 messages per second, exactly 1 concurrent session. Tunable.
- A report captures the last ~20 messages of that chat only. Exact N is OQ-8.
- Reconnect grace window is ~10s; a CLI ban takes effect within ~10s (backend ban re-read interval / `SIGHUP`).
- Message relay round-trip is under ~500ms p90; a hook blocks the Claude Code session ≤~50ms; the backend targets ~99% monthly reachability, best-effort, no SLA.
- The open-source license is MIT or Apache-2.0 (choice is OQ-10).

## Open Questions

- **OQ-5** — Exact auto-cooldown rules: how long a pairing must last before a cooldown row is written, and how long the cooldown lasts. Owner: architect + Diego.
- **OQ-7** — Keyword-filter approach and word-list governance. Owner: maintainer.
- **OQ-8** — Report payload size N (assumed ~20) and the moderation-queue delivery mechanism (`REPORT_WEBHOOK` vs. other). Owner: architect.
- **OQ-10** — Open-source license choice, MIT vs. Apache-2.0. Owner: Diego.
- **OQ-A** — Self-attested 18+ may be held legally inadequate (UK Online Safety Act / Ofcom "Highly Effective Age Assurance" in force since 2025-07-25; EU trending the same way). Risk is accepted for v1; revisit if the public instance draws a jurisdiction's attention or outgrows hobby scale. Owner: Diego.
- **OQ-B** — "Matching-as-defective-product" liability (*A.M. v. Omegle* let negligent-design claims past Section 230). Get an informal legal read before public launch and keep the FIFO / 18+ / text-only rationale documented in the repo. Owner: Diego.
- **Transcript contract spike** — The Claude Code session-transcript file location and JSONL schema are not a documented, stable contract, yet the companion depends on both. Needs an implementation spike to pin the current format, build a defensive parser, and CI-test the full cross-build matrix (including `modernc.org/sqlite` on Windows).
- **Legal docs** — The ToS and privacy policy in the maintainer's personal name are still to be drafted. Owner: Diego.
