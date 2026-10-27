---
title: 'Story 7.5: Mod chat surface (parity with Epics 2–3)'
type: 'feature'
created: '2026-10-18'
status: 'done'
review_loop_iteration: 0
baseline_commit: '4008c81b2eef3ef91b83a8010016cd4192fa1201'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-7-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The 7.4 `/v1` link joins the queue and acks events, but it renders nothing. A matched user has no spinner, no chat and no ending inside Claude Code, so the companion is still the only client.

**Approach:**
- Feed each fresh event from `pollLoop` into a new chat module. It keeps the chat in `$.state` and draws:
  - a band spinner while queued;
  - a bounded flourish on `matched`;
  - a chat Pane (header, opener, history, Input, and leave/block/report Buttons);
  - one calm ended line.
- The Pane's actions go out through `/v1/send` and are mirrored as `/claudingtin leave|block|report|chat`.

## Boundaries & Constraints

**Always:**
- Hooks never await the network. All sends are `void`ed through link's abandon-timer `request`, and every failure is quiet: no throw, no log, no content and no handle anywhere.
- Peer, opener, pseudonym and blurb text renders only through `Text`, after `inert()`. `inert()` is a TS port of `companion/internal/chatui/inert.go`:
  - keep `\n` and U+200D;
  - drop C0 (except `\n`), DEL, C1 and every other `\p{C}`/`\p{Zl}`/`\p{Zp}`;
  - replace lone surrogates with U+FFFD.
  
  Pseudonym and blurb also collapse to one line. Never use `Markdown`, `Link` or `Code`.
- Chat state lives in `$.state` key `chat`, in memory only (it survives hot reload, never disk):
  - `phase: 'idle'|'searching'|'chatting'`
  - `match: {sessionId,pseudonym,blurb,opener}|null`
  - `lines: {id,from:'peer'|'self',text}[]`, capped at 200
  - `ended: boolean`
  - `placed: boolean`
  - `left: boolean`
- Events are applied in ascending `seq` order before `after` is acked. Only `queued`, `matched`, `chat_msg` and `session_ended` act; any other type is ignored.
- Outbound text is capped at 2000 code points, enforced both on `ui.input` change and on submit. Text that is empty after trimming sends nothing. Each line gets a fresh `client_msg_id` (`crypto.randomUUID()`) and is appended optimistically before the send.
- Copy, all lowercase:
  - band searching: `<frame> looking for someone`
  - flourish: `◐ ◓ ◑ ◒ ◐ ◓` + `  here we go`, 6 frames at 90 ms
  - header: `you're chatting with <name|someone new>`
  - placeholder: `say something`
  - ended: `looks like their claude's back — catch you later`
  - leave button: `my claude's back`

**Ask First:** a backend or `/proto` change; adding `session_id` to hello (resume after a lost handle); a new dependency.

**Never:**
- persist or keepsake (3.6/3.7);
- real block/report semantics (Epic 4);
- deleting the companion, `/ws` or the session-start launcher (7.6, same PR);
- position, ETA or count on the spinner;
- a confirm on leave;
- polling outside a burst.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Queued | `queued` event | phase `searching`; band shows animated spinner | — |
| Matched | `matched{…}` | phase `chatting`, `lines=[]`, `ended=false`, `left=false`; flourish plays in band; `$.ui.open({id:'claudingtin-chat',title:'claudingtin',focus:true,rows:16})`; `placed=isPlaced` | open fails → `placed=false` |
| Narrow / not placed | `placed=false` while chatting | band: `claudingtin · chatting with <name> — /claudingtin chat`; `/claudingtin chat` opens it (person-initiated ⇒ placed inline at any width) | — |
| Peer line | `chat_msg` while chatting and `!left` | append peer line | otherwise dropped |
| Send | Input submit `"  hi "` | self line `hi` appended at once; `POST /v1/send {type:'chat_msg',v:1,client_msg_id,text:'hi'}` | send fails → line stays, quiet |
| Peer ended | `session_ended` while chatting | `ended=true`, match cleared; Pane shows ended line only; phase `searching` if link `want==='ready'`, else `idle` | not chatting → ignored |
| Leave | button / `/claudingtin leave` while chatting | exactly one `leave` sent; `left=true`; chat cleared; Pane closed (plugin); phase `searching` if burst active | not chatting → `no chat going right now.` |
| Block / report | button or slash command while chatting | send `block` (or `report{last_n:5}`) once, then the Leave row | as leave |
| Own Claude back | main `turn.complete` | chat reset to idle, Pane closed; 7.4 sends `busy` | — |
| Handle lost | events 410 (quiet) while chatting | chat reset to idle, Pane closed | — |
| Poll gap | poll error/backoff while chatting | nothing visible changes | — |
| Hot reload | `session.start` with `chat` in state | spinner ticker restarts if `searching`; Pane redraws from state | — |
| Hostile text | peer `"\x1b]8;;http://x\x07[a](b)<i>"` | drawn as `Text` with ESC/BEL removed, markup literal | — |

