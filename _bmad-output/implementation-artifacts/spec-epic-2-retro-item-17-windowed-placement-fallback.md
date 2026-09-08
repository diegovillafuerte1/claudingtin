---
title: 'Epic 2 retro item 17 — new-OS-window placement fallback'
type: 'feature'
created: '2026-09-08'
status: 'done'
review_loop_iteration: 0
context: []
baseline_commit: '59b1f93e1a0255851b22e7f73479afb7638a97b2'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Epic 2 retro F10 (action item 17) shipped its first half — the `muxStrategies` registry (WezTerm / Zellij / Kitty / Windows Terminal). The second half is still open: users in a bare terminal or a VS Code / JetBrains integrated terminal fall to the Story 1.7 detached spawn, which wires the companion's stdin to `/dev/null`, so `safety.Gate` reads EOF, declines, and the companion is permanently inert (Epic-1 F7). The retro's "open a new OS window" fallback — the one that hands the companion a real PTY so the first-run gate works — was never built, and `epic-1-retro-item-4` is blocked on it.

**Approach:** Add one rung to `launch()`, after `placePane` and before the detached spawn: open the companion in a **new OS terminal window** — macOS Terminal via a self-deleting `.command` script; a probed Linux terminal emulator; `cmd /c start` on Windows. A window gives the companion its own PTY, so the first-run 18+/safety screen is reachable. No emulator, or the attempt fails → fall through to the unchanged detached-spawn + one-line hint. `CLAUDINGTIN_NO_WINDOW` forces the old behaviour.

## Boundaries & Constraints

**Always:**
- Fail-open: the new path ends at `main`'s single `os.Exit(0)`; never block the hook, never exit non-zero, never `Wait` unboundedly.
- The window attempt sits strictly between the `placePane` block and the `spawn` block in `launch()`; tmux and `placePane` paths are untouched.
- Successful window launch → `launch` returns `nil` and writes nothing to stdout (same as the tmux / mux success paths). The one-line hint stays success-only on the detached-spawn path, byte-identical to today.
- Companion argv unchanged: `[transcriptPath, "", ""]`. No session id / account key / transcript content in any script, argv, or filename beyond the existing `claudingtin-<id>` lock convention.
- Plugin module stays dependency-free — standard library only.
- Bound every window child with a short grace (`windowSpawnGrace`, a package `var` so tests shrink it): exits non-zero within the grace → failure (fall through); still running after it → success (window is up).
- `go test -race ./...` opens no real terminal window on any host OS, macOS included: `launch` tests stub the `openWindow` seam; compiled-launcher e2e tests set `CLAUDINGTIN_NO_WINDOW=1`.

**Ask First:**
- Adding per-OS `SysProcAttr` (Setsid / detach flags) to the window child — the plan omits it (the emulator owns its lifecycle). If review shows reparenting / SIGHUP kills the window, HALT and confirm before adding build-tagged files.

