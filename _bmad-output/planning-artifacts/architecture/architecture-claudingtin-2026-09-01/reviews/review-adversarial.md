# Adversarial Review — Architecture Spine (Claude Think-Time Chat Roulette)

**Target:** `ARCHITECTURE-SPINE.md` (draft, 2026-09-01)
**Driving PRD:** `prd-claudingtin-2026-08-31/prd.md` (+ `addendum.md`)
**Method:** Construct pairs of implementation units one level below the spine — two backend
modules, or the companion team vs. the backend team — where **each unit obeys every AD
(AD-1..AD-13) and every Consistency Convention to the letter**, yet the two together build
something that does not interoperate or that breaks a PRD guarantee. Each viable pair is a
hole to close with a new or tightened AD.

---

## Verdict

**The spine is directionally sound but under-constrains the hub↔spoke contract in ways that
will produce silent interoperability failures and PRD-guarantee violations.** The paradigm
(one authoritative hub, dumb spokes, versioned JSON in `/proto`) is the right call and AD-1,
AD-2, AD-8, AD-10, AD-11 are load-bearing and correct. But:

1. The spine draws a hard line between **in-memory authoritative state** (queue, pairings —
   AD-1, AD-8) and **persisted state** (blocks, cooldowns, bans — AD-10) and then **never
   specifies the bridge between them**. Every persisted-policy check the matcher does
   (FR8, FR32, FR34, FR40) crosses that unspecified bridge. This yields at least three
   independent, spec-compliant ways to violate a "permanent"/"immediate" PRD guarantee.
