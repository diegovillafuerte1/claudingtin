# Digest: Implementation Reality — Round 1, Assistant 1

**Dimension:** Implementation reality
**Questions owned:** (1) Tooling/SDK and learning curve; (2) operational constraints vs. a real terminal pane/pty process, cross-platform support; (3) reported issues with long-lived connections/live updates.

**Access date for all sources: 2026-10-06**

## Claims

1. Claude Code's only native mechanism for a plugin to draw interactive UI inside the session is a feature called "Mods" — JS/TS event-handler functions, registered via `register(on)` in a hooks module, that run inside the Claude Code process and can draw panes (`Pane`), a band above the prompt (`AbovePrompt`), buttons, inputs, selects, etc., via a `$.ui` API and a `ui.render` hook.
   Source: https://code.claude.com/docs/en/plugins/mods/overview and https://code.claude.com/docs/en/plugins/mods/reference — Anthropic official docs (versioned "as of v2.1.290") — confidence: high — class: tooling

2. Mods require no separate SDK beyond Claude Code itself — a plugin directory (`plugin.json`, `hooks/hooks.json`, a JS/TS hooks module) — but need Claude Code CLI **v2.1.287+** (terminal) or Desktop app **v2.1.286+**; the dev loop uses `claude --plugin-dir` (hot reload on save), `claude plugin validate`, and `claude plugin test`.
   Source: https://code.claude.com/docs/en/plugins/mods/overview, .../mods/reference — Anthropic — confidence: high — class: tooling

3. Mods shipped **October 1, 2026** (v2.1.287), on by default — roughly 5 days old at time of research. v2.1.288 already shipped "several mod bugs" fixes plus a new API method (`$.ui.selection()`); v2.1.289 followed quickly.
   Source: https://code.claude.com/docs/en/changelog (search aggregation), corroborated by https://blogs.eagentix.com/2026/10/02/claude-code-mods-what-changed-who-it-affects-how-to/ and https://superpowerdaily.com/posts/anthropic-adds-claude-code-mods-that-can-rewrite-prompts-and-replace-built-in-features — Anthropic changelog (primary) + two secondary blogs — confidence: high — class: tooling/cross-platform

4. A builder (ruvnet, public field-guide gist) explicitly reports the mod API is "early access and moves between releases," requiring reference to auto-generated type definitions, and documents concrete pitfalls: module-level variables reset on hot reload (killing in-flight timers), keyboard focus not behaving as documented, layout breaking when content exceeds allocated screen space, and animation-timing assumptions being wrong.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — independent builder — confidence: medium (single account, unverified identity, but concrete and specific) — class: tooling/reliability-issue

5. Mod UI rendering is **not** a general terminal-pty capability — it only renders where Claude Code's own Ink-based TUI renders it, and this varies sharply by surface: works in the CLI terminal (incl. editor-integrated terminals, JetBrains plugin) and the Desktop app's Code tab (except WSL sessions, where plugins don't load at all); hooks run but **nothing draws** in the VS Code extension's chat panel, `claude -p`, the Agent SDK, or cloud sessions; in Remote Control (claude.ai/mobile), hooks run on the local machine and drawing appears only in the local terminal, not the remote viewer.
   Source: https://code.claude.com/docs/en/plugins/mods/overview ("Where mods run" table) — Anthropic — confidence: high — class: operational-constraint/cross-platform

6. This documented surface inconsistency is actively manifesting as open bugs/feature-requests within days of launch: VS Code extension never draws any mod UI (Pane/AbovePrompt/status/toast) — confirmed by maintainers' own code inspection showing zero handlers for `ui_render`/`ui_attach`/`ui_press` in the extension (issues #99045, #99423, #99401 feature-request); Remote Control sessions show the same gap for desktop/iOS viewers (#99217, corroborating the patterns-dimension's finding).
   Source: https://github.com/anthropics/claude-code/issues/99045, /99423, /99401, /99217 — Anthropic GitHub issue tracker — confidence: high — class: cross-platform/reliability-issue

7. Mods cannot render an arbitrary embedded web page/iframe — an open feature request ("a pane element that renders a local web page (WebView), no server needed") confirms this is *not yet supported*; the element set is constrained to `Box`, `Text`, `Button`, `Link`, `Code`, `Markdown`, `Input`, `Select`, `Svg` (Desktop only), `Client` (custom JS module), `Raster` (cell grid, terminal only), `Image` (PNG/RGBA ≤2MiB, terminal only).
   Source: https://github.com/anthropics/claude-code/issues/99213; element table at https://code.claude.com/docs/en/plugins/mods/reference — confidence: high — class: operational-constraint

8. Mods are **not sandboxed** — a mod runs with the user's full permissions (reads/writes any file the user's account can, starts processes, makes network requests, reads env vars/secrets, can approve tool calls, can spend API usage) — and a process a mod starts runs **outside** Claude Code's sandboxing feature even when sandboxing is otherwise enabled.
   Source: https://code.claude.com/docs/en/plugins/mods/overview ("Decide whether to trust a mod") — Anthropic — confidence: high — class: operational-constraint (corroborates integration/patterns dimensions)

