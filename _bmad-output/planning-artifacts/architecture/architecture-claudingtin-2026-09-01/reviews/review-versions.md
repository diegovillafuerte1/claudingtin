# Adversarial Reality-Check — Stack & Technology Decisions

**Target:** `../ARCHITECTURE-SPINE.md` (§ Stack, § Structural Seed)
**Review date:** 2026-09-01
**Reviewer role:** verify every committed technology decision was researched against current reality, not asserted from training data.
**Method:** each row of the Stack table and each named deployment/packaging dependency checked on the web against upstream release feeds and vendor docs on 2026-09-01.

---

## Verdict: PASS WITH REVISIONS

The paradigm and the four load-bearing infrastructure choices hold up: **coder/websocket, modernc.org/sqlite, fsnotify, and Fly.io shared-cpu-1x + volumes** are all current, maintained, and fit their stated roles — verified, not just asserted.

Two things must change before the Stack table can honestly carry its "verified current 2026-09-01" header:

1. **The Bubble Tea row is a full major version stale and internally inconsistent** with the `bubbles` / `lipgloss` rows. `bubbletea v1.3.10` is the last release of the frozen v1 line; v2 has been stable since 2026-02-24.
2. **Two decisions are asserted rather than researched:** (a) Bubble Tea v1 vs v2 for a greenfield project, and (b) *how* the companion binary actually reaches the user's machine through a Claude Code plugin — bundling is supported for a git marketplace but carries a constraint the spine never states.

Nothing here invalidates the spine's invariants (AD-1…AD-13). The fixes are to the Stack table and to two sentences of the Structural Seed.

---

## Findings by severity

### HIGH

#### H1 — `bubbletea v1.3.10` is stale, mislabeled "current", and inconsistent with the bubbles/lipgloss rows

- **Pinned:** `charmbracelet/bubbletea` = `v1.3.10`; `charmbracelet/bubbles, lipgloss` = `current`.
- **Reality (2026-09-01):**
  - `bubbletea v1.3.10` shipped **2025-09-17**. It is the **final release of the v1 line.**
  - `bubbletea v2.0.0` went stable **2026-02-24**; current is **v2.0.9** (2026-08-19). The v1→v2 gap is a deliberate breaking rewrite (declarative `View` fields replacing imperative commands/program-options; new "Cursed Renderer"). Upgrade guides exist for humans and for LLMs.
  - `bubbles` current is **v2.2.1** (2026-08-24); a `v1.0.0` compatibility tag was cut 2026-02-10 for projects staying on bubbletea v1.
  - `lipgloss` current is **v2.0.6** (2026-08-11). (`lipgloss` v1.x remains usable standalone.)
  - Charm's own guidance: **"New projects should start with v2."**
  - `bubbles v2.x` depends on `bubbletea v2.x`. **You cannot combine `bubbletea v1.3.10` with `bubbles`/`lipgloss` "current" (= v2).** The table as written does not describe a buildable dependency set.
- **Why this matters here:** the header says "verified current 2026-09-01". For this row that claim is not true — it reads as a training-data value that was never re-checked. A greenfield TUI started now on the v1 line inherits a frozen dependency (Charm pushes only incidental bugfixes to v1 now that v2 is merged to `main`).
- **Also:** Charm introduced `charm.land` vanity import paths in 2026; v2 is importable as `charm.land/bubbletea/v2` as well as `github.com/charmbracelet/bubbletea` (module path carries the `/v2` suffix either way).
- **Recommended fix — pick one and state it:**
  - **(preferred) Commit to v2 across all three:** `bubbletea v2.0.9`, `bubbles v2.2.1`, `lipgloss v2.0.6`. Matches Charm's new-project guidance and keeps the three in a supported set.
  - **Deliberately stay on v1** (e.g. to reuse existing v1 knowledge / avoid the declarative-API port): then the table must say so explicitly and pin `bubbletea v1.3.10`, `bubbles v1.x`, `lipgloss v1.x` with a one-line rationale acknowledging the line is in maintenance-only.
  - Either way: replace "current" with exact minors, and drop or qualify "verified current" for this row.
- Sources:
  - https://github.com/charmbracelet/bubbletea/releases
  - https://github.com/charmbracelet/bubbles/releases
  - https://github.com/charmbracelet/lipgloss/releases
  - https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md
  - https://github.com/charmbracelet/bubbletea/discussions/1374
  - https://pkg.go.dev/charm.land/bubbletea/v2

---

### MEDIUM

#### M1 — How the companion binary reaches the machine is unstated, and the bundling path has a constraint the spine doesn't mention

