---
title: 'Story 2.4 — In-memory message relay'
type: 'feature'
created: '2026-09-07'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'd243d9e70ac0aef290c58856978df65f1be326d5'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-2-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Story 2.1 pairs two sessions and Story 2.3 draws the chat surface with an optimistic local echo, but nothing carries a typed line from one peer to the other. `chat_msg` frames are decoded and dropped on the backend (`server.go` read loop) and never sent by the companion, so a matched pair cannot actually talk.

**Approach:** Add a `chatMsgCmd` to the single-writer hub that looks up the sender's peer in `pairings` and hands the peer's connection handler the `proto.ChatMsg` to write — never the sender. The companion sends `chat_msg` on Enter (the optimistic echo already happened in 2.3) and renders inbound peer `chat_msg` as a `chatui.PeerMsg` history entry. Everything stays in memory; no content is logged or written to disk; a torn-down pairing relays nothing further.

## Boundaries & Constraints

**Always:**
- Relay routing happens only inside the `hub.Hub.Run` single-writer goroutine (AD-8): a new `chatMsgCmd{s, msg}` joins the command union; the arm reads `p.pairings[s]` and, if a peer exists, `deliver(peer, msg)`. No other code path reads `pairings` to route a message. The hub goroutine keeps doing **zero network I/O and zero logging** — the frame reaches the peer only by the existing non-blocking `deliver` onto `Session.Outbound`, drained by that peer's handler (its sole writer).
- `chat_msg` is **never echoed to its sender** — the hub delivers only to the peer; the companion already showed the sender's own line optimistically keyed by `client_msg_id` (2.3 `appendSelf`). The relayed `proto.ChatMsg` passes through unchanged in `client_msg_id`/`text` (modulo the UTF-8 normalization below); the backend does not parse, trim, length-check, or link-inert it (Epic 4 duty).
- **UTF-8 contract:** `proto.Encode`/`Decode` (via `encoding/json`) replace invalid UTF-8 in `ChatMsg.Text` with U+FFFD in transit — the accepted, tested contract, matching the companion's own `inert()`. Lock it with a proto round-trip test (closes the spec-1-1 deferred item).
- **After a pairing ends** (`busy` / disconnect / takeover — all already tear it down in 2.1), a later `chat_msg` from the survivor finds no `pairings` entry and is **dropped silently**: no delivery, no `error`. Frames received before the end were already delivered.
- **No message content on disk or in logs, either process** (test-verified): the hub logs nothing; the backend handler adds **no** log line for `chat_msg` in either direction; `wsclient` still logs nothing; `run` logs only its existing content-free intent lines. No chat-content disk path exists this epic (no SQLite; `chatui` history is in-memory).
- Companion outbound: `chatui` emits from `appendSelf` (after the optimistic append) via a new `WithSend` callback carrying `client_msg_id` + trimmed text; `run.loop` — never the Bubble Tea goroutine — does the actual `client.SendChat`, so a slow socket cannot block the UI. Whitespace-only Enter stays local-only, matching 2.3.
- Companion inbound: `wsclient` surfaces `proto.ChatMsg` on a new non-terminal `ChatMsgs()` channel (mirrors `Matched()`); `run.loop` forwards it to the live chat program as `chatui.PeerMsg`, or drops it if no chat is active.
- `Session.Outbound` buffer rises 4→32 (it now carries chat traffic); relay `deliver` stays best-effort non-blocking — a full/nil channel means the handler is gone, so dropping is correct, and v1 does not ack or retry `chat_msg`.
- `go build`, `go vet`, `go test -race` over the `go.work` set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, `bash scripts/checks_test.sh` all pass; `check_deps.sh` still reports only `companion → proto` and `backend → proto`.

**Ask First:**
- Any `/proto` change beyond adding test samples (`ChatMsg` is already defined and registered; no struct/registry/envelope change is needed) → HALT.
- Any new third-party dependency → HALT.
- Adding a `typing` indicator / message, or any read-receipt / delivery-ack state → HALT (typing deferred, receipts forbidden).
- If the relay cannot be delivered without blocking the hub goroutine or adding a second writer per connection → HALT.

