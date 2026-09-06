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

- **A genuine Claude Code plugin install + real session.** The launch path, binary resolution, detached spawn, lock, gate, and backend handshake are all proven against real binaries, but the plugin was not registered into Claude Code's own plugin system and no real CC session was started. That step touches the developer's `~/.claude` config and is left for the owner to run once.
- **tmux pane placement.** No `tmux` on the test machine — only the no-tmux fallback path was exercised (which is the F7 path, the important one).
- Non-darwin/arm64 targets (CI cross-build already covers compilation for all four).

## Verdict

The Epic 1 launch + think-time + first-run-gate chain works end to end against real binaries and a real backend. F8 stays **in-progress** pending the genuine Claude Code session install and the tmux path; neither is a blocker for starting Epic 2.
