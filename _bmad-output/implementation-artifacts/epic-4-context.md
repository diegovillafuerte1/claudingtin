# Epic 4 Context: Staying safe with strangers

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

Give stranger chat a safety floor that a small project can run: a one-tap permanent block with an in-mod block-list view, an automatic re-match cooldown between just-paired users, concrete per-key rate limits, a one-tap report that also blocks and keeps only the last ~20 messages of that one chat, backend-side link inerting / length caps / bad-frame rejection, a keyword filter whose list lives in the repo, and a maintainer `ban` / `reports` CLI. The backend decides all of this; the mod only shows the results. Planning text written before the pivot talks about a companion, `/ws` websocket frames and panes. Read that as history: the client is now the TypeScript mod (`plugin/hooks/*.tsx`), which talks to the Go backend over HTTPS long-poll `/v1`.

## Stories

- Story 4.1: Persisted policy store and single-writer load
- Story 4.2: One-tap block
- Story 4.3: Block-list view and unblock
- Story 4.4: Auto-cooldown between just-matched pairs
- Story 4.5: Per-account-key rate limits
- Story 4.6: One-tap report
- Story 4.7: Backend content sanitization — links, length, frame type
- Story 4.8: Keyword filter
- Story 4.9: Maintainer ban CLI

## Requirements & Constraints

- Two keys are never matched if either has blocked the other, if they have an active cooldown, or if either is banned. Blocks are permanent until the user removes them, and they are keyed only to the account key.
- Blocking changes nothing in the user's profile, preference or tag fields. The blocked peer gets no signal: they see an ordinary `session_ended`, which is the same "their Claude came back" ending every other chat end gets. The blocker returns to the spinner and is re-enqueued if still busy.
- The block list shows manual blocks only, never cooldowns. Removing an entry allows the pair to be matched again.
- Cooldown (default ~24h, configurable) is written only when a pairing lasted at least the threshold (~1 min, configurable) OR both sides sent at least one message. It is never written at pairing time, and never for a pairing torn down inside the reconnect grace window with no exchange. Expired rows stop counting and are pruned.
- Rate limits, all configured, none unbounded: ≤~5 `chat_msg`/s per key (extra messages get an `error`, and the handle stays alive); at most 1 new match per ~10s; exactly one concurrent session. Grace resumes and silent re-enqueues never count against the match-rate limit.
- A report also blocks the peer. It writes exactly one `report` row: the last N messages of that session only (default 20, configurable), both account keys and a timestamp. If `REPORT_WEBHOOK` is set, the payload is also POSTed there; if not, the row still persists for CLI review. The report row is the only place message content is ever persisted. Content is deleted when the report is marked actioned.
- Persisted state is limited to profile, block, cooldown, ban, account, report and `schema_version`. Rate-limit counters, notes, the queue, pairings and relayed messages stay in memory only. Logs carry no message content and no account keys. The account key never appears in any payload a peer receives.
- Sanitization applies to `chat_msg` AND `note_put`: URLs are stripped or made inert by a documented rule, text over the cap is rejected with an `error` and not relayed, and oversized or non-text bodies are rejected.
- The keyword filter is loaded from `KEYWORDS_PATH` at startup and reloaded on `SIGHUP`. It runs after sanitization and before relay. A match is blocked or flagged depending on config, and the sender gets an `error`/notice. The repo ships the list, a governance note and a description of the list format.
- Bans take effect within ~10s, or immediately on `SIGHUP`. A banned key cannot queue or chat, and its active session ends with an ordinary `session_ended`.

## Technical Decisions

