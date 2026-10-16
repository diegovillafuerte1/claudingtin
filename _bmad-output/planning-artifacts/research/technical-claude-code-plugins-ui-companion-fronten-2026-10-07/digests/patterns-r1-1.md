# Digest: Architecture Patterns in Practice — Round 1, Assistant 1

**Dimension:** Architecture patterns in practice
**Questions owned:** (1) Real example plugins using native UI for anything persistently interactive; (2) community-converged patterns; (3) gotchas/anti-patterns builders report.

## Context note

Claude Code shipped a new native mechanism called **"Mods"** on Oct 1, 2026 (v2.1.287) — corroborates the landscape dimension's finding. Distinct from (and much more capable than) the older **statusline** mechanism, which is display-only.

## Claims

1. Claude Code's "Mods" system (released Oct 1, 2026, v2.1.287) lets a plugin run a persistent JS/TS module inside the CLI/desktop process that hooks events (`tool.call`, `ui.render`, session start/end, slash commands) and draws its own UI — docked panes, inline bands above the prompt (`AbovePrompt`), buttons, text input, Markdown, Image, and raw terminal-cell "Raster" graphics with up to 60fps animation via `$.ui.blit`.
   Source: https://the-decoder.com/claude-codes-new-mods-system-lets-developers-rewrite-the-ai-coding-tool-from-the-inside and https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — the-decoder.com (tech news) / community gist (ruvnet) — accessed 2026-10-06 — confidence: medium-high — class: pattern/mechanism

2. A concrete shipped example of a persistently-interactive Mod UI is **ruview-live**, a Wi-Fi sensor dashboard with three tabbed views (Overview with sparklines, Waterfall as a half-block heatmap replaying at measured frame rate, Radar as a braille range-fan with an animated sonar ping) — a full interactive monitoring application rendered inside the session.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — community gist — confidence: medium (single, secondhand source) — class: example-plugin

3. Anthropic's own first official example of a Mods-based plugin is **"Token Weather"**, using three hooks — `tool.call` on Bash plus `ui.render` on both `Pane` and `AbovePrompt` — to render a persistent status surface.
   Source: search snippet referencing the gist — confidence: low-medium (not independently fetched/confirmed on an Anthropic-owned page) — class: example-plugin

4. Anthropic also shipped **"You Should Know"** as "the first official Mod plugin" — runs a separate agent that monitors Claude's output and surfaces alerts about potentially overlooked critical information.
   Source: https://the-decoder.com/claude-codes-new-mods-system-lets-developers-rewrite-the-ai-coding-tool-from-the-inside — confidence: medium — class: example-plugin

5. **Pattern — community convention:** `AbovePrompt` is the recommended first target for a plugin's persistent UI because "Claude Code draws nothing there by default." Multiple mods can co-exist in that band via `await next(e)` to get other mods' rendered output and compose it together (middleware-style chaining), as seen in community mod `claude-code-effort-cycle` ("lets other mods draw above the prompt").
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea; https://github.com/Anerco/claude-code-effort-cycle/issues/2 — confidence: medium — class: pattern

6. **Gotcha — persistence of state across reloads:** Module-level JS variables are wiped on hot reload; builders are advised to store state in `$.state` (persistent atoms) or re-fetch, and to "resume polling from the render hook" so a pane that stays mounted across reloads doesn't go blank.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — confidence: medium — class: gotcha

7. **Gotcha — layout breaks in inline mode:** A pane is placed `dock` (full-height sidebar) only on fullscreen terminals ≥110 columns, otherwise falls back to `inline`, capped to roughly a third of screen height. The guide author reports a pane "designed docked" got cut off inline, with "the bottom row of buttons never appeared" — fixed by requesting explicit row height and testing against `bodyRows`.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — confidence: medium — class: gotcha

8. **Gotcha — keyboard focus requires explicit ceremony:** Opening a pane does not by itself grant hotkey access; builders must open with `focus`, `closeOnEscape`, and `holdToasts` together for the pane to "take the keys," and should mirror hotkeys as slash-command fallbacks.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — confidence: medium — class: gotcha

