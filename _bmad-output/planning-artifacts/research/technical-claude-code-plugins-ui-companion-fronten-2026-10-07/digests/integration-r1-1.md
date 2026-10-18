# Digest: Integration & Interoperability — Round 1, Assistant 1

**Dimension:** Integration & interoperability
**Questions owned:** (1) What APIs/primitives does the UI capability expose and how does a plugin attach a surface; (2) lifecycle model — can the surface persist/react independently of a conversational turn; (3) permission/sandboxing model.

## Key finding up front

Claude Code has a documented, first-party mechanism for exactly this — called a **"mod."** It is an event-driven plugin extension (not a separate process/pty/iframe) that draws into the CLI/Desktop-app interface via a declarative element tree, with its own namespaced API (`$`) for files, processes, network, timers, and cross-session messaging. All primary evidence below comes from `code.claude.com/docs` (official Claude Code docs), accessed 2026-10-06, documented as of Claude Code v2.1.290.

## Claims

1. A mod is a plugin whose JS/TS module registers event handlers ("hooks") via `register(on)`; one event, `ui.render`, fires each time Claude Code is about to draw a "render site" (a pane beside the transcript, a band above the prompt, the spinner, message rows, tool-call rows, the question dialog, status lines, etc.), and the hook returns an element tree (`Box`, `Text`, `Button`, `Input`, `Select`, `Markdown`, `Raster`, `Image`, `Svg`, `Client`) built via `$.ui.resolve(e)`. A mod opens a pane with `$.ui.open({id, title, focus, closeOnEscape, rows, columns})` and closes it with `$.ui.close`.
   Source: https://code.claude.com/docs/en/plugins/mods/interface — Anthropic, official docs, accessed 2026-10-06 — confidence: high — class: api-surface

2. A mod registers UI elements and reacts to input (`Button.onPress`, `Input.onSubmit`/`onInput`, `Select.onSelect`) entirely inside Claude Code's own process; there is no pty-attach, iframe/webview, or separate-process rendering model. `Client` is the one escape hatch — "a region drawn by a second file of yours, for animation and pointer input. That file gets no mods API. It reaches your hooks by posting data, which arrives as a `ui.message` event."
   Source: https://code.claude.com/docs/en/plugins/mods/interface — same — confidence: high — class: api-surface

3. A mod can run work that "outlives one event" using `$.clock.every`/`$.clock.after` (replacing `setInterval`/`setTimeout`), started from a `session.start` hook. Verbatim: "The timer's callback runs outside any event, so it keeps running between turns and doesn't start one." It can update the UI without a conversational turn via `$.ui.status`, `$.ui.toast`, `$.ui.log`, and can proactively start a turn via `$.prompt.submit({text})` — "The call waits until the session is idle and then starts a new turn." Timers are scoped to the module instance and "stop when the module reloads."
   Source: https://code.claude.com/docs/en/plugins/mods/api — same — confidence: high — class: lifecycle-model

4. Lifecycle/persistence model: a mod's hooks module stays loaded and "runs for the length of a session" (not torn down between turns); `session.start` fires "once for each loaded mod, before the first prompt, and again after a reload of that mod" but NOT after `/clear`, `/resume`, or `/branch` (a separate `classic.SessionStart` event covers those). `$.state` ("reactive state") persists across module reloads but resets to defaults on `/clear`/`/resume`/`/branch`; `$.store` is a JSON key-value store on disk (`~/.claude/plugins/store/…`) shared across all sessions on the machine and persists across sessions/restarts. A module-level JS variable only survives until "the module reloads, which happens every time you save a file during development."
   Source: https://code.claude.com/docs/en/plugins/mods/interface and https://code.claude.com/docs/en/plugins/mods/reference — same — confidence: high — class: lifecycle-model

5. Network access is through `$.http.fetch(url, init)` only, described as resolving "to `{status, ok, headers, text}` once the body is read" over http/https — i.e., a one-shot request/response fetch. The docs do not mention any websocket, SSE, or persistent-socket primitive anywhere in the mods API reference table (`$.http` lists only `fetch`). This is a material gap against a "maintain a persistent websocket connection" requirement — unverified whether raw sockets could be opened via `$.process.spawn` (spawning a helper process) as a workaround, but no native websocket-in-mod API is documented.
   Source: https://code.claude.com/docs/en/plugins/mods/api and https://code.claude.com/docs/en/plugins/mods/reference — same — confidence: high (for "no documented websocket primitive"); medium (that spawn-based workaround would work, since not explicitly tested) — class: api-surface / lifecycle-model

6. Filesystem and process access are available and sandboxed only through the `$` namespace — no ambient Node globals. Verbatim: "The hooks module itself has no Node.js APIs, no timer globals such as `setTimeout`, and no network or file access of its own... Standard JavaScript and web APIs such as `URL`, `TextEncoder`, `AbortController`, and `crypto.subtle` are available." `$.fs.{read,write,list,exists,stat,ancestors}` (4 MiB file size limit), `$.process.run` (30s default timeout, 10 min max, no shell, argument-list only) and `$.process.spawn` ("streams a long-running command's output").
   Source: https://code.claude.com/docs/en/plugins/mods/api and https://code.claude.com/docs/en/plugins/mods/reference — same — confidence: high — class: permission-model