- **Spine assumes** (Structural Seed source tree; AD-7): `plugin/` contains the "bundled companion binary", and the `SessionStart` hook spawns it with three args.
- **Reality (Claude Code plugin docs, 2026-09-01):**
  - A plugin **can** ship executables, and hooks can invoke them via `${CLAUDE_PLUGIN_ROOT}/...`. `SessionStart` is a real hook event and runs shell commands. So AD-7's mechanism is supported **for a git-based marketplace (GitHub)** — no documented `bin/`-style restriction there.
  - **Constraint not in the spine:** a **top-level `bin/` directory is rejected** by claude.ai *organization-settings* distribution — both marketplace sync and direct upload fail with `Plugin contains a top-level bin/ directory`. The spine's tree puts the binary under `plugin/` (not a top-level `bin/`), so GitHub distribution is fine, but **org-settings distribution of a plugin with a committed binary is foreclosed** unless the binary is fetched on first run instead.
  - **Weight:** the release flow cross-builds macOS arm64/x64, Linux, Windows. Committing *all* target binaries into the plugin repo multiplies plugin download size by ~4–5× (tens of MB per platform) on every plugin release. No documented plugin size cap, but it is a real distribution cost and an argument for download-on-first-run into a cache dir (a pattern larger plugins use; not a docs-blessed flow, so it needs its own small spike).
- **Recommended fix:** add one decision line to the Structural Seed — *"the plugin commits the per-platform companion binary under `plugin/<dir>/` (not `bin/`) and references it via `${CLAUDE_PLUGIN_ROOT}`; org-settings distribution is out of scope for v1"* — **or** *"the plugin downloads the pinned companion binary on first `SessionStart` into a cache dir"*. AD-7 depends on the binary being present when the hook fires; that precondition should be explicit.
- Sources:
  - https://code.claude.com/docs/en/plugins-reference
  - https://code.claude.com/docs/en/plugin-marketplaces
  - https://github.com/anthropics/claude-code/blob/main/plugins/README.md

#### M2 — `bubbles` + `lipgloss` = "current" is not a pinned decision

Covered by H1. "current" must become an exact minor consistent with the chosen Bubble Tea major. Flagged separately because the spine's own note ("Pin exact minors at project init") is not yet satisfied for these two rows, and "current" silently resolves to v2 today.

---

### LOW

#### L1 — `Go 1.26.x` is one major behind and mislabeled "verified current"

- **Reality (2026-09-01):** **Go 1.27.0 released 2026-08-19** (13 days before the spine date). Latest 1.26 patch is **go1.26.7** (2026-08-19). Go supports the last two majors, so 1.26 is still fully supported and receives security patches.
- **Assessment:** not wrong to run 1.26, but "verified current 2026-09-01" is inaccurate — 1.27.0 already existed. Also the row gives no minor ("1.26.x").
- **Recommended fix:** bump to `Go 1.27.x`, **or** keep 1.26 and add "(deliberately trailing latest by one major)". Pin the exact patch at init per the table's own note.
- Sources:
  - https://go.dev/doc/devel/release
  - https://go.dev/doc/go1.26

#### L2 — `modernc.org/sqlite` cross-compile to Windows: confirmed, but pin + CI-test it early

- **Reality:** latest **v1.57.0** (2026-08-19), ~monthly cadence, actively maintained by Jan Mercl. Pure-Go, **no cgo** — as the spine states. Windows is explicitly supported: **windows/amd64 and windows/386** (bundled SQLite 3.53.1; windows/386 since v1.31.0). Cross-compiles from a single dev machine because the transpiled amalgamation ships one generated Go file per GOOS/GOARCH.
- **Residual risk:** the supported GOOS/GOARCH set is **finite** — a target with no generated file simply won't build. The spine's release targets (darwin arm64, darwin amd64, linux amd64, windows amd64) are **all covered today**, but this is the one dependency where an upstream target-arch gap would be a hard blocker rather than an annoyance.
- **Recommended fix:** pin an exact `v1.57.x` at init and stand up the full cross-build matrix (`CGO_ENABLED=0`, all four targets, a real `INSERT`/`SELECT` smoke test) in CI in the first sprint — this is a natural companion to the existing AD-6 transcript-format spike.
- Sources:
  - https://pkg.go.dev/modernc.org/sqlite
  - https://gitlab.com/cznic/sqlite/-/tags
  - https://github.com/modernc-org/sqlite

#### L3 — `fsnotify` = "current": confirmed maintained; watch the atomic-rename quirk

- **Reality:** latest **v1.10.1** (2026-05-04); v1.10.0 (2026-04-29) ended a ~1-year quiet stretch, so maintenance is active again. Fits the transcript-watch role.
- **Residual risk (implementation, not spine):** fsnotify has no recursive watch, and editors/writers that save via atomic-rename require the watch to be re-added on the rename event. The companion tails a JSONL that Claude Code appends to — if CC ever rotates or rewrites that file, a naive single `Add()` will silently stop delivering events. Fold this into the AD-6 parser spike.
- **Recommended fix:** pin `v1.10.x`; note the rename/rotation handling as a parser-spike concern.
- Sources:
  - https://github.com/fsnotify/fsnotify/releases

#### L4 — `SessionStart` hook spawning a long-lived process needs explicit detachment

- Hooks are expected to run and return; `SessionStart` firing a persistent process works only if the companion is properly detached (new session/process group, stdio redirected) or it risks being reaped when the hook returns or the CC session ends. AD-7's "Prevents" note gestures at this but the detachment requirement isn't stated. Implementation detail — flag for the launch spike.
- Sources:
  - https://code.claude.com/docs/en/plugins-reference

---

## What checked out (verified, no change needed)

