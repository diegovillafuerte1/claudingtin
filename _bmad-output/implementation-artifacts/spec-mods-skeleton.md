---
title: 'Mods skeleton: backend-status band in Claude Code'
type: 'feature'
created: '2026-10-16'
status: 'done'
baseline_commit: 'a3008d5b1a00c465fafa6015a2f121682dc22206'
review_loop_iteration: 1
context:
  - '{project-root}/_bmad-output/planning-artifacts/research/technical-claude-code-plugins-ui-companion-fronten-2026-10-07/research.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** claudingtin is pivoting from a terminal-multiplexer companion pane to a Claude Code mod (in-process plugin UI) that talks to the backend over HTTP. No mod exists yet, so the plugin has no in-session surface to build the chat on.

**Approach:** Add a mod hooks module to the existing `plugin/`. It polls the backend's existing `GET /status` with `$.http.fetch` on a `$.clock.every` timer and draws a one-line AbovePrompt band: `claudingtin · connected` or `claudingtin · offline`. The backend URL comes from plugin `userConfig`.

## Boundaries & Constraints

**Always:**
- Fail open. No mod hook throws or blocks; any fetch error, non-2xx status or bad URL shows `offline`.
- At most one poll in flight at a time. A poll unresolved after 4 s counts as `offline` and releases the guard; the abandoned fetch's late result is ignored.
- Store state in `$.state` (an atom), never in module variables, because hot reload resets those.
- Yield the band (`return next(e)`) when `e.props.hasSurvey` is true.
- Keep the existing SessionStart command hook and its behaviour exactly as they are, apart from quoting `${CLAUDE_PLUGIN_ROOT}`.

**Ask First:**
- Any change to the backend, `proto/`, the companion, or `plugin/cmd/session-start`.
- Adding npm, node, or a build step to the repo.

**Never:**
- Chat, queueing, matching, busy/ready detection, or the HTTP chat endpoints. Those come in later slices.
- Removing pane placement (deferred in `deferred-work.md`).
- CI changes.
- Writing to `$.state` from a render hook.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Backend up | `GET {base}/status` → 200 | Band: `claudingtin · connected` | N/A |
| Backend error | → 503 / any non-2xx | Band: `claudingtin · offline` | none surfaced |
| Unreachable | fetch rejects | Band: `claudingtin · offline` | caught |
| Before first result | atom still `null` | Band draws nothing (`next(e)`) | N/A |
| Trailing slash | `backendUrl` = `http://h:1/` | Requests `http://h:1/status` | N/A |
| Unset config | no `backendUrl` set | Uses default `http://127.0.0.1:8080` | N/A |
| Survey showing | `hasSurvey: true` | Band yields to the engine | N/A |
| Slow poll | fetch unresolved after 4 s | Band shows `offline`; the next tick polls again (a timed-out fetch is abandoned, never awaited) | late result ignored |

</frozen-after-approval>

## Code Map

