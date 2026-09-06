---
title: 'Story 1.8 — Pane placement'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'd52c48a533be9946dc4aa2c0d0f8f0ef17174a13'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The Story 1.7 launcher spawns the companion detached with all stdio
to `/dev/null`, so its status line renders nowhere and nothing puts it next to
the Claude session.

**Approach:** Add a placement step to `plugin/cmd/session-start`, reached only
after the launcher has already decided to spawn. Inside tmux (`$TMUX` set): bring
the companion up in an adjacent `tmux split-window` pane, focus left on Claude.
Outside tmux: keep today's detached spawn and, on success, write one line to the
hook's stdout telling the user how to open a live view. All in the Go launcher —
the shell wrapper and the companion are untouched.

## Boundaries & Constraints

**Always:**
- Placement runs only after every existing `launch` precondition passes (opt-out,
  `transcript_path`, `session_id`, `O_EXCL` lock, resolved+executable companion
  binary). Any short-circuit ⇒ no split, no spawn, no stdout — exactly Story 1.7.
- The tmux/no-tmux choice is a pure function of `getenv`: `strings.TrimSpace(getenv("TMUX")) != ""`.
  Keep it isolated and table-testable.
- **tmux path:** `exec.LookPath("tmux")`, then run `tmux split-window` as a child
  with stdio to `os.DevNull`. It must produce a pane adjacent to the Claude
  session (side-by-side `-h` preferred) that does **not** take focus (`-d` or
  equivalent), targeting `$TMUX_PANE` when set. The pane command is the committed
  companion binary with its unchanged three args `transcript_path`, `""`, `""` —
  passed as separate argv where tmux allows, else one single-quoted `sh -c`
  string. Any tmux failure (missing binary, non-zero exit, dead server) ⇒ fall
  back to the manual path. Nothing is written to stdout on this path.
- **Manual path:** `detachedSpawn` the companion exactly as Story 1.7. **Only if
  the spawn succeeds**, write exactly one line to a new `stdout io.Writer` seam on
  `launch` (wired to `os.Stdout` in `main`). The line follows the repo voice
  guide (warm, lowercase-friendly, one line, never names the machinery), tells
  the user to open a split/window and run the companion, may contain the
  companion binary path and the transcript path, and carries no `session_id`, no
  account key, no transcript content; phrase it so it is inert if the model reads
  it. A spawn failure is swallowed with no stdout, as Story 1.7 (`main` still
  writes its one non-identifying stderr line).
- Fail-open preserved: `launch` returns no error it did not already return in 1.7
  (a real manual-path `detachedSpawn` failure); `main` still ends at its single
  deferred `os.Exit(0)`; never a non-zero exit, never a blocking wait.
- `plugin` stays stdlib-only: no `plugin/go.mod` `require`, no `go.sum`, no
  `golang.org/x/sys`; `scripts/check_deps.sh` output unchanged.

**Ask First:**
- Changing the companion's `transcript-path config-dir server-url` arg contract,
  `/proto`, or the companion at all.
- Moving placement into `plugin/hooks/session-start.sh`.
- Any `require` in `plugin/go.mod` or a `golang.org/x/sys` import.
- A split that steals focus from the Claude pane, or blocks the hook past the
  instant `split-window` return.

**Never:**
- No new positional arg / flag / env marker on the companion; no second-instance
  bookkeeping here (Story 1.4 takeover reconciles a manual instance).
- No opening a new OS terminal/window on the manual path (`open -a Terminal`,
  `cmd /c start`); no `screen` / other multiplexer support; no tmux retry loop;
  no `remain-on-exit`.
- No change to `plugin/hooks/hooks.json`, `plugin/.claude-plugin/plugin.json`,
  `.github/workflows/*`, the shell wrapper, any committed binary, or the
  companion; no new CI job.
