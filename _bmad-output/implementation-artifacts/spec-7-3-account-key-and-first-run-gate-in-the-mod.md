---
title: 'Story 7.3: Account key and first-run gate in the mod'
type: 'feature'
created: '2026-10-18'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'cb3f37c66eb19a2fc37f8c71fca6cdc2ff3830f2'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-7-context.md'
  - '{project-root}/_bmad-output/specs/spec-claudingtin/voice.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The mod has no identity and no safety gate yet, so 7.4 can't send `hello`. Companion users already have a key and a safety acknowledgement on disk, and those have to carry over.

**Approach:** Port `identity` and `safety` to the mod over `$.fs`, reading and writing the same files in the same directory. Add a focused first-run Pane, a `/claudingtin safety` command, and an exported `requireGate($)` that 7.4 calls before any non-`/status` request.

## Boundaries & Constraints

**Always:**
- Config dir is Go's `os.UserConfigDir()/claudingtin`, computed as follows:
  - `APPDATA` set → `%APPDATA%\claudingtin`.
  - Otherwise, if `$HOME/Library/Application Support` exists → that path + `/claudingtin`.
  - Otherwise → `$XDG_CONFIG_HOME` (if absolute) or `$HOME/.config`, + `/claudingtin`.
- Key file `account-key`: a canonical lowercase UUIDv4 + `\n`, made with `crypto.randomUUID()`.
  - Missing, empty, oversized, or non-canonical → regenerate.
  - After a write, re-read and return the on-disk value.
- Ack file `safety-ack` is byte-compatible with the companion (`version 1\naccepted_at <ms>\n`). Only `version 1` counts as accepted.
- On non-Windows, chmod both files to 0600 after each write, best-effort via `$.process.run`.
- Every failure fails open: no throw escapes a hook. The key never reaches `$.ui.log`, `$.ui.status`, a toast, rendered UI, `$.state`, or an error message.
- Copy follows `voice.md`. It carries the companion screen's four points (stranger, keep private stuff private / screenshots, leave + block + report with no notice, 18+). The "type yes" line is rewritten for buttons.

**Ask First:** a new dependency; any backend or `/proto` change; changing the companion's file formats or `ScreenVersion`.

**Never:**
- `hello`, turn events, or chat (7.4/7.5).
- Removing the companion (7.6).
- Sending acceptance or the key anywhere.
- Storing the key in `$.store`.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Key carries over | valid companion key on disk | same key returned, file untouched (chmod only) | — |
| Key first run | no file / no dir | new UUIDv4 written, 0600, returned | write fails → `undefined` |
| Key corrupt | empty, whitespace, uppercase, v1 UUID, >512 B | regenerated | — |
| No home | none of `APPDATA`/`HOME` set | `undefined` key, gate treated as not accepted | no throw |
| Already accepted | `safety-ack` version 1 | no Pane; `requireGate` → true | unreadable → not accepted |
| Not accepted, session start | no/older/garbled ack | focused Pane `claudingtin-safety` opened (unasked) | open fails → inert |
| Accept | press "i am 18 or older" (hotkey `y`) | ack written, Pane closed, `requireGate` → true | write fails → accepted for this session only |
| Decline | press "not now" (`n`) or person closes Pane | nothing written; `requireGate` → false without reopening for the rest of the session | — |
| Re-open | `/claudingtin safety` | declined flag cleared, Pane opened focused (asked) | — |
| Unknown args | `/claudingtin foo` | short usage text naming `safety` | — |

</frozen-after-approval>

## Code Map

- `plugin/hooks/register.tsx`:
  - The `session.start` and `ui.render` AbovePrompt hooks.
  - The atom pattern: `atom({plugin,key}, init)` with `read`/`update`.
  - The fail-open try/catch style.
  - Add the wiring here: register the command on `session.start`, plus the Pane render, `ui.close`, and `command.run` hooks.
- `plugin/types/index.d.ts` -- `PluginState.claudingtin`; add the gate's state type.
- `plugin/hooks/register.test.tsx` -- the test-kit idioms: `mock.clock(on)`, `on('http.fetch', …)` beneath, `$.ui.mount`, `ui.find`. Mock fs with `on('fs.read'|'fs.write'|'fs.exists', …)`, process with `on('process.run', …)`, env with `mock.env(on, {...})`.
- `plugin/.claude-plugin/types/claude-code/index.d.ts` -- API truth (2.1.301):
  - `$.fs` L3306: `write` creates dirs and has no mode; `read` rejects when missing.
  - `$.env.get` L3671: names must be string literals.
  - `$.process.run` L3581.
  - `$.ui.open`/`close`/`panes` L2512: an unasked open is placed only from 144 cols, and `focus: true` is a request.
  - `$.command.register` L3174 is per session.
  - `ButtonProps` L1093 (`hotkey`, `variant`).
  - `PaneCloseOrigin` L7532.
