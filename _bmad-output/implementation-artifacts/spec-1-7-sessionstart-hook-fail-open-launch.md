---
title: 'Story 1.7 — SessionStart hook: fail-open launch'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: '1f98de4c77aabfbf8e5793b7411a611da6ffc943'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The Story 1.6 companion is a finished binary that nothing starts.
`plugin/hooks/session-start.sh` and `plugin/cmd/session-start/main.go` are
placeholders and the plugin has no manifest or hook registration — so installing
claudingtin does nothing, and there is no wired, fail-open path from a Claude
Code `SessionStart` to a running companion.

**Approach:** Make `plugin/cmd/session-start` a real launcher: read the
`SessionStart` JSON on stdin, honor a `CLAUDINGTIN_DISABLE` opt-out and a
per-`session_id` lock, resolve `bin/<os>-<arch>/companion` from the plugin root,
spawn it detached with `transcript_path "" ""` without waiting, and always exit 0
in well under 50 ms. Add `plugin/hooks/session-start.sh` as a minimal POSIX
arch-dispatch wrapper, register it via `plugin/.claude-plugin/plugin.json` +
`plugin/hooks/hooks.json` (`matcher: "startup|resume"`, short `timeout`), and
extend CI/release to cross-build the launcher alongside the companion.

## Boundaries & Constraints

**Always:**
- **Fail-open is absolute.** Every failure path ends in `os.Exit(0)` with nothing
  on stdout. The launcher never exits non-zero (never 2 — that blocks the
  session) and never blocks on the spawned child.
- The launcher's testable core is a pure function (mirror `companion/main.go`'s
  `run` seam): stdin `io.Reader`, `getenv func(string) string`, a temp-dir
  string, and an injected `spawn func(bin string, args []string) error` in; a
  result the thin `main` maps to `os.Exit(0)` out. `os.Exit` lives only in `main`.
- Input is the `SessionStart` stdin JSON only — no positional args. Parse
  `transcript_path` and `session_id`; ignore all other fields.
- **Opt-out:** `getenv("CLAUDINGTIN_DISABLE")` truthy (`1`/`true`/`yes`/`on`,
  case-insensitive) ⇒ do nothing, exit 0. Anything else ⇒ proceed.
- **Once per session:** create `<tempDir>/claudingtin-<sanitized
  session_id>.lock` with `O_CREATE|O_EXCL` before spawning; any create failure ⇒
  no spawn, exit 0. The launcher never removes the lock.
- Companion path is `<plugin-root>/bin/<runtime.GOOS>-<runtime.GOARCH>/companion`
  (`companion.exe` on Windows); `<plugin-root>` from `os.Executable()`, with
  `${CLAUDE_PLUGIN_ROOT}` as fallback only. Missing / non-executable ⇒ silent
  exit 0.
- Spawn is detached and non-blocking: `cmd.Start()` (never `Run`/`Wait`), child
  stdio to `os.DevNull`, new session / process group via a build-tagged
  `SysProcAttr` (`Setsid` on unix; `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS`
  on windows, the latter a local const). Companion args are exactly
  `[transcript_path, "", ""]`.
- `plugin/hooks/session-start.sh`: POSIX `sh`, no bashisms, `shellcheck`-clean.
  Its whole job — compute `<os>-<arch>`, `exec`
  `"${CLAUDE_PLUGIN_ROOT:-<$0-relative>}"/bin/<os>-<arch>/session-start` with
  stdin inherited, `|| exit 0` on `exec` failure. No opt-out / dedup / spawn
  policy in the shell.
- `plugin/hooks/hooks.json`: one `SessionStart` entry — `matcher:
  "startup|resume"`, one `command` hook →
  `"${CLAUDE_PLUGIN_ROOT}"/hooks/session-start.sh`, `timeout: 10`.
  `plugin/.claude-plugin/plugin.json`: `name` `claudingtin`, `version` `0.1.0`,
  `description`, `author` (name only, no email), `"hooks": "./hooks/hooks.json"`.
- `plugin` stays dependency-free: stdlib only, no sibling imports, no
  third-party; `plugin/go.mod` gains no `require` and no `go.sum`;
  `scripts/check_deps.sh` output is unchanged.