**Never:**
- No `leave` handling, unified `session_ended` routing, reconnect grace, or silent re-enqueue — Epic 3. A relay with no pairing just drops.
- No block / cooldown / ban / rate-limit / keyword filter, link-inerting, hard length cap, or non-text/oversized-frame rejection — Epic 4.
- No SQLite / persistence; no chat content to disk or logs anywhere; no persisted transcript.
- No delivery guarantee, retry, ordering fix-up, or de-duplication beyond in-order single-hop best-effort.
- No change to the `matched` / `queued` / `session_ended` flows, the FIFO scan, or the eligibility seam.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| Happy path relay | A and B paired; A sends `chat_msg{client_msg_id:"c1", text:"hey"}` | B's socket receives `chat_msg{client_msg_id:"c1", text:"hey"}`; A receives nothing back; A's companion already showed "hey" keyed by `c1` | N/A |
| Multi-byte / emoji text | paired; text is `"héllo 😀 مرحبا"` | peer receives the identical string | N/A |
| Invalid UTF-8 in text | paired; `text` carries a lone `0x80` byte | peer receives the text with `0x80` replaced by U+FFFD (json normalization); no error, no drop | normalized, not rejected |
| Sender never echoed | paired; A sends 5 `chat_msg` | B receives 5; A receives 0 `chat_msg` within a bounded read window | N/A |
| Relay after teardown | A and B were paired; B disconnects (peer A got `session_ended`); A sends `chat_msg` | nothing is delivered anywhere; A gets no `error` frame; no panic | silent drop |
| Unpaired sender | conn is queued or idle (never matched) and sends `chat_msg` | ignored — no state change, no `error` (matches the v1 discard behavior) | silent |
| Unknown/oversized frame | paired conn sends a non-`chat_msg` undecodable frame | ignored as before | silent |
| Companion inbound, chat up | `run` has a live chat program; `proto.ChatMsg` arrives | a `them` history entry appears, `inert`-rendered, keyed by the peer's `client_msg_id`; sticks to bottom only if already there | N/A |
| Companion inbound, no chat | `proto.ChatMsg` arrives with no active chat program | dropped; no panic, no write to `cfg.Out` | silent |
| Companion send failure | user hits Enter; the socket is down | optimistic line already shown; `run` logs one content-free "a chat line could not be sent" via `chat.Println`; nothing crashes | logged content-free |
| Whitespace-only send | user hits Enter on `"   "` | input box clears; no history entry; no wire frame | N/A |
| Round-trip latency | local backend, paired pair, ~50 messages | p90 wall-clock A-send → B-receive is well under ~500ms (single in-process hop, non-blocking sends, no disk/CPU work) | N/A |

</frozen-after-approval>

## Code Map