- `companion/internal/identity/identity.go` -- READ-ONLY reference: `looksLikeUUIDv4`, `maxKeyFileSize = 512`, reread-after-write.
- `companion/internal/safety/safety.go` -- READ-ONLY reference: `Screen()` copy, `parseAckVersion`, `Record` body format.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/hooks/configdir.ts` -- `configDir($)` per the Always rule, plus a best-effort `chmod600($, path)` that skips Windows. Keep it pure enough to unit-test.
- [x] `plugin/hooks/identity.ts` -- `isCanonicalUUIDv4(s)` and `loadAccountKey($): Promise<string | undefined>`.
- [x] `plugin/hooks/safety.tsx`:
  - `SAFETY_COPY`
  - `isAccepted($)`, `recordAccepted($)`
  - the gate atom `{ accepted: boolean, declined: boolean }`
  - `openSafety($, asked)`
  - `requireGate($): Promise<boolean>` (exported for 7.4)
  - the Pane render: `Text` paragraphs + two Buttons
- [x] `plugin/hooks/register.tsx`:
  - `session.start` keeps the status poll as is, in the background.
  - It registers the `claudingtin` command (`argumentHint: '[safety]'`).
  - If accepted, it warms `loadAccountKey` (`void`, result discarded); otherwise it opens the Pane unless declined this session.
  - Add the hooks: `ui.render` for Pane `claudingtin-safety`, `ui.close` (origin `person` → decline), and `command.run`.
- [x] `plugin/types/index.d.ts` -- add the gate state type.
- [x] `plugin/hooks/identity.test.ts` -- the key rows of the matrix, including the macOS/Linux/Windows/XDG path choice and no-key-in-logs.
- [x] `plugin/hooks/safety.test.tsx` -- the gate rows of the matrix; the band tests in `register.test.tsx` stay green.

**Acceptance Criteria:**
- Given a companion-written `account-key` and `safety-ack` in the companion's dir, when the mod starts, then no Pane opens and `loadAccountKey` returns the companion's key byte-for-byte (trimmed).
- Given not accepted, when the session starts, then no `http.fetch` other than `GET /status` happens before acceptance (asserted on the fetch log).
- Given `claude plugin validate --strict plugin`, when run, then it reports no errors, and the env names it lists are exactly `APPDATA`, `HOME`, `XDG_CONFIG_HOME`.

## Design Notes

- `$.fs` has no chmod or rename, so atomic publish is out. That leaves a microsecond 0644 window before the chmod, and a first-run race between two sessions could split keys. Both are acceptable: the key is read lazily, and the re-read converges the common case.
- The darwin probe uses `fs.exists` because the API exposes no platform.
- The ack format is reused, so companion acceptances carry over (the warning copy is unchanged; only the input method differs).

Copy sketch for the Pane, in voice.md's register:

```
here's the deal, before anything connects.
while claude is off thinking, this puts you in a room with one other person …
[ i am 18 or older ]   [ not now ]
```

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all tests pass.

**Manual checks:**
- In a real session with `~/Library/Application Support/claudingtin/safety-ack` moved aside, the Pane opens. Accepting writes the ack (0600) and `/claudingtin safety` re-opens it.

## Suggested Review Order

**The gate (what 7.4 calls)**

- Entry point: accepted in memory, else on disk, else offer the pane once.
  [`safety.tsx:106`](../../plugin/hooks/safety.tsx#L106)

- Session start reuses the same gate, then warms the key file.
  [`register.tsx:28`](../../plugin/hooks/register.tsx#L28)

- Unasked opens respect a decline; asked opens clear it and report placement.
  [`safety.tsx:85`](../../plugin/hooks/safety.tsx#L85)

- Accept records the companion-format ack; a failed write still accepts this session.
  [`safety.tsx:122`](../../plugin/hooks/safety.tsx#L122)

- Only a person's close counts as decline; plugin and unload closes don't.
  [`safety.tsx:156`](../../plugin/hooks/safety.tsx#L156)

**Companion compatibility on disk**

- Go's `os.UserConfigDir()` approximated without a platform API.
  [`configdir.ts:26`](../../plugin/hooks/configdir.ts#L26)

- Ack parse mirrors the companion's `parseAckVersion`; only version 1 counts.
  [`safety.tsx:34`](../../plugin/hooks/safety.tsx#L34)

- Key load: unreadable is left alone, corrupt regenerated, disk value returned.
  [`identity.ts:62`](../../plugin/hooks/identity.ts#L62)

- Concurrent loads in one process share one write.
  [`identity.ts:53`](../../plugin/hooks/identity.ts#L53)

- Best-effort 0600, skipped on Windows paths.
  [`configdir.ts:64`](../../plugin/hooks/configdir.ts#L64)

**Surface and copy**

- The pane: companion copy, two buttons, no default focus on accept.
  [`safety.tsx:161`](../../plugin/hooks/safety.tsx#L161)

- Hook wiring: pane render, close, and `/claudingtin safety`.
  [`register.tsx:122`](../../plugin/hooks/register.tsx#L122)

**Tests**

- Companion files carry over end to end; only `/status` is fetched.
  [`safety.test.tsx:316`](../../plugin/hooks/safety.test.tsx#L316)

- Not accepted: pane opens and nothing but `GET /status` goes out.
  [`safety.test.tsx:337`](../../plugin/hooks/safety.test.tsx#L337)

- Config dir choice across platforms.
  [`identity.test.ts:73`](../../plugin/hooks/identity.test.ts#L73)
