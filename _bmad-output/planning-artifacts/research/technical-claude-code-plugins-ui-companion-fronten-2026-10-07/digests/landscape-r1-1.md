# Digest: Landscape & Maturity — Round 1, Assistant 1

**Dimension:** Landscape & maturity
**Questions owned:** (1) Official name/docs/release date of the capability; (2) maturity/stability; (3) what it newly makes possible in the CLI specifically (vs. other surfaces).

## Claims

1. The official Anthropic feature that lets a plugin/extension render persistent, interactive UI inside a live Claude Code CLI session is called **"mods"** (not "plugins" generically — mods are a category of plugin with a `hooks.json` pointing to a JS/TS "hooks module"). A mod draws into named "render sites" (Pane, AbovePrompt/band, Spinner, ToolUse, AskUserQuestion, etc.) via a `ui.render` hook event, and can take keyboard focus, buttons, text inputs, and persist state across redraws/sessions (`$.state`, `$.store`).
   Source: https://code.claude.com/docs/en/plugins/mods/interface — Anthropic official docs — accessed 2026-10-06 — confidence: high — class: feature-identity

2. Mods require Claude Code CLI version **2.1.287 or later** ("Use Claude Code v2.1.287 or later. In your shell, run `claude --version` to check.").
   Source: https://code.claude.com/docs/en/plugins/mods/create — Anthropic official docs — accessed 2026-10-06 — confidence: high — class: maturity

