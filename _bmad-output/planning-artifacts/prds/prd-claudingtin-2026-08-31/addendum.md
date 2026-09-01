# Addendum — Claude Think-Time Chat Roulette

Depth that informs the PRD but belongs downstream (architecture, safety design, UX spec).
Sources cited inline. Some items are fast-moving — re-verify before building.

---

## 1. Prior-art and moderation research (web, 2026-08-31)

### Random-stranger 1:1 chat precedents

- **Chatroulette (2009–)** nailed the zero-friction loop (instant, no signup, "next" button)
  but never solved unsolicited nudity. One bad first session permanently lost users —
  disproportionately women and minors. Self-attested "16+" gate. AI nudity detection (2020)
  helped but introduced false-ban bias (skin tone, lighting). Lesson: anonymity + weak age
  assurance + reactive-only moderation = reputational death spiral.
  ([techdirt](https://www.techdirt.com/2021/02/24/content-moderation-case-study-chatroulette-leverages-new-ai-to-combat-unwanted-nudity-2020/),
  [vice](https://www.vice.com/en/article/chatroulette-moderation-penis-problems/))
- **Omegle (2009 – Nov 8 2023)** — closest analog (random pairing, text option). Shut down as
  a **settlement term** in *A.M. v. Omegle* (C.A. Goldberg, ~$22M product-liability / sex-
  trafficking suit; 11-year-old paired with an adult predator). The Oregon court (July 2022)
  **denied Section 230 immunity**, allowing negligent-design and failure-to-warn claims on the
  theory that the **matching feature itself is a defective product**. Founder cited financial
  and psychological unsustainability, plus app-store / payment-processor / advertiser pressure.
  Omegle reported ~600,000 CSAM incidents in 2022.
  ([blog.ericgoldman.org](https://blog.ericgoldman.org/archives/2022/07/omegle-denied-section-230-dismissal-am-v-omegle.htm),
  [cagoldberglaw.com](https://www.cagoldberglaw.com/ca-goldberg-pllc-requires-omegle-com-to-shut-down-forever-as-settlement-term-in-product-liability-lawsuit-for-sex-trafficking-our-client/),
  [npr](https://www.npr.org/2023/11/09/1211807851/omegle-shut-down-leif-k-brooks))
- **Monkey** pulled from Apple App Store over moderation; **Emerald Chat** sells "24/7 human
  moderators." Current video-centric roster (OmeTV, Chatrandom, Bazoocam, Chatspin, Paltalk,
  Tinychat) changes quarterly. **Text-only stranger chat is an underserved niche.**
- **Yik Yak** died in 2017 of the same anonymity-without-governance pattern (harassment, hate,
  threats). Its relaunch added downvote-to-remove.

### Trust & safety patterns

- **Age assurance is the regulatory shift.** UK Online Safety Act / Ofcom requires "Highly
  Effective Age Assurance" for services with adult/harmful content since **25 July 2025** — a
  checkbox is explicitly *not enough*; methods must resist a "determined child." EU (DSA)
  trending the same way through 2026. Privacy-preserving age-estimation services exist (return
  only a yes/no, store no DOB).
  ([ofcom](https://www.ofcom.org.uk/online-safety/protecting-children/age-checks-to-protect-children-online),
  [natlawreview](https://natlawreview.com/article/you-must-be-tall-click-online-safety-act-and-age-appropriate-access))
- **Reactive moderation** (user reports + post-hoc review) is the realistic model for a small
  project; proactive scanning needs scale. Baseline expectations: documented guidelines,
  in-flow report/block, immediate disconnect, rate limits, keyword filters, fast bans for
  threats / hate / CSAM.
  ([getstream](https://getstream.io/glossary/reactive-moderation/),
  [sendbird](https://sendbird.com/blog/content-moderation-strategy))
- **CSAM legal floor (US):** 18 U.S.C. §2258A — no duty to proactively monitor, but on
  **actual knowledge** you must report to NCMEC "as soon as reasonably possible." If media/links
  are ever allowed, hash-matching is the baseline; open-source options are Meta's **PDQ**
  (photo) / **TMK+PDQF** (video); PhotoDNA/NCMEC hash sets are gated to registered platforms.
  ([technologycoalition.org](https://technologycoalition.org/wp-content/uploads/CSAM-Identification_Reporting_R3-1.pdf))
- **Abuse vectors specific to this concept:** the dating-adjacent framing attracts solicitation;
  the v2 "missed connections" board is the highest-risk surface (persistent, public, doxxing /
  harassment vector) — needs pre-publication moderation or heavy filtering.
- **What a small OSS project can realistically do:** text-only (no media, no live links);
  18+ ToS with an *honest risk warning* (failure-to-warn was an Omegle claim); ephemeral by
  default (little to subpoena); server-side rate limiting + keyword filter; one-tap report that
  captures only the last N messages of *that* transcript; instant block/ban by account key
  (best-effort — a UUID is regenerable); published guidelines; documented NCMEC path; do
  **not** run profile-biased "pull-in"
  matching without age controls.

### Ephemeral vs re-connect ramp

- Snapchat's thesis: messages that disappear **lower social stakes** and remove "permanent
  record" performance pressure — a strong fit for users who fear judgment.
  ([medium](https://medium.com/design-bootcamp/snapsnapchat-the-psychology-of-ephemeral-content-c72be20f5b82))
- **Craigslist Missed Connections** worked on serendipity + volume + free; failed on
  one-directional hope, low hit rate, and creep/spam drift over time.
  ([getmaude](https://getmaude.com/blogs/themaudern/a-brief-history-of-craigslist-missed-connections/))
- **Opt-in re-connect that converts:** user-initiated beats auto-triggered. Default to nothing
  persisting; surface "keep this?" only on a **mutual** signal (both opt in), never unilaterally
  — which also matches the "my Claude came back" no-rejection framing and kills most harassment
  re-contact. Make the shareable session ID / board post an explicit *second* action.
  ([eleken](https://www.eleken.co/blog-posts/sign-up-flow))

### Cold-start for real-time matching pools

- Liquidity = P(match within a reasonable wait); for a real-time pool **concurrency matters
  more than total registered users**.
- Plays: **shrink the market to one community** (Tinder-at-USC, dorm by dorm); **power-hour
  events** ("chat night, Thu 8pm ET") to concentrate a thin pool; **pre-build the audience**
  (existing Claude/AI Discord, subreddit, the plugin's own GitHub following); **single-player
  fallback** for an empty queue — prompt library, "leave a note for the next person" (async →
  the missed-connections board doubles as the liquidity buffer), or an opt-in bot.
- **FIFO roulette is correct for a thin pool** — any matching algorithm needs volume to beat
  random and adds legal surface.

---

## 2. Technical feasibility — Claude Code plugin (OQ-1 investigation, 2026-09-01)

**Verdict: buildable, with constraints. Ships as a plugin-bundled companion TUI, not a pure
in-conversation pane.** (Fast-moving area — re-verify against current docs before building.)

### The timing problem and its fix

- **No hook for a thinking block starting/ending.** Only `UserPromptSubmit` (≈ turn begins)
  and `Stop` (≈ turn ends) bracket the busy window; `PreToolUse`/`PostToolUse` bracket tool
  calls. `Stop` reliably fires on turn end including on ESC/interrupt; `SubagentStop` is a
  separate event for subagents.
  ([hooks](https://code.claude.com/docs/en/hooks.md))
- **Those hooks are too weak to drive the UI:** `UserPromptSubmit` hooks are capped at ~30s
  (can't hold a pane open), and `Stop` fires *after* the turn (too late to open a chat for
  the think-time). There is no mid-turn hook.
- **Fix:** a long-lived **companion process** (launched at `SessionStart`) **watches the
  session transcript file** and detects turn start/end itself — no hook deadline, no
  mid-turn gap. Hooks stay as a secondary signal only. Residual: transcript file
  location/format stability, parse edge cases.

### The UI problem and its fix

- **Hooks are shell commands that run and exit — they cannot hold a UI.** ([hooks](https://code.claude.com/docs/en/hooks.md))
- **Nothing can push unsolicited text into the Claude Code TUI mid-turn.** MCP `list_changed`
  notifications, MCP channel messages, and plugin *monitors* all surface *to the model* (as
  something Claude then processes, +1–3s), not to a UI pane. Statusline is read-only
  (display-only, no input). ([mcp](https://code.claude.com/docs/en/mcp.md),
  [statusline](https://code.claude.com/docs/en/statusline.md),
  [plugins](https://code.claude.com/docs/en/plugins.md))
- **Fix:** the chat lives **entirely in the companion's own pane** and never needs to reach
  the Claude TUI. The companion holds the websocket to the backend directly; inbound messages
  render in its pane in real time.

### Placement and packaging

- **tmux:** a hook/companion can detect tmux (`$TMUX`) and `tmux split-window` reliably on
  macOS/Linux. **No tmux** in VS Code / JetBrains integrated terminals or native Windows
  PowerShell (WSL2 can) → there the user opens/positions the pane manually. This is the
  accepted degradation.
- **Packaging is fine:** plugins can bundle a compiled binary (`bin/` is added to PATH),
  hooks/plugins can spawn long-lived **unsandboxed** processes and open sockets, and
  `monitors/` supports session-lifetime background processes.
  ([plugins](https://code.claude.com/docs/en/plugins.md),
  [plugins-reference](https://code.claude.com/docs/en/plugins-reference.md))
- **Real-time networking:** a bundled MCP server is a separate process and can hold a
  persistent socket; but per the UI problem above, the companion (not an MCP tool surface) is
  where the chat renders. An MCP tool MAY still be offered so the model can, e.g., report
  "your Claude is back" — optional.

### claude.ai

- **claude.ai has no hooks and no background UI** — the whole mechanism is Claude Code
  (/ Desktop) only. Confirms **v1 = Claude Code plugin**. (The v2 webapp is reconnect-only —
  PRD §10 — *not* a real-time UI home.)
- SSE transport for remote MCP is being deprecated in favor of streamable HTTP (early 2026).

### If it had been infeasible

The fallback would have been a fully standalone companion app the user runs and alt-tabs to.
The resolved design is close to that already — the difference is the plugin *bundles and
launches* the companion and *auto-places* it in tmux, rather than leaving it entirely to the
user.

### Identity / account-linkage — no stable account key is available to a plugin

Investigated because we wanted bans/blocks to survive a reinstall (OQ-3). Findings
(doc-cited):

- **Hook input JSON** carries only `session_id`, `prompt_id`, `transcript_path`, `cwd`,
  `permission_mode`, `hook_event_name`, `agent_id`/`agent_type` — **no user or account
  identity**. ([hooks](https://code.claude.com/docs/en/hooks.md))
- **No documented env var or API** gives a plugin an account id, email, org id, or the
  OAuth/API credential. `/status` shows the login email but only to the user, not to plugin
  code. ([authentication](https://code.claude.com/docs/en/authentication.md))
- `~/.claude/.credentials.json` exists but its schema is undocumented, it's mode `0600`, and
  the credential rotates when the user re-logs-in — unusable as a stable key.
- `ANTHROPIC_API_KEY`, when set, is stable and account-linked for that cohort — but most
  subscription (Pro/Max) users authenticate via OAuth and have no API key, and reading it
  from the environment is a mild security smell.
- **Even a client-computed `hash(identifier + salt)` is unverifiable server-side** — the
  backend receives a number it must trust. For an open-source client the salt is public, so a
  determined evader forges any key. Account-linkage would only have raised the bar against a
  *casual* plugin reinstall.

**Resolved (FR41):** account key = a random UUID persisted at a path *outside* the plugin's
own directory (an OS config dir), so a plugin reinstall/update keeps it. That captures the
one meaningful win (survive reinstall) with zero dependency on undocumented internals.
Motivated evasion (delete the id file / modified client) is accepted; keyword filter, rate
limits, and the small pooled community are the real anti-abuse layers.

---

## 3. Design decisions captured for downstream (not repeated in the PRD body)

- **Rejected direction — micro-consult / work-help marketplace.** Killed in brainstorming:
  needs too much personal + situational context; real human-help needs are too domain-specific
  ("everything breaks"). Product stays scoped to light serendipitous social chat.
- **Rejected — minigames / feature bloat.** Dead beats get lightweight tooltip nudges only
  (e.g. "why not edit your profile"), nothing more.
- **Per-match generated icebreakers** (built from both profiles) are likely too slow / costly to
  compute at match time. Pre-written rotating openers are the MVP fallback; revisit if
  generation gets cheap and fast.
- **Block primitive outranks tags and match criteria.** Blocking a specific person must never
  cost the user an interest tag or force a change to match criteria to escape someone. One
  per-person block primitive, separate from all preference settings.
- **Auto-block cooldown** to prevent small-pool repeats: rough sketch is ~1 minute together →
  auto-block for a day (temporary cooldown) to force variety. Exact anti-repeat rules for
  pull-in and for small pools need careful design beyond this sketch.
- **Load-bearing insight:** the "my Claude came back" social fiction is what makes every risky
  feature (ephemeral exits, disconnects, pull-in, dating-ish matching) feel safe rather than
  like rejection. Every exit path in the UX must route through it.
- **The commitment ramp is one continuous mechanism:** persist toggle → session ID → missed-
  connections board. Each step lets a user voluntarily escalate from "forget this person" to
  "actually connect," without ever forcing the choice.
- **Pull-in doubles as the cold-start fix:** matching against active non-waiting users keeps the
  queue from feeling empty in a small pool.