</frozen-after-approval>

## Code Map

- `plugin/hooks/link.ts`:
  - `pollLoop` L267–313: at the `// 7.5 renders these` seam, call `applyEvents`.
  - Replace `parseSeqs` L316 with `parseEvents`, which returns `{seq, type, ...raw}`. Keep the seq validation, and update the `parseSeqs` import in `link.test.ts`.
  - `send` L388: generalise to `postMsg($, base, handle, body)`. Export `sendNow($, base, body)`, which reads the handle and is a no-op without one.
  - The quiet-410 branch L280 and `onTurnEnd` L76 call `resetChat`.
  - Export `getLink`.
- `plugin/hooks/chat.tsx` (new):
  - the `chat` atom;
  - `inert`;
  - `applyEvents`;
  - `submitLine`, `leaveChat` (`kind: 'leave'|'block'|'report'`), `resetChat`, `openChat`, `resumeChat`;
  - `renderChat` (Pane) and `bandLine` (band);
  - a spinner/flourish ticker. It is a module-level `$.clock.every(100)` that bumps a frame counter and calls `$.ui.invalidate('ui.render')`. It runs only while searching or while the flourish is playing.
- `plugin/hooks/register.tsx`:
  - Pane render on `requestId:'claudingtin-chat'`;
  - a `ui.input` cap hook on element `claudingtin-input` (check the matcher shape in `UiInputArgument`, d.ts L14629);
  - the band uses `bandLine` before its status line;
  - the command grows `leave|block|report|chat`, with a new `argumentHint` and USAGE;
  - `session.start` calls `resumeChat`.
- `plugin/hooks/safety.tsx:156` -- `renderSafety` is the pane-render and Button idiom to copy.
- `plugin/types/index.d.ts` -- add the `Chat` type and the `chat` key.
- `plugin/.claude-plugin/types/claude-code/index.d.ts`:
  - `InputProps` L5597 has no maxLength, so the cap goes through `ui.input`.
  - `PaneOpenArgs` L7550: a plugin-initiated open waits undrawn below 144 cols.
  - Pane `placement` L10758.
  - `ui.render` L4072: a `$.state` read while drawing subscribes the redraw.
  - Test kit: `ui.input({key,value,kind})` L15774 and `ui.press` L15839.
