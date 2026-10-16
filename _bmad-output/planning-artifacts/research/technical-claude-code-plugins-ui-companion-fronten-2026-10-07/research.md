---
title: 'technical research: Claude Code plugin/mods UI capability for claudingtin companion'
type: 'technical'
topic: 'Claude Code plugin/mods UI capability for claudingtin companion'
decision: 'Whether/how to restructure claudingtin companion frontend to use Claude Code native plugin UI instead of terminal-multiplexer pane placement'
source: 'native run'
status: complete
preset: 'standard'
validation: 'normal'
created: '2026-10-07'
updated: '2026-10-07'
claims_verified: 5
claims_unverified: 0
claims_disputed: 1
claims_overturned: 0
---

# technical research: Claude Code plugin/mods UI capability for claudingtin companion

**Decision this research serves:** Whether/how to restructure claudingtin's companion frontend to use Claude Code's native plugin UI mechanism instead of terminal-multiplexer pane placement (tmux/wezterm/kitty/zellij/iterm2/new-OS-window).

## Executive summary

Claude Code shipped a real, first-party mechanism for exactly this on **October 1, 2026** (v2.1.287) — called **"mods."** A mod is a JS/TS plugin that runs inside the Claude Code process for the life of the session, draws persistent interactive UI (a docked pane, a band above the prompt, buttons, inputs, live text) via a `ui.render` hook, and can do background work between conversational turns via `$.clock.every`/`after` timers. This is a genuine capability gap closed since claudingtin's architecture was designed — nothing like it existed before.

But two hard findings argue against restructuring the companion onto it **now**:

