---
title: 'Story 3.6: Persist toggle, default-on'
type: 'feature'
created: '2026-10-28'
status: 'done'
baseline_commit: 'c70a0c881d8af65b0c35059da66343fd46e74c82'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-3-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** When the model returns, the mod ends the chat: `onTurnEnd` resets it and sends `busy`, and in the hub `busy` while paired tears the pair down. A good conversation is cut off the instant the person's Claude finishes.

**Approach:** A `userConfig.persist` boolean, default `true`. Each chat snapshots it at `matched`. With persist on, the model returning keeps the chat open: the mod holds back `busy`, keeps polling past the burst, and shows a quiet "your claude's back" note. It sends `busy` only once that held chat ends. With persist off, the model returning sends `leave` then `busy` and closes the chat. The backend is unchanged (the human chose mod-only).

## Boundaries & Constraints

**Always:**
- Hooks never await the network or throw. Sends are voided and every failure is quiet.
- The persist value used is the one snapshotted into the chat at `matched`. A toggle mid-chat changes only the next chat.
- A non-boolean `options.persist` reads as `true`.
- While held, the events long-poll keeps running so the lease (and the chat) survives.
- When a held chat ends (peer `session_ended`, own leave/block/report, handle 410), the hold clears, and `busy` goes out if the model is still back.
- A main-loop `turn.start` during a held chat clears the hold and the note. The chat simply continues in the new burst.
- Copy is lowercase: note `your claude's back`; band while held and placed `claudingtin · your claude's back — chat's still open`.

**Ask First:** any backend, hub or `/proto` change; a new dependency.

**Never:**
- the session-ID keepsake (3.7);
- persisting chat content;
- a confirm on leave;
- polling when neither in a burst nor held.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Fresh install | no `persist` option | chat snapshots `persist=true` at `matched` | non-boolean → `true` |
| Persist on, model back | chatting, `turn.complete` | chat stays; `back=true`; band/pane show the note; no `busy`, no `leave` sent; polling continues | — |
| Held, peer ends | `session_ended` while held | ended line (if placed), phase `idle`, hold cleared, `busy` sent once | events after it in the same batch are ignored |
| Held, own leave | button / `/claudingtin leave` | exactly one `leave`, Pane closed, hold cleared, `busy` sent | — |
| Held, new burst | `turn.start` | hold and `back` cleared; chat continues; no new `ready` effect on the pair | — |
| Persist off, model back | chatting, `turn.complete` | `leave` then `busy` sent in order; chat reset, Pane closed | send failure quiet |
| Not chatting | `turn.complete` | today's behaviour: `busy` | — |
| Toggle | `/claudingtin persist` | `$.config.set({key:'claudingtin.persist', value:!current})`; reply `persist is on — your next chat stays open when your claude's back` / `persist is off — chats end when your claude's back` | set fails → `couldn't change persist here.` |
| Hot reload while held | `session.start` | hold, note and polling resume from state | — |

</frozen-after-approval>

## Code Map

- `plugin/.claude-plugin/plugin.json` -- add `userConfig.persist`: `{type:'boolean', title:'Persist chats', description:…, default:true}`.
- `plugin/types/index.d.ts`:
  - `Chat` gains `persist: boolean` (the snapshot) and `back: boolean` (the note shows);
  - `Link` gains `held: boolean` (the type lives in this file; check it).
- `plugin/hooks/register.tsx`:
  - L39 `register`: call `setPersistDefault(options.persist)` (new export in chat.tsx; a module var, reset on reload and set again by register);
  - L139 `turn.complete` passes through to `onTurnEnd` unchanged;
  - command L186: new `persist` verb, plus `argumentHint` and `USAGE` (L26).
- `plugin/hooks/link.ts`:
  - `INITIAL` L29: `held: false`.
  - `onTurnEnd` L76 decides by the chat:
    - chatting, not left, `persist` → `held=true`, `want='busy'`, chat `back=true`, no reset;
    - chatting, not persist → `void sendSeqNow([leave])` first, then today's reset and busy;
    - else → today's path.
  - `onTurnStart` L66: `held=false`, chat `back=false`.
  - `reconcile` L172: `want==='busy' && held` → return (send nothing).
  - `polling` L243: also true when `held && sent==='ready'`.
  - New `releaseHold($, base)`: `held=false`, then `void work($, base)`.
  - The `pollLoop` 410 branch L284 clears `held`.
  - Pass `base` into `applyEvents`.
- `plugin/hooks/chat.tsx`:
  - `IDLE` L38: `persist:false, back:false`;
  - `matched` L189: snapshot `persist` from the module setting;
  - `session_ended` L210: when the link is held, call `releaseHold` and stop applying later events in that batch (`applyEvents` breaks after it);
  - `leaveChat` L269: `releaseHold` after the leave;
  - `bandLine` L352: when `c.back`, the held band line (placed) or the existing narrow hint;
  - `renderChat` L368: one dim `Text` note under the header when `c.back`.