- **Policy is decided by the backend only.** The mod sends intents and renders results. It never filters or decides eligibility.
- **Single writer.** The hub goroutine in `backend/internal/hub` owns the queue, pairings and in-memory policy. At startup it loads blocks, cooldowns and bans from SQLite. After that, SQLite is a write-through mirror, and no other code reads policy for a matching decision. Matching scans from the queue head for the first eligible successor, within a bounded scan. Policy is added by ANDing predicates into the existing `eligible(a, b)` closure in `hub.Run`, which today checks only `a.Key != b.Key`. The scan and the loop stay untouched.
- **Persistence:** `modernc.org/sqlite` v1.57.0 (pure Go, no cgo; not yet a dependency). The database is opened in WAL mode with `busy_timeout` so `serve` and the CLI can share the file. Migrations are embedded and forward-only, gated by `schema_version`, with no down path; they go under `backend/migrations/`. Tables are snake_case. IDs are UUIDv4. Timestamps are epoch-ms `INTEGER`. Entities: `block(blocker_key, blocked_key, created_ms)`, `cooldown(key_a, key_b, expires_ms)`, `ban(account_key, created_ms, note)`, `report(id, reporter_key, reported_key, created_ms, last_messages)`, plus `account` and `profile`.
- **Config is env only:** `PORT`, `DB_PATH`, `KEYWORDS_PATH`, `REPORT_WEBHOOK` (optional), plus the cooldown, rate-limit and report-N tuning values, each with a documented default. Exact values are operational tuning owned by the maintainer.
- **No admin HTTP surface.** The only endpoints are `/v1` (`hello`, `events`, `send`) and `GET /status`. Admin is CLI only (`ban <key> [--note]`, `reports` with a mark-actioned option) against the shared SQLite file. `ban` sends `SIGHUP` to `serve` through a pidfile, and `serve` also re-reads bans every ~10s. Stub mains already exist at `backend/cmd/ban` and `backend/cmd/reports`, next to `backend/cmd/serve`. The planning docs call these "subcommands of one binary", so pick one shape and keep it consistent.
- **Wire (`/proto`).** `Block{}`, `Report{LastN}` and `Blocklist{}` are declared as placeholders. Epic 4 gives them their payloads and adds the unblock and blocklist-request messages. Wire types are PascalCase structs with a snake_case `type`. Client-facing failures are a `proto.Error` message, never a 5xx.
- **What exists today:** `POST /v1/send` (`backend/internal/server/v1.go`) accepts `block` and `report` and then ignores them; only `ready`/`busy`/`leave`/`chat_msg` reach the hub. Bodies are capped at 64 KiB and undecodable bodies get `bad_frame`. Because `/ws` is gone, the "non-text frame" check now means a `/v1/send` body that is oversized or is not a valid JSON client message. The hub keeps no per-session message history, so Story 4.6 needs a bounded in-memory ring per pairing, discarded at teardown. Leave/block/report in the mod (`plugin/hooks/chat.tsx` `leaveChat`) sends `block` or `report` (`last_n: 5`) and then `leave`, in order. The backend must therefore treat a report as report+block on its own, and it, not the client, sets the limit on N. The planning default of 20 and the mod's current 5 need to be reconciled.
- Go workspace: only `./backend` and `./proto`. The dependency guard allows only backend→proto.

## UX & Interaction Patterns

- The mod renders all peer text only as `Text` with control characters stripped, never as `Markdown`, `Link` or `Code`. The only links are our own, such as the community-guidelines `Link` in the report/block UI. Peer text is never one.
- The block list opens in a Pane from `/claudingtin blocks`. Its empty state is calm and follows `voice.md`. Leave, block and report stay as always-visible Buttons, mirrored by `/claudingtin leave|block|report`.
- Every end looks the same to the peer: no cause is shown and nothing is alarming. Mod hooks never await the network, and failures become a calm offline state.

## Cross-Story Dependencies

- 4.1 (the store plus loading it into the hub) blocks 4.2, 4.3, 4.4, 4.6 and 4.9. 4.3 depends on 4.2. 4.6 reuses 4.2's block path. 4.7 must come before 4.8, because the filter runs after sanitization. 4.9 builds on 4.1's ban load and on WAL sharing between processes.
- Builds on Epic 3 and Epic 7: neutral `session_ended`, silent re-enqueue, the reconnect grace window (needed for the cooldown and rate-limit exemptions), and the one-handle-per-key supersede (which already gives "one concurrent session").
- Epic 5's `note_put` shares 4.7/4.8 sanitization. Epic 6's `/status` "reports outstanding" reads 4.6's report rows, and its docs cover the report-handling process and the community guidelines.
