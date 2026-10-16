# Digest: Integration & Interoperability — Round 2, Assistant 1 (targeted follow-up)

**Follow-up question:** Can `$.process.spawn` feed push-based async events back into a mod (vs. only polling), i.e. is companion's "background process reacting instantly to async network events" architecture viable under mods?

## Core answer

**Push-based (yield-driven), not poll-only — with one notable documentation/source-of-truth discrepancy that was not fully resolved.**

The two official docs pages (reference + API guide) describe `$.process.spawn` as fundamentally different from `$.process.run`: `run` is a one-shot, buffered call bound by a 30s/10min timeout, while `spawn` "streams a long-running command's output" and is explicitly classified as an **async-generator hook**, in the same category as `turn.step` (which the events guide shows streaming model output via `yield* next(e)`, suspending the consumer until the next chunk arrives — genuine push, not a buffer poll). There is no documented timeout on `spawn` in the limits table, consistent with it being meant for indefinite-lifetime children. However, the canonical `mods/types/claude-code.d.ts` file on the `main` branch, fetched directly, contains **no `process.spawn` at all** — only `process.run` — which undercuts full certainty about the exact type signature/event payload for spawn's streamed chunks.

No native inbound websocket/duplex-socket API exists regardless — a builder (sleeyax) confirms directly: "A mod cannot listen on a port or socket. It only has outbound calls."

## Claims

1. The official mods reference page states: "Events are grouped by what they concern... Hooks on `turn.step` and `process.spawn` are async generators, and other hooks are async functions."
   Source: https://code.claude.com/docs/en/plugins/mods/reference — Anthropic official docs (v2.1.290) — accessed 2026-10-06 — confidence: high (direct quote) — class: event-model

2. The mods API guide states: "`$.process` | `run(['git', 'status'])` starts a command and resolves when it exits. `spawn` streams a long-running command's output." — distinguishing spawn's streaming model from run's one-shot/timeout-bound model.
   Source: https://code.claude.com/docs/en/plugins/mods/api — Anthropic — accessed 2026-10-06 — confidence: high — class: process-spawn-semantics

3. The events guide shows the concrete async-generator pattern for the sibling event `turn.step`: `on('turn.step', async function* ($, e, next) { const result = yield* next(e) ... })` — "Write a `turn.step` hook as an async generator, because the event streams. `yield* next(e)` forwards the response as it streams and evaluates to the finished result." By the reference page's explicit grouping (claim 1), `process.spawn` hooks follow the identical mechanism — meaning a mod is suspended on `yield` until the child process's next output chunk exists, i.e. a push/wake rather than a poll.
   Source: https://code.claude.com/docs/en/plugins/mods/events — Anthropic — accessed 2026-10-06 — confidence: medium-high (mechanism demonstrated for `turn.step` directly; application to `process.spawn` is by the reference page's explicit same-category statement, not a `process.spawn`-specific code sample) — class: event-model

4. The limits table lists a timeout only for `$.process.run` ("30 seconds by default, 10 minutes at most") and lists no corresponding timeout for `$.process.spawn`, consistent with spawn supporting a genuinely long/indefinite-lived child.
   Source: https://code.claude.com/docs/en/plugins/mods/reference (Limits section) — Anthropic — accessed 2026-10-06 — confidence: high (absence directly observable in the table) — class: process-spawn-semantics