- `plugin/hooks/hooks.json` -- Add `"modules": ["./register.tsx"]` (path relative to hooks.json, not `${CLAUDE_PLUGIN_ROOT}`) next to the existing `hooks.SessionStart`. Validate warns that `${CLAUDE_PLUGIN_ROOT}` in the command is unquoted, so quote it.
- `plugin/.claude-plugin/plugin.json` -- Add `"types": "./types/index.d.ts"` and `userConfig.backendUrl` (string, default `http://127.0.0.1:8080`). The backend's default `PORT` is 8080 (`backend/cmd/serve/main.go:32`). Do NOT add `engines`; validate flags it as an unknown field.
- `backend/internal/server/server.go:62` -- `handleStatus`: GET only, 200 with `{"concurrent_users":N}`. Read-only here; the mod looks only at `ok`.
- Probe reference (throwaway, validated and its tests passing; adapt it, don't copy blindly): `/private/tmp/claude-502/-Users-k-Documents-Github-claudingtin/48659d88-b64d-4cd6-a55d-8eee784af26e/scratchpad/modprobe/` (`hooks/register.tsx`, `hooks/band.test.tsx`, `types/index.d.ts`). It is missing the in-flight guard and the URL fallback, and it uses the wrong default port.
- API types (authoritative): `/private/tmp/claude-502/bundled-skills/2.1.301/18f4c8da8bb191990deda18cf8d0e9c5/plugin-authoring/types/claude-code.d.ts`.
  - `register(on, options)`, where `options` carries `userConfig`.
  - `atom`/`read`/`update` come from `'claude-code'`. An `update` redraws subscribed renders.
  - `$.ui.resolve(e)` returns the elements. JSX uses the global `h`.
  - Start timers in `session.start`, which re-fires after each reload; timers are cancelled automatically on reload.
- `$.clock.after(ms, fn)` -- Use it for the 4 s timeout (race it against the fetch). `HttpInit` has no timeout or signal field, so a hung fetch cannot be cancelled, only abandoned.
- Test API: `'claude-code/testing'` provides `test`, `expect`, `mock.clock(on)` and `$.ui.mount`.
  - A test's `http.fetch` hook must return `{ value }`.
  - A test needs a terminal `ui.render` hook and a terminal `session.start` hook that return `e`.
  - Find elements by `{ type: 'Text', text: /…/ }`, not by `key`.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/tsconfig.json` -- Keep `"jsx":"react","jsxFactory":"h"` -- the JSX in `register.tsx` needs it.
- [x] `plugin/types/index.d.ts` -- Declare `PluginState.claudingtin.online: boolean | null` and export at least one type -- validate rejects a bare `export {}`.
- [x] `plugin/.claude-plugin/plugin.json` -- Add `types` and `userConfig.backendUrl` -- makes the URL configurable through `/config` and `claude plugin configure`.
- [x] `plugin/hooks/register.tsx` -- Implement the mod:
  - an `online` atom
  - normalize the URL (trim it, strip trailing slashes, fall back to the default when empty)
  - a `poll` with try/catch, an in-flight guard and a 4 s timeout via `$.clock.after`
  - a generation token, so results from an abandoned fetch or an earlier `session.start` never write `online` or release a newer guard
  - `session.start` runs one immediate poll, then `$.clock.every(5000)`, then `return next(e)`
  - a dim one-line AbovePrompt `Text` for the band; `return next(e)` when `maxRows < 1` or when reading state throws

  -- This is the skeleton every later mods slice builds on.
- [x] `plugin/hooks/hooks.json` -- Add `modules` and quote `"${CLAUDE_PLUGIN_ROOT}"` in the command -- this wires in the mod and leaves SessionStart behaviour unchanged.
- [x] `plugin/hooks/register.test.tsx` -- Cover every I/O matrix row. The slow-poll row uses a fetch that never resolves and advances the clock past 4 s and then 5 s. Also cover offline→connected, a poll after a failed fetch, and `maxRows: 0`. The "before first result" test must start a session with a pending fetch. Name tests after what they actually exercise (code default vs. manifest default) -- this is the unit coverage for the edge cases.

**Acceptance Criteria:**
- Given the plugin directory, when `claude plugin validate --strict plugin` runs, then it passes, lists the `session.start` and `ui.render` hooks, and still lists the SessionStart command hook.
- Given a local backend (`cd backend && go run ./cmd/serve`) and `claude --plugin-dir plugin`, when the session starts, then the band reads `claudingtin · connected` within about 5 s. When the backend is stopped, it flips to `claudingtin · offline` within about 5 s.
- Given the change, when `go test ./...` runs in `plugin/`, then it passes, showing the Go launcher is untouched.

## Spec Change Log

- **Iteration 1 (2026-10-16)**
  - *Trigger:* all three reviewers found it. A `/status` fetch that never settles left the in-flight guard set forever, freezing the band on a stale state instead of failing open to `offline`.
  - *Amended:* with the human's approval, the frozen Always rule and the Slow-poll matrix row now specify a 4 s timeout, after which the band shows `offline`. Code Map and Tasks gained the timeout, a generation token, `maxRows < 1` and render-path fail-open, and extra tests.
  - *Known-bad state avoided:* a permanently wedged poller that shows `connected` while the backend is unreachable.
  - *KEEP:* the shape of the iteration-0 implementation. Reference copy: `/private/tmp/claude-502/-Users-k-Documents-Github-claudingtin/48659d88-b64d-4cd6-a55d-8eee784af26e/scratchpad/keep-iter0/`.
    - `online`/`polling` atoms in `$.state` with test-and-set acquisition in one `update`.
    - `normalizeBaseUrl` export.
    - The test helpers `start()`, `bandText()` and `engineRender`. The test kit rejects `null` from the terminal render hook, so use the `engine` Text stand-in.
    - Quoted `${CLAUDE_PLUGIN_ROOT}`.
    - `wrap="truncate-end"`.
    - No `engines` field.

## Design Notes

The URL default is HTTP, not the companion's `ws://…/ws` `SERVER_URL`. The mod speaks HTTP only, and in this slice the companion's configuration stays separate. Showing nothing until the first poll lands avoids flashing "offline" at startup.

## Verification

**Commands:**
- `claude plugin validate --strict plugin` -- expected: no errors.
- `claude plugin test plugin` -- expected: all tests pass.
- `cd plugin && go test ./...` -- expected: passes.

**Manual checks:**
- Run `claude --plugin-dir plugin` against a running and then a stopped local backend, and watch the band switch between connected and offline.

## Suggested Review Order

**Poll lifecycle (the core of the mod)**

- Entry point: one immediate poll, then every 5 s; the reset abandons polls from before a reload.
  [`register.tsx:83`](../../plugin/hooks/register.tsx#L83)

- Test-and-set guard plus a 4 s timeout that fails open to offline.
  [`register.tsx:38`](../../plugin/hooks/register.tsx#L38)

- Only the newest in-flight gen writes `online`; it writes before releasing, closing the overwrite race.
  [`register.tsx:31`](../../plugin/hooks/register.tsx#L31)

- Outer catch releases this gen's guard so a throw can't freeze polling.
  [`register.tsx:72`](../../plugin/hooks/register.tsx#L72)

**Band rendering**

- Yields on a survey, no rows, a state-read failure, or before the first result; otherwise draws one dim line.
  [`register.tsx:95`](../../plugin/hooks/register.tsx#L95)

**Configuration**

- Trim, strip trailing slashes, fall back to the default for blank or missing values.
  [`register.tsx:14`](../../plugin/hooks/register.tsx#L14)

- `userConfig.backendUrl` makes it editable in `/config`; `types` declares the state shape.
  [`plugin.json:8`](../../plugin/.claude-plugin/plugin.json#L8)

- Mod wired next to the untouched SessionStart hook; the launcher path is now quoted.
  [`hooks.json:3`](../../plugin/hooks/hooks.json#L3)

**Peripherals**

- State atoms (`online`, `poller`) are declared here for the engine.
  [`index.d.ts:12`](../../plugin/types/index.d.ts#L12)

- JSX compiles against the engine's global `h`.
  [`tsconfig.json:4`](../../plugin/tsconfig.json#L4)

- Hung-fetch timeout path: offline at 4 s, re-poll at 5 s, late result ignored.
  [`register.test.tsx:144`](../../plugin/hooks/register.test.tsx#L144)

- Reload re-fire: abandons the hung poll and polls at once.
  [`register.test.tsx:186`](../../plugin/hooks/register.test.tsx#L186)