**Never:**
- No `osascript` / AppleScript / Apple Events (avoids the macOS Automation TCC prompt) — this is why the iTerm2 split adapter was cut from item 17. No iTerm2 in-place split at all.
- No new proto messages, no companion-side changes, no changes to `safety.Gate`.
- No retry across Linux emulators after one is found and fails — try the first present, then fall through.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Window opens (any OS) | outside `$TMUX`/mux, `placePane`→false, `CLAUDINGTIN_NO_WINDOW` unset, emulator present. darwin: write `claudingtin-<sanitized id>.command` (0700: `#!/bin/sh` + `rm -f "$0"` + `exec "<bin>" "<transcript>" "" ""`) to tempDir, run `open -a Terminal <script>`. linux: `<term> <flag> "<bin>" "<transcript>" "" ""` for the first of `gnome-terminal --`, `konsole -e`, `xfce4-terminal -x`, `alacritty -e`, `kitty`, `xterm -e`, `x-terminal-emulator -e` on PATH. windows: `cmd /c start "" "<bin>" "<transcript>" "" ""` | `launch`→nil, stdout empty. Child still running after grace, or exit 0 within it → success |
| Window attempt fails | emulator missing, or the chosen child exits non-zero within `windowSpawnGrace` | `openWindow`→false → Story 1.7 detached spawn; on spawn success one hint line to stdout; on spawn error, non-nil return, stdout empty | fail-open; `main` still exits 0 |
| No display (linux/other) | `GOOS` not darwin/windows and both `$DISPLAY` and `$WAYLAND_DISPLAY` blank | `windowSupported`→false; no child spawned → detached spawn + hint (unchanged) | n/a |
| Opt-out knob | `CLAUDINGTIN_NO_WINDOW` truthy (`1/true/yes/on`, trimmed, case-insensitive), outside mux | `windowSupported`→false → no window attempt → detached spawn + hint | n/a |
| Inside tmux / mux | `$TMUX` set, or a mux env whose split succeeds | window path never reached | n/a |
| Fresh install via window | companion started in the new window, first run | its stdin is an interactive PTY, not EOF → `safety.Gate` shows the 18+/safety screen instead of declining | closes Epic-1 F7 for bare-terminal / editor-terminal hosts |

</frozen-after-approval>

## Code Map