5. **Discrepancy flagged:** the canonical TypeScript declarations file (`mods/types/claude-code.d.ts`, `main` branch, fetched live) defines `process: { run: (argv, init) => Promise<ProcessRunResult> }` and **contains no `spawn` method, no `ProcessSpawn*` type, and no `process.spawn` event entry anywhere in the file** (verified via full-text grep across all 13,186 lines). The file's last commit touching it predates the Oct 1, 2026 release that shipped `$.process.spawn` per the changelog. The docs page itself preemptively explains this kind of gap: "The copy on GitHub can be older than the Claude Code version you have installed. When the two disagree, trust the copy Claude Code writes for your version" — flagged as a sync-lag discrepancy, not proof spawn doesn't exist, but it means the exact type signature/event payload for spawn's streamed chunks could not be independently confirmed from the one fully-authoritative source.
   Source: https://raw.githubusercontent.com/anthropics/claude-code/main/mods/types/claude-code.d.ts (fetched and grepped directly) — Anthropic GitHub repo — accessed 2026-10-06 — confidence: high on the observation itself; the interpretation (sync lag, not absence) is the docs' own stated caveat — class: process-spawn-semantics

6. A third-party changelog aggregator records, under Claude Code v2.1.287/v2.1.290: "Changed what a mod's `$.process.spawn` rejects with when another mod denies it after the child ran: it now says the call ran and a plugin withheld its result" — independent corroboration that `$.process.spawn` is a real, shipped, actively-iterated API.
   Source: https://www.gradually.ai/en/changelogs/claude-code/ — third-party changelog aggregator (secondary) — accessed 2026-10-06 — confidence: medium — class: process-spawn-semantics

7. **Builder account:** sleeyax, in a GitHub issue proposing to replace a different tool's transcript-polling/keystroke-driving approach with a Claude Code mod: "Claude Code 2.1.287 added mods: JavaScript or TypeScript event hooks that a plugin loads into the Claude process itself," and specifically: "A mod cannot listen on a port or socket. It only has outbound calls: `$.http.fetch` (TCP, or a Unix socket via `socketPath`), with a hard 30s timeout per request (measured). `$.process.spawn`, which streams a child's stdout."
   Source: https://github.com/sleeyax/paseo-plugins/issues/111 — independent builder, posted 2026-10-03 — accessed 2026-10-06 — confidence: high on the quoted constraint (no inbound socket); the issue does not itself report empirical push-vs-poll behavior of spawn's stdout delivery, and had no comments. — class: builder-account

## Leads (not run down — worth a follow-up pass for more certainty)
- `$.process.spawn`'s precise consumer-facing type (does a direct `await $.process.spawn(...)` call also return an `AsyncIterable<{stdout?, stderr?}>`?) — not found in any fetched source. `claude plugin create#get-the-types-for-your-build` is referenced as authoritative per-version truth but requires a live v2.1.287+ installation to extract.
- GitHub issues #91870, #99767 touch adjacent extensibility gaps but do not discuss `process.spawn` push/poll semantics specifically.
- No issue/discussion explicitly framed as "mods need a push-based process output event" was found — this specific complaint does not appear to exist publicly as of the access date.

## Could not find
- A firsthand builder account that actually spawned a long-lived process (websocket client, `tail -f`, persistent connection) inside a mod and empirically reported whether new stdout lines triggered immediate hook re-entry/UI redraw versus requiring a `$.clock.every` poll of a buffer.
- The exact TypeScript signature for `process.spawn` (return type, stdout/stderr event shape) — absent from the only primary-source `.d.ts` file reachable (GitHub `main`, which itself appears to lag the shipped version per the docs' own disclaimer).
- Any GitHub issue explicitly naming the gap this research was checking for (push-based process-output callback vs. polling).

## Sources
- [Mods reference](https://code.claude.com/docs/en/plugins/mods/reference)
- [Mods API guide](https://code.claude.com/docs/en/plugins/mods/api)
- [Mods events guide](https://code.claude.com/docs/en/plugins/mods/events)
- [claude-code.d.ts (main, raw)](https://raw.githubusercontent.com/anthropics/claude-code/main/mods/types/claude-code.d.ts)
- [gradually.ai Claude Code changelog aggregator](https://www.gradually.ai/en/changelogs/claude-code/)
- [sleeyax/paseo-plugins issue #111](https://github.com/sleeyax/paseo-plugins/issues/111)