2. The `/proto` type list is presented as exhaustive ("no message shape exists anywhere
   else") but is missing message types that named FRs require: voluntary leave, block/unblock,
   block-list read, session-ID delivery, typing, age attestation, note-delivered-to-arrival,
   profile delete, heartbeat. Teams will overload `chat_msg` / `session_ended` /
   `profile_update` in mutually incompatible ways and still pass AD-3.
3. **FR20 ("silently re-enter the queue") has no owner.** AD-4 governs only the *framing* of
   the exit, not the requeue. Companion-owns and backend-owns are both defensible readings of
   AD-1; they compose into either a double-enqueue (breaks FR36) or a permanent spinner
   (breaks FR20/G1).
4. Lifecycle races around disconnect, reconnect/re-announce, and cooldown-arming are
   unpinned. AD-1's "recover by reconnecting and re-announcing" has no grace-window,
   session-resume, or connection-displacement semantics, so a 200 ms network blip is
   indistinguishable from a departed user and destroys live pairings + burns cooldowns.

None of these are paradigm-level; all are closable with 6–10 tightened or added ADs. They
**should** be closed before build — several (session-ID minting, requeue ownership, the
persisted↔in-memory bridge) are the kind of ambiguity that produces a working demo and a
broken product at 3 a.m. with 4 people in the pool, which is exactly the condition the PRD
says the product lives or dies on (M1, UJ-4).

---

## Divergence pairs (most severe first)

### DP-1 — Persisted policy state has no defined path into the in-memory matcher

**Units.** Backend module **`matcher`** (runs inside the AD-8 single writer; decides pairs,
checks eligibility) vs. backend module **`store`** (owns SQLite: `PROFILE`, `BLOCK`,
`COOLDOWN`, `BAN`, `REPORT` per the ER diagram; AD-10).

**Every AD each obeys.** `matcher` mutates queue/pairing through exactly one goroutine
(AD-8), holds all live state in memory (AD-1), enforces policy server-side (AD-2). `store`
persists exactly the six permitted entity types and nothing else (AD-10), writes no message
content (AD-10). No admin HTTP surface is added (AD-13). Config is env-only (AD-12).

**The clash.** Nothing in the spine says the matcher's in-memory view of blocks / cooldowns /
bans is *seeded from* `store` at startup or *kept in sync* with it on every write. The
"Persisted entities" note even reinforces the split: "Everything else … is in-memory only."
Three concrete failures, each fully AD-compliant:

- **Block not honored across a backend restart (breaks FR32 "permanent until the user
  removes it").** `matcher` keeps an in-memory `blockedBy` map that it populates only from
  *live* block events received during this process's uptime (the natural reading — blocks
  arrive over the wire, AD-2/AD-9 style). A block user A created last week from another
  machine is a row in `BLOCK` but not in this run's map. After any deploy or crash-restart,
  A is re-matched with someone A permanently blocked. UJ-3's "The blocked pair won't be
  matched again" is false after the first restart.
- **Ban not immediate (breaks FR40 "immediately ending that key's ability to queue or
  chat").** See DP-5 — the ban path is `backend ban <key>` (AD-13), which writes a `BAN` row
  in a *separate process*. The running `serve` has an in-memory ban set and, by AD-13, no
  HTTP surface and no other IPC to learn a row appeared. The ban takes effect on next
  restart — hours or days later.
- **Cooldown not honored (breaks FR8 / FR34).** `store` writes `COOLDOWN(A,B)` at
  `session_ended`; `matcher` reads cooldowns from an in-memory set that `store` only updates
  on startup. Between the write and the next restart, `matcher` re-pairs A and B inside the
  24 h window. AD-8 is satisfied (one goroutine mutates the queue); AD-10 is satisfied (row
  written). The invariant that would have caught this does not exist.

**Reverse case — cooldown never expires.** If the startup loader filters `expires_ms > now`
at load time and then never re-checks (a reasonable implementation), a row that expires
during uptime stays active in memory until the *next* restart. FR34 "expires on its own" is
violated in the other direction.

**Fix — new AD.** *"Persisted policy state is authoritative in memory."* Blocks, cooldowns,
and bans are held by the AD-8 single writer as in-memory structures that are (a) fully loaded
from SQLite at startup before the first connection is accepted, (b) written to SQLite
**synchronously inside the same critical section** that mutates the in-memory copy, with no
second write path, and (c) re-evaluated for expiry on a defined tick. There is exactly one
owner of each policy structure and it is the single writer.

---

### DP-2 — FR20 "silently re-enter the queue" has no owner

**Units.** **`companion` team** vs. **`backend` team**, on what happens to the *surviving*
user when a chat ends (`session_ended`) while that user's think-time burst is still active.

**Every AD each obeys.** AD-4 is satisfied by both: the exit is one identical `session_ended`
with no cause field, same copy rendered. AD-1, AD-6, AD-8 all satisfied under either reading.

**Reading A (companion owns requeue).** The companion knows its own burst state (AD-6 —
"the companion alone determines think-time boundaries"). On `session_ended`, if the burst is
still active it sends a fresh `enqueue`. Defensible: FR20 says "**the plugin** SHALL return
the user to the spinner and silently re-enter them."

**Reading B (backend owns requeue).** AD-1: "all queue / pairing … state lives in the
backend and only there. Companions hold no authoritative state." Re-entering a queue *is* a
queue-state mutation, so it must be a backend action through the AD-8 writer. The companion
just shows a spinner and waits for the next `matched`. Defensible from AD-1's text.

**The clash.**
- **Both teams pick their own reading → double-enqueue.** The key is now in the FIFO queue
  twice and can be matched to two different peers. **Breaks FR36 "exactly 1 concurrent chat
  session."** AD-8 serializes the two enqueue messages but nothing in AD-8 says a key may
  appear in the queue at most once.
- **Each team assumes the other owns it → permanent spinner.** Companion (Reading B) waits
  forever; backend (Reading A) does nothing on peer-exit beyond sending `session_ended`.
  User sees "your Claude is back," then nothing, until their burst ends. **Breaks FR20 and
  G1.** This is the pure "concern with no owner" case.

**Fix — tighten AD-4 or add an AD.** State explicitly: on `session_ended`, the **backend**,
inside the AD-8 writer, immediately re-enqueues the surviving party *iff the companion has
not signalled burst-end for that party*; the companion never sends its own re-`enqueue`.
Also add to AD-8: **the queue holds at most one entry per account key**; a second enqueue
for a key already queued or already in a chat is a no-op (not an error — FR10).

---

### DP-3 — Session ID: who mints it, when, and on which message

**Units.** Two `backend` request handlers (one per connected peer) that both construct
outgoing messages after the AD-8 writer has decided a pair — vs. the `companion` that must
surface the session ID (FR24).

**Every AD each obeys.** AD-8 governs *state mutation*, not response-message construction —
each handler independently building its outbound `matched` is not an AD-8 violation. AD-4:
`session_ended` carries no cause field. Convention: session IDs are opaque, unguessable
strings. All satisfied.

**Clash 3a — two IDs for one chat.** The writer hands each handler `{peer, opener}` and each
handler mints its own `uuidv4()` for the session ID it puts in `matched` (or mints it lazily
at persist time — FR24 only requires it "when a persisted chat ends"). The two peers now
hold **different** session IDs for the same conversation. v2 reconnect (FR25, "both parties …
an explicit keep/connect action") is structurally impossible — they can never search the
same ID. M3 (commitment-ramp usage) is unmeasurable. Even routing the mint through the writer
does not fix it unless the writer explicitly keys the ID to the *pairing* and dedupes — two
"mint for my persist" messages racing into the writer still produce two IDs.

**Clash 3b — session ID on `session_ended` leaks the exit cause.** FR24 says the ID is
offered "when a **persisted** chat ends." The literal implementation attaches `session_id`
to `session_ended` only for persisted-chat exits. Now `session_ended` is **not byte-identical
across exit types** — its presence tells the client "this was a clean persisted exit, not a
mid-burst block/disconnect." **Breaks AD-4's stated purpose and FR19** (no event may reveal
you were blocked/left). The companion cannot use "did I get a session_id?" as the
persisted-vs-not signal without also becoming able to distinguish a block from a model-return.

**Fix — tighten the conventions.** The session ID is minted **once per pairing by the AD-8
single writer** and delivered in the `matched` message to **both** peers (identical value).
It never appears on `session_ended`. The companion decides whether to *offer* it purely from
its own local state (see DP-4). Add `session_id` to the pinned `matched` payload in `/proto`.

---

### DP-4 — Companion cannot tell "persisted chat ended" from "mid-burst disconnect"

**Units.** `companion` state machine (burst tracker, per AD-6) vs. `proto`/`backend`
(`session_ended` with no cause, per AD-4).

**Every AD each obeys.** AD-4 to the letter: one `session_ended`, no cause, identical copy.
AD-6: companion owns burst boundaries. AD-9: persist toggle is in the profile, backend
canonical.

**The clash.** FR24 requires the session ID be **offered when a persisted chat ends** and
FR20 requires a **silent requeue with nothing offered** when a chat ends mid-burst. AD-4
denies the companion any server-side signal to tell these apart, so it must decide from local
state: "was I past my burst, in an active chat, with persist on?" That works — *except* at
the boundary. Consider persist ON, model returned 10 minutes ago, chat still going, peer
blocks you. The companion processes the incoming `session_ended` and the transcript's
"assistant turn complete" event near-simultaneously (they are independent inputs on different
goroutines). If `session_ended` is handled while the burst still reads active, the companion
treats it as FR20 (silent requeue, no offer) and **the user loses the session ID for a chat
that had been persisting for ten minutes** — a lost reconnect, a lost M3 event. If handled
after, it offers correctly. Pure race, no tiebreak rule in the spine.

Symmetric hole: a persist-OFF user whose model returns exactly as the peer sends a final
`chat_msg` — see DP-9 (message draining).

**Fix — add an AD.** Once a chat has *ever* outlived its originating burst (entered
persist-past-burst state), its termination **always** offers the session ID, regardless of
the burst tracker's momentary reading when `session_ended` arrives. The companion latches a
`wasPersisted` bit at burst-end-while-chatting and never clears it for that chat.

---

### DP-5 — Admin subcommands (AD-13) cannot reach the running server

**Units.** `backend serve` (long-lived, holds the SQLite file open, caches bans/blocks in
memory per AD-8) vs. `backend ban` / `backend reports` (AD-13 subcommands, "operating on the
same SQLite file").

**Every AD each obeys.** AD-13 to the letter — maintainer actions are subcommands on the
same file; the only HTTP endpoints are the ws upgrade and `GET /status`. AD-12: env-only
config. AD-10: only permitted rows touched.

**The clash.** `backend ban <key>` writes a `BAN` row from a **separate process**. The
running `serve`:
- must cache bans in memory (per-enqueue disk hits are not viable for the single writer),
- has, by AD-13, **no admin HTTP surface** to be poked, and
- has no other IPC channel defined anywhere in the spine (no file-watch on the DB, no
  SIGHUP, no unix socket).

So the ban is invisible to `serve` until it restarts. **Breaks FR40 "immediately ending
that key's ability to queue or chat."** Same for un-ban, and for `backend reports` deleting
an actioned report row while `serve` may still hold it in an in-memory report buffer
(DP-8). Concurrent access to one SQLite file from two processes under `modernc.org/sqlite`
also needs an explicit WAL + busy-timeout decision the spine does not make — a `DELETE`
from the subcommand can hit `SQLITE_BUSY` against `serve`'s writes.

**Fix — tighten AD-13.** Define the propagation mechanism: e.g. `serve` watches the `BAN`
(and `BLOCK` removal) tables via a short poll or a DB-level change hook and reloads affected
keys within N seconds; document the mandatory WAL mode + busy-timeout for multi-process
access. State the bound in the AD ("a ban takes effect within ≤ Ns"). This is *not* an admin
HTTP surface, so AD-13's intent is preserved.

---

### DP-6 — `chat_msg` relay: ordering and sender-echo are unpinned

**Units.** Two `backend` relay implementations, or `backend` relay vs. `companion` renderer.

**Every AD each obeys.** AD-8 covers queue/pairing only — relay is explicitly *not* under
the single writer. Convention: "Message relay is in-memory only (AD-10)." AD-3: `chat_msg`
matches its `/proto` type. Nothing constrains ordering, delivery, or whether the sender sees
their own message.

**Clash 6a — reordering.** `coder/websocket` requires serialized writes per connection.
Team A implements a per-connection outbound **pump** (one goroutine draining a channel) →
per-destination FIFO order preserved. Team B implements a per-connection **write mutex** and
spawns a goroutine per relayed message → goroutines race for the mutex → messages from one
sender can arrive out of order. Both are AD-compliant. The peer's `chat_msg` history (FR16
"the message history for the current chat") is silently scrambled under load. No sequence
number or `sent_ms` ordering key is pinned to let the renderer re-sort.

**Clash 6b — you never see your own messages.** The `/proto` spec does not say whether
`chat_msg` is echoed to its sender. Backend team implements "relay = forward to the *other*
party" (the natural reading of "relay"). Companion team renders own messages only on receipt
from the server, because AD-1 says "companions hold no authoritative state" and the server is
the source of truth. Result: the sender's own text never appears in their pane. The mirror
bug — companion renders optimistically on send *and* backend echoes — produces every
outbound message twice.

**Fix — add to `/proto` / conventions.** `chat_msg` carries a monotonic per-sender `seq`
(int) and a server-stamped `recv_ms`; the companion renders in `(recv_ms, seq)` order.
Specify explicitly that `chat_msg` is **not** echoed to the sender and the companion renders
its own messages optimistically on send, reconciling on nothing (there is no delivery
receipt — FR17). Specify per-destination in-order delivery as a relay invariant.

---

### DP-7 — One account key, many companions (multiple Claude Code sessions) — unowned

**Units.** N independent `companion` processes on one machine (AD-7 spawns one per Claude
Code session; AD-1 forbids them talking to each other) vs. `backend` connection handling.

**Every AD each obeys.** AD-11: every companion reads the *same* UUID from the stable OS
config path → **same account key**. AD-7: one companion per session, "runs independently."
AD-1: no companion-to-companion channel. All satisfied — the spine *guarantees* this
collision and says nothing about resolving it.

**The clash — FR36 "exactly 1 concurrent chat session" per key has three AD-compliant
implementations, each breaking a different guarantee:**
- **Backend allows N connections, N chats.** One human, three panes, three simultaneous
  chats on one key. **FR36 flatly violated.** Two of the three peers are talking to someone
  who physically cannot answer → looks like abandonment; the no-rejection fiction (FR19)
  technically holds but the pool's effective liquidity is a lie (corrodes M1).
- **Backend allows N connections, 1 chat; extra enqueues get an `error`.** The second and
  third companions' users have genuine, separate think-time bursts they can now never fill,
  and they receive an `error` message — **breaks FR10** ("a 'searching' spinner and nothing
  else"). No owner for retrying when the first chat ends.
- **Backend allows 1 connection per key; connection #2 displaces #1.** Opening a second
  Claude Code window silently kills the first companion's in-progress chat every time. Peer
  gets `session_ended` (FR19 OK) but the pool keeps losing live pairings to normal user
  behavior.

**Fix — add an AD.** Pin both: (a) **one live websocket per account key** — a new connection
for a key with an existing live connection is rejected with a defined `error` code, *or*
gracefully supersedes it with the old one told via `session_ended` — pick one and write it
down; (b) **a key may be in at most one queue entry and one active chat at a time**, enforced
in the AD-8 writer. Decide and document whether N Claude Code sessions on one machine is
"one queue presence shared" or "only the first session participates."

---

### DP-8 — The report-evidence buffer has no owner (FR38)

**Units.** `backend` relay (stateless pass-through per AD-10 "in memory only for delivery")
vs. `companion` (renders and therefore holds the visible history, FR16).

**Every AD each obeys.** AD-10: "message content is never written to disk or to logs. The
sole exception is a report row (the last N messages of that one chat)." AD-2: companion never
gates policy. NFR11: server treats message content as untrusted.

**The clash.** FR38: a report captures "the last N ≈ 20 messages of *that* chat." Something
must retain a rolling in-memory buffer per active pairing. AD-10 says relay is "in memory
only *for delivery*" — a relay team can legitimately read that as "deliver and forget, keep
no buffer." Then:
- **Backend has no buffer → the `report` row's `last_messages` comes from the companion.**
  The report payload's evidence now originates from the *client*, which AD-2 says must never
  be safety-load-bearing and NFR11 says must be treated as untrusted. A malicious reporter
  fabricates 20 messages to frame someone; the maintainer bans on client-authored fiction.
- **Backend was *supposed* to buffer but the relay team read AD-10 literally → reports are
  empty**, and moderation (FR37–FR40, a section-8 safety pillar) is toothless.

Also **FR37 "Reporting SHALL also block that user"** — one tap, two effects, and there is no
`block` message type (DP-10). If the companion assumes `report` implies a server-side block
and the backend just files the row, the reporter can be re-matched with the abuser.

**Fix — add an AD.** The **backend** maintains an in-memory ring buffer of the last N
messages **per active pairing**, discarded on `session_ended` unless a `report` fires, in
which case exactly those messages are written to the `REPORT` row and deleted once actioned.
Report evidence is **never** client-supplied. State that `report` atomically also creates a
permanent `BLOCK` row for the reporter against the reported key, in the AD-8 writer.

---

### DP-9 — Reconnect / re-announce has no grace, resume, or displacement semantics

**Units.** `companion` recovery path (AD-1: "a companion's only recovery action is to
reconnect and re-announce") vs. `backend` connection lifecycle.

**Every AD each obeys.** AD-1 verbatim on both sides. AD-5 negotiates the protocol version on
connect. AD-4 governs the exit message.

**The clash.** AD-1 says re-announce is *the* recovery but defines nothing about what the
backend does with it:
- **No grace window.** A 200 ms wifi blip drops the socket. Backend team's reaper tears the
  pairing down immediately, sends `session_ended` to the peer, arms a cooldown (DP-11),
  requeues the survivor. The companion reconnects 300 ms later into a world where its chat is
  gone. In a thin 3 a.m. pool (UJ-4) every transient blip permanently destroys a live
  pairing and burns a 24 h cooldown between two people who were mid-sentence.
- **Resume vs. reset ambiguity.** Companion team assumes re-announce *resumes* the existing
  chat (AD-1 says "recover … by reconnecting and re-announcing" — recover implies the state
  is still there). Backend team treats every announce as a fresh session. The two never
  agree on whether the post-reconnect companion is in the old chat or a new queue.
- **Displacement ambiguity.** During the window before the old socket is reaped, the
  re-announce arrives as a *second* live connection for the key. First-holder-wins vs.
  last-writer-wins is undefined (compounds DP-7).
- **No history on resume anyway.** Even if resume were defined, AD-10 stores no history
  anywhere, so a "resumed" chat pane is blank — arguably worse than a clean exit.

**Fix — tighten AD-1.** Define: a dropped connection holds its pairing open for a grace
window of Ns before `session_ended` fires; a re-announce within the window with the same
account key **resumes** the pairing (backend replays nothing; companion shows a neutral
"reconnected" with no history, or the pairing is ended cleanly if resume is deemed not worth
it — decide); a re-announce for a key with a still-live socket supersedes it. Cooldown is
**not** armed for a pairing that ended inside the grace window without a message exchanged.

---

### DP-10 — The `/proto` type list is declared exhaustive but is missing types named FRs need

**Units.** Any two units that must exchange a concept with no wire type: `companion` and
`backend`.

**Every AD each obeys.** AD-3: "every websocket message is JSON matching a type declared in
`/proto`; … no message shape exists anywhere else." The listed types are exactly:
`chat_msg, session_ended, enqueue, matched, profile_update, report, note_left, error`.

**The clash.** Named FRs require concepts with **no type in the list**, so teams overload the
existing eight — differently — and still pass AD-3:
- **Voluntary leave ("my Claude came back" tap, FR22/UJ-2).** Is it a client→server
  `session_ended`? AD-4 frames `session_ended` as *terminating* a chat, direction
  unspecified. Team A: companion sends `session_ended` upstream to mean "I'm leaving." Team
  B: backend rejects client `session_ended` (it's a server→client terminal event) → the
  companion has **no way to leave a persisted chat** (the entire FR22–FR24 flow is
  unreachable). Or the backend echoes the leaver's `session_ended` back and the companion
  double-handles it.
- **Block / unblock (FR32, FR35, FR37).** No type. Overloaded onto `profile_update` (mixing
  the block list into the profile document — conflicts with AD-9's "profile = pseudonym /
  blurb" and with patch-vs-replace, DP-11) or onto `report`.
- **Block-list read (FR35 "view and remove entries").** No request/response type at all.
- **Session-ID delivery (FR24).** No type — see DP-3.
- **Typing indicator (FR17 "MAY be shown").** No type. Crammed into `chat_msg` with an empty
  body → the peer that doesn't know the convention renders empty message bubbles on every
  keystroke, polluting FR16 history. (FR21 also forbids "user is typing…" language, so FR17
  is half-retracted by the PRD itself — the spine should resolve, not inherit, this.)
- **Age / safety attestation (FR44).** No type, no field — see DP-13.
- **Note delivered to the next arrival as their opener (FR50).** `note_left` is one type; the
  reverse direction ("here is a note, use it as your opener") is undefined. Reusing
  `note_left` server→client → the companion that only implemented it client→server drops it →
  the note-leaver's note evaporates and the arrival sees a normal opener. **FR50 broken.**
- **Profile delete (NFR4).** No type — see DP-11.
- **Heartbeat / liveness (needed for DP-12).** No app-layer ping type.

**Fix.** Either expand the pinned `/proto` list to cover every FR-required concept
(recommended: `leave`, `block`, `unblock`, `blocklist_req`/`blocklist`, `note_offer`,
`ping`/`pong`, and add `session_id` to `matched`), or explicitly delegate a named subset to
"defined in `/proto` during build" so AD-3's exhaustiveness claim stays true.

---

### DP-11 — `profile_update`: patch vs. replace, null handling, and NFR4 delete

**Units.** `companion` profile editor vs. `backend` profile store (AD-9: companion pushes a
local draft on change; backend copy canonical).

**Every AD each obeys.** AD-9 verbatim. AD-10: `PROFILE` is a permitted row.

**The clash.** AD-9 says "pushes it on change" but never says whether `profile_update` is a
**full-document replace** or a **sparse patch**, nor how empty string vs. absent field is
read:
- Companion sends `{pseudonym:"X"}` after the user clears their blurb. Patch-semantics
  backend keeps the old blurb forever. The user's cleared field silently persists.
- Companion sends `{blurb:""}` meaning "cleared"; replace-semantics backend that treats `""`
  as "not provided" keeps the old value.
- **NFR4 "delete their profile and block list."** There is no delete verb. Companion sends
  `profile_update{}` expecting a wipe; patch-semantics backend no-ops. **NFR4 violated** —
  the user cannot actually delete their server-side state. NFR4's "except active bans" carve
  also has nowhere to live.
- The **persist toggle** (FR22/FR23) and the **v2 preference hint** (FR29) are profile
  fields per the capability map, so they ride `profile_update` too — a replace that omits
  `persist` silently flips a default-on toggle off.

**Fix — tighten AD-9.** Declare `profile_update` a **full-document replace** with an explicit
schema (all fields always present; `null`/`""` means cleared); add a distinct `profile_delete`
message (or a `delete:true` sentinel) for NFR4 with defined semantics ("removes all
server-side state keyed to this account key except active `BAN` rows"). State that `enqueue`
MUST NOT carry profile fields — the profile has exactly one channel.

---

### DP-12 — Backend pairs a user who has already disconnected

**Units.** `backend` connection handler (detects socket close, tells the writer) vs.
`backend` matcher (inside the AD-8 writer).

**Every AD each obeys.** AD-8: the "remove A from queue" signal and the "match A↔C" decision
are both messages on the single writer's channel — serialized, compliant. AD-1: all state in
memory. AD-4: disconnect → `session_ended`.

**The clash.** A enqueues, then A's companion is killed (user Ctrl-C'd the whole Claude Code
session — AD-7: the companion dies with it). TCP close can lag seconds; the OS/websocket lib
surfaces the read error late; the handler's "remove A" message reaches the writer *after* the
writer has already popped A and C and marked them paired. `matched` is sent to C (write to
A's dead socket buffers or errors silently). Now:
- C is "in a chat" with a corpse. C sees a match, an opener, types "hi" — nothing.
- No AD says a failed relay-write, or a read-close on one leg of an *active pairing*, must
  end the pairing and emit `session_ended` to the other leg. If the matcher already moved
  on, C's chat is never torn down. C's FR20 spinner-return never triggers because from the
  backend's view C is happily chatting. C burns their whole think-time burst on a ghost — in
  UJ-4 conditions this is the product failing.

**Fix — add to AD-8 / AD-4.** Any relay-write failure or read-close on **either leg of an
active pairing** MUST, via the single writer, immediately end that pairing and emit
`session_ended` to the surviving leg (then requeue per DP-2). The writer verifies both
sockets are live at the instant of matching (last successful ping within Ns — requires the
`ping`/`pong` type from DP-10); a queue entry whose socket is already closed is dropped, not
matched.

---

### DP-13 — Age gate: AD-2 (backend enforces all policy) vs. UJ-5/FR44 (client-side, nothing sent)

**Units.** `companion` first-run flow vs. `backend` policy enforcement.

**Every AD each obeys.** AD-2: "identity acceptance … decisions are made only by the backend.
The companion renders outcomes; it never gates on policy locally." AD-10: persisted data is
limited to the six listed entity types.

**The clash.** FR44: the user must confirm 18+ "before being allowed to queue." FR46:
declining leaves the plugin "inert (no queue, no chat)." UJ-5: "Nothing is sent to the
server until she finishes this screen." FR43: the safety screen shows "before any network
call."
- If the companion enforces the gate locally (won't send `enqueue` until 18+ is tapped —
  the literal reading of FR44/FR46/UJ-5), that is **the companion gating on policy locally**,
  which AD-2 forbids. And a modified open-source client (FR41 `[DESIGN NOTE]` explicitly
  contemplates these) simply skips the screen — the one attestation section 8 leans on for
  legal posture has **zero server-side backstop**.
- If the backend enforces it (per AD-2), the attestation must be sent — but there is no wire
  field for it (DP-10) and **no `ATTESTATION` row in AD-10's permitted-persistence list**. A
  backend team that enforces server-side has nowhere compliant to store "this key attested
  18+," so it re-prompts every session, or stores it in `PROFILE` (stretching AD-10), or
  trusts a per-connexion flag that a modified client omits.
- If the two teams split (companion gates locally and sends nothing; backend expects an
  `attested_18` on `enqueue` and rejects without it) → **the product is inert for every
  user**.

**Fix.** Decide explicitly. Recommended: the age/safety acknowledgement is a **client-side
onboarding gate** (a stated, narrow exception to AD-2, consistent with FR41's "modified
clients can evade" stance) AND the backend records an attestation flag — add `attested_18`
to the `PROFILE` row in AD-10 and require it `true` on `enqueue` as a cheap server backstop.
Write the AD-2 exception down so it is not a silent violation.

---

## Lower-severity pairs (brief)

### DP-14 — Opener delivery: ID vs. text, and content-table versioning
`matched` can carry an opener **index** (`opener_id:7`, wire stays small, text lives in a
repo file both sides bundle) or the **rendered text**. Companion team expects one, backend
sends the other → blank opener, **breaks FR11**. If it's an index, the opener list now
exists in two places and a contributor's opener #200 (FR31) deployed to the backend but not
to the plugin-bundled (one release behind — AD-5 anticipates this) companion → index out of
range → crash or blank. AD-5 negotiates *protocol* version, not *content-table* version.
**Fix:** `matched` carries fully rendered opener **text**; the opener table is backend-only.

### DP-15 — Empty-queue note lifecycle
FR50 says the note-leaver may "keep waiting," implying they stay in the live FIFO queue. But
FR7 then matches the next arrival to that still-waiting person as a *live* chat — the note is
never consumed. Team X: leaving a note removes you from the live queue (you go async) → the
arrival "chats" with someone whose companion has long exited (AD-7) → instant `session_ended`,
burned slot + cooldown on a ghost. Team Y: note-leaver stays queued → note is dead-letter.
Also: note **consumption** (by an arrival) and note **expiry** (a timer) are two mutation
paths, and a note is arguably neither "queue" nor "pairing" state, so **it is outside AD-8** —
its own unspecified concurrency story (double-free, note handed to two arrivals, oldest vs.
newest when several notes exist). **Fix:** define the note as single-writer state with an
explicit lifecycle: leaving a note removes you from the live queue; a note is consumed
atomically by the first arrival when the live queue is empty; FIFO across multiple notes;
expiry runs through the writer.

### DP-16 — Cooldown armed for a phantom pairing
If cooldown is armed by a 60 s timer set **at match** (one reasonable implementation) rather
than written at `session_ended` after a duration check, then DP-12's phantom pairing (A dead,
matched to C, relay fails) still **writes `COOLDOWN(A,C)` 60 s later** — barring C from
matching A for 24 h over a chat that never happened. In a thin pool that is a real
eligibility loss. **Fix:** cooldown is written only for pairings that actually established
(both `matched` delivered to live sockets AND a minimum real duration / first message
exchanged).

### DP-17 — FR9 "skip to the next eligible pair" algorithm is unpinned
FR9 is already a mild algorithm on top of "FIFO only" (FR7). Which pointer advances when the
head pair is ineligible (block/cooldown) is undefined. Matcher A advances the newcomer past a
parked longest-waiter; Matcher B advances past the longest-waiter. Under sustained partial
ineligibility (many cooldowns in a thin pool — exactly UJ-4) one design **starves the longest
waiter indefinitely** (always someone at the head they're cooled-down with), the other
starves the newcomer. Both are "FIFO with skip"; both violate FR7's "the longest waiter is
matched to the next arrival," differently. **Fix:** pin the exact skip rule and an
anti-starvation guarantee (e.g. longest-waiter pointer never regresses; a waiter skipped K
times gets priority).

### DP-18 — Rate limit counts involuntary FR20 requeues
FR36: ≤ 1 new match per ~10 s per key. FR20: silent requeue on every peer-drop while the
burst is active. A griefer script that matches, drops after 2 s, repeat, forces the victim to
requeue repeatedly — and each involuntary requeue **consumes the victim's own match-rate
budget**, freezing them on the spinner for 8+ s at a time for reasons that have nothing to do
with pool depth. Both modules obey their ADs (AD-2 enforces the limit; AD-8 serializes).
**Fix:** the FR20 involuntary-requeue path bypasses the match-rate limiter (or the limiter
only counts matches that produced a first message exchange).

### DP-19 — Version-reject form is ambiguous
The conventions say client-facing errors are an `error` message, "never a transport close,
**except the AD-5 version reject**." AD-5 says "refused with a single 'please update'
message." Is the reject an `error` message, a transport close, or a message *then* a close?
Backend sends `error{code:"version"}` and keeps the socket open; companion (expecting a close
as the signal) retries `enqueue` forever. Or backend closes abruptly; companion (expecting a
message) shows the generic NFR8 "no one around right now" calm state and **the user never
learns they must update**. **Fix:** pin exactly one form — `error` with a reserved `code`,
followed by a server-initiated close.

### DP-20 — In-flight `chat_msg` at chat-end: drain or drop
Persist-OFF user's model returns; the companion tears down the chat pane while the peer's
last `chat_msg` is in flight. Backend team flushes pending relay on `session_ended` → a
message renders *after* "your Claude is back." Companion team drops anything received after
local burst-end → the peer's heartfelt last line silently vanishes; the peer, meanwhile, has
no idea their message wasn't delivered (no receipts — FR17). Neither is wrong per the ADs.
**Fix:** define chat-end draining — the backend delivers everything already accepted before
it processed the terminating event, and nothing after; the companion renders late-but-valid
messages *above* the "Claude is back" line.

---

## Consolidated recommendation — ADs to add or tighten

| # | New / tighten | One-line invariant |
|---|---|---|
| AD-14 (new) | — | Persisted policy state (blocks, cooldowns, bans) is authoritative **in memory**, owned by the AD-8 writer: fully loaded at startup before accepting connections; written to SQLite synchronously in the same critical section as the in-memory mutation; single write path; expiry re-evaluated on a defined tick. (DP-1) |
| AD-4 / AD-8 (tighten) | tighten | On `session_ended`, the **backend** re-enqueues the surviving party through the writer iff its burst has not ended; the companion never self-re-`enqueue`s. The queue holds **at most one entry per account key**; a redundant enqueue is a silent no-op. (DP-2) |
| Conventions / `/proto` (tighten) | tighten | The **session ID is minted once per pairing by the AD-8 writer** and delivered, identical, in `matched` to both peers. It never appears on `session_ended`. (DP-3) |
| AD-15 (new) | — | A chat that ever outlived its originating burst always offers the session ID on termination, regardless of the burst tracker's state when `session_ended` arrives (companion latches `wasPersisted`). (DP-4) |
| AD-13 (tighten) | tighten | Define how out-of-process admin mutations reach the live `serve` process (table watch / poll / signal — not HTTP) with a stated latency bound; mandate WAL + busy-timeout for multi-process SQLite access. (DP-5) |
| `/proto` (tighten) | tighten | `chat_msg` carries a per-sender monotonic `seq` and a server `recv_ms`; per-destination in-order delivery is a relay invariant; `chat_msg` is **not** echoed to its sender; the companion renders own messages optimistically. (DP-6) |
| AD-16 (new) | — | Exactly one live websocket per account key (define reject-vs-supersede); a key is in at most one queue entry and one active chat, enforced in the writer; define N-Claude-Code-sessions-on-one-machine behavior. (DP-7) |
| AD-10 (tighten) | tighten | The backend keeps an in-memory last-N ring buffer per active pairing; report evidence is backend-captured, never client-supplied; `report` atomically also writes a `BLOCK` row. (DP-8) |
| AD-1 (tighten) | tighten | Define the reconnect grace window, resume-vs-reset on re-announce, and connection displacement; no cooldown for a pairing ended within the grace window with no message exchanged. (DP-9) |
| AD-3 (tighten) | tighten | Expand the pinned type list to cover every FR-required concept (`leave`, `block`/`unblock`, `blocklist_req`/`blocklist`, `note_offer`, `ping`/`pong`, `session_id` on `matched`, `profile_delete`) — or explicitly mark that subset "defined in `/proto` at build." (DP-10, DP-11) |
| AD-9 (tighten) | tighten | `profile_update` is a full-document replace with an explicit schema; `null`/`""` = cleared; add `profile_delete` for NFR4 ("all server-side state except active bans"); `enqueue` carries no profile fields. (DP-11) |
| AD-8 / AD-4 (tighten) | tighten | Any relay-write failure or read-close on either leg of an active pairing ends the pairing via the writer and emits `session_ended` to the survivor; the writer matches only sockets with a recent successful pong. (DP-12) |
| AD-2 (tighten) | tighten | State the age/safety-gate exception explicitly (client-side onboarding gate); add `attested_18` to `PROFILE` (AD-10) and require it on `enqueue` as a server backstop. (DP-13) |
| AD-5 / conventions (tighten) | tighten | `matched` carries rendered opener **text**, not an index; opener table is backend-only; pin the version-reject wire form (`error` + reserved code, then server close). (DP-14, DP-19) |
| AD-8 (tighten) | tighten | The empty-queue note is single-writer state with an explicit lifecycle (leaving a note leaves the live queue; atomic single-consumer; FIFO across notes; timer expiry via the writer). (DP-15) |
| FR9 impl AD (new) | — | Pin the skip-ineligible algorithm and an anti-starvation guarantee. (DP-17) |
| AD-2 (tighten) | tighten | The FR20 involuntary-requeue path bypasses the match-rate limiter. (DP-18) |
| `/proto` (tighten) | tighten | Define chat-end message draining (deliver everything accepted before the terminating event, nothing after; render late-valid messages above the "Claude is back" line). (DP-20) |

---

## What the spine gets right (so it is not lost in the fixes)

- **AD-1 + AD-8** (single authoritative hub, single-writer queue) is the correct backbone and
  makes most of the above *fixable in one place* rather than distributed.
- **AD-4** (one causeless `session_ended`) correctly identifies the no-rejection convention as
  load-bearing and enforces it at the wire — the DP-3/DP-4 issues are about *adjacent* data
  leaking cause, not about AD-4 itself being wrong.
- **AD-10** (no content at rest, one narrow report exception) is a genuine safety asset and
  the enumerated persistence whitelist is the right shape — it just needs the `attested_18`
  and in-memory-buffer clarifications.
- **AD-11** (present-not-verify UUID) correctly accepts the platform limit instead of
  pretending around it.
- **AD-13** (no admin HTTP in v1) is a defensible attack-surface reduction; it only needs a
  non-HTTP propagation path specified.
- The **Open Question** about transcript format instability is correctly flagged as the top
  build risk.