- `plugin/cmd/session-start/launch.go` — `launch()` is the whole policy. Insert the `openWindow` rung between `if placePane(getenv, bin, args) { return nil }` and the `spawn(bin, args)` + `fmt.Fprintln(stdout, hint)` block. `optedOut` is the truthy-env parser to reuse for `CLAUDINGTIN_NO_WINDOW`; `lockPath` has the session-id sanitiser to reuse for the `.command` filename; `args := []string{in.TranscriptPath, "", ""}` is already built. Extend the `launch()` doc-comment ladder.
- `plugin/cmd/session-start/placement.go` — copy this pattern exactly: `var placePane = placeInPane` outer seam, `var runPlacement = runPlacementTool` inner seam, `muxPlacementTimeout` const, and `runPlacementTool` (LookPath → CommandContext → DevNull stdio → Run). The new file is its twin.
- `plugin/cmd/session-start/tmux.go` — `tmuxSplit`: the other "LookPath, bounded child, DevNull stdio, error ⇒ caller falls back" precedent.
- `plugin/cmd/session-start/spawn_unix.go` / `spawn_windows.go` — `detachedSpawn` is NOT reused (it is the long-lived companion; the window child is the emulator). Only referenced by the Setsid question in Ask First.
- `plugin/cmd/session-start/main.go` — `main()` calls `launch(..., detachedSpawn, tmuxSplit)`; unchanged (the window seam is a package `var` like `placePane`). Extend the package doc-comment placement list.
- `plugin/cmd/session-start/placement_test.go` — `envFrom` helper; `TestLaunchTriesPlacementBeforeSpawn` / `TestLaunchPlacementFailureFallsBackToSpawn` are the templates for the two new `launch`-level window tests. The latter reaches the fallback → must stub `openWindow`.
- `plugin/cmd/session-start/launch_test.go` — `recordingSpawn` / `recordingTmux`, `fakePluginRoot`, `writeExecutable`. Four `TestLaunch` subtests reach the fallback and must stub `openWindow`: "normal launch (no tmux)…", "tmux split failure falls back…", "opt-out falsey proceeds", "spawn error is returned…". `TestLauncherEndToEndExitsZero` and `TestLauncherEndToEndNoTmuxPrintsHint` run the compiled launcher via `sh` with `cmd.Env = append(os.Environ(), …)` — add `CLAUDINGTIN_NO_WINDOW=1`.
- `companion/internal/safety/safety.go` — `Gate` reads one stdin line; EOF ⇒ `OutcomeDeclined` (the F7 mechanism). Read-only; the fix is upstream (give it a PTY). Also `deferred-work.md:168`.
- `_bmad-output/planning-artifacts/epics.md` — FR6 (`[ASSUMPTION on placement fallback]`), FR5 map line, Story 1.8 AC (the "prints exactly one line" branch).
- `_bmad-output/implementation-artifacts/{spec-1-8-pane-placement.md, epic-2-retro-2026-09-07.md, sprint-status.yaml}` and `README.md` — reconciliation targets; see Tasks.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/cmd/session-start/windowterm.go` — NEW. `var openWindow = openInWindow` with signature `openInWindow(getenv func(string) string, tempDir, sessionID, bin string, companionArgs []string) bool`. `windowSupported(getenv) bool`: false if `CLAUDINGTIN_NO_WINDOW` truthy (reuse `optedOut`'s parse); else true on darwin/windows; on linux/other true only if `$DISPLAY` or `$WAYLAND_DISPLAY` is non-blank. Per-OS argv via `runtime.GOOS` as in the I/O matrix row 1; darwin also writes the self-deleting `.command` script (0700) into `tempDir` named with the reused sanitiser. `var runWindow = runWindowTool`; `runWindowTool(argv []string) error`: nil/empty → `exec.ErrNotFound`; LookPath argv[0]; `exec.Command`; DevNull stdin/stdout/stderr; `Start()`; then `select` a `Wait()` goroutine against `time.After(windowSpawnGrace)` — Wait-first → its error, timeout-first → nil. `var windowSpawnGrace = 700 * time.Millisecond`. Package doc comment: the grace model + the no-`SysProcAttr` decision.
- [x] `plugin/cmd/session-start/launch.go` — after `if placePane(...) { return nil }`: `if openWindow(getenv, tempDir, in.SessionID, bin, args) { return nil }`. Extend the `launch()` doc-comment ladder (mux → new OS window → detached spawn + hint).
- [x] `plugin/cmd/session-start/main.go` — extend the package doc-comment placement list with the new-window rung and the `CLAUDINGTIN_NO_WINDOW` override.
- [x] `plugin/cmd/session-start/windowterm_test.go` — NEW. `windowSupported` table (knob truthy/falsey/unset × display set/unset × GOOS); per-OS argv builders, `runtime.GOOS`-guarded/skipped like `TestSessionStartWrapper`; darwin script contents + mode + `open -a Terminal` argv via a swapped `runWindow`; `runWindowTool` real calls — nil argv → err, missing binary → err, a `writeExecutable` stub exiting 0 → nil, a stub that sleeps past a shrunk `windowSpawnGrace` → nil; `TestLaunchTriesWindowBeforeSpawn` (placePane→false, openWindow→true ⇒ no `recordingSpawn` call, empty stdout, lock present) and `TestLaunchWindowFailureFallsBackToSpawn` (openWindow→false ⇒ one detached spawn + one hint line), modelled on `TestLaunchTriesPlacementBeforeSpawn`.
- [x] `plugin/cmd/session-start/launch_test.go` — add `stubNoWindow(t)` (sets `openWindow` to a `return false` func, `t.Cleanup` restore, mirroring the `placePane` restore idiom); call it in the four fallback-reaching `TestLaunch` subtests; add `"CLAUDINGTIN_NO_WINDOW=1"` to `cmd.Env` in the two compiled-launcher e2e tests.
- [x] `plugin/cmd/session-start/placement_test.go` — call `stubNoWindow(t)` in `TestLaunchPlacementFailureFallsBackToSpawn`.
- [x] `_bmad-output/planning-artifacts/epics.md` — Story 1.8 AC not-inside-tmux branch → "…placed in an adjacent pane by that multiplexer, or — where a terminal emulator is available — opened in a new OS window; only with none of those does the plugin print exactly one line…". FR6: replace `[ASSUMPTION on placement fallback]` with a note that the new-window fallback resolves it and only truly headless / API-only contexts print the hint. FR5 map: "(tmux / mux split, new-window fallback, one-line hint last)".
- [x] `_bmad-output/implementation-artifacts/spec-1-8-pane-placement.md` — append a Spec Change Log entry: Epic 2 retro F10 / item 17 added a new-OS-window rung before the printed hint; the "prints exactly one line" AC now fires only when no window can be opened; KEEP the tmux-first ordering and the fail-open contract.
- [x] `_bmad-output/implementation-artifacts/epic-2-retro-2026-09-07.md` — update the F10 "Progress (2026-09-08)" paragraph and action-items row 9: the new-OS-window fallback landed (macOS / Linux / Windows), Epic-1 F7's inert-companion dead end closed for bare-terminal and editor-terminal users, `epic-1-retro-item-4` retired, iTerm2 split intentionally not built (TCC prompt).
- [x] `_bmad-output/implementation-artifacts/sprint-status.yaml` — `epic-2-retro-item-17-f10-…` `in-progress` → `done`; `epic-1-retro-item-4-f7-…` `open` → `done` with a ref note to this spec; bump `last_updated`.
- [x] `README.md` — module table line and placement narration: add "…or, in a bare terminal / editor terminal, opens the companion in a new OS window (real PTY, so the first-run screen works); only with no window either does it spawn detached and print one line". Add `CLAUDINGTIN_NO_WINDOW` beside `CLAUDINGTIN_DISABLE` — "set truthy to force the detached-spawn fallback instead of a new window".

**Acceptance Criteria:**
- Given the launcher runs outside tmux and every `placePane` strategy declines, when a terminal emulator is available and `CLAUDINGTIN_NO_WINDOW` is unset, then the companion is launched in a new OS window, `launch` returns `nil`, and nothing is written to stdout.
- Given the new-window attempt fails or `windowSupported` is false, when `launch` continues, then the detached spawn runs and prints exactly one hint line byte-identical to today, and a spawn error is still a non-nil return with empty stdout.
- Given `CLAUDINGTIN_NO_WINDOW` is truthy, when the launcher runs outside a multiplexer, then no window child is spawned.
- Given the companion is started in a new window on a fresh install, when `safety.Gate` reads stdin, then it sees an interactive PTY rather than EOF and shows the 18+/safety screen (Epic-1 F7 closed for these hosts).
- Given `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test -race ./...` on any host OS including macOS, then all pass and the suite opens no real terminal window.
- Given `scripts/check_deps.sh`, then the plugin module still has no third-party dependency.

## Design Notes

**Grace model (the one non-obvious bit).** Emulators split into "spawn the window then exit" (`open`, `gnome-terminal`, `cmd start`, `konsole`) and "stay attached until the window closes" (`xterm -e`, `alacritty -e`, `kitty`). `runWindowTool` handles both blindly: `Start()`, then `select` a `Wait()` goroutine against `time.After(windowSpawnGrace)`. Exited within the grace → return `Wait`'s error (non-zero = bad flags / no server → fall through). Still running after → the window is up → return nil and leave it; `main` calls `os.Exit(0)` moments later, so the orphaned `Wait` goroutine and unreaped child are harmless.

**macOS without osascript.** A `#!/bin/sh` file with a `.command` extension is run by Terminal.app on open, with a real PTY, and `open -a Terminal x.command` raises no Automation prompt (no Apple Event leaves this process). The script `rm -f "$0"`s itself first, so nothing lingers — unlike the per-session lock, deliberately never removed.

