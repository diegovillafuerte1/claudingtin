# Epic 3 Context: Leaving without rejection

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

This epic makes every way a chat can end feel the same: calm, cheerful, and never like rejection. Whatever the real cause — the peer's model returned, they tapped leave, they blocked or reported, their connection dropped past the grace window, they were banned, or their connection was taken over — the remaining user sees one identical "their Claude came back" state with no cause leaked and no alarm language anywhere. The epic also delivers the mechanics that support this fiction: a single byte-identical termination frame from the backend, a one-tap leave control with no confirmation, a short reconnect grace window so a brief network blip does not kill a conversation or burn a cooldown, silent re-enqueue of a user who is still in think-time when a chat ends, the persist toggle (shipped default-on) that keeps a good conversation alive past the think-time burst, and the offer to copy an opaque session ID when a persisted chat ends — the one thread that could let two people find each other again later. This is the load-bearing social contract of the product: leaving must never sting, for either party.

## Stories

- Story 3.1: Unified session_ended on every backend-side end
- Story 3.2: "Their Claude came back" presentation and no alarm language
- Story 3.3: Explicit leave control
- Story 3.4: Reconnect grace window
- Story 3.5: Silent re-enqueue while busy
- Story 3.6: Persist toggle, default-on
- Story 3.7: Session-ID keepsake on persisted-chat end

## Requirements & Constraints

- Every chat-ending event is presented to the remaining user with identical, neutral framing: the other person's model came back. No event ever tells a user they were left, rejected, blocked, reported, banned, or disconnected.
- No alarm or rejection vocabulary anywhere in user-facing copy — no "left", "disconnected", "connection lost", "rejected", "blocked", "reported", "user is typing…", "are you sure?". Exit copy reads as an easy, guilt-free "catch you later" with no apology and no confirm modal.
- The companion always shows a connection-state indicator (searching / connected / your Claude is back / disconnected) using only the no-rejection language.
- On any disconnect or chat end while the user is still in a think-time burst, the user is silently returned to the spinner and re-entered in the queue with no visible "you were requeued" message. If the model has already returned, the user is not re-enqueued.
- Persist is a per-user profile toggle. When on, a chat continues past the think-time burst until the user explicitly leaves; when off, the model returning ends the chat automatically (the friction-free exit). Persist ships default-on, so the default user makes one explicit "my Claude came back" tap to leave.
- When a persisted chat ends, the user is offered that chat's session ID to copy, with a one-line note that it is the only way to find this person again. A non-persisted chat shows no copy-ID affordance.
- Session IDs are opaque, unguessable, and carry no lookup capability in v1. Any reconnect built on a session ID is a v2 concern and out of scope here; v1 only avoids precluding it.
- All copy must match the companion voice guide (`voice.md`) tone rules: warm, light, unpolished-in-a-human-way, never corporate or clinical.

## Technical Decisions

- One session ID per chat: the backend mints exactly one opaque `session_id` at pairing and returns it only in `matched`. Every termination cause emits exactly one `session_ended` frame with no cause field and no `session_id`, byte-identical across all causes (peer leave, peer model-return/`busy`, peer disconnect past grace, ban, connection takeover). This type already exists in the v1 protocol set; the epic adds no new wire type.
- On `leave` or burst-end the backend stops relaying immediately: messages already received are delivered, later ones dropped, and `session_ended` is the last frame the peer receives on that session — sent after the relay is closed.
- The companion decides whether to show the "copy session ID" offer purely from its local persist state, never from `session_ended` contents.
- Explicit leave sends exactly one `leave`, no confirmation prompt; the companion shows the local end state immediately without waiting for the server. Leaving a persisted chat is identical to leaving mid-burst. Late peer `chat_msg` after leaving is not shown.
- The backend owns queue membership and the requeue. The companion only sends `ready` / `busy`; the backend silently re-enqueues a still-busy user (last signal `ready`) onto the queue tail within one tick after any session end, with no client-visible event. One active connection per account key: a second `hello` for a connected key takes over and the prior connection gets `session_ended` + close.
- Reconnect grace window (~10s): on spoke disconnect the backend holds the pairing; a reconnect presenting the same account key + `session_id` resumes it with the relay continuing and no `session_ended` sent during the window. On expiry the peer gets `session_ended` and both sides are re-enqueued if still busy.
- Grace resumes and involuntary/silent re-enqueues do not consume match-rate budget and are exempt from rate limiting.
- Companion persist behavior: on model return with persist on, the chat stays open with only a quiet "model is back" note; with persist off, the companion shows the end state and sends `leave`. A toggle change takes effect on the next chat; a fresh install reads persist as on.
- Convention constraints: wire messages are JSON envelopes defined only in `/proto`; `session_id` is an opaque unguessable backend-minted string; client-facing problems are an `error` message, never a transport close (except `please_update`); structured stdout logs carry no message content and no account keys.

## UX & Interaction Patterns

- Happy path (persist off): the model returns, the chat pane closes on its own, the user reads Claude's output and keeps working — no decision required.
- Persist path (default): the user gets matched during think-time; when the model returns the chat stays open with a quiet note, the user keeps talking while reading Claude's output, then later taps "my Claude came back" to leave. Same no-rejection framing as an automatic exit. Before the chat closes the user is offered the session ID to copy.
- When the end state is shown: if the user is still in a think-time burst the pane returns to the spinner; if the model is back the pane closes.
- The session-ID offer is non-blocking — dismissing it blocks nothing.
- The persist → copy-session-ID sequence is the first two rungs of the voluntary commitment ramp (later rungs are v2). Each step is opt-in; none is forced.

## Cross-Story Dependencies

- Depends on Epic 1 (companion launch, transcript watch producing `ready`/`busy`, websocket `hello`, account key) and Epic 2 (FIFO queue and single-writer pairing loop, `matched` carrying `session_id`, in-memory relay, chat surface, searching/connected state display, spinner).
- Within the epic: Story 3.1 (unified `session_ended`) is the backend foundation for 3.2, 3.3, 3.4, and 3.5. Story 3.6 (persist state) gates Story 3.7 (the offer is driven by local persist state). Story 3.4 (grace window) and Story 3.5 (silent re-enqueue) both interact with the single-writer pairing loop and share the rate-limit-exemption rule.
- Story 3.4's "no cooldown for a pairing torn down inside the grace window with no exchange" is consumed by Epic 4's cooldown-write rule.
- Story 3.1 must account for the ban termination cause, whose trigger is implemented in Epic 4; the `session_ended` path must already be cause-agnostic so Epic 4 needs no new end frame.