3. v2.1.287 is the release that introduced the feature, per its "What's changed" notes: "Added Claude Mods: plugins may now modify deeper behavior" plus a built-in example mod ("You should know"). This release maps to **October 1, 2026** (one fetch mis-rendered the year as 2025, but this conflicts with (a) an independent X/social post explicitly dating the announcement to "Oct 2, 2026, one day after launch," and (b) the session's current date of 2026-10-06 shortly after; 2026 is treated as correct).
   Source: https://github.com/anthropics/claude-code/releases (primary) and https://x.com/AGTPinsights/status/2105768033419743253 (corroborating) — Anthropic / third-party — accessed 2026-10-06 — confidence: medium (date corroborated across two sources but with one internal year-rendering inconsistency flagged) — class: maturity

4. Mods are explicitly labeled **"early access"** by Anthropic's own repository README, and the underlying API is stated to be unstable: "Early access: hooks modules load only where function hooks are enabled, and the API these mods are written against may change between releases without notice." The official docs independently reinforce this: "The events and methods can change between releases, so trust these [generated type] files over any page, this one included, when they disagree."
   Source: https://github.com/anthropics/claude-code/tree/main/mods (README) and https://code.claude.com/docs/en/plugins/mods/create — Anthropic — accessed 2026-10-06 — confidence: high — class: maturity

5. As of this research, mods have existed only **about one week** (first shipped ~Oct 1, 2026; current date Oct 6, 2026) — a brand-new, not long-lived, feature. No major redesign/breaking-change history found yet (too recent), only incremental follow-on releases: v2.1.289 (Oct 3) closed permission gaps; v2.1.290 (Oct 5) expanded capabilities (added `agentId` to `tool.check`, fixed a pane redraw-without-end bug) and fixed mods staying off for gateway users without an Anthropic account.
   Source: https://github.com/anthropics/claude-code/releases — Anthropic — accessed 2026-10-06 — confidence: medium (derived from a summarization pass, not independently cross-checked against a second source) — class: maturity

6. What this newly makes possible in the CLI specifically: before mods, a Claude Code plugin/extension could only add slash commands, static text output, hooks that intercept tool calls, skills, or MCP servers — none of which could draw a persistent, stateful, interactive region of the terminal screen. Mods add a true render-and-event-loop UI system: a mod can open a sidebar "Pane" (or a framed region above the prompt in narrow terminals), draw buttons/inputs/tabs/text/SVG-like boxes, receive presses/typed input via callbacks (`onPress`, `onSubmit`, `onSelect`), redraw on data change or a timer, and persist state across reloads (`$.state`) or across sessions (`$.store`). This is a genuine step beyond slash-command text output.
   Source: https://code.claude.com/docs/en/plugins/mods/interface — Anthropic — accessed 2026-10-06 — confidence: high — class: capability-scope

7. CLI vs. other surfaces is explicitly scoped by Anthropic: "The terminal and the Desktop app don't raise all the same sites. `Pane`, `AbovePrompt`, `Spinner`, and the transcript sites work in both. A few other status lines are raised in the terminal only." Certain elements are terminal-only (`Raster`, `Image`) while `Svg` is Desktop-only. The docs make no mention of this mechanism applying to claude.ai web "artifacts" — artifacts are a wholly separate, unrelated system (web chat rendering), not referenced anywhere in the mods docs.
   Source: https://code.claude.com/docs/en/plugins/mods/interface — Anthropic — accessed 2026-10-06 — confidence: high — class: capability-scope

8. Mods also require trust/permission gating: a mod only loads in a "trusted workspace," and organizations can block mods entirely via managed settings; some environments (non-interactive `-p` runs, `--safe-mode`, `--bare`, `disableAllHooks`) skip or disable mod loading outright — relevant to stability for a shipped-product use case, since a scripted/headless session may not run mods at all.
   Source: https://code.claude.com/docs/en/plugins/mods/create — Anthropic — accessed 2026-10-06 — confidence: high — class: maturity / capability-scope

9. **Unverified belief, flagged low confidence:** a secondary search summary claimed mods require explicitly opting in via `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`, distinct from the "trusted workspace" gating found in primary docs. This specific env-var name was NOT found verbatim in either primary source fetched (docs or GitHub README) — the README only said "hooks modules load only where function hooks are enabled" without naming the variable.
   Source: WebSearch aggregation, no single primary URL — confidence: low — class: maturity — **flagged unverified**

## Leads (follow-up round)
- https://code.claude.com/docs/en/plugins/mods/overview — likely the canonical "mods vs settings/hooks/skills/MCP" framing and explicit stability/GA language.
- https://code.claude.com/docs/en/plugins/mods/admin — organization controls (`disableAllHooks`, managed settings) — relevant to whether a team could rely on this for a shipped product.
- https://code.claude.com/docs/en/plugins/mods/troubleshoot — may confirm/deny the `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS` env var claim.
- https://github.com/anthropics/claude-code/blob/main/mods/types/claude-code.d.ts — authoritative, version-pinned type declarations.
- https://thenewstack.io/anthropic-claude-code-mods-plugins/ — full article text not retrievable via WebFetch (nav chrome only); worth a direct re-fetch.
- Anthropic's weekly changelog pages — could resolve the 2025-vs-2026 release-date discrepancy with full certainty from a primary, non-aggregated source.

## Could not find
- An explicit Anthropic blog-post/press-release announcement page for mods (only inferred from release notes + a third-party X post).
- Primary-source confirmation of an exact enabling env-var name, or whether any such var is still required as of v2.1.290 vs. at initial launch.
- Any record of a "major redesign" or breaking change to the mods API (reasonable — the feature is ~5 days old).
- Full text of The New Stack article.
- Independent confirmation of the GitHub release date's year directly from raw GitHub HTML (not the WebFetch summarizer, which rendered "October 1, 2025" — almost certainly a summarizer error given all other context points to 2026).

## Sources
- [Draw in the interface with a mod](https://code.claude.com/docs/en/plugins/mods/interface)
- [Create a mod](https://code.claude.com/docs/en/plugins/mods/create)
- [anthropics/claude-code releases](https://github.com/anthropics/claude-code/releases)
- [anthropics/claude-code mods README](https://github.com/anthropics/claude-code/tree/main/mods)
- [X corroborating post](https://x.com/AGTPinsights/status/2105768033419743253)