- `plugin/hooks/chat.test.tsx`, `link.test.ts` -- existing harnesses (`start`, the fetch log) to copy for the new rows.
- `backend/internal/hub/hub.go:296` busyCmd -- READ-ONLY. `busy` while paired tears down, which is why the hold withholds it.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/.claude-plugin/plugin.json`, `plugin/types/index.d.ts` -- the `persist` field and state types.
- [x] `plugin/hooks/link.ts` -- hold, `onTurnEnd`/`onTurnStart`, reconcile and poll gating, `releaseHold`.
- [x] `plugin/hooks/chat.tsx` -- snapshot, end/leave release, note in band and pane, `setPersistDefault`.
- [x] `plugin/hooks/register.tsx` -- the option and the `persist` verb.
- [x] `plugin/hooks/chat.test.tsx` (and `link.test.ts` as needed) -- every matrix row. Also:
  - the fetch log never shows `busy` while held;
  - exactly one `busy` after a held chat ends;
  - persist off sends `leave` before `busy`;
  - toggling mid-chat doesn't change the current chat.
- [x] `plugin/hooks/register.test.tsx` -- the manifest test also covers the `persist` default (no options ⇒ a matched chat has `persist=true`).

**Acceptance Criteria:**
- Given a hung backend, when the model returns during a chat (either setting), then `turn.complete` resolves without waiting on any fetch.
- Given `claude plugin validate --strict plugin` and `claude plugin test plugin`, when run, then there are no errors and all tests pass.

## Design Notes

- **Why withhold `busy`:** the hub ends a pair on `busy`. Holding `busy` keeps the pair alive, at the cost of a known race the human accepted. After the peer ends a held chat, the hub (which still thinks we want a match) may silently re-enqueue us and match a third person before our `busy` lands. That `busy` then ends the new pair, so the third person sees a match end at once. Ignoring events after `session_ended` stops the mod from opening a Pane for it.
- **Persist off sends `leave` first:** it ends the pair explicitly, as the AC asks, so the leaver is never re-enqueued. `busy` then clears queue intent.

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all pass.

**Manual checks:**
- Two local sessions against a local backend. Match them, let A's model finish with persist on, and check the chat continues both ways. Then toggle persist off via `/claudingtin persist`, match again, let A's model finish, and check B sees the ended line.
- The toggle reaching the next chat depends on the engine reloading the plugin when `userConfig` changes, which `claude plugin test` does not simulate. Check it by hand: `/claudingtin persist` mid-chat leaves that chat held, and the next chat ends on return.
- `/claudingtin persist` writes `claudingtin.persist`; confirm in `/config` that the row really changes (tests stub `config.set`).

## Suggested Review Order

**The model returns: hold or leave**

- Entry point: hold only on a live poll; persist off sends leave, then the worker sends busy.
  [`link.ts:85`](../../plugin/hooks/link.ts#L85)

- The decision comes from the chat's snapshot at `matched`, never the live setting.
  [`chat.tsx:348`](../../plugin/hooks/chat.tsx#L348)

- The poll keeps running while held, so the lease and the pair survive.
  [`link.ts:295`](../../plugin/hooks/link.ts#L295)

**The held chat ends: busy goes last**

- The peer's `session_ended` lifts the hold and drops the rest of the batch (the accepted rematch race).
  [`chat.tsx:234`](../../plugin/hooks/chat.tsx#L234)

- Own leave/block/report: its sends settle first, then the hold is released.
  [`chat.tsx:298`](../../plugin/hooks/chat.tsx#L298)

- Release kicks the worker, which sends the withheld busy.
  [`link.ts:122`](../../plugin/hooks/link.ts#L122)

**The setting**

- The userConfig field, default-on.
  [`plugin.json:16`](../../plugin/.claude-plugin/plugin.json#L16)

- The module setting that register writes, and that the toggle also updates live.
  [`chat.tsx:53`](../../plugin/hooks/chat.tsx#L53)

- `/claudingtin persist`: `config.set`, then the live value.
  [`register.tsx:203`](../../plugin/hooks/register.tsx#L203)

**Drawing**

- Band while held and placed.
  [`chat.tsx:419`](../../plugin/hooks/chat.tsx#L419)

- The quiet note under the header in the pane.
  [`chat.tsx:483`](../../plugin/hooks/chat.tsx#L483)

**Peripherals**

- Default-on behaviour with no persist option (the manifest default fills in).
  [`chat.test.tsx:698`](../../plugin/hooks/chat.test.tsx#L698)

- Wire order for block/report/leave while held.
  [`chat.test.tsx:737`](../../plugin/hooks/chat.test.tsx#L737)

- Snapshot vs live setting.
  [`chat.test.tsx:767`](../../plugin/hooks/chat.test.tsx#L767)