- No first-run / 18+ gate (Story 1.9); no chat UI.
- Do not remove or rename the existing `detachedSpawn` seam or its tests.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Behavior |
|---|---|---|
| tmux, normal | `$TMUX` set; not opted out; lock free; companion binary present; stdin has `session_id` + `transcript_path` | acquire lock; `tmux split-window` (adjacent, no focus change) whose pane command is `<bin> <transcript_path> "" ""`; no `detachedSpawn`; stdout empty; core returns nil |
| tmux split fails | as above but `tmux` absent / `split-window` exits non-zero | fall back to the manual path; tmux error swallowed |
| no tmux, spawn ok | `$TMUX` unset / empty / whitespace; `detachedSpawn` succeeds | `detachedSpawn(<bin>, [transcript_path,"",""])` then exactly one stdout line naming the companion binary and the `<transcript> "" ""` invocation |
| no tmux, spawn fails | as above but `detachedSpawn` errors | no stdout; `main` writes its one non-identifying stderr line; `os.Exit(0)` — as Story 1.7 |
| precondition short-circuit | opted out / no `transcript_path` / no `session_id` / lock held / binary missing or non-exec | no split, no spawn, no stdout, `os.Exit(0)` — as Story 1.7 |
| resume | second `SessionStart`, same `session_id`, `$TMUX` set | `acquire` fails ⇒ no split, no spawn, no stdout |
| output scrub | any stdout line | one line, manual-path only; no `session_id`, no account key, no transcript content |

</frozen-after-approval>

## Code Map

- `plugin/cmd/session-start/launch.go` — **edit.** Add a pure placement-decision
  helper (`TrimSpace(getenv("TMUX"))`). Extend `launch` with a `stdout io.Writer`
  and a `tmuxRun func(args []string) error` seam (keep the existing `spawn
  func(bin string, args []string) error` for the companion). After the current
  early-return chain (`optedOut` → `parse` → `TranscriptPath` → `SessionID` →
  `companionPath`/`isExecutable` → `acquire`, `launch.go:171`–`210`): tmux ⇒ build
  the `split-window` argv, call `tmuxRun`, on error fall through; manual ⇒
  `spawn(bin, args)` then, on success only, one `fmt.Fprintln(stdout, …)`. Hint
  copy is a package const in this file. `launch` still returns only a real
  manual-path spawn error.
- `plugin/cmd/session-start/tmux.go` — **new, not build-tagged.** `tmuxSplit(args
  []string) error`: `exec.LookPath("tmux")` (missing ⇒ error → caller falls
  back), `exec.Command(tmux, args...)`, stdio to `os.DevNull`, `cmd.Run()` (a
  non-zero tmux exit is the error). No `SysProcAttr`. On Windows `LookPath` fails
  and the manual path is taken.
- `plugin/cmd/session-start/main.go` — **edit.** Pass `os.Stdout` and `tmuxSplit`
  into `launch`; refresh the package doc comment to name all three outcomes
  (tmux split / detached + hint / silent no-op). Deferred `os.Exit(0)` and the
  stderr fallback line unchanged.
- `plugin/cmd/session-start/launch_test.go` — **edit.** See Tasks. Model on
  existing `fakePluginRoot` / `writeExecutable` / `recordingSpawn`
  (`:38` / `:26` / `:219`); add a `recordingTmux` twin; every `launch` call site
  gains the two new args. `TestSessionStartWrapper` (`:432`) and
  `TestLauncherEndToEndExitsZero` (`:496`) run the real launcher with
  `os.Environ()` — add `"TMUX="` to their `cmd.Env` so they deterministically hit
  the manual path; their spawn-error case still asserts empty stdout (hint is
  success-only).
- `plugin/cmd/session-start/{spawn_unix,spawn_windows,spawn_unix_test}.go`,
  `plugin/hooks/session-start.sh`, `companion/**` — **read-only.** `detachedSpawn`
  reused verbatim (tmux seam is a separate function). The wrapper already
  forwards stdin and inherits `$TMUX` / `$TMUX_PANE`.
