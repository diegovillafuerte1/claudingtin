# Epic 3 Context: Leaving without rejection

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

Every way a chat can end must feel the same: calm, cheerful, and never like rejection. Whatever the real cause (peer leave, peer model-return, disconnect past grace, block/report, ban, handle takeover), the remaining user sees one identical "their Claude came back" state, with no cause leaked and no alarm language. Stories 3.1–3.5 are done. They delivered the cause-agnostic `session_ended`, the end presentation, one-tap leave, the reconnect grace window and silent re-enqueue, and their backend semantics carry forward unchanged. The client was rebuilt as a Claude Code mod in Epic 7 (TypeScript in `plugin/hooks/`, HTTPS long-poll `/v1` to the Go backend). The Go companion, `/ws` and tmux pane placement are gone, so any planning text about companions, transcripts or panes is history. The two stories left are client-side mod work. 3.6 adds the persist toggle (default-on), which keeps a good chat alive past the think-time burst. 3.7 offers the opaque session ID as a keepsake when a persisted chat ends: the one thread that could let two people find each other again later.

## Stories

- Story 3.1: Unified `session_ended` on every backend-side end (done)
- Story 3.2: "Their Claude came back" presentation and no alarm language (done)
- Story 3.3: Explicit leave control (done)
- Story 3.4: Reconnect grace window (done)
- Story 3.5: Silent re-enqueue while busy (done)
- Story 3.6: Persist toggle, default-on
- Story 3.7: Session-ID keepsake on persisted-chat end

## Requirements & Constraints

- **Persist** is a per-user toggle. When it is on, a chat continues past the think-time burst until the user explicitly leaves. When it is off, the model returning ends the chat automatically: the mod shows the end state and sends `leave` (the friction-free exit).
- Persist **ships default-on**. This is an accepted tradeoff: the default user ends every chat with an explicit "my Claude came back" tap. No idle auto-close; that alternative was rejected.
- With persist on, a model return shows only a quiet note that the model is back. The chat stays open.
- A toggle change takes effect on the **next** chat, never the current one. A fresh install reads persist as on.
- When a persisted chat ends, offer that chat's session ID to copy, with a one-line note that it is the only way to find this person again. A chat that was not persisted shows no copy-ID affordance.
- The offer is non-blocking: dismissing it blocks nothing.
- Session IDs are opaque and unguessable, with no lookup capability in v1. Any reconnect built on them is v2 and must require mutual opt-in. v1 only avoids ruling it out.
- No chat content is ever persisted, on client or server. The session ID is not content and may be shown.
- No alarm or rejection vocabulary in user-facing copy: "left", "disconnected", "connection lost", "rejected", "blocked", "reported", "are you sure?". No apology, no confirm modal. Copy follows the `voice.md` tone: warm, light, never corporate. The UI noun is "pane".

## Technical Decisions

- **Config:** the client reads plugin `userConfig` only. `persist` is a boolean defaulting to `true`, next to `backendUrl`. There are no config files of our own. The user toggles it via `/config` or a `/claudingtin persist` slash command. The manifest test asserting that `userConfig` defaults match the code's defaults must keep passing.
- **Think-time:** the burst runs from a main-loop `turn.start` (`e.agentId` undefined) to its `turn.complete`, sent as `ready` / `busy`. The model "returning" means `turn.complete`. The mod alone decides busy vs back, and the backend never infers it.
- **No backend or wire changes** are expected for 3.6–3.7. The backend mints exactly one `session_id` per pairing and returns it only in `matched`. Every termination emits a byte-identical `session_ended` with no cause and no `session_id`. The client must decide whether to show the keepsake purely from **local persist state** (was this chat persisted past the burst), never from `session_ended` contents.
- **Leaving:** leave sends exactly one `leave` with no confirmation, and the end state shows locally at once. Late peer lines are dropped. Leaving a persisted chat is identical to leaving mid-burst. The backend owns re-enqueue. With persist off on `turn.complete`, the user is no longer busy, so there is no re-enqueue.
- **Clipboard:** the mods API exposes a clipboard write (`isCopied`). When no clipboard is reachable, show the ID as plain text instead.
- **Mod rules:** hooks never await the network or throw. All backend I/O runs in background tasks, and any failure falls back to a calm offline state. State that must survive hot reload lives in `$.state`. Peer text renders only through `Text` with control characters stripped. Logs carry no content, keys or handles.
- **Future coupling:** `forget_me` (Epic 5) will reset `persist` to its `userConfig` default.

## UX & Interaction Patterns

- **Persist off:** the model returns, the chat ends on its own with the standard end state, and the user reads Claude's output with no decision to make.
- **Persist on (default):** the user is matched during think-time. When the model returns, the chat stays open with a quiet "model is back" note. The user keeps talking, then taps "my Claude came back" (or `/claudingtin leave`). The end state then offers the session ID with Copy, plus the one-line note.
- If the burst is still active when a chat ends, the band returns to the searching spinner. Otherwise the chat pane closes or settles into the end state.
- Persist followed by copy-session-ID forms the first two rungs of the voluntary commitment ramp. Each step is opt-in, and none is forced.

## Cross-Story Dependencies

- 3.6 and 3.7 build on Epic 7's mod: the `/v1` transport, the account key and first-run gate, turn-event think-time with "no surface, no queue", and the chat pane with its end presentation and leave/block/report.
- 3.7 depends on 3.6. The keepsake offer is gated on the local "persisted past the burst" state that 3.6 introduces, and it uses the `session_id` captured from `matched`.
- Build order after this epic: Epic 4, then Epic 5, then Epic 6. Epic 5's `forget_me` touches the persist preference.