9. **Gotcha — animation/raster pitfalls:** Mismatched Raster dimensions between a render pass and a `blit` call cause the blit to be refused outright; timing should use the real clock (fixed-delta stepping caused visible phase jumps); blits should never be `await`ed since they queue between frames anyway; a UI tree referencing an element type the current surface doesn't support (e.g. `ui.Raster`) is refused whole, so plugins need surface guards and text fallbacks for non-terminal surfaces.
   Source: https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea — confidence: medium — class: gotcha

10. **Gotcha — cross-surface rendering gap is a live, unresolved bug:** Filed against Anthropic's own repo (`anthropics/claude-code` issue #99217, labels `area:plugins`, `bug`): a mod's `AbovePrompt`/`Pane` `ui.render` output renders correctly in a terminal-hosted session but **does not appear at all** when that same session is viewed through Remote Control in the Claude desktop or iOS apps — confirmed on Claude Code 2.1.287/macOS. The issue notes the Mods docs don't clarify whether `ui.render` is meant to reach Remote Control viewers at all, and that `/feedback` has the same gap. No Anthropic staff response visible at time of access.
    Source: https://github.com/anthropics/claude-code/issues/99217 — Anthropic GitHub repo (user-filed) — confidence: high (primary source, specific repro) — class: gotcha

11. **Gotcha — related open feature requests confirm rough edges:** Two further open issues — #99401 ("VS Code extension: draw function-hook mod UI") and #98986 ("Mods: let a plugin observe or own the collapse of the AbovePrompt band") — indicate VS Code extension support for Mod UI is currently absent/partial, and plugins can't currently observe/control when a user collapses their AbovePrompt band.
    Source: https://github.com/anthropics/claude-code/issues/99401 ; https://github.com/anthropics/claude-code/issues/98986 — confidence: medium (titles/existence confirmed via search snippet only) — class: gotcha

12. **Security posture, not sandboxing:** Mods "run with the user's permissions and aren't sandboxed" — Anthropic's stated mitigation is a trust-based warning ("only install from trusted sources") rather than isolation; mods work in CLI and desktop app but only "partly in the VS Code extension."
    Source: https://the-decoder.com/claude-codes-new-mods-system-lets-developers-rewrite-the-ai-coding-tool-from-the-inside — confidence: medium — class: gotcha (corroborates integration-dimension's finding #7)

13. **The older, display-only mechanism (statusline) is NOT a fit for interactive UI:** Claude Code's native `statusLine` setting runs a shell command on a tick (≤ every 300ms) and renders only the first line of stdout as static text at the bottom of the terminal; enabling a custom statusline actually **removes** the normal footer's interactive keyboard hints (esc to interrupt, `?` shortcuts, hold-space voice dictation). Confirms statusline is display-only, not a path to persistent interactivity.
    Source: https://code.claude.com/docs/en/statusline (per search synthesis) and a Claude HUD build writeup (pub.towardsai.net) — confidence: medium-high — class: gotcha / anti-pattern-to-avoid

14. **Non-Mods category — separate-process dashboards (contrast case):** Several well-known community "Claude Code dashboards" exist but are **not** rendered inside the CLI session itself — they are separate web apps/servers reading Claude Code's local session/log files and serving their own browser UI (e.g. `developer-hasm/claude-code-dashboard` — Next.js local web dashboard; `hoangsonww/Claude-Code-Agent-Monitor` — Node/Express/React/WebSockets real-time monitor packaged as a native macOS/Windows app; `netil/oh-my-hi` — SQLite-backed local HTTP dashboard). These are architecturally the same pattern as today's separate-pane companion, not an example of the native in-session mechanism.
    Source: https://github.com/developer-hasm/claude-code-dashboard ; https://github.com/hoangsonww/Claude-Code-Agent-Monitor ; https://github.com/netil/oh-my-hi — confidence: high (repo descriptions directly state architecture) — class: example-plugin (negative/contrast case)

