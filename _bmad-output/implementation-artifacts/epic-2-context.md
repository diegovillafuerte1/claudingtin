# Epic 2 Context: Get matched and chat

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

This epic builds the core loop that turns think-time into a conversation: a strict FIFO queue and a single-writer pairing loop in the backend, a `matched` message carrying a curated rotating pre-written opener shown to both users at the same instant, the text-only chat surface in the companion (peer pseudonym and blurb, current-chat history, input box, and always-visible block/report/leave controls), an in-memory-only message relay with optimistic local echo, connection-state display in no-rejection language, and a small searching→matched "spin" flourish. After this epic a user who submits a prompt in Claude Code is queued, paired with another waiting user within their burst, and can hold a real-time text conversation that leaves no trace. The unified exit convention, silent re-enqueue, reconnect grace, and persist land in Epic 3; block/cooldown/ban eligibility, link inerting, hard caps, keyword filter, and rate limits land in Epic 4.

## Stories

- Story 2.1: FIFO queue and single-writer pairing loop
- Story 2.2: Curated opener set and per-match selection
- Story 2.3: Chat surface — render and input
- Story 2.4: In-memory message relay
- Story 2.5: Searching and matched transition, with the spin

## Requirements & Constraints

- **Strict FIFO, no algorithm.** The longest waiter is paired with the first eligible successor, scanning from the queue head; if none is found within a bounded scan the head waits. No scoring, no preference weighting. In this epic the only ineligibility predicate is self-match; the scan must be structured so block/cooldown/ban predicates slot in later without reshaping the loop.
- **An ineligible queue head never stalls the queue** — the loop skips to the next eligible pair.
- **The queued user sees only a "searching" spinner** — no queue position, no ETA, no "N people online" count, nothing else.
- **Leaving the queue while unmatched is silent.** A `busy` signal, disconnect, or burst-end before a match removes the user from the queue with no error and no client-visible event.
- **One opener per match.** The `matched` payload carries a non-empty `opener` string chosen by the backend from a curated set; both peers get the byte-identical opener at the same time. Openers are not generated per match. Selection rotates — the same opener is never used back-to-back (not random-with-repeats).
- **The opener set is a plain repo file** a contributor can append to, guarded by a length-bound format check. Its companion is the `voice.md` opener guide: playful, disarming, low-stakes human curiosity, never survey- or interview-like.
- **Text only.** No image, file, audio, or video affordance exists anywhere in the surface. Input past the length cap is prevented client-side (a UX affordance; the authoritative cap is a backend concern in Epic 4).
- **Peer text renders as inert literal.** No ANSI/control-byte interpretation, no HTML, no markdown, no link activation.
- **No message content touches disk or logs**, client or server — verified by test. The relay holds messages in memory only for delivery.
- **`chat_msg` is never echoed to its sender.** The companion shows its own outbound message immediately, optimistically keyed by `client_msg_id`.
- **After a session ends** (`leave` or burst-end) the backend stops relaying at once: messages already received are delivered, later ones from the peer are dropped.
- **Relay round-trip is under ~500ms p90** at normal load.
- **Typing indicators MAY be shown; read receipts never are.**
- **All copy follows `voice.md`** — warm, light, human; never names the machinery ("waiting," not "in the queue"); connection state uses the no-rejection, no-alarm language.
- Success target this epic serves: median time-to-match under ~15s, so the spinner reliably resolves inside a think-time burst. (The instrumentation behind that number is Epic 6.)

## Technical Decisions

- **Single-writer state.** Exactly one channel-driven goroutine mutates the queue, pairings, and in-memory policy; all connection handlers interact with it only by sending on that channel. It owns pairing teardown and mints the `session_id`. The opener rotation cursor lives in this goroutine's in-memory state; losing it on restart is acceptable.
- **The backend owns queue membership.** The companion sends only `ready` / `busy`; the backend translates those into enqueue/dequeue. One active connection per account key is already enforced from Epic 1.
- **`matched` message** (declared in `/proto`): carries `session_id`, the peer's `pseudonym` / `blurb`, and the `opener` text. `queued` is sent to a waiting client until it is matched.
- **`session_id`** is an opaque, unguessable string minted by the backend, exactly one per chat, returned in `matched`.
- **Profile fields in `matched` come from the backend's canonical copy.** No profile editing exists yet (Epic 5), so peers see a default/blank pseudonym. A `matched` payload a peer receives never contains the account key.
- **Content sanitization has one owner per direction.** In this epic the companion's duty is literal rendering of peer text; backend link-inerting, hard length caps, and non-text/oversized frame rejection arrive in Epic 4.
- **Everything in this epic is in-memory** — FIFO queue, active pairings, relayed messages, any counters — and dies with the process. No SQLite yet (the schema is Epic 4).
- **Wire conventions:** envelope `{ "type": <snake_case>, "v": <int>, ...payload }`; timestamps are Unix epoch milliseconds; a client-facing error is an `error` message, never a transport close. Structured JSON logs to stdout carry no message content and no account keys.
- **Companion UI is Bubble Tea v2** (bubbletea v2.0.x / bubbles v2.2.x / lipgloss v2.0.x). The chat and spinner views replace the Epic 1 minimal status line.

## UX & Interaction Patterns

- **Searching state:** a single calm "searching" spinner, nothing else.
- **The spin:** the searching→matched transition plays a brief, bounded, non-blocking flourish in the pane; input readiness is not delayed by it; if `matched` arrives while it is still playing, no message or state is lost; it disappears immediately if the queue resolves; it is never a minigame and costs no perceptible time.
- **Chat view:** peer pseudonym and optional blurb as a header, a scrollable current-chat history with the opener shown once at the top, an input box, and always-visible block, report, and leave ("my Claude came back") controls. The controls are present in this epic; their backend behaviour is wired in Epics 3–4.
- **Optimistic send:** the user's own message appears the moment they send it, keyed by `client_msg_id`; the server never sends it back.
- Inbound messages render only in the companion's own pane; nothing is pushed into the Claude Code TUI.
- Connection-state wording is no-rejection and no-alarm per `voice.md`; the full exit-convention routing is Epic 3.

## Cross-Story Dependencies

- **Depends on Epic 1:** the `/proto` types (`ready`, `busy`, `chat_msg`, `queued`, `matched`, `session_ended`, `error`), the backend connection registry and single-writer skeleton, the companion websocket client with `hello` / `ready` / `busy`, the account key, the transcript-driven burst signal, and pane placement.
- Story 2.1 produces the `matched` message and `session_id` that Stories 2.2, 2.3, and 2.5 all consume; the opener text from 2.2 is carried in the `matched` payload 2.1 builds.
- Stories 2.3 (render/input) and 2.4 (relay) are the two halves of a working chat — 2.4's optimistic-echo contract pairs with 2.3's input handling.
- Story 2.5 renders the states 2.1 drives (`queued` → `matched`).
- **Downstream:** Epic 3 routes every end through one neutral `session_ended` and adds silent re-enqueue, reconnect grace, and persist; Epic 4 adds the block/cooldown/ban predicates to the 2.1 scan plus backend link-inerting, hard caps, non-text-frame rejection, keyword filter, and rate limits; Epic 5 supplies the real profile behind the `matched` pseudonym/blurb and the empty-queue note that can replace the opener; Epic 6 adds the time-to-match instrumentation the FIFO loop feeds.
