# Epic 1 — end-to-end proof run (retro action item F8)

Date: 2026-09-06 · Platform: darwin/arm64 · Ref: `epic-1-retro-2026-09-06.md` finding F8

## What was run

Built the real binaries (`CGO_ENABLED=0 -trimpath`, same flags as `ci.yml` / `release.yml`) and placed them at `plugin/bin/darwin-arm64/{companion,session-start}`, then drove the `SessionStart` launch path exactly as the hook does — `session-start` invoked with a real SessionStart JSON on stdin and `CLAUDE_PLUGIN_ROOT` set — against a real `backend serve` on `:8080`. Scratch `$HOME` and config-dir throughout; nothing written to the real `~/Library/Application Support/claudingtin`. Binaries and scratch removed after; `plugin/bin/` is back to just `.gitkeep`.

## Results

| Check | Outcome |
|---|---|
| `session-start` resolves the committed companion via `os.Executable()` → `<root>/bin/darwin-arm64/companion` (no `CLAUDE_PLUGIN_ROOT` needed) | ✅ pass |
| Launcher exits 0, prints the one `(claudingtin) companion is running…` hint line, spawns the companion detached with `[transcript, "", ""]` | ✅ pass |
| Once-per-session lock at `$TMPDIR/claudingtin-<session_id>.lock` (mode 0600); a second hook fire for the same `session_id` is a silent no-op (no new process, no hint line) | ✅ pass |
| `CLAUDINGTIN_DISABLE=1` → launcher exits 0, no spawn | ✅ pass |
| **F7 confirmed:** detached spawn wires stdin to `/dev/null` → `safety.Gate` reads EOF → declines → companion inert; only `account-key` written, no `safety-ack` | ✅ reproduced |
| Companion in a visible terminal (stdin `yes`) → prints the first-run screen, writes `safety-ack` (`version 1`, `accepted_at …`, mode 0600) and `account-key` (valid UUIDv4, mode 0600) | ✅ pass |
| Companion connects to the real backend: `connecting…` → `hello` accepted (`backend log: ws connected pv:1`), `GET /status` → `{"concurrent_users":1}` | ✅ pass |
| Seeded `user` transcript line → `TurnStart` → status "claude's thinking — you're free to chat" (Ready on the wire) | ✅ pass |
| Appended `assistant` line, `stop_reason: end_turn` → `TurnEnd` → status "you're back with claude — waiting for the next quiet moment" (Busy) | ✅ pass |
| Backend killed mid-session → status "lost the thread for a moment — picking it back up" (reconnect backoff) | ✅ pass |
| Second companion run with `safety-ack` already on disk → **no** first-run screen reprinted | ✅ pass |
| `CLAUDINGTIN_SAFETY_REVIEW=1 companion` (no args) → reprints the screen, exits 0 | ✅ pass |

## Not covered

- **tmux pane placement.** No `tmux` on the test machine — only the no-tmux fallback path was exercised (which is the F7 path, the important one).
- Non-darwin/arm64 targets (CI cross-build already covers compilation for all four).

---

## Round 2 — genuine Claude Code plugin install + real session (2026-09-06, later)

Added `.claude-plugin/marketplace.json` at the repo root (`source: "./plugin"`), then in a live Claude Code 2.1.263:

```
/plugin marketplace add /Users/k/Documents/Github/claudingtin
/plugin install claudingtin@claudingtin
```

### F9 — the plugin did not load: `hooks.json` was in the wrong schema

First install: **"Installed claudingtin. The plugin couldn't be loaded."** Claude Code's debug log:

```
[ERROR] Failed to load hooks from ./hooks/hooks.json for claudingtin:
        "hooks.json must have `hooks` (the hook matchers) or `modules`, or both"
[DEBUG] Registered 0 hooks from 1 plugins
```

**Cause:** `plugin/hooks/hooks.json` shipped in the `settings.json` hook shape — the event key (`SessionStart`) at the top level — instead of the *plugin* shape, where events are nested under a top-level `"hooks"` object (`{ "hooks": { "SessionStart": [...] } }`), which every official hook-using plugin uses. Story 1.7 shipped it that way; nothing caught it because there was no schema test and `plugin/bin/` was empty, so the plugin had never actually been installed — the exact gap F8 exists to close.

Compounding it: `plugin/.claude-plugin/plugin.json` also carried `"hooks": "./hooks/hooks.json"`, which made the loader read the same malformed file twice (two `Failed to load` lines).

**Fix (this PR):**
- `plugin/hooks/hooks.json` → `{ "description": ..., "hooks": { "SessionStart": [ { "matcher": "startup|resume", "hooks": [...] } ] } }`
- `plugin/.claude-plugin/plugin.json` → drop the redundant `"hooks"` key (auto-discovery finds `hooks/hooks.json`)
- `plugin/cmd/session-start/manifest_test.go` → new regression guard: asserts the shipped `hooks.json` is the plugin shape (fails on a top-level `SessionStart`), the command references `${CLAUDE_PLUGIN_ROOT}` + `hooks/session-start.sh` with a positive timeout, `plugin.json` has no `hooks` key, and `session-start.sh` exists and is executable
- spec-1-7 annotated with the correction

### After the fix — verified against a real session

```
/plugin uninstall claudingtin@claudingtin
/plugin marketplace update claudingtin
/plugin install claudingtin@claudingtin      →  "Plugin is now active."
```

Fresh `claude --debug` session, debug log:

```
[DEBUG] Read hooks.json for plugin claudingtin: .../plugin/hooks/hooks.json
[DEBUG] Registered 1 hooks from 1 plugins
[DEBUG] Hook SessionStart:startup (SessionStart) success:
        (claudingtin) companion is running; to watch it live, run:  .../companion .../<session>.jsonl "" ""
```

| Check | Outcome |
|---|---|
| Plugin loads, `Registered 1 hooks from 1 plugins` | ✅ pass |
| `SessionStart:startup` fires, hook exits success within budget, prints the hint line | ✅ pass |
| Companion process spawns detached, bound to the real session's transcript, and **survives** the hook exit | ✅ pass |
| Companion connects to a running `backend serve`: `GET /status` → `{"concurrent_users":1}` | ✅ pass |
| Per-`session_id` lock created in `$TMPDIR` | ✅ pass |
| `safety-ack` already on disk (from the round-1 manual accept) → new session connects with no prompt | ✅ pass |

Still not covered: **tmux pane placement** (no tmux on the machine).

## Verdict

The Epic 1 launch + think-time + first-run-gate chain works end to end from a real Claude Code plugin install, once the F9 `hooks.json` schema bug is fixed. F8 is **done**; F9's fix is in this PR with a regression test. The only remaining gap is the tmux placement path, which is not a blocker for Epic 2.