- `proto/messages.go` — **read-only.** `ChatMsg` (L52-55) already has `ClientMsgID`/`Text` with `snake_case` tags and is in `registry` (L180). No change.
- `proto/messages_test.go` — **edit.** `roundTripSamples` L19 has an ASCII-only `ChatMsg`. Change its `Text` to a multi-byte string (accents + emoji + RTL) so the round-trip and snake-case tag tests exercise real UTF-8. Add `TestChatMsgInvalidUTF8NormalizesToReplacementChar`: `Encode` a `ChatMsg` whose `Text` holds an invalid byte, `Decode`, assert the result has U+FFFD and is valid UTF-8 — locks the wire contract (deferred item, spec-1-1).
- `backend/internal/hub/hub.go` — **edit, core.** Command union L35-51: add `chatMsgCmd struct { s *Session; msg proto.ChatMsg }` + `func (chatMsgCmd) isCommand() {}`. `Run` switch L100-159: add `case chatMsgCmd: p.relay(cmd.s, cmd.msg)`. `pairingState` (L167-172) gains a method `relay(from *Session, msg proto.ChatMsg)` — `if peer, ok := p.pairings[from]; ok { deliver(peer, msg) }`, nothing otherwise. Public API near `Ready`/`Busy` L317-325: add `func (h *Hub) Relay(s *Session, msg proto.ChatMsg) { h.send(chatMsgCmd{s: s, msg: msg}) }` with a doc line ("delivers msg to s's paired peer only, never back to s; a no-op if s is not paired"). Package doc L1-10: add relay to the list the hub goroutine owns.
- `backend/internal/hub/hub_test.go` — **edit.** Add: paired A/B, `Relay(A, ChatMsg{...})` → B's `Outbound` gets the identical `ChatMsg`, A's `Outbound` stays empty; `Relay` on an unpaired session delivers nothing and does not panic; `Relay` after `teardownPair` drops; extend the existing `-race` stress to also fire `Relay` on random sessions.
- `backend/internal/server/server.go` — **edit.** Read goroutine L191-209: change `switch msg.(type)` to `switch m := msg.(type)` and add `case proto.ChatMsg: s.hub.Relay(sess, m)`. Main `select` L218-227: `writeFrame` already handles any registered proto value, so a relayed `proto.ChatMsg` on `sess.Outbound` writes as-is — do **not** add a `case proto.ChatMsg` to the post-write logging switch (no per-message log line). `sess.Outbound` cap L176: `make(chan any, 32)`.
- `backend/internal/server/server_test.go` — **edit.** Add: two dials match, dial A sends `chat_msg` → dial B reads the identical frame, dial A reads nothing back within a bounded window; after dial B closes (A got `session_ended`), a `chat_msg` from A produces no delivery and no `error`; extend the log-scrub lifecycle (`TestLogsCarryNoAccountKeyOrFrameText`) through a relayed message and assert the known message text never appears in captured logs; a coarse p90 latency check over ~50 relayed messages against a loose ceiling.
- `companion/internal/wsclient/wsclient.go` — **edit.** `Client` L82-93: add `chatMsg chan proto.ChatMsg`. `New` L96-103: `chatMsg: make(chan proto.ChatMsg, 64)`. Add `func (c *Client) ChatMsgs() <-chan proto.ChatMsg { return c.chatMsg }` beside `Matched()` L121, with a doc note (buffered, never closed, non-blocking send, drop-on-full acceptable — v1 has no ack). Add `func (c *Client) SendChat(ctx context.Context, m proto.ChatMsg) error { ... writeFrame ... }` beside `SendState` L202 (returns `ErrNotConnected` with no live conn). `serve` read goroutine switch L255-275: add `case proto.ChatMsg:` → non-blocking send onto `c.chatMsg`, not terminal, keep serving. Package doc L1-13: "three inbound frames that matter" → four (add `chat_msg`).
- `companion/internal/wsclient/wsclient_test.go` — **edit.** Add: server writes `proto.ChatMsg` → client surfaces it on `ChatMsgs()` with fields intact and keeps serving; `SendChat` with no live connection returns `ErrNotConnected`; nothing is logged.
- `companion/internal/chatui/chatui.go` — **edit.** Add exported `OutboundMsg struct { ClientMsgID, Text string }` (symmetric with `PeerMsg` L81-84). `Model` L146-159: add `send func(clientMsgID, text string)`. Add `Option` `WithSend(fn func(clientMsgID, text string)) Option`. `appendSelf` L282-296: after the history append, `if m.send != nil { m.send(id, text) }` where `id`/`text` are exactly what was appended; the whitespace-only early return still sends nothing. Package scope note L8-14: relay is now wired (outbound on Enter, inbound via `PeerMsg`); keep the "never echoed back by the server" line.
- `companion/internal/chatui/chatui_test.go` — **edit.** Add: Enter with a `WithSend` callback fires it exactly once with the fresh `client_msg_id` and the trimmed text, and the optimistic self entry is still appended; whitespace-only Enter does not fire it; a `PeerMsg` still appends a `them` entry (extend existing coverage if absent).
- `companion/internal/run/run.go` — **edit.** `stateClient` L125-130: add `ChatMsgs() <-chan proto.ChatMsg` and `SendChat(ctx context.Context, m proto.ChatMsg) error`. `chatProgram` L139-144: add `Send(tea.Msg)` (satisfied by `*tea.Program.Send`). `loop` chat state block L210-215: add `chatSends chan chatui.OutboundMsg`. `launchChat` L226-245: create `chatSends` (buffered 32), build a non-blocking `send` closure, pass it via `chatui.WithSend`. `stopChat` L249-272: nil out `chatSends` alongside `chatIntents`. New `select` arms: `case cm := <-client.ChatMsgs():` → if `chatActive && chat != nil`, `chat.Send(chatui.PeerMsg{ClientMsgID: cm.ClientMsgID, Text: cm.Text})`, else drop; `case om := <-chatSends:` → `if err := client.SendChat(ctx, proto.ChatMsg{ClientMsgID: om.ClientMsgID, Text: om.Text}); err != nil && chat != nil { chat.Println("companion: a chat line could not be sent") }`.
- `companion/internal/run/run_test.go` — **edit.** Extend `fakeClient`: add a `chatMsgs chan proto.ChatMsg` + nil-safe `ChatMsgs()`, and record `SendChat` calls (payloads). Extend the fake `chatProgram` with `Send(tea.Msg)` capturing messages. New tests: after a `matched`, an inbound `proto.ChatMsg` is forwarded to the program as a `chatui.PeerMsg`; a `WithSend` invocation from the surface drives `client.SendChat` with the right `client_msg_id`/`text`; a `SendChat` error yields one content-free `Println`; teardown stops forwarding. Existing pre-match and 2.3 tests stay green.
- `README.md` — **edit.** Backend section: `chat_msg` is relayed in-memory to the paired peer only, never echoed to the sender, never logged, dropped after the pairing ends. Companion section: Enter sends `chat_msg` (the optimistic echo is local), inbound peer `chat_msg` renders inert in the history.
- `backend/cmd/serve/main.go`, `companion/main.go`, `scripts/check_deps.sh`, `.github/workflows/ci.yml` — **read-only.** No new deps, no new routes, no signature changes; CI already runs `go test -race`.

