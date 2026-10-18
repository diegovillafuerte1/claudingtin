# Epic 7 Context: Native in Claude Code (mods pivot)

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

Replace the Go companion TUI and terminal pane placement with a Claude Code mod (TypeScript, in `plugin/`) that draws the chat inside the session and talks to the backend over a long-poll HTTPS API. The epic reaches parity with everything Epics 1–3 delivered (think-time signal, first-run gate, matching, chat, calm endings, silent re-enqueue, grace resume), then deletes the companion, the `/ws` transport, the session-start launcher and the binary release pipeline, leaving one client and one transport. It runs before Stories 3.6–3.7 and Epics 4–6.

## Stories

- Story 7.1: Mods skeleton — backend-status band (done)
- Story 7.2: Backend HTTP long-poll transport
- Story 7.3: Account key and first-run gate in the mod
- Story 7.4: Think-time from turn events, and "no surface, no queue"
- Story 7.5: Mod chat surface (parity with Epics 2–3)
- Story 7.6: Remove the companion and pane placement

## Requirements & Constraints

- The mod must never block or error the Claude Code session: no hook awaits the network; all backend I/O runs in background tasks with bounded timeouts; any failure becomes a calm offline state.
- No chat content is persisted anywhere, client or server. Logs are structured JSON with no message content, no account keys, and no handles.
- The account key is sent only in `hello`; later requests carry only the opaque handle. It never appears in any peer-visible message or in UI/logs.
- All traffic is TLS/HTTPS. Client-facing errors are an `error` body — never a 5xx or dropped request — except `please_update` (unsupported version) and HTTP 410 on a superseded handle.
- Peer text renders only through `Text` with control characters stripped (never `Markdown`/`Link`/`Code`); the backend owns link-inerting and length caps on ingest.
- Relay round-trip target < ~500ms p90. Long-poll hold (~20s) must sit under the hosting proxy's idle limit (Fly: 60s) — needs one real check.
- Copy follows `voice.md`: warm, light; endings read as "their Claude came back" with no cause, apology, or confirm.

## Technical Decisions

- **Transport:** `POST /v1/hello` → `welcome{handle}` or `please_update` (no handle). `GET /v1/events?after=<seq>` is held up to ~20s and returns an ordered `{seq, ...msg}` list (empty on timeout). `POST /v1/send` carries one client→server `/proto` message. The handle travels in a request header.
- **Ordered delivery:** each handle has exactly one bounded event queue with monotonic `seq`; every server→client message flows through it in hub order. `after=N` acknowledges (drops ≤ N); delivery is at-least-once and the mod dedupes by `seq`. Overflow ends the session with `session_ended`, never a silent drop.
- **Lease / grace:** a handle is connected while an events poll is in flight or ~5s after one ends. A lapsed lease is a disconnect and starts the existing ~10s grace window; a poll on the same handle, or `hello` with the same key + `session_id`, inside grace resumes with nothing sent to the peer.
- **One handle per key:** a second `hello` supersedes the old handle — its queue gets `session_ended`, then its requests get 410. A superseded mod stays quiet until its next `turn.start`.
- **Hub is unchanged:** the single-writer hub goroutine owns queue, pairings, re-enqueue and teardown; transport handlers only send on its channel. The backend owns queue membership; the mod sends only `ready`/`busy`.
- **Protocol:** `/proto` is the single source of message shapes, envelope `{type, v, ...}`. `heartbeat` is removed; server→client gains `welcome` (`handle`). `/proto` emits golden JSON fixtures for every type; the mod's TS types are checked against them in `claude plugin test`. Version negotiated in the `hello` body (current + previous minor accepted).
- **Mod:** busy/ready from main-loop `turn.start`/`turn.complete` (`agentId` undefined). Hello lazily on the first eligible `ready`; handle kept in `$.state` so hot reload resumes. Never send `ready` where mod UI doesn't draw (`claude -p`, VS Code panel, Remote Control, cloud).
- **Config:** backend env only; mod reads plugin `userConfig` only (`backendUrl`, `persist`). Account key at `os.UserConfigDir()/claudingtin/account-key` (mode 0600) via `$.fs`, so companion keys carry over.
- Take mods API facts from the bundled `claude-code.d.ts`, not memory; the API is early access and may churn.

## UX & Interaction Patterns

- Band above the prompt: status, then a searching spinner (no position/ETA/count) while queued; a brief bounded flourish on `matched`.
- Chat Pane: peer pseudonym + blurb, opener once, current-chat history, Input, always-visible leave/block/report (also `/claudingtin leave|block|report`); optimistic outbound keyed by `client_msg_id`; inline fallback on narrow terminals.
- First-run: focused Pane with product explanation, safety warning, block/report explainer, "I am 18 or older"; stored locally only; decline leaves the mod inert.

## Cross-Story Dependencies

- 7.2 (backend transport) is the base for 7.3–7.5; `/ws` stays live until 7.6 so the companion keeps working meanwhile.
- 7.4 depends on 7.3 (no `hello` before the gate clears and a key exists).
- 7.5 and 7.6 ship in one PR (the mod surface replaces the companion in the same change).
- Story 6.3 status must count live leases and held sessions separately (grace-window count note).