- `companion/internal/chatui/chatui.go`, `searchui/searchui.go` and `statusline/statusline.go` -- copy source only. They are READ-ONLY, and 7.6 deletes them.
- `backend/internal/server/v1.go:566` handleSend -- READ-ONLY. It accepts `chat_msg|leave|block|report`; block and report are ignored until Epic 4. A leaver is never re-enqueued.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/types/index.d.ts` -- add the `Chat` type and the `chat` state key.
- [x] `plugin/hooks/chat.tsx` -- the chat module described in the Code Map.
- [x] `plugin/hooks/link.ts` -- `parseEvents`, the `applyEvents` hand-off, `sendNow`, and the `resetChat` hooks.
- [x] `plugin/hooks/register.tsx` -- Pane render, input cap, band phases, command verbs and resume.
- [x] `plugin/hooks/chat.test.tsx` -- every matrix row, using `link.test.ts`'s `start` harness (copy it). Also cover:
  - the Pane tree contains no `Markdown`/`Link`/`Code`;
  - the Buttons are present in every chatting render;
  - exactly one `leave` per leave;
  - the fetch log never carries chat text except in a `chat_msg` body.
- [x] `plugin/hooks/link.test.ts` -- switch to `parseEvents`; the 7.4 rows stay green.

**Acceptance Criteria:**
- Given a hung backend, when a line is submitted or leave is pressed, then the press/input resolves without waiting on the fetch.
- Given `claude plugin validate --strict plugin` and `claude plugin test plugin`, when run, then there are no errors and all tests pass.

## Design Notes

- **Own return closes, peer return shows the line.** Without persist (3.6), the person's own Claude returning ends the chat, and they go back to work. The ended line is the remaining user's "their Claude came back" fiction.
- **A leaver gets no line.** The Pane closes and the leaver is never told anything about the peer (3.3 rationale).
- **Block and report end the chat too.** Until Epic 4 tears the pair down on the server, the local leave is what ends it for the peer.
- **The spinner after a mid-burst leave never resolves.** The backend does not re-enqueue a leaver. This keeps parity with 3.3's accepted interim.
- **The history window approximates rows.** Draw the newest lines that fit `scroll.bodyRows` minus the fixed rows (header, blurb, buttons, input). A line's rows are estimated as `ceil(len/bodyColumns)`. The header and Buttons sit at the top, so they stay visible.
- **Unverified in a real host:** whether an Input clears after submit once the redraw passes `value: ''`. If it doesn't, key the Input by line count.

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all pass.

**Manual checks:**
- Run two local sessions against a local backend. Prompt both, and they should match. Then check:
  - the spinner, then the flourish, then the Pane;
  - lines relay both ways;
  - leave in one session shows the ended line in the other;
  - at under 144 cols the band shows the `/claudingtin chat` hint.

## Suggested Review Order

**Events in (where the poll becomes chat)**

- Entry point: fresh events are re-checked against the live burst, then applied, then acked.
  [`link.ts:303`](../../plugin/hooks/link.ts#L303)

- One switch: queued, matched, chat_msg and session_ended act; everything else is ignored.
  [`chat.tsx:178`](../../plugin/hooks/chat.tsx#L178)

- Matched opens the Pane in the background, so a hung open never stalls polling.
  [`chat.tsx:225`](../../plugin/hooks/chat.tsx#L225)

- Peer lines are keyed `p-<seq>` (never the peer's id), capped, deduped.
  [`chat.tsx:196`](../../plugin/hooks/chat.tsx#L196)

- Peer ended: the line shows only if the Pane is placed; the band goes back to the spinner mid-burst.
  [`chat.tsx:209`](../../plugin/hooks/chat.tsx#L209)

**Text safety**

- A TS port of inert.go: control, format and separator characters dropped; lone surrogates become U+FFFD.
  [`chat.tsx:52`](../../plugin/hooks/chat.tsx#L52)

- Code-point caps, shared by outbound input and inbound fields.
  [`chat.tsx:70`](../../plugin/hooks/chat.tsx#L70)

**The person's actions (never await the network)**

- Leave/block/report: state first, then one voided ordered send (block or report before leave).
  [`chat.tsx:268`](../../plugin/hooks/chat.tsx#L268)

- Ordered background sends on a handle read once.
  [`link.ts:432`](../../plugin/hooks/link.ts#L432)

- Submit: trimmed, capped, appended optimistically, then sent.
  [`chat.tsx:244`](../../plugin/hooks/chat.tsx#L244)

- Own Claude back, or the handle lost: the chat resets and the Pane closes.
  [`link.ts:83`](../../plugin/hooks/link.ts#L83)

**Drawing**

- Pane: header, buttons at the top, a history window sized to the body, Input.
  [`chat.tsx:367`](../../plugin/hooks/chat.tsx#L367)

- Band phases: flourish, spinner, then the narrow-mode `/claudingtin chat` hint.
  [`chat.tsx:351`](../../plugin/hooks/chat.tsx#L351)

- One ticker, running only while searching or during the flourish; invalidate drives the frames.
  [`chat.tsx:90`](../../plugin/hooks/chat.tsx#L90)

**Hook wiring**

- Pane render, person close, the input cap with a single `next`.
  [`register.tsx:152`](../../plugin/hooks/register.tsx#L152)

- Slash verbs mirror the buttons.
  [`register.tsx:186`](../../plugin/hooks/register.tsx#L186)

- The band draws the chat line before the status line.
  [`register.tsx:205`](../../plugin/hooks/register.tsx#L205)

**Peripherals**

- `Chat` state type: memory only, survives hot reload.
  [`index.d.ts:50`](../../plugin/types/index.d.ts#L50)

- Race tests: an open that never settles, and a stale poll after turn.complete.
  [`chat.test.tsx:528`](../../plugin/hooks/chat.test.tsx#L528)

- Hostile text never reaches Markdown, Link or Code.
  [`chat.test.tsx:460`](../../plugin/hooks/chat.test.tsx#L460)