## Tasks & Acceptance

**Execution:**
- [x] `proto/messages_test.go` — multi-byte `ChatMsg` round-trip sample; `TestChatMsgInvalidUTF8NormalizesToReplacementChar`
- [x] `backend/internal/hub/hub.go` — `chatMsgCmd` + `isCommand`; `Run` arm; `pairingState.relay`; public `Hub.Relay`; package doc
- [x] `backend/internal/hub/hub_test.go` — peer-only delivery, no self-echo, unpaired no-op, post-teardown drop, `-race` stress with `Relay`
- [x] `backend/internal/server/server.go` — decode+forward `proto.ChatMsg` to `hub.Relay`; `Outbound` cap 32; no chat log line
- [x] `backend/internal/server/server_test.go` — end-to-end relay, no echo to sender, post-teardown drop, extended log-scrub, coarse p90 latency
- [x] `companion/internal/wsclient/wsclient.go` — `chatMsg` channel + `ChatMsgs()`; `SendChat`; `serve` case; package doc
- [x] `companion/internal/wsclient/wsclient_test.go` — inbound `chat_msg` surfaced intact + still serving; `SendChat` not-connected; no logs
- [x] `companion/internal/chatui/chatui.go` — `OutboundMsg`; `WithSend`; `appendSelf` emits after the optimistic append; scope note
- [x] `companion/internal/chatui/chatui_test.go` — Enter fires `WithSend` once with the fresh id + trimmed text; whitespace-only does not; `PeerMsg` appends a `them` entry
- [x] `companion/internal/run/run.go` — `stateClient` + `chatProgram` seams; `chatSends`; inbound forward + outbound send arms; teardown nils the send path
- [x] `companion/internal/run/run_test.go` — inbound forward, outbound send, send-failure log, teardown; `fakeClient` + fake program extended
- [x] `README.md` — backend relay + companion send/render notes
- [x] `go work sync` — no diff after commit