1. **No documented native websocket/duplex-socket API.** Mods expose only one-shot `$.http.fetch` and MCP-server calls for networking. There is a plausible, but unconfirmed, path via `$.process.spawn` (an async-generator primitive that streams a long-lived child process's output with no documented timeout) to host a helper process holding the real websocket — but the canonical TypeScript type declarations on Anthropic's own GitHub repo don't even list `process.spawn` yet, and no builder account of anyone actually doing this was found anywhere. claudingtin's core mechanic — an instant, asynchronous "you've been matched" push while the user's own Claude is still thinking — sits exactly on this unresolved edge.
2. **The feature is five days old at time of research**, explicitly labeled "early access" with an API Anthropic says "may change between releases without notice," and it already has open, maintainer-acknowledged bugs where mod UI silently fails to render at all on several surfaces (VS Code extension, Remote Control desktop/iOS viewers) within days of launch.

**Recommendation:** Do not restructure the companion onto mods today. Prototype the specific unresolved question — a mod spawning a long-lived background process that reacts to external async events and redraws UI without polling — as a small throwaway spike, not a production rewrite. Revisit in 4–8 weeks once the API stabilizes past early access and the `process.spawn` type surface either lands in the public `.d.ts` or gets a documented example. The terminal-multiplexer placement strategy already shipped (Epic 1 Story 1.8) remains the right production architecture for now; mods are a promising enhancement to *layer alongside* it later (e.g., a lightweight in-session status indicator), not a wholesale replacement.

## Landscape & maturity

The official Anthropic feature is **"mods"** — not "plugins" generically. A mod is a plugin category with a `hooks.json` pointing to a JS/TS hooks module; it registers event handlers via `register(on)`, and one event, `ui.render`, fires whenever Claude Code is about to draw a "render site" (`Pane`, `AbovePrompt`/band, `Spinner`, message rows, tool-call rows, the question dialog, status lines) `[1][9]`.

It shipped **October 1, 2026** in Claude Code **v2.1.287** `[6][21][22]` — requiring that version or later (`claude --version` to check) `[5]`. At time of this research (2026-10-06/07) it is **five days old**. Anthropic's own GitHub README labels it **"early access"** and states plainly that "the API these mods are written against may change between releases without notice"; the official docs independently echo this, telling readers to "trust these [generated type] files over any page, this one included, when they disagree" `[7][5]`. Two incremental releases already followed within the week — v2.1.288 fixed "several mod bugs" and added a new API method; v2.1.289 closed permission gaps; v2.1.290 expanded capabilities and fixed a pane redraw bug `[6][21]`.

What this newly makes possible in the CLI specifically: before mods, a plugin could only add slash commands, static text output, tool-call-intercepting hooks, skills, or MCP servers — none of which could draw a persistent, stateful, interactive region of the screen. Mods add a true render-and-event-loop UI system: open a sidebar pane or an above-prompt band, draw buttons/inputs/markdown/raster graphics, receive input via callbacks, redraw on a timer, and persist state across reloads (`$.state`) or across sessions on disk (`$.store`, machine-shared, capped at 4 MiB) `[1][4]`. This is explicitly scoped to the Claude Code CLI and the Desktop app's "Code" tab — the docs never mention claude.ai web "artifacts," which are an unrelated, separate rendering system `[1]`.

**Caveat on sourcing:** the exact October 1 date and version number are corroborated independently by Anthropic's own GitHub release notes, two independent third-party technical blogs (the-decoder.com, blogs.eagentix.com), and a social-media post — strong agreement across unrelated publishers `[6][9][21][22]`. One secondary claim — that mods require an opt-in environment variable (`CLAUDE_CODE_ENABLE_FUNCTION_HOOKS`) — could not be verified against either primary source fetched and is flagged as an unverified belief, not reported as fact.

## Integration & interoperability

A mod draws entirely **in-process** — there is no pty-attach, iframe/webview, or separate-process rendering model. The one escape hatch, `Client`, is "a region drawn by a second file of yours, for animation and pointer input," which gets no mods API and reaches the mod's hooks only by posting data that arrives as a `ui.message` event `[1]`.

**Lifecycle — this is the one genuinely good fit for claudingtin's needs:** a mod's hooks module "runs for the length of a session," not torn down between turns. `$.clock.every`/`$.clock.after` let it do work "outside any event, so it keeps running between turns and doesn't start one" — it can update the UI without a conversational turn via `$.ui.status`/`toast`/`log`, and can even proactively start a turn via `$.prompt.submit`, which "waits until the session is idle." `session.start` fires once per loaded mod, but *not* after `/clear`, `/resume`, or `/branch` `[1][2][4]`.

**Networking — this is the real gap.** The only documented networking primitive is `$.http.fetch`, a one-shot request/response call, plus `$.mcp.call`/`connect` to an MCP server the plugin's own manifest lists `[2][4]`. No websocket, SSE, or persistent-socket API is documented anywhere. A targeted round-2 follow-up chased the one plausible workaround — `$.process.spawn`, described as streaming "a long-running command's output" and explicitly categorized by the official reference page as an **async-generator hook**, the same category as the model's own streaming `turn.step` event, with no documented timeout (unlike `$.process.run`'s 30s default/10min max) `[2][4][27]`. A third-party changelog aggregator independently corroborates that `$.process.spawn` is a real, actively-iterated shipped API, not a documentation artifact `[29]`. That pattern (`yield* next(e)`) is a genuine push mechanism for `turn.step`, and the docs group `process.spawn` identically — suggesting a mod *could* be woken the instant a long-lived child process (e.g. a helper binary holding the real websocket) produces new output, rather than only polling it on a timer.

But this is not confirmed. The canonical TypeScript type declarations on Anthropic's own `main` branch — the one source the docs themselves call authoritative when it disagrees with prose — **contain no `process.spawn` entry at all**, only `process.run`; a full-text grep across all 13,186 lines found nothing `[28]`. One independent builder, investigating the exact same question for an unrelated project, confirmed only the negative half: "A mod cannot listen on a port or socket. It only has outbound calls... `$.process.spawn`, which streams a child's stdout" — without reporting whether that streaming is push-based in practice `[30]`. No firsthand account of anyone actually running a long-lived background process inside a mod and observing real-time event delivery was found anywhere.

Permissions: a mod is **not sandboxed** and runs with the full permissions of the user running Claude Code — file system, process, and network access are gated only by the `$` namespace API surface, not by an isolation boundary; a process a mod spawns runs *outside* Claude Code's own sandboxing feature even when that feature is otherwise enabled `[1][3][4]`. A mod can also approve a tool call before the user is asked, and bypass some deny rules under documented admin conditions.

## Architecture patterns in practice

Concrete examples exist but the ecosystem is thin and mostly secondhand, as expected five days post-launch. Anthropic's own first examples are **"Token Weather"** (a `Pane`/`AbovePrompt` status surface driven by `tool.call` hooks) and **"You Should Know"** (a monitoring agent that surfaces alerts about overlooked information) `[9][10]` — neither independently confirmed against its own repo. The most detailed community account found is **ruview-live**, a tabbed Wi-Fi sensor dashboard (sparklines, a half-block heatmap, an animated braille radar) built as a real Mods application `[10]`.

Community-converged patterns: use `AbovePrompt` as the first UI target since nothing draws there by default; multiple mods can compose output in that band via `await next(e)` middleware chaining `[10][14]`. Reported gotchas, all from a single detailed builder field-guide (medium confidence, uncorroborated elsewhere): module-level JS variables reset on every hot reload, so timers and state must live in `$.state`; a pane only docks full-height on terminals ≥110 columns, otherwise falling back to a cramped inline mode that can clip content (a builder reports losing an entire row of buttons this way); opening a pane does not by itself grant keyboard focus — `focus`, `closeOnEscape`, and `holdToasts` must be set together, with hotkeys mirrored as slash-command fallbacks `[10]`.

**The cross-surface rendering gap is the most load-bearing finding in this section**, because it is not a blog claim but a primary source: Anthropic's own issue tracker already has open, specific bug reports — filed within days of launch — that mod UI (`Pane`/`AbovePrompt`) renders correctly in the local terminal but **does not appear at all** when the same session is viewed through Remote Control on the Claude desktop or iOS apps (#99217), and the VS Code extension's chat panel draws **no mod UI at all** — confirmed by a maintainer's own code inspection finding zero render-event handlers in that extension (#99045, #99423) `[11][23][24]`. A further open feature request confirms mods cannot yet embed an arbitrary web page/iframe (`WebView`) — the element set is a closed, Anthropic-defined list (`Box`, `Text`, `Button`, `Link`, `Code`, `Markdown`, `Input`, `Select`, `Svg`, `Client`, `Raster`, `Image`) `[25]`.

As a contrast case: several well-known community "Claude Code dashboards" already exist (e.g. `claude-code-dashboard`, `Claude-Code-Agent-Monitor`) but are explicitly **not** rendered in-session — they're separate web servers reading Claude Code's local log files, served to their own browser UI. That is architecturally the same "separate process" pattern claudingtin uses today via tmux; it is evidence of what builders reach for when they need something mods doesn't yet do well, not an example of the native mechanism `[15][16][17]`.

## Implementation reality

No separate SDK is required beyond a plugin directory (`plugin.json`, `hooks/hooks.json`, a JS/TS module); the dev loop is `claude --plugin-dir` for hot reload, `claude plugin validate`, and `claude plugin test` `[2][4]`. The one detailed builder account available (medium confidence, single source) independently confirms the "early access, moves between releases" characterization and adds concrete pitfalls already covered above `[10]`.

**Cross-platform support is uneven by design, not by bug**, per Anthropic's own "Where mods run" table: full rendering only in the CLI terminal (including editor-integrated terminals) and the Desktop app's Code tab; hooks run but **nothing draws** in the VS Code extension's chat panel, `claude -p`, the Agent SDK, or cloud sessions; mods don't load at all in WSL sessions in the Desktop app `[3]`. This is compounded by the open bugs already noted (§ Architecture patterns) showing even the *documented* surfaces aren't fully reliable yet.

**Throughput limits relevant to a live chat-style UI:** `$.ui.invalidate('ui.render')` is throttled to 10/sec generally, 30/sec in the terminal for the visible pane/band; a hook's own synchronous execution is capped at 10 seconds (50ms for the `prompt.edit` event specifically) — any live/streaming logic has to live in a background timer or process, never inline in the hook call `[4]`. One builder reports a separate `$.ui.blit` animation path supporting up to 120 frames/sec for pure redraw, distinct from state invalidation `[10]`.

A prior Anthropic GitHub feature request from **April 2026** — "Plugin API for custom animated UI components" — sat open and was closed as *not planned* only months before mods shipped `[26]`. This confirms mods are a genuinely new capability, not the maturation of something long-promised; there is correspondingly no multi-year track record to draw on.

## Cross-dimension insights

- **The maturity finding and the networking finding compound each other.** A five-day-old, explicitly-unstable API would already argue for caution on its own; that the one capability claudingtin's architecture most needs — a background process reacting to an async network event — sits on the single most *under-documented* corner of that API (the type declarations don't even list the method the docs describe) is a second, independent reason for the same caution, not a repeat of the first.
- **The cross-surface rendering gaps reframe what "restructure the frontend" would even mean.** claudingtin's companion today works identically regardless of which terminal/emulator hosts the pane. Moving to mods would, as of this research, make the chat surface *invisible* the moment a user views their session through VS Code, Remote Control, or (per the docs' explicit scoping) claude -p/Agent SDK/cloud sessions — a regression in reach, not just a risk to validate.
- **The permission-model finding changes the risk profile of the rewrite itself, independent of whether it technically works.** Today's companion is a separate OS process the user can inspect, kill, or sandbox independently of Claude Code. A mod runs *inside* Claude Code with the user's full permissions and no isolation — folding the companion into a mod would mean a crash, infinite loop, or bug in companion code can now directly affect the host Claude Code session's stability and trust boundary, which is a different category of blast radius than today's separate-process design.