| Row / claim | Verified state on 2026-09-01 | Source |
| --- | --- | --- |
| `coder/websocket` `v1.8.15` | Exact match. Released 2026-06-15, stable (not prerelease). Latest in the line. | https://github.com/coder/websocket/releases |
| `coder/websocket` = maintained successor to `nhooyr.io/websocket` | Confirmed. Transfer from the original author to Coder formalized **August 2024**; repo actively maintained since (v1.8.14 2025-09, v1.8.15 2026-06). Idiomatic, `context.Context`-based, safe concurrent writes — fits the hub↔spoke JSON-over-WSS role. | https://coder.com/blog/websocket • https://github.com/coder/websocket |
| `modernc.org/sqlite` = pure-Go, no cgo, static binaries | Confirmed. v1.57.0 (2026-08-19), monthly cadence, maintained. `CGO_ENABLED=0` static builds, cross-compile from one machine. (Pin + CI-test per L2.) | https://pkg.go.dev/modernc.org/sqlite |
| Fly.io still offers `shared-cpu-1x` + volumes in 2026, within ~$10/mo | Confirmed. `shared-cpu-1x`: ~**$2.02/mo** (256MB), ~**$5.70–5.92/mo** (1GB). Fly Volumes still **$0.15/GB/mo** (billed even while the machine is stopped; volume snapshots first 10GB/mo free). One machine + one small volume + TLS terminated by Fly fits the budget. | https://fly.io/docs/about/pricing/ |
| Bundling an executable in a Claude Code plugin is supported | Confirmed for a **git-based marketplace**: `${CLAUDE_PLUGIN_ROOT}`-referenced binaries, hooks run shell commands, `SessionStart` exists. (Constraint + open decision per M1.) | https://code.claude.com/docs/en/plugins-reference |
| Fly single-instance by design | Consistent with reality — no free/hobby tier remains (discontinued 2024), so the public instance carries a small real monthly cost the maintainer must own; single machine = deploy/host-failure downtime. Both are acceptable at v1 hobby scale and already in § Deferred, but worth a sentence in § Structural Seed so the maintainer isn't surprised by a bill. A WSS service can use Fly's free **shared** IPv4 — avoid the **$2/mo dedicated IPv4** unless a conscious choice. | https://fly.io/docs/about/pricing/ |

---

## Recommended edits to the spine

1. **Stack table — Bubble Tea rows:** resolve v1-vs-v2 (H1). Preferred: `bubbletea v2.0.9`, `bubbles v2.2.1`, `lipgloss v2.0.6`. If staying on v1, say so with a rationale and pin `bubbles v1.x` / `lipgloss v1.x`. Replace every "current" with an exact minor.
2. **Stack table — Go:** `1.27.x` (or annotate 1.26 as a deliberate one-major lag). Note go1.26.7 / go1.27.0 both dated 2026-08-19.
3. **Stack table — header:** either genuinely re-verify each row on the stamped date or soften "verified current" to "seed versions — re-verify at init".
4. **Stack table — pin exact minors now** for `coder/websocket` (v1.8.15), `modernc.org/sqlite` (v1.57.x), `fsnotify` (v1.10.x).
5. **Structural Seed — plugin:** add one line stating how the companion binary reaches the machine (commit under `plugin/<dir>/`, not `bin/`, referenced via `${CLAUDE_PLUGIN_ROOT}`; or download-on-first-run) and that org-settings distribution is out of v1 scope (M1).
6. **Structural Seed — Fly.io:** one sentence that the public instance is pay-as-you-go (~$6–10/mo, no free tier) on Fly's free shared IPv4.
7. **Open Questions / spike list:** add the `modernc.org/sqlite` cross-build matrix (L2), fsnotify rename/rotation handling (L3), and companion process detachment (L4) — all naturally bundled with the existing AD-6 transcript spike.

---

## Sources (consolidated)

- Go releases: https://go.dev/doc/devel/release • https://go.dev/blog/go1.26 • https://go.dev/doc/go1.26
- Bubble Tea: https://github.com/charmbracelet/bubbletea/releases • https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md • https://github.com/charmbracelet/bubbletea/discussions/1374 • https://pkg.go.dev/charm.land/bubbletea/v2
- Bubbles: https://github.com/charmbracelet/bubbles/releases • https://pkg.go.dev/github.com/charmbracelet/bubbles/list
- Lip Gloss: https://github.com/charmbracelet/lipgloss/releases
- coder/websocket: https://github.com/coder/websocket • https://github.com/coder/websocket/releases • https://coder.com/blog/websocket • https://pkg.go.dev/github.com/coder/websocket
- fsnotify: https://github.com/fsnotify/fsnotify/releases
- modernc.org/sqlite: https://pkg.go.dev/modernc.org/sqlite • https://gitlab.com/cznic/sqlite/-/tags • https://github.com/modernc-org/sqlite
- Fly.io pricing: https://fly.io/docs/about/pricing/
- Claude Code plugins: https://code.claude.com/docs/en/plugins-reference • https://code.claude.com/docs/en/plugin-marketplaces • https://github.com/anthropics/claude-code/blob/main/plugins/README.md