15. **"Claude HUD" is a statusline plugin, not an interactive panel:** `jarrodwatts/claude-hud` adds a persistent statusline (context health bar, live tool activity, subagent tracking, git status) — built on the statusline mechanism (display-only per claim #13), not Mods-style interactive panes.
    Source: secondhand blog summaries (lobehub.com, heyuan110.com) — confidence: low-medium (not confirmed against primary repo/README) — class: example-plugin

## Leads (follow-up round)
- Official Anthropic docs page specifically for "Mods" — (NOTE: the parallel landscape+integration digests DID find `code.claude.com/docs/en/plugins/mods/{interface,api,overview,reference,create}` directly — this assistant missed that primary doc tree; reconcile in synthesis.)
- `claude.dev/blog/getting-started-with-claude-code-mods/` — appears official-adjacent "getting started" post, not yet fetched.
- `dev.to/nakadadev/how-to-make-a-claude-code-mod-with-three-files-that-work-in-the-desktop-app-26mm` — builder tutorial with concrete gotchas, not yet fetched.
- `Anerco/claude-code-effort-cycle` repo itself (beyond the one issue) — real community mod demonstrating composable AbovePrompt rendering.
- The actual `ruview-live` repo (not located/confirmed directly — only described secondhand).
- GitHub topic/search `claude-code-mod` for more third-party Mods built since the Oct 1 2026 release.
- Anthropic's own "Token Weather" and "You Should Know" plugin source — not located as direct repos, only described secondhand.
- Reddit/HN threads from actual builders (search surfaced article coverage, not forum discussion).

## Could not find
- A dedicated, official Anthropic documentation page for "Mods" **as far as this assistant's own search went** (reconciled in synthesis: the landscape/integration assistants DID find `code.claude.com/docs/en/plugins/mods/*`).
- Primary-source (repo/README) confirmation of "ruview-live," "Token Weather," or "You Should Know" — all three described only secondhand.
- Any Hacker News or Reddit thread from an actual builder recounting hands-on experience with Mods UI.
- Confirmation of whether Mods support persistent two-way "chat-like" conversational surfaces (vs. dashboards/panels) — no example of a chat-surface-shaped Mod found.
- Independent verification of the Claude HUD plugin's actual mechanism directly from its own repo.

## Overall assessment (assistant's own synthesis)
There is a real, very recently shipped (Oct 1, 2026) native mechanism — "Mods" — matching what's being asked about: persistent, interactive panes/bands/dashboards drawn directly inside a live Claude Code session. However, the ecosystem is only days old: concrete third-party examples are thin and mostly secondhand, and Anthropic's own GitHub issue tracker already shows real cross-surface rendering gaps (desktop/iOS Remote Control views not showing Mod UI, partial VS Code support) — builders should expect genuine platform-coverage gotchas, not just polish issues, if committing to this mechanism now.

## Sources
- [The Decoder: Claude Code's new Mods system](https://the-decoder.com/claude-codes-new-mods-system-lets-developers-rewrite-the-ai-coding-tool-from-the-inside)
- [Community field-guide gist (ruvnet)](https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea)
- [anthropics/claude-code issue #99217 — cross-surface rendering gap](https://github.com/anthropics/claude-code/issues/99217)
- [anthropics/claude-code issue #99401 — VS Code extension support](https://github.com/anthropics/claude-code/issues/99401)
- [anthropics/claude-code issue #98986 — AbovePrompt collapse observability](https://github.com/anthropics/claude-code/issues/98986)
- [Anerco/claude-code-effort-cycle issue #2](https://github.com/Anerco/claude-code-effort-cycle/issues/2)
- [developer-hasm/claude-code-dashboard](https://github.com/developer-hasm/claude-code-dashboard)
- [hoangsonww/Claude-Code-Agent-Monitor](https://github.com/hoangsonww/Claude-Code-Agent-Monitor)
- [netil/oh-my-hi](https://github.com/netil/oh-my-hi)
