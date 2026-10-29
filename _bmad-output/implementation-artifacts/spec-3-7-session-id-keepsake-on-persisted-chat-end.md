---
title: 'Story 3.7: Session-ID keepsake on persisted-chat end'
type: 'feature'
created: '2026-10-28'
status: 'done'
baseline_commit: 'aac74b72775449ccf0a1b5dcccd3d47fa429c2f9'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** When a good persisted chat ends, nothing is left of it. The `session_id` from `matched`, the one thread that could let two people find each other again, is discarded.

**Approach:** The first time a chat is held past the burst (3.6's `hold`), mark it `kept`. When a kept chat ends, the mod's end state offers its session ID: shown as text, with a Copy button (`$.ui.copy`), a one-line note, and a dismiss. The offer is decided from local state only.

## Boundaries & Constraints

**Always:**
- The gate is the chat's own `kept` flag, set when it was held at least once. It is never read from `session_ended` contents (which carry no `session_id`).
- `kept` survives a later `turn.start` (the chat that carries on into a new burst stays kept).
- The offer never blocks. A new `matched` replaces it, `done` or a person's Pane close dismisses it, and the band spinner wins while searching.
- The ID is always shown as `inert` text, so it works with no clipboard. A failed copy shows a calm line, never an error.
- Hooks never await the network or throw.
- Copy is lowercase and free of alarm words:
  - note `this chat's id — the only way to find them again`;
  - buttons `copy id` and `done`;
  - after a press, `copied` or `no clipboard here — the id's above`;
  - band when unplaced `claudingtin · keep this chat's id — /claudingtin chat`.

**Ask First:** any backend, hub or `/proto` change; a new dependency.

**Never:**
- offering the ID after block or report;
- offering the ID for a chat that never outlived a burst;
- persisting the ID or any chat content to disk, store or logs;
- a lookup or reconnect by ID.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Kept, peer ends | held chat, `session_ended` | Pane shows ended line + note + ID + `copy id` / `done` | — |
| Kept, own leave | held, button or `/claudingtin leave` | one `leave`, then busy (3.6); Pane stays open on note + ID (no ended line) | — |
| Kept, block/report | held, block or report | 3.6 behaviour, Pane closed, no offer | — |
| Kept, then new burst, then ends | held → `turn.start` → `session_ended` | offer shown; band spinner while searching | — |
| Kept, handle lost / Claude back again unholdable | 410, or `turn.complete` with `onModelBack` → `leave` | chat reset and the offer kept | — |
| Not kept | ended mid-burst (persist on), or persist off | today's end state; no ID, no Copy | — |
| Copy | press `copy id` | `$.ui.copy({text:id, surface:press.surface})`; label → `copied` | `isCopied:false` / throw → `no clipboard here — the id's above` |
| Dismiss | `done`, or person closes Pane | offer cleared, Pane closed | — |
| Unplaced (narrow) | offer pending, not placed, idle | band hint; `/claudingtin chat` opens the offer | — |
| New match | `matched` while offer pending | offer replaced by the new chat | — |
| Empty ID | kept chat with `sessionId === ''` | no offer | — |

</frozen-after-approval>

## Code Map

- `plugin/types/index.d.ts` (`Chat`, about L53) -- gains `kept: boolean` and `keepsake: { id: string; copy: 'none' | 'copied' | 'failed' } | null`.
- `plugin/hooks/chat.tsx`:
  - `COPY` L20 -- new strings.
  - `IDLE` L43 -- `kept:false, keepsake:null`.
  - `matched` L216 -- resets both.
  - `onModelBack` L348 -- on `hold`, also sets `kept:true`.
  - New helper `keepsakeOf(c)` -- `c.kept && c.match?.sessionId` gives `{id, copy:'none'}`, else null.
  - Apply it in `session_ended` L240, `leaveChat` L305 (kind `leave` only; skip `closePane` when an offer results) and `resetChat` L326 (when the result holds an offer, the Pane stays open).
  - `renderChat` L439, non-chatting branch: ended line (if `ended`), then the offer rows when `keepsake`.
  - `bandLine` L415 -- the hint when phase is idle, there is an offer and the Pane is unplaced.
  - `openChat` L372 -- also opens for a pending offer.
  - `onChatClose` L392 -- a person close clears `keepsake`.
  - New `copyKeepsake($, surface)` and `dismissKeepsake($)`, never throwing.
- `plugin/hooks/link.ts` -- no change expected. `onTurnStart` clears only `held`/`back`, and `resetChat` callers are L108 and L340.
- `plugin/hooks/register.tsx` L216 -- the `chat` verb's `'none'` stays only when there's neither a chat nor an offer.
- `plugin/hooks/chat.test.tsx` -- reuse `held()` (L692), `pane`, `texts`, `ui.press`, `fakeChat$` (about L620). Mock `ui.copy` like `ui.open`.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/types/index.d.ts` -- the `kept` and `keepsake` fields.
- [x] `plugin/hooks/chat.tsx` -- the flag, the offer on every non-safety end, the drawing, copy, dismiss, band and open.
- [x] `plugin/hooks/register.tsx` -- the `chat` verb for a pending offer. (No edit was needed: `openChat` now returns `open`/`unplaced` for an offer, and the narrow tests cover it.)
- [x] `plugin/hooks/chat.test.tsx` -- every matrix row, plus:
  - the offer never appears without a hold (peer ends mid-burst with persist on);
  - a `session_ended` that carries a `session_id` field changes nothing about the gate;
  - the ID never shows up in any fetch body or log line;
  - the existing 3.6 held tests are updated where the Pane now stays open (own leave, peer end, 410).

**Acceptance Criteria:**
- Given an offer is showing, when the person ignores it and a new burst finds a match, then the new chat opens normally (nothing is blocked).
- Given `claude plugin validate --strict plugin` and `claude plugin test plugin`, when run, then there are no errors and all tests pass.

## Design Notes

- **Why `kept` is not `persist`:** "persisted past the burst" means the chat actually outlived the person's Claude. A persist-on chat the peer ends mid-burst was never persisted, so it gets no offer.
- **Kept only once the hold lands:** `link.ts` `onTurnEnd` calls `markKept` after the held `setLink` succeeds. A hold that fails is never kept.
- **Offer lifetime:** no timeout. It lives in memory in `$.state` (never on disk) until `done`, a person's close, or the next `matched`. While searching, the spinner owns the band.
- **Why no offer on block/report:** a way back to someone you just blocked or reported is the opposite of what the person asked for.

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all pass.

**Manual checks:**
- In a real terminal, hold a chat, leave, press `copy id`, and paste it. The UUID should arrive.

## Suggested Review Order

**The gate: kept, from local state only**

- Entry point: the offer comes from the chat's own `kept` flag and the `matched` id.
  [`chat.tsx:170`](../../plugin/hooks/chat.tsx#L170)

- A chat is kept only once the held write lands.
  [`link.ts:102`](../../plugin/hooks/link.ts#L102)

- `markKept`: chatting, back, and not yet kept.
  [`chat.tsx:393`](../../plugin/hooks/chat.tsx#L393)

**Every end leaves the offer**

- Peer `session_ended`: the offer stays even when unplaced and the ended line is dropped.
  [`chat.tsx:261`](../../plugin/hooks/chat.tsx#L261)

- Own leave offers it; block and report never do. The Pane stays open for it.
  [`chat.tsx:329`](../../plugin/hooks/chat.tsx#L329)

- Reset (410, a return that can't hold): a kept chat offers it, and one already pending stays.
  [`chat.tsx:360`](../../plugin/hooks/chat.tsx#L360)

**The offer: draw, copy, dismiss**

- Note, the id in bold, `copy id` (label becomes `copied`) and `done`, plus the no-clipboard line.
  [`chat.tsx:537`](../../plugin/hooks/chat.tsx#L537)

- Copy onto the pressing surface; the result lands only on the same offer.
  [`chat.tsx:447`](../../plugin/hooks/chat.tsx#L447)

- `done` clears it and closes the Pane outside a chat.
  [`chat.tsx:466`](../../plugin/hooks/chat.tsx#L466)

- A person's close dismisses it.
  [`chat.tsx:440`](../../plugin/hooks/chat.tsx#L440)

- Narrow: a band hint, and `/claudingtin chat` opens the offer.
  [`chat.tsx:499`](../../plugin/hooks/chat.tsx#L499)
  [`chat.tsx:417`](../../plugin/hooks/chat.tsx#L417)

**Peripherals**

- `kept` and `Keepsake` types.
  [`index.d.ts:65`](../../plugin/types/index.d.ts#L65)

- The 3.7 tests, starting at the matrix rows.
  [`chat.test.tsx:858`](../../plugin/hooks/chat.test.tsx#L858)

- Review-driven tests: a pending offer outlives turns, a stray `session_id`, done while searching.
  [`chat.test.tsx:1036`](../../plugin/hooks/chat.test.tsx#L1036)