9. There is no documented native WebSocket/duplex-socket API for mods. The networking surface is `$.http.fetch` (request-style) and `$.mcp.call`/`$.mcp.connect` (connect to an MCP server the plugin's own manifest lists); long-running work is instead done via `$.process.run` (30s default timeout, 10 min max) or `$.clock.every`/`$.clock.after` polling — not a persistent inbound push channel.
   Source: https://code.claude.com/docs/en/plugins/mods/reference (API methods table) — Anthropic — confidence: high — class: reliability-issue/operational-constraint (confirms the integration-dimension's "no websocket primitive" finding)

10. Redraw/update throughput for live data is explicitly rate-limited: `$.ui.invalidate('ui.render')` is throttled to 10/sec generally (30/sec in the terminal for the visible pane, expanded band, hint line), with faster calls coalesced; a separate `$.ui.blit` path supports animation frames at higher rates (builder account reports up to 120/sec) for frame-like redraws distinct from state invalidation. A hook's own synchronous execution is capped at 10 seconds (50ms for `prompt.edit`), so any live/streaming logic must live in background timers/processes, not inside the hook call itself.
    Source: https://code.claude.com/docs/en/plugins/mods/reference ("Limits" table); corroborated by https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — confidence: high — class: reliability-issue

11. Session lifecycle for mods: `session.start` fires once per loaded mod before the first prompt and again after a mod reload, but explicitly **not** after `/clear`, `/resume`, or `/branch`; cross-session/cross-restart persistence exists only via `$.store` (machine-shared key-value store, capped 4 MiB total JSON), not via the rendered UI itself surviving a CLI close/reopen — the pane/band is redrawn fresh via `ui.render` each time the surface attaches.
    Source: https://code.claude.com/docs/en/plugins/mods/reference (Session events; `$.store`) — Anthropic — confidence: medium-high (state persistence is documented; behavior on a full process close+reopen with a previously-open pane is inferred, not directly stated) — class: operational-constraint

12. A prior GitHub feature request from April 2026 ("Plugin API for custom animated UI components in tool output," #52235) shows that as recently as ~6 months before mods shipped, Anthropic had not committed to any plugin UI-rendering mechanism and the request sat open/stale before closure (closed as not-planned) — i.e. mods is a genuinely new capability, not a maturation of a long-standing one.
    Source: https://github.com/anthropics/claude-code/issues/52235 (opened Apr 23 2026, closed not-planned) — confidence: high — class: tooling (context/history)

## Leads (follow-up round)
- https://github.com/anthropics/claude-code-playground/tree/main/claude-code/mods — Anthropic's own sample mods (`token-weather`, `blast-radius`, `replay-theater`) with README build notes.
- https://github.com/anthropics/claude-code/blob/main/mods/types/claude-code.d.ts — canonical TypeScript declarations; may reveal undocumented `$.process.spawn` semantics (vs `$.process.run`) relevant to long-lived background processes.
- `.../mods/interface`, `.../mods/events`, `.../mods/api#run-work-in-the-background`, `.../api#reach-files-processes-and-the-network` — deeper guides bearing directly on whether a background process can maintain its own long-lived websocket and feed the UI.
- https://github.com/anthropics/claude-code/tree/main/mods/diff — source of the real built-in `/diff` mod; concrete non-toy reference implementation.
- Issue #99213 (WebView pane request) comment thread — may show Anthropic's roadmap signal for embedding richer live content.
- GitHub issue search for `$.clock.every` + "memory leak"/"mod crashed", and `prependPlugins`/managed-settings interactions in team/enterprise environments.

## Could not find
- Any report (official or builder) of a mod maintaining an actual persistent WebSocket connection end-to-end with live push updates — only polling (`$.clock.every`) patterns found in the wild; whether `$.process.spawn` (vs `$.process.run`) permits an indefinitely-long-lived child process holding a socket is undocumented in the fetched pages.
- Explicit statement of what happens to a mod's open pane across a full `claude` process exit and restart (vs. `/clear`/`/resume`/`/branch`, which are documented) — inferred but not directly confirmed.
- Any macOS-vs-Linux-vs-Windows difference specific to the *rendering* of mod UI itself (only a Windows-specific path-separator note for `CLAUDE_CODE_PLUGIN_DIRS` was found).
- Independent (non-Anthropic) builder accounts beyond the single ruvnet gist describing production use of mods for a long-running companion-app-like UI — the feature is too new (5 days old) for a broad corroborated builder corpus yet.

## Sources
- [Mods overview](https://code.claude.com/docs/en/plugins/mods/overview)
- [Mods reference](https://code.claude.com/docs/en/plugins/mods/reference)
- [Claude Code changelog](https://code.claude.com/docs/en/changelog)
- [eagentix blog: what changed](https://blogs.eagentix.com/2026/10/02/claude-code-mods-what-changed-who-it-affects-how-to/)
- [Superpower Daily](https://superpowerdaily.com/posts/anthropic-adds-claude-code-mods-that-can-rewrite-prompts-and-replace-built-in-features)
- [Community field-guide gist (ruvnet)](https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea)
- [anthropics/claude-code issue #99045](https://github.com/anthropics/claude-code/issues/99045)
- [anthropics/claude-code issue #99423](https://github.com/anthropics/claude-code/issues/99423)
- [anthropics/claude-code issue #99401](https://github.com/anthropics/claude-code/issues/99401)
- [anthropics/claude-code issue #99217](https://github.com/anthropics/claude-code/issues/99217)
- [anthropics/claude-code issue #99213 — WebView pane request](https://github.com/anthropics/claude-code/issues/99213)
- [anthropics/claude-code issue #52235 — closed not-planned, pre-mods history](https://github.com/anthropics/claude-code/issues/52235)