- `README.md` — **edit.** `plugin` module-table cell + the launcher "how it
  works" paragraph (~`README.md:150`–`164`): note the tmux auto-split beside the
  Claude session and the one-line hint on the no-tmux fallback.
- `.../architecture/architecture-claudingtin-2026-09-01/ARCHITECTURE-SPINE.md` —
  **read-only.** Its component table puts "pane placement" under `companion`;
  this story puts it in the launcher instead (only the launcher owns the spawn
  strategy and has a user-surfaced channel). Noted, not edited.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/cmd/session-start/launch.go` — placement helper; `launch` gains
  `stdout` + `tmuxRun`; tmux-branch argv + fall-through; manual-branch spawn +
  success-only hint; hint copy const.
- [x] `plugin/cmd/session-start/tmux.go` — new `tmuxSplit` (LookPath, DevNull
  stdio, `cmd.Run`).
- [x] `plugin/cmd/session-start/main.go` — wire `os.Stdout` + `tmuxSplit`;
  refresh the package doc comment.
- [x] `plugin/cmd/session-start/launch_test.go` — table-test the placement
  decision (`$TMUX` set / unset / whitespace); tmux path (recorded `split-window`
  argv carries a no-focus flag, the companion bin, `[transcript,"",""]`; no
  `detachedSpawn`; empty stdout); tmux-error fallback (spawn called + one stdout
  line); manual/spawn-ok (one stdout line with the companion path);
  manual/spawn-fail (no stdout); every precondition row still yields no spawn /
  no split / no stdout; scrub assertion (no `session_id`); add `"TMUX="` to the
  two `sh`-wrapper end-to-end tests.
- [x] `README.md` — module-table cell + launcher paragraph.

**Acceptance Criteria:** (per-branch behavior is the I/O Matrix)
- Given the real compiled launcher wired to `tmuxSplit`, an environment with no
  `tmux` on `PATH` and `$TMUX` unset, and a resolvable companion stub, when a
  `SessionStart` JSON with a `transcript_path` is piped in, then it exits 0 and
  writes exactly one stdout line naming the companion invocation and nothing else.
- Given the finished tree, when `go build` / `go vet` / `go test -race` over
  `scripts/workspace_modules.sh`, `gofmt -l .`, `go work sync` +
  `git diff --exit-code`, `shellcheck $(git ls-files '*.sh')`,
  `bash scripts/check_deps.sh`, `bash scripts/workspace_modules.sh --check`, and
  `bash scripts/checks_test.sh` run, then all pass and `check_deps.sh` still
  prints `companion → proto` / `backend → proto` with `plugin` importing no
  sibling and carrying no `require`.
- Given `plugin/hooks/session-start.sh`, `plugin/hooks/hooks.json`,
  `plugin/.claude-plugin/plugin.json`, and `.github/workflows/*`, when diffed
  against `baseline_commit`, then they are unchanged.

## Spec Change Log

## Design Notes

- **Launcher, not companion.** The spine lists "pane placement" under `companion`,
  but the launcher owns the spawn strategy (detached vs. into a pane) and is the
  only piece with a user-surfaced channel — `SessionStart` stdout is folded into
  the model's context and FR5 sanctions "the plugin SHALL print". A
  companion-side version would still need the launcher for that line, splitting
  the logic. Deliberate deviation.
- **Manual path still spawns** (FR6 "the companion still runs"): it connects and
  signals think-time headless; when the user runs a visible instance, Story 1.4
  takeover ends the headless one with `session_ended`/exit 0. One idle socket
  until then — invisible for Epic 1 (status line only).
- **tmux is exec, not shell policy.** `split-window` returns in ms (talks to the
  server over `$TMUX`), so `cmd.Run()` sits well inside `hooks.json` `timeout:
  10`. Prefer separate-argv `split-window -- <bin> <args>`; fall back to a
  single-quoted `sh -c` only if the installed tmux needs it.
- **A stale pane after Claude Code exits** is the pre-existing companion
  lifecycle gap (Story 1.6 deferred the forced-exit watchdog); tmux closes the
  pane once the companion itself exits. Out of scope — noted so review does not
  re-flag it.

## Verification

**Commands** (repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new placement rows in the
  `plugin/cmd/session-start` suite
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no diff
- `shellcheck $(git ls-files '*.sh')` — no output (wrapper unchanged)
- `bash scripts/check_deps.sh` — exit 0, edges unchanged, `plugin` sibling-free
  and `require`-free
- `bash scripts/workspace_modules.sh --check` — OK
- `bash scripts/checks_test.sh` — exit 0
- `git diff --stat d52c48a533be9946dc4aa2c0d0f8f0ef17174a13 -- plugin/hooks plugin/.claude-plugin .github/workflows`
  — empty

**Manual checks:**
- `README.md`: launcher paragraph names the tmux auto-split and the no-tmux
  one-line hint; module-table cell updated.
- `plugin/cmd/session-start/main.go`: package doc comment describes all three
  outcomes.

## Suggested Review Order

**The placement decision (entry point)**

- The whole policy in one linear function — placement runs only after every Story 1.7 precondition passes.
  [`launch.go:162`](../../plugin/cmd/session-start/launch.go#L162)
- Inside tmux: build an adjacent (`-h`), no-focus (`-d`) `split-window`, target `$TMUX_PANE`, run the companion with its unchanged three args; any tmux failure drops through.
  [`launch.go:197`](../../plugin/cmd/session-start/launch.go#L197)
- No tmux, or the split failed: Story 1.7's detached spawn verbatim, then — only on spawn success — one hint line to stdout.
  [`launch.go:213`](../../plugin/cmd/session-start/launch.go#L213)
- The tmux/no-tmux switch: a pure `strings.TrimSpace($TMUX) != ""`, kept isolated and table-tested.
  [`launch.go:142`](../../plugin/cmd/session-start/launch.go#L142)

**The tmux seam**

- `exec.LookPath` first (missing tmux ⇒ error ⇒ fallback), all stdio to `os.DevNull`, and a 3s `context` bound so a hung server can't stall the hook.
  [`tmux.go:26`](../../plugin/cmd/session-start/tmux.go#L26)
- Why the bound is 3s and what elapsing it does.
  [`tmux.go:16`](../../plugin/cmd/session-start/tmux.go#L16)

**The stdout hint**

- Declarative, `(claudingtin)`-namespaced, one line — a SessionStart hook's stdout goes into the model's context, so it must not read as a directive; carries no session id / key / transcript content.
  [`launch.go:137`](../../plugin/cmd/session-start/launch.go#L137)

**Wiring**

- `main` passes the real `os.Stdout` and `tmuxSplit` into the pure core; the deferred `os.Exit(0)` is unchanged.
  [`main.go:46`](../../plugin/cmd/session-start/main.go#L46)

**Peripherals — tests, docs**

- Real-seam end-to-end: a recording `tmux` shell stub on `$PATH` drives the compiled launcher — argv shape, empty stdout, and the `exit 1` fallback to a single hint line.
  [`tmux_test.go:78`](../../plugin/cmd/session-start/tmux_test.go#L78)
- Branch coverage through the `recordingTmux` fake: adjacent split + no spawn, and the split-failure fallback.
  [`launch_test.go:330`](../../plugin/cmd/session-start/launch_test.go#L330)
- The placement decision in isolation: `$TMUX` set / unset / whitespace / padded.
  [`launch_test.go:243`](../../plugin/cmd/session-start/launch_test.go#L243)
- README: module-table cell + launcher paragraph now cover the tmux auto-split and the no-tmux hint.
  [`README.md:12`](../../README.md#L12)
