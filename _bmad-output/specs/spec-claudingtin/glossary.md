# Glossary — Claude Think-Time Chat Roulette

Load-bearing terms every downstream consumer of `SPEC.md` shares. Ordered roughly by when a reader meets them.

- **Think-time burst** — The stretch from the user submitting a prompt to the model finishing its response: the dead time the product fills. Approximate; there is no true "thinking started/stopped" signal, so the companion derives the boundaries by tailing the session transcript (CAP-1).

- **Companion** — The long-lived Go TUI the plugin bundles and launches once per Claude Code session. It watches the transcript, holds the websocket to the backend, renders the chat in its own pane, and places that pane (tmux auto-split, or manual elsewhere). It holds no authoritative state.

- **Backend / hub** — The one hosted Go service that holds all queue, pairing, and session state in memory and enforces every matching and moderation policy. Companions are spokes that render and relay only.

- **Account key** — A random UUIDv4 generated once and stored at a stable path outside the plugin directory (an OS config dir), so a plugin reinstall or update keeps it. The single identifier every block, auto-cooldown, ban, and rate limit is keyed to. No Claude-account linkage is possible (platform limit); the backend only ever sees the UUID and trusts it unverified. Resettable by deleting the id file, so enforcement is best-effort. Never shown to other users or sent in any client-visible payload.

- **Pseudonym** — The optional display name other users see. User-set, unconnected to the account key.

- **Blurb** — An optional short free-text profile line, shown to a peer alongside the pseudonym.

- **Persist** — A per-user profile toggle, default-on. When on, a chat continues past the end of the think-time burst until the user explicitly leaves (CAP-5). With persist default-on and no idle auto-close, the default user ends every chat with an explicit "my Claude came back" tap; session length (C1) is the primary guardrail against this drifting into a time sink.

- **The "my Claude came back" convention** — The social fiction that every chat exit — voluntary, blocked, reported, disconnected, or model-returned — is framed identically as the other person's model returning, so leaving never reads as rejection (CAP-4). Every exit path in the UX routes through it.

- **Block** — A permanent, user-initiated bar on being re-matched with a specific person (CAP-8), keyed to the account key, independent of all preference/tag settings. Removable from within the plugin.

- **Auto-cooldown** — A temporary, automatic bar on re-matching two people who were just paired (CAP-8) — forces variety in a thin pool. Written only after a pairing that actually happened (~1 minute together or a real exchange); expires on its own; not listed in the plugin.

- **Session ID** — An opaque, unguessable identifier for a specific chat, minted once by the backend at pairing and delivered in `matched`. Offered to the user to copy only when a persisted chat ends. Inert in v1; becomes searchable on the v2 webapp.

- **Commitment ramp** — The one continuous, always-voluntary opt-in escalation: persist toggle → copy session ID → (v2) post to the missed-connections board → mutual-consent reconnect. No step is ever forced.

- **Empty-queue note** — A short note a waiting user may leave after a threshold wait with no match (CAP-12). The next arrival sees it as their opener and may reply to start a normal chat. Ephemeral, backend-memory only, same content rules as chat.

- **Beachhead** — The single existing community the product launches into for liquidity, rather than "all lonely Claude users."

- **Liquidity** — The probability that a user entering the queue finds a match within a think-time burst; the primary success metric (M1). For a real-time pool, driven by concurrency, not total installs.

- **Pull-in (v2)** — Matching a searcher against an active but non-waiting user whose profile fits opt-in criteria. Also the planned cold-start liquidity patch. Not in v1; the "prefer to be matched with" hint (CAP-6) is stored only to seed it.