7. Mods are explicitly **not sandboxed** and run with the full permissions of the user running Claude Code. Verbatim: "A mod reaches the file system, processes, and the network through the mods API, with the same permissions as the user running Claude Code." And from the overview page's trust section: "Mods aren't sandboxed. If you turn on sandboxing, the sandbox isolates the Bash commands Claude runs, and a process that a mod starts runs outside it." A mod can also "Act without asking you: approve a tool call before you're asked" and bypass `PreToolUse`/ask/deny rules under documented conditions (`allowModsToOverrideDenyRules`). The only thing a mod cannot alter is the permission-approval prompt dialog itself.
   Source: https://code.claude.com/docs/en/plugins/mods/overview — same — confidence: high — class: permission-model

8. Documented numeric limits constrain a hook's own execution per invocation, not the background timer mechanism itself: a hook has a 10-second execution budget per event (50ms for `prompt.edit`), `.catch` handlers get 1s, `session.end` hooks collectively get ~1.5s. UI redraw requests (`$.ui.invalidate('ui.render')`) are throttled to 10/sec generally (30/sec in the terminal for the visible pane/band/hint line), with excess calls coalesced. `$.store` is capped at 4 MiB total JSON; `$.session.messages()` returns at most the newest 4,096 entries. No documented cap on how many `$.clock.every` timers a mod may run or how long they may run in total — they appear intended to run indefinitely for the life of the session/module.
   Source: https://code.claude.com/docs/en/plugins/mods/reference ("Limits" table) — same — confidence: high — class: lifecycle-model / permission-model

9. Mods render in the terminal CLI and the Code tab of the Claude Desktop app (drawing included), and their hooks (but not drawing) also run in the VS Code extension's chat panel, `claude -p`/Agent SDK, Remote Control, and (for plugins reaching the cloud) cloud sessions. They do not run at all in WSL sessions in the Desktop app ("because plugins aren't available in WSL sessions").
   Source: https://code.claude.com/docs/en/plugins/mods/overview ("Where mods run" table) — same — confidence: high — class: lifecycle-model

10. Cross-session/cross-agent messaging exists as a separate primitive (`$.session.send`/`session.receive`, mirrors the `SendMessage` tool) for a mod to talk to other Claude Code sessions or subagents with plain text — this is the closest documented analogue to "reacting to an external asynchronous event," but it's inter-session messaging between Claude Code instances, not a generic external-service webhook/websocket listener.
   Source: https://code.claude.com/docs/en/plugins/mods/api — same — confidence: high — class: api-surface

## Leads (follow-up round)
- Canonical TypeScript type declarations for the mods API: https://github.com/anthropics/claude-code/blob/main/mods/types/claude-code.d.ts
- Official sample mods illustrating background/timer patterns: https://github.com/anthropics/claude-code-playground/tree/main/claude-code/mods (`token-weather`, `blast-radius`, `replay-theater`)
- Built-in mod source for patterns handling long tasks / side-agent notifications: `cc-plugin-you-should-know` and https://github.com/anthropics/claude-code/tree/main/mods (`diff`, `agents-md`, `sec-default`, `telemetry`)
- Secondary commentary flagging the "not sandboxed" design as a security concern (not used as evidence, flagged for awareness): https://dev.to/yong_yu_f98e15562e9b120a0/claude-code-mods-just-landed-first-hook-blast-radius-guard-and-why-theyre-not-sandboxed-3225 and https://pluto.security/blog/claude-code-function-hooks-security/
- Admin/org controls on mods (`allowManagedModsOnly`, `prependPlugins`/`appendPlugins`, `sec-default` guard): https://code.claude.com/docs/en/plugins/mods/admin
- `claude plugin validate` / `claude plugin test` tooling for pre-install capability auditing: /create and /test pages not yet fetched

## Could not find
- Any documented native websocket or persistent-socket API inside the mods sandbox (only one-shot `$.http.fetch` is documented) — could not verify whether `$.process.spawn` is an officially sanctioned way to host a websocket client as a child process that then talks back to the mod.
- Any explicit statement of a hard ceiling on the total lifetime or count of `$.clock.every` background timers, or on total background CPU/memory a mod may consume over a long session.
- Independent (non-Anthropic) verification of real-world reliability/performance of `ui.render` + pane timers for a workload resembling "watch a file and react to async network events continuously, independent of user typing."

## Sources
- [Draw in the interface with a mod](https://code.claude.com/docs/en/plugins/mods/interface)
- [Use the mods API](https://code.claude.com/docs/en/plugins/mods/api)
- [Mods overview](https://code.claude.com/docs/en/plugins/mods/overview)
- [Mods reference](https://code.claude.com/docs/en/plugins/mods/reference)