## Recommendations

- **Do not restructure claudingtin's companion onto mods now.** *Confidence: high — rests on two independently-corroborated findings (maturity/stability, no documented websocket) that hold regardless of how the one disputed claim (`process.spawn` push semantics) eventually resolves.*
- **Run a small, explicitly throwaway spike — not a production branch — to resolve the one open question:** have a mod call `$.process.spawn` on a tiny helper binary that holds a real persistent connection (even just a local TCP echo server) and confirm empirically whether new output wakes the mod immediately or only on the next `$.clock.every` tick. This is cheap (a day, not a sprint) and is the single piece of evidence that would most change this recommendation. *Confidence basis: this exact gap is the one claim in the ledger marked `disputed`, not `verified`.*
- **Keep the existing terminal-multiplexer placement architecture (Epic 1 Story 1.8, the `windowterm.go`/mux-strategy registry) as the production path for the foreseeable future.** It already works uniformly across every surface a user might view a session from; mods currently do not. *Feeds: architecture spine — this is an operational constraint to record there if the team was considering scheduling mods work into a near-term roadmap.*
- **If the spike confirms push-based background delivery, treat a mods-based UI as a parallel, additive surface first** (e.g. a lightweight "searching…/matched" status indicator in `AbovePrompt`), not a replacement for the full chat relay — given the documented cross-surface gaps, shipping only on mods today would mean the product silently stops working for any user who ever views their session from VS Code, mobile Remote Control, or a cloud/headless run.
- **Set a revisit trigger, not a calendar date:** re-evaluate once mods drop "early access" status, or once `process.spawn` appears in the canonical `claude-code.d.ts` with a documented type, whichever comes first — both are concrete, checkable signals rather than a guessed timeline.