**Acceptance Criteria:**
- Given two paired sessions, when one sends `chat_msg`, then only the other peer's socket receives it (byte-identical `client_msg_id`/`text` modulo UTF-8 normalization) and the sender receives no `chat_msg` back — verified over the wire in `server_test.go` and at the hub in `hub_test.go`.
- Given a full relay lifecycle exercised with a known message-text constant, when captured backend logs and any companion output are inspected, then the message text appears nowhere, and no code path writes chat content to disk.
- Given a pairing that has ended, when the surviving peer sends `chat_msg`, then nothing is delivered and no `error` frame is produced; frames sent before the end were delivered.
- Given `ChatMsg.Text` containing invalid UTF-8, when it round-trips through `proto.Encode`/`Decode`, then the invalid bytes become U+FFFD and the value stays valid UTF-8 — asserted by a proto test.
- Given a running companion with an active chat, when a peer `chat_msg` arrives, then a single inert-rendered `them` entry is appended keyed by the peer's `client_msg_id`; when the user presses Enter on non-empty input, then the line is echoed locally once and one `chat_msg` with a fresh `client_msg_id` goes on the wire.
- Given the finished tree, when `gofmt -l .`, `go vet`, `go test -race` over the `go.work` set, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` run, then all pass and `check_deps.sh` still reports only `companion → proto` and `backend → proto`.

## Design Notes

**Why the relay lives in the hub goroutine.** `pairings` is single-writer state (AD-8); routing a message is just a read of it. A `chatMsgCmd` on the existing command channel needs no new lock and no synchronized view of `pairings`, and the goroutine stays I/O-free — it only does the same non-blocking `deliver` onto `Session.Outbound` that `matched`/`session_ended` already use.

**Best-effort, no ack.** v1 promises "~500ms p90" and "already-received messages were delivered", not lossless delivery. `deliver` drops onto a full/nil `Outbound`, which only happens once the peer's handler has stopped draining (it is effectively gone); the 4→32 buffer bump keeps that from biting under a normal burst. The deferred spec-2-1 `deliver`-reports-success hardening stays open — it targets `matched`/`session_ended`, not the v1 relay.

**UTF-8 normalization is the contract.** `encoding/json` replaces invalid UTF-8 with U+FFFD on both marshal and unmarshal, and the companion's `inert()` does the same. Rather than add a bytes-preserving codec, 2.4 accepts and tests "text is UTF-8; invalid sequences become U+FFFD end-to-end", closing the spec-1-1 deferred question.

**Outbound send goes through `run.loop`, not Bubble Tea.** `WithSend` fires synchronously inside `Update`; calling `client.SendChat` there would block the UI for up to `frameWriteTimeout` on a bad socket. The callback only does a non-blocking channel push (mirrors `notify`/`chatIntents`); `run.loop` owns the write and the content-free failure log. No per-message log line either side: even a content-free one leaks volume/timing metadata at chat cadence.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` and `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass, incl. new hub relay, server end-to-end relay, `wsclient` inbound/`SendChat`, `chatui` send, and `run` forward/send tests
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — clean after commit
- `bash scripts/check_deps.sh` — prints only `companion → proto`, `backend → proto`; `bash scripts/checks_test.sh` — exit 0

**Manual check:**
- `PORT=8080 go run ./backend/cmd/serve`; two companions (or two scripted ws clients) each `hello` then `ready`; on match, a line typed in one pane appears in the other pane's history within a beat and never re-appears in the sender's; close one client and confirm a further line from the survivor is silently dropped with no error frame; grep the backend stdout for the typed text — absent.

## Suggested Review Order

**The relay hop (design entry point)**

- Entry point — the hub arm that routes a chat line: a pure `pairings` read plus the existing non-blocking `deliver`, no I/O, no logging.
  [`hub.go:165`](../../backend/internal/hub/hub.go#L165)
- `pairingState.relay` — peer-only delivery; a no-op (silent drop) when the sender is not paired, which covers post-teardown for free.
  [`hub.go:242`](../../backend/internal/hub/hub.go#L242)
- Public `Hub.Relay` + the `chatMsgCmd` it sends — mirrors `Ready` / `Busy`; the sender is never echoed.
  [`hub.go:360`](../../backend/internal/hub/hub.go#L360)
- Connection handler decodes `chat_msg` and forwards it; deliberately no log line in either direction.
  [`server.go:207`](../../backend/internal/server/server.go#L207)
- `Session.Outbound` raised 4→32 now that it also carries chat traffic; delivery stays best-effort.
  [`server.go:176`](../../backend/internal/server/server.go#L176)

**Companion: send and receive**

- `chatui` emits the outbound line via a new `WithSend` callback, right after the optimistic append — the Bubble Tea goroutine never writes the socket.
  [`chatui.go:113`](../../companion/internal/chatui/chatui.go#L113)
- `appendSelf` hands the just-appended id + trimmed text to that callback; whitespace-only Enter still sends nothing.
  [`chatui.go:321`](../../companion/internal/chatui/chatui.go#L321)
- `wsclient` surfaces inbound `chat_msg` on a non-terminal `ChatMsgs()` channel (mirrors `Matched()`), non-blocking, drop-on-full.
  [`wsclient.go:294`](../../companion/internal/wsclient/wsclient.go#L294)
- `SendChat` writes the outbound frame; `ErrNotConnected` with no live socket (same shape as `SendState`).
  [`wsclient.go:229`](../../companion/internal/wsclient/wsclient.go#L229)
- `run.loop` owns the socket write: `chatSends` closure is non-blocking, the actual `SendChat` runs here, off the UI goroutine.
  [`run.go:252`](../../companion/internal/run/run.go#L252)
- The two new `loop` arms: forward inbound peer lines to the live surface; send outbound lines, content-free notice on failure.
  [`run.go:442`](../../companion/internal/run/run.go#L442)
- `newChatModel` seam — extracted so a test can drive the real `chatui.Model` (with the real `WithSend` wiring) without a PTY.
  [`run.go:157`](../../companion/internal/run/run.go#L157)

**Wire contract**

- Locks the UTF-8 contract: `Encode`/`Decode` normalize invalid bytes in `ChatMsg.Text` to U+FFFD (closes a spec-1-1 deferred item).
  [`messages_test.go:268`](../../proto/messages_test.go#L268)

**Tests (peripherals)**

- Hub: peer-only delivery, no self-echo, unpaired no-op, post-teardown drop, `Relay` added to the `-race` stress.
  [`hub_test.go:595`](../../backend/internal/hub/hub_test.go#L595)
- Over the wire: relay + no echo to sender, distinct-payload ordering, post-`session_ended` drop, extended log-scrub, coarse p90.
  [`server_test.go:657`](../../backend/internal/server/server_test.go#L657)
- Companion: inbound forward to the program, and outbound Enter driven through the real `chatui.Model` to `client.SendChat`.
  [`run_test.go:1659`](../../companion/internal/run/run_test.go#L1659)