**No `SysProcAttr`.** `detachedSpawn` needs `Setsid` because it *is* the long-lived companion; here the child is the emulator, which owns its own session/window. Adding Setsid would mean two build-tagged files for no established need — flagged in Ask First.

**Windows `cmd /c start` (adversarial-review note).** Windows uses `cmd /c start "" "<bin>" "<transcript>" "" ""` per the frozen I/O matrix. Review flagged that `cmd.exe`'s re-tokenisation of `start`'s arguments may collapse the trailing empty args, in which case the companion receives fewer than 3 argv and exits 2 (bad args). This is **unverified on a real Windows host** (no Windows CI leg exercises the window path — see `deferred-work.md`); Windows Terminal users are served by the `$WT_SESSION` mux path in `placement.go` regardless. If the collapse proves real, `conhost <bin> <args>` — a new console with no shell re-parse — is the likely fix; that is a frozen-intent change and needs human sign-off.

**Darwin script safety (adversarial-review revision).** Each value in the `.command` script is single-quoted (`'` → `'\''`), not raw-double-quoted, so a transcript path containing `$`, backtick, `"`, `\`, or a newline stays inert. The script is created with `O_CREATE|O_EXCL|O_WRONLY` (like the sibling `acquire()`) so it never follows a symlink or clobbers a predictable path in a shared temp dir, and `openInWindow` removes it when `runWindow` returns an error (the script's own `rm -f "$0"` only runs if Terminal actually executes it).

## Verification

**Commands:**
- `cd plugin && go test -race ./cmd/session-start/` — all pass incl. new `windowterm_test.go`; no Terminal window appears on macOS.
- `go build ./... && go vet ./... && gofmt -l .` (repo root) — clean; no `gofmt -l` output.
- `go test -race ./...` (repo root) — every module green.
- `bash scripts/check_deps.sh` — OK; plugin still dependency-free.

**Manual checks:**
- macOS, plain Terminal.app (no tmux): trigger a SessionStart → a new Terminal window opens running the companion and the 18+ screen is interactive (not an instant decline).
- `CLAUDINGTIN_NO_WINDOW=1`: same trigger falls back to the detached spawn and the single stdout hint line, exactly as before.

## Suggested Review Order

**The new fallback rung**

- Entry point — the one new line in the launch ladder: mux split declined → try a window before the detached spawn.
  [`launch.go:247`](../../plugin/cmd/session-start/launch.go#L247)
- The rung's whole policy: decline fast if unsupported, build per-OS argv, run it, clean up on failure.
  [`windowterm.go:192`](../../plugin/cmd/session-start/windowterm.go#L192)
- The grace model — success is a liveness heuristic (still alive after `windowSpawnGrace`), child is never killed; contrast tmux/mux.
  [`windowterm.go:222`](../../plugin/cmd/session-start/windowterm.go#L222)
- When a window is even attempted: `CLAUDINGTIN_NO_WINDOW`, then GOOS, then `$DISPLAY`/`$WAYLAND_DISPLAY` on Linux.
  [`windowterm.go:64`](../../plugin/cmd/session-start/windowterm.go#L64)

**Per-OS window construction**

- GOOS switch: darwin writes an `O_EXCL` self-deleting script + returns a cleanup; windows/linux delegate to pure builders.
  [`windowterm.go:158`](../../plugin/cmd/session-start/windowterm.go#L158)
- macOS `.command` body — every value single-quoted (`'\''`) so a metacharacter-laden transcript path stays inert.
  [`windowterm.go:118`](../../plugin/cmd/session-start/windowterm.go#L118)
- Linux emulator probe list + injectable `lookPath` for first-present selection; no retry across the list (F10 boundary).
  [`windowterm.go:143`](../../plugin/cmd/session-start/windowterm.go#L143)
- Windows `cmd /c start` per the frozen matrix — see the "adversarial-review note" Design Note for the unverified empty-arg-collapse risk.
  [`windowterm.go:135`](../../plugin/cmd/session-start/windowterm.go#L135)

**Doc reconciliation**

- FR6: the placement-fallback assumption is resolved; only a headless / no-emulator context now prints the one-line hint.
  [`epics.md:31`](../planning-artifacts/epics.md#L31)
- Action item 17 → `done`, and Epic-1 F7's `epic-1-retro-item-4` retired against this spec.
  [`sprint-status.yaml:232`](sprint-status.yaml#L232)

**Supporting**

- `stubNoWindow` helper — every fallback-reaching launch test points `openWindow` at a decline so no real window opens under `go test`.
  [`launch_test.go:54`](../../plugin/cmd/session-start/launch_test.go#L54)
- New tests: `windowSupported` table, `linuxWindowArgv` first-present, darwin script/cleanup/`O_EXCL`, `runWindowTool` grace, the two launch-level wiring tests.
  [`windowterm_test.go:20`](../../plugin/cmd/session-start/windowterm_test.go#L20)