## Open questions

- **Does `$.process.spawn` deliver output to a mod as a true push (wakes the hook the instant new data arrives) or only via polling a buffer?** The official docs' own categorization (async-generator, same class as streaming `turn.step`) suggests push; the canonical type declarations omit the method entirely; no firsthand builder account was found either way. *What would resolve it: the spike described above, or Anthropic shipping the type and a documented example.*
- **What exactly happens to a mod's open pane across a full `claude` process exit and restart** (as opposed to `/clear`/`/resume`/`/branch`, which are documented)? Inferred from session-lifecycle docs but not directly stated. *What would resolve it: a direct test, or a docs clarification.*
- **Is there any roadmap signal for a native websocket/push-socket primitive, or for WebView/iframe support?** An open feature request exists for the latter (#99213) but had no visible Anthropic response at time of access. *What would resolve it: watching that issue, or Anthropic's own changelog.*

## Source appendix

| # | Claim(s) supported | Publisher | Pub. date | Accessed | Confidence |
|---|---|---|---|---|---|
| [1] | [Draw in the interface with a mod](https://code.claude.com/docs/en/plugins/mods/interface) — feature identity, element/API surface, lifecycle, CLI-vs-other-surface scoping | Anthropic (official docs) | undated/current, v2.1.290 | 2026-10-06 | high |
| [2] | [Use the mods API](https://code.claude.com/docs/en/plugins/mods/api) — `$.process.run`/`spawn`, `$.clock`, networking primitives | Anthropic (official docs) | undated/current | 2026-10-06 | high |
| [3] | [Mods overview](https://code.claude.com/docs/en/plugins/mods/overview) — "where mods run" table, sandboxing/trust posture | Anthropic (official docs) | undated/current | 2026-10-06 | high |
| [4] | [Mods reference](https://code.claude.com/docs/en/plugins/mods/reference) — limits table, event groupings, element set | Anthropic (official docs) | undated/current | 2026-10-06 | high |
| [5] | [Create a mod](https://code.claude.com/docs/en/plugins/mods/create) — version requirement, trust/permission gating | Anthropic (official docs) | undated/current | 2026-10-06 | high |
| [6] | [anthropics/claude-code releases](https://github.com/anthropics/claude-code/releases) — v2.1.287/288/289/290 shipped dates and changes | Anthropic (GitHub) | 2026-10-01 to 2026-10-05 | 2026-10-06 | medium (one year-rendering inconsistency via summarizer, resolved by cross-source corroboration) |
| [7] | [anthropics/claude-code mods README](https://github.com/anthropics/claude-code/tree/main/mods) — "early access" labeling | Anthropic (GitHub) | current | 2026-10-06 | high |
| [9] | [The Decoder: Claude Code's new Mods system](https://the-decoder.com/claude-codes-new-mods-system-lets-developers-rewrite-the-ai-coding-tool-from-the-inside) — independent corroboration of feature, examples, sandboxing posture | the-decoder.com (independent tech press) | ~Oct 2026 | 2026-10-06 | medium |
| [10] | [Community field-guide gist (ruvnet)](https://gist.github.com/ruvnet/a485e930b148185197fc53fd38b429ea) — builder patterns, gotchas, ruview-live example | independent builder | undated, recent | 2026-10-06 | medium |
| [11] | [anthropics/claude-code issue #99217](https://github.com/anthropics/claude-code/issues/99217) — Remote Control rendering gap | Anthropic GitHub issue tracker (primary, user-filed) | ~Oct 2026 | 2026-10-06 | high |
| [14] | [Anerco/claude-code-effort-cycle issue #2](https://github.com/Anerco/claude-code-effort-cycle/issues/2) — AbovePrompt composition pattern | independent GitHub repo | recent | 2026-10-06 | medium |
| [15] | [developer-hasm/claude-code-dashboard](https://github.com/developer-hasm/claude-code-dashboard) — contrast case, separate-process dashboard | independent maintainer | recent | 2026-10-06 | high |
| [16] | [hoangsonww/Claude-Code-Agent-Monitor](https://github.com/hoangsonww/Claude-Code-Agent-Monitor) — contrast case | independent maintainer | recent | 2026-10-06 | high |
| [17] | [netil/oh-my-hi](https://github.com/netil/oh-my-hi) — contrast case | independent maintainer | recent | 2026-10-06 | high |
| [21] | [eagentix blog](https://blogs.eagentix.com/2026/10/02/claude-code-mods-what-changed-who-it-affects-how-to/) — independent corroboration of launch date/version | blogs.eagentix.com (independent) | 2026-10-02 | 2026-10-06 | medium |
| [22] | [Superpower Daily](https://superpowerdaily.com/posts/anthropic-adds-claude-code-mods-that-can-rewrite-prompts-and-replace-built-in-features) — independent corroboration | superpowerdaily.com (independent) | ~Oct 2026 | 2026-10-06 | medium |
| [23] | [anthropics/claude-code issue #99045](https://github.com/anthropics/claude-code/issues/99045) — VS Code extension draws no mod UI | Anthropic GitHub issue tracker | ~Oct 2026 | 2026-10-06 | high |
| [24] | [anthropics/claude-code issue #99423](https://github.com/anthropics/claude-code/issues/99423) — VS Code extension, corroborating | Anthropic GitHub issue tracker | ~Oct 2026 | 2026-10-06 | medium |
| [25] | [anthropics/claude-code issue #99213](https://github.com/anthropics/claude-code/issues/99213) — WebView/iframe not supported | Anthropic GitHub issue tracker | ~Oct 2026 | 2026-10-06 | high |
| [26] | [anthropics/claude-code issue #52235](https://github.com/anthropics/claude-code/issues/52235) — closed-not-planned, pre-mods history | Anthropic GitHub issue tracker | opened 2026-04-23 | 2026-10-06 | high |
| [27] | [Mods events guide](https://code.claude.com/docs/en/plugins/mods/events) — async-generator pattern for streaming hooks | Anthropic (official docs) | undated/current | 2026-10-06 | high |
| [28] | [claude-code.d.ts (main, raw)](https://raw.githubusercontent.com/anthropics/claude-code/main/mods/types/claude-code.d.ts) — canonical type declarations, `process.spawn` absent | Anthropic (GitHub, primary) | last touched pre-2026-10-01 | 2026-10-06 | high (on the observation itself) |
| [29] | [gradually.ai changelog aggregator](https://www.gradually.ai/en/changelogs/claude-code/) — `$.process.spawn` iteration corroboration | gradually.ai (secondary aggregator) | current | 2026-10-06 | medium |
| [30] | [sleeyax/paseo-plugins issue #111](https://github.com/sleeyax/paseo-plugins/issues/111) — independent builder confirming no inbound socket | independent builder (sleeyax) | 2026-10-03 | 2026-10-06 | high (on the quoted constraint) |

## Staleness map

All six ledger claims are freshly dated (2026-10) with no stale entries yet. Given the pack's AI-adjacent landscape bar (≤3 months) and this feature's own "early access, changes without notice" status, five of six claim classes (feature identity, lifecycle model, API surface, permission model, process-spawn semantics) were conservatively windowed at **1 month** rather than the pack default — this is a fast-moving, days-old feature, not a settled landscape claim. The cross-platform/rendering-gap class was windowed at 6 months (ecosystem signal).

**Earliest re-check date: 2026-11-01.** Before that date, re-verify in priority order: (1) whether `process.spawn` has landed in the public type declarations, (2) whether mods has dropped "early access," (3) whether the VS Code/Remote Control rendering gaps have closed.