- CI `cross-build` and `release.yml` `binaries` also build `session-start` for
  `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `windows/amd64`. `README.md`'s
  module table, "CI & releases" bullets, and "refreshing `plugin/bin/`" step note
  the launcher binary travels with the committed companion binary.
- `go build` / `go vet` / `go test -race` over `scripts/workspace_modules.sh`,
  `gofmt -l .`, `go work sync` + `git diff --exit-code`,
  `shellcheck $(git ls-files '*.sh')`, `bash scripts/check_deps.sh`,
  `bash scripts/workspace_modules.sh --check`, `bash scripts/checks_test.sh` —
  all pass.

**Ask First:**
- Any `require` in `plugin/go.mod`, or any `golang.org/x/sys` use for the
  detached-spawn attributes.
- Changing the companion's `<transcript-path> <config-dir> <server-url>`
  contract, or `/proto`, or the companion.
- Moving launch policy (opt-out, dedup, spawn) into the shell wrapper.
- Reading "hard timeout" as a lifetime cap on the companion process — it
  contradicts Story 1.6's forever-reconnect design (see Design Notes).

**Never:**
- No positional-arg contract for the launcher; no writing to stdout on any path;
  no non-zero exit; no blocking wait on the child; no retry loop.
- No handle / IPC kept to the companion after spawn (AD-7 launch-only); no health
  check, backend probe, or transcript parsing here.
- No first-run / 18+ gate (Story 1.9); no pane placement / tmux logic
  (Story 1.8); no chat UI.
- No new CI job or workflow file; no committed binary outside `plugin/bin/**`.
- Not adding `-race` to CI's shared `test` job (tracked in deferred-work).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Behavior |
|---|---|---|
| Normal launch | stdin `{"session_id":"s1","transcript_path":"/p/t.jsonl",…}`; no lock; `CLAUDINGTIN_DISABLE` unset; companion binary present | create `claudingtin-s1.lock`; `Start` `<root>/bin/<os>-<arch>/companion /p/t.jsonl "" ""` detached; exit 0, empty stdout |
| Opt-out truthy | `CLAUDINGTIN_DISABLE=1` / `true` / `yes` / `on` | no lock, no spawn, exit 0 |
| Opt-out falsey | `=0` / `false` / empty / unset | proceed as normal launch |
| Already launched | lock for `session_id` exists (`O_EXCL` create fails) | no spawn, exit 0 |
| Malformed / empty stdin | not JSON, or `""` | no spawn, exit 0 (parse error swallowed) |
| Missing `transcript_path` | JSON present, field absent or `""` | no spawn, exit 0 |
| Missing / non-exec companion binary | resolved path absent or not executable | no spawn (or `Start` error), exit 0 |
| Spawn error | `cmd.Start()` errors | exit 0, error swallowed, lock left in place |
| Wrapper, binary present | `session-start.sh` via `sh`, `CLAUDE_PLUGIN_ROOT` set, stub `bin/<os>-<arch>/session-start` | `exec`s the stub with stdin passed through |
| Wrapper, binary absent | `session-start.sh`, no launcher at the resolved path | `exec` fails ⇒ `exit 0`, nothing on stdout/stderr |
| Output scrub | any row above | stdout empty; stderr at most a short non-identifying diagnostic; no `session_id`, no transcript content anywhere |

</frozen-after-approval>

## Code Map

- `plugin/cmd/session-start/main.go` — **rewrite.** Replace the arg-count stub
  with a thin `main`: call the core with `os.Stdin`, `os.Getenv`, `os.TempDir()`,
  and the real `detachedSpawn`, then one unconditional `os.Exit(0)`. Package
  consts for the opt-out env var and the lock-file prefix.
- `plugin/cmd/session-start/launch.go` — **new.** The pure core: `parse` (stdin
  JSON → `{SessionID, TranscriptPath}`), `optedOut(getenv)`,
  `companionPath(getenv)` (`os.Executable()` up three dirs, else
  `CLAUDE_PLUGIN_ROOT`, then `bin/<GOOS>-<GOARCH>/companion` + exe suffix),
  `acquire(tempDir, sessionID)` (`O_CREATE|O_EXCL|O_WRONLY`, `0o600`), and
  `launch(...)` tying them together. No `os.Exit`, no direct `exec`.
- `plugin/cmd/session-start/spawn_unix.go` / `spawn_windows.go` — **new,
  build-tagged.** `detachedSpawn(bin, args)`: `exec.Command`, stdio to
  `os.DevNull`, `SysProcAttr` (`Setsid` on unix; `CREATE_NEW_PROCESS_GROUP |
  detachedProcess` with `const detachedProcess = 0x00000008` on windows),
  `cmd.Start()` then return — never `Wait`.
- `plugin/cmd/session-start/launch_test.go` — **new.** Table tests for `parse`,
  `optedOut`, `acquire`, `companionPath`, and `launch` with a recording fake
  spawn covering the I/O-matrix rows; plus an end-to-end test (`t.Skip` on
  `runtime.GOOS == "windows"`) running the real `session-start.sh` via `sh`
  against a fixture tree with a stub `session-start` that records its stdin, and
  the stub-absent silent-exit-0 case.
- `plugin/hooks/session-start.sh` — **rewrite.** POSIX arch-dispatch wrapper per
  Boundaries; `shellcheck`-clean; ends `exec … || exit 0`.
- `plugin/hooks/hooks.json` — **new.** `SessionStart` → `matcher:
  "startup|resume"`, `command` → `"${CLAUDE_PLUGIN_ROOT}"/hooks/session-start.sh`,
  `timeout: 10`.
- `plugin/.claude-plugin/plugin.json` — **new.** Manifest: `name`, `version`
  `0.1.0`, `description`, `author` (name only), `"hooks": "./hooks/hooks.json"`.
- `plugin/go.mod` — **read-only.** Stays `module …/plugin` + `go 1.27`, no
  `require`.
- `companion/main.go` — **read-only reference.** Arg contract
  `<transcript-path> <config-dir> <server-url>`; empty config-dir / server-url
  tolerated (see `resolve`). Header comment already names Story 1.7 — leave it.
- `.github/workflows/ci.yml` — **edit.** `cross-build` job also builds
  `session-start` for the four `goos/goarch` targets and uploads them as
  artifacts.
- `.github/workflows/release.yml` — **edit.** `binaries` job also builds and
  attaches `session-start-<os>-<arch>` for the four targets.
- `.gitignore` — **edit.** Add `/plugin/cmd/session-start/session-start` beside
  `/companion/companion` (`.exe` already ignored; `!plugin/bin/**` re-includes
  committed ones).
- `README.md` — **edit.** Module-table `plugin` row; "CI & releases" bullets;
  "Manual step: refreshing `plugin/bin/`" (copy `session-start` beside
  `companion`, one pinned version); mention `plugin/.claude-plugin/plugin.json` +
  `hooks/hooks.json`.
- `plugin/bin/.gitkeep` — **read-only.** No binaries added by this story.

## Tasks & Acceptance

**Execution:**
- [x] `plugin/cmd/session-start/launch.go` — pure core (`parse`, `optedOut`,
  `companionPath`, `acquire`, `launch`); all policy, no `os.Exit`, no direct
  process start.
- [x] `plugin/cmd/session-start/spawn_unix.go` + `spawn_windows.go` —
  build-tagged `detachedSpawn`: detached, non-blocking `cmd.Start()`, child stdio
  to `os.DevNull`.
- [x] `plugin/cmd/session-start/main.go` — rewrite the stub: thin `main` →
  `launch` → unconditional `os.Exit(0)`.
- [x] `plugin/cmd/session-start/launch_test.go` — table tests + I/O-matrix rows +
  the skipped-on-Windows end-to-end `session-start.sh` test.
- [x] `plugin/hooks/session-start.sh` — rewrite as the POSIX arch-dispatch
  wrapper, `shellcheck`-clean.
- [x] `plugin/hooks/hooks.json` — new `SessionStart` registration.
- [x] `plugin/.claude-plugin/plugin.json` — new manifest with the `hooks`
  pointer.
- [x] `.github/workflows/ci.yml` — cross-build the launcher for the four targets.
- [x] `.github/workflows/release.yml` — build + attach the four launcher
  binaries.
- [x] `.gitignore` — ignore the local `session-start` build output.
- [x] `README.md` — module table, CI/release bullets, `plugin/bin/` refresh
  step, plugin-manifest mention.

**Acceptance Criteria:** (system-level; per-branch behavior is the I/O Matrix)
- Given a temp dir, a recording fake spawn, `CLAUDINGTIN_DISABLE` unset, and
  stdin `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`,
  when the core runs, then the recorder saw exactly one spawn of
  `…/bin/<GOOS>-<GOARCH>/companion` (`.exe` on Windows) with args
  `["/tmp/t.jsonl","",""]`, `claudingtin-s1.lock` exists, and the core reports no
  error.
- Given `plugin/hooks/session-start.sh` run by `sh` with `CLAUDE_PLUGIN_ROOT`
  pointing at a fixture tree whose `bin/<os>-<arch>/session-start` records its
  stdin, when a SessionStart JSON is piped in, then the stub runs and receives
  that JSON; with the stub absent the wrapper exits 0 and writes nothing.
- Given the finished tree, when `go build` / `go vet` / `go test -race` over
  `scripts/workspace_modules.sh`, `gofmt -l .`, `go work sync` +
  `git diff --exit-code`, `shellcheck $(git ls-files '*.sh')`,
  `bash scripts/check_deps.sh`, `bash scripts/workspace_modules.sh --check`, and
  `bash scripts/checks_test.sh` run, then all pass and `check_deps.sh` still
  prints exactly `companion → proto` and `backend → proto` with `plugin`
  unreferenced and importing no sibling.
- Given `ci.yml` and `release.yml`, when their build matrices are read, then each
  builds `session-start` for all four `os/arch` targets alongside the companion,
  and no comment still defers the launcher to a later story.
- Given any diagnostic output from the launcher or wrapper, when inspected, then
  it carries no `session_id` and no transcript content, and stdout is empty.

## Spec Change Log

## Design Notes

- **"Hard timeout" = the hook invocation, not the companion.** Story 1.6's
  companion reconnects forever by design; a lifetime cap would fight that. The
  bound is `hooks.json` `timeout: 10` (Claude Code kills a hung hook and starts
  the session anyway) plus a launcher that never `Wait`s. Re-interpreting this is
  Ask-First.
- **Why a shell wrapper at all.** `hooks.json` has no per-OS command selection
  and a compiled launcher is per-arch, so `session-start.sh` does only
  `<os>-<arch>` → `exec`. Every decision (opt-out, dedup, spawn mode, exit-0)
  lives in the Go binary — unit-tested once, identical on every platform. A
  native Windows shell without POSIX `sh` is the accepted degradation (same class
  as Story 1.8's non-tmux fallback): the hook no-ops and the session is
  untouched.
- **exit 0, empty stdout, always.** Per the hooks contract, exit 2 blocks the
  session and SessionStart stdout is injected into Claude's context. `main` has a
  single `os.Exit(0)` and writes nothing to stdout on any path.
- **Lock, not process-scan.** `O_CREATE|O_EXCL` on
  `<tmp>/claudingtin-<session_id>.lock` is the whole dedup: cheap, race-free, no
  dependency. It is never cleaned up; a stale lock after a companion crash within
  the same `session_id` is accepted (a fresh Claude Code session gets a fresh id)
  and noted for a later refinement.
- **Detached spawn** has no in-repo precedent, so keep it minimal: build-tagged
  `SysProcAttr`, stdio to `os.DevNull`, `Start()` then return. `DETACHED_PROCESS`
  (`0x00000008`) is a local const to avoid a `golang.org/x/sys` dependency.

## Verification

**Commands** (repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `plugin/cmd/session-start` suite green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no diff
- `shellcheck $(git ls-files '*.sh')` — no output (covers the new
  `session-start.sh`)
- `bash scripts/check_deps.sh` — exit 0, edges unchanged; `plugin` imports no
  sibling
- `bash scripts/workspace_modules.sh --check` — OK
- `bash scripts/checks_test.sh` — exit 0
- `python3 -c 'import json; json.load(open("plugin/hooks/hooks.json")); json.load(open("plugin/.claude-plugin/plugin.json"))'`
  — both parse

**Manual checks:**
- `ci.yml` / `release.yml`: a `session-start` build for all four `os/arch`
  targets; no "Story 1.7 / later story" deferral comment remains.
- `README.md`: module table + `plugin/bin/` refresh step mention the launcher
  binary; `plugin/.claude-plugin/plugin.json` + `hooks/hooks.json` referenced.

## Suggested Review Order

**The launch decision (pure core)**

- Entry point: the whole policy in one linear function — every early return is a fail-open no-op.
  [`launch.go:138`](../../plugin/cmd/session-start/launch.go#L138)
- `os.Executable()` first, `${CLAUDE_PLUGIN_ROOT}` fallback; never returns a relative path, so the companion can't resolve against the user's CWD.
  [`launch.go:59`](../../plugin/cmd/session-start/launch.go#L59)
- Empty `session_id` → no-op, so a missing id can't wedge every later session on a shared lock.
  [`launch.go:150`](../../plugin/cmd/session-start/launch.go#L150)
- Unresolved root or non-runnable file → no spawn; then the `O_CREATE|O_EXCL` per-session lock, never removed.
  [`launch.go:158`](../../plugin/cmd/session-start/launch.go#L158)

**Always exit 0, never touch the session**

- Single deferred `os.Exit(0)` that also swallows a panic; the error path writes one non-identifying stderr line, never stdout.
  [`main.go:31`](../../plugin/cmd/session-start/main.go#L31)
- The wrapper deliberately does not `exec` — a failed `exec` under dash exits 126/127; run-as-child then `exit 0` cannot.
  [`session-start.sh:34`](../../plugin/hooks/session-start.sh#L34)

**Detached spawn**

- `Setsid` + stdio to `/dev/null` + `Start` (never `Wait`): the companion outlives the hook.
  [`spawn_unix.go:15`](../../plugin/cmd/session-start/spawn_unix.go#L15)
- Windows twin: `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS`, `detachedProcess` a local const (no `x/sys` dep).
  [`spawn_windows.go:20`](../../plugin/cmd/session-start/spawn_windows.go#L20)

**Host → platform dispatch (shell and Go must agree)**

- `uname` → Go's `<os>-<arch>`, incl. `MINGW*/MSYS*/CYGWIN* → windows` + `.exe`; unknown → `exit 0`.
  [`session-start.sh:15`](../../plugin/hooks/session-start.sh#L15)
- The Go side's matching `runtime.GOOS+"-"+runtime.GOARCH` + `.exe`.
  [`launch.go:75`](../../plugin/cmd/session-start/launch.go#L75)

**Registration**

- One `SessionStart` entry: `matcher: "startup|resume"`, `timeout: 10`.
  [`hooks.json:4`](../../plugin/hooks/hooks.json#L4)
- Manifest with `"hooks": "./hooks/hooks.json"`.
  [`plugin.json:1`](../../plugin/.claude-plugin/plugin.json#L1)

**Build & release wiring**

- `cross-build`: the launcher joins the companion's four-target matrix + upload.
  [`ci.yml:122`](../../.github/workflows/ci.yml#L122)
- `binaries`: four `session-start-<os>-<arch>` attached to the release.
  [`release.yml:61`](../../.github/workflows/release.yml#L61)
- Local `go build` outputs for the new `cmd` ignored.
  [`.gitignore:9`](../../.gitignore#L9)
- Module table, the `SessionStart` hook-chain paragraph, and `CLAUDINGTIN_DISABLE`.
  [`README.md:153`](../../README.md#L153)

**Peripherals — tests**

- Every I/O-matrix row, incl. `missing session_id` and `non-executable companion binary`.
  [`launch_test.go:231`](../../plugin/cmd/session-start/launch_test.go#L231)
- Real `detachedSpawn`: returns fast, child is its own session leader.
  [`spawn_unix_test.go:22`](../../plugin/cmd/session-start/spawn_unix_test.go#L22)
- Real compiled launcher through the real wrapper: exit 0 + empty stdout on spawn-error and malformed stdin.
  [`launch_test.go:496`](../../plugin/cmd/session-start/launch_test.go#L496)
- The real `session-start.sh` under `sh`: stdin passthrough; silent when the binary is absent.
  [`launch_test.go:432`](../../plugin/cmd/session-start/launch_test.go#L432)
