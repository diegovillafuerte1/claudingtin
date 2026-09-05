---
title: 'Story 1.3 — Transcript format spike and defensive parser'
type: 'feature'
created: '2026-09-05'
status: 'done'
review_loop_iteration: 0
baseline_commit: '0ed6c599e0158977102d06260f191955da02bb2b'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Think-time detection (Story 1.6) will tail the Claude Code session transcript, but that file's location and JSONL schema are undocumented and unstable — no pinned contract, no parser, no tests.

**Approach:** Run the spike against real transcripts and commit a repo note pinning the file location, line schema, observed version, and stability caveats. Ship a dependency-free turn-boundary state machine in `companion/internal/transcript` plus an fsnotify tailer that survives mid-line truncation, atomic rename, and rotation, emitting exactly one turn-start and one turn-end per user turn regardless of tool-call count. No websocket and no `ready` / `busy` wire messages — that is Story 1.6.

## Boundaries & Constraints

**Always:**
- The parser core is pure (decoded line in, `Event{Kind, At}` out, no I/O). The fsnotify tailer is a separate file that feeds it.
- Exactly one turn-start and one turn-end per user turn; N tool calls never split a turn; the several JSONL lines of one assistant reply (shared `message.id`) collapse to a single turn-end.
- Turn-start = a `user` line whose `message.content` is not a `tool_result` and that is not `isMeta` / `isSidechain` (covers human prompts, queued prompts, `task-notification` wake-ups). Turn-end = the first `assistant` line with a terminal `stop_reason` (`end_turn`, `stop_sequence`, `max_tokens`) after a turn-start.
- Unknown `type` values and unparseable lines are skipped, never fatal. A final line with no trailing newline is incomplete — buffer it, consume when the rest arrives.
- The tailer recovers from size shrinking below the read offset (truncation → re-read from 0) and from fsnotify REMOVE / RENAME on the path (rotation → re-open with a bounded retry). Cancellation via `context.Context`.
- `github.com/fsnotify/fsnotify v1.10.1` (pinned stack) is the only new dependency. Commit `go.sum` and any `go.work.sum`; `go work sync` stays a no-op afterward.
- New Go code is `gofmt`-clean and builds/tests on `ubuntu-latest` + `windows-latest`. No OS-specific path logic — the transcript path is an input.
- Fixtures are structural only: real transcript lines stripped of all conversation content (`thinking`, `text`, `tool_result` bodies, `usage`, tokens); keep `type`, `message` role / content-shape / `stop_reason` / `id`, `uuid`, `parentUuid`, `timestamp`, `isSidechain`, `isMeta`, `origin`, `promptSource`.

**Ask First:**
- Any dependency other than `fsnotify`.
- Wiring the parser into `companion/main.go` or emitting `ready` / `busy` (Story 1.6).
- Adding `macos-latest` to the CI test matrix (`ci.yml` defers it to Story 1.6).

**Never:**
- No websocket, reconnect/backoff, `proto` import from the new package, or `ready` / `busy` messages.
- No change to `check_deps.sh`, `workspace_modules.sh`, `release.yml`, `deploy/Dockerfile`.
- No real transcript excerpts or conversation content committed anywhere.
- No treating the transcript schema as a stable API — the note says plainly it is not.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Behavior | Error Handling |
|----------|--------------|-------------------|----------------|
| Normal turn | human prompt → assistant `end_turn` | one `TurnStart`, then one `TurnEnd` | — |
| Turn with N tool calls | prompt → N×(assistant `tool_use` → user `tool_result`) → assistant `end_turn` | exactly one `TurnStart` + one `TurnEnd` | — |
| Multi-line assistant reply | thinking + text lines, both `end_turn`, same `message.id` | single `TurnEnd` | dedupe via turn state |
| task-notification / queued prompt | `user` line, `origin.kind` ≠ `human`, string content, not meta | `TurnStart` if idle | — |
| tool_result / isMeta lines | `user` line with `tool_result` content, or `isMeta: true` | ignored, no event | — |
| Unknown line type | `atis-latch`, `bridge-session`, `mode`, … | skipped silently | — |
| Truncated / partial tail | file ends mid-JSON, or on-disk size < read offset | partial line buffered or re-read from 0; events resume | never panics |
| Atomic rename / rotation | watched path gets REMOVE / RENAME, new file appears | tailer re-opens the path within a bounded retry and resumes | retries exhausted → error on the error channel, no panic |
| Idle transcript | mode/system lines, no human turn yet | no events; `State()` reports idle | — |

</frozen-after-approval>

## Code Map

- `docs/transcript-format.md` -- **new.** Pinned note: path derivation (`~/.claude/projects/<cwd, '/' and '.' → '-'>/<sessionId>.jsonl`); newline-delimited JSON with a per-line `type`; the `user` / `assistant` shapes (Anthropic Messages API `message` payload plus `uuid` / `parentUuid` / `timestamp` / `sessionId` / `version` / `isSidechain`); observed Claude Code `version` `2.1.250`–`2.1.261`; caveats — not a public contract, many harness-specific line types present (`mode`, `permission-mode`, `atis-latch`, `bridge-session`, `last-prompt`, `ai-title`, `agent-name`, `queue-operation`, `cost-state`, `file-history-*`, `attachment`), field set grows across versions, `isSidechain` marks subagent turns.
- `companion/internal/transcript/parser.go` -- **new.** `Parser` state machine. `Event{Kind EventKind; At time.Time}`; `EventKind` = `TurnStart` / `TurnEnd`. Feed method takes one raw line, returns any events; `State()` exposes idle/in-turn for mid-file attach. Stdlib only.
- `companion/internal/transcript/watch.go` -- **new.** fsnotify tailer over the path: append-read with persisted offset, partial-line buffer, truncation + rotation handling per the matrix, `context` cancellation. Exposes event + error channels.
- `companion/internal/transcript/parser_test.go` / `watch_test.go` -- **new.** Table-driven over `testdata/`; watcher tests drive truncation / rename / rotation against a temp file.
- `companion/internal/transcript/testdata/*.jsonl` -- **new.** Sanitized structural fixtures, one per matrix row: `normal_turn`, `turn_with_tools`, `multiline_reply`, `task_notification`, `unknown_types`, `truncated_tail`, `idle_session`.
- `companion/go.mod` -- **edit.** Add `require github.com/fsnotify/fsnotify v1.10.1` (+ `golang.org/x/sys` indirect).
- `go.work.sum` -- **new** if `go work sync` produces it; commit it (CI `lint` runs `go work sync` then `git diff --exit-code`).
- `README.md` -- **edit.** Link `docs/transcript-format.md` near the module layout; note the companion now carries a third-party dependency.
- `companion/main.go` -- read-only. Stays the Story 1.6 stub and keeps the `companion → proto` edge alive for `check_deps.sh`; do not wire the parser in.
- `scripts/check_deps.sh`, `.github/workflows/ci.yml` -- read-only. `fsnotify` is an allowed third-party import for `companion`; the `test` matrix already covers `./companion/...` on Linux + Windows.

## Tasks & Acceptance

**Execution:**
- [x] `docs/transcript-format.md` -- write the pinned note from the spike (location, line schema, `user` / `assistant` shapes, observed `version`, stability caveats)
- [x] `companion/go.mod` -- add `fsnotify v1.10.1`; run `go work sync`; commit `go.sum` (no `go.work.sum` is produced — only `companion` carries external deps, fully covered by `companion/go.sum`)
- [x] `companion/internal/transcript/parser.go` -- pure turn-boundary state machine (`Event`, `EventKind`, feed method, `State`)
- [x] `companion/internal/transcript/watch.go` -- fsnotify tailer feeding the parser; truncation + atomic-rename + rotation recovery; `context` cancellation
- [x] `companion/internal/transcript/testdata/*.jsonl` -- sanitized structural fixtures for every I/O Matrix row (+ `interrupt` for the Design Notes interrupt case)
- [x] `companion/internal/transcript/parser_test.go` + `watch_test.go` -- table-driven tests covering every I/O Matrix row
- [x] `README.md` -- link the note; record the new companion dependency

**Acceptance Criteria:**
- Given the spike is done, when `docs/transcript-format.md` is read, then it records the transcript file location, the JSONL line schema, at least one observed Claude Code `version`, and an explicit "not a stable contract" caveat.
- Given `go test ./companion/...` on a clean checkout with module downloads available, when it runs on Linux and on Windows, then the parser and watcher suites pass on both.
- Given `bash scripts/check_deps.sh` and `bash scripts/workspace_modules.sh --check`, when they run on the resulting tree, then both exit 0 with output unchanged from Story 1.2.
- Given `gofmt -l companion` and `go vet ./companion/...`, when they run, then `gofmt` prints nothing and `vet` is clean.
- Given `go work sync` is re-run after the change, when `git diff --exit-code` is checked, then there is no diff.
- Given a mid-file attach on a transcript whose last user turn has no terminal `stop_reason` yet, when the parser is seeded from the tail, then `State()` reports an in-progress turn.

## Design Notes

- **Structural, not whitelist, turn detection.** Turn-start is any `user` line that is not a `tool_result`, not `isMeta`, not `isSidechain` — not an enumeration of `origin.kind`. One rule covers human prompts, queued prompts, and `task-notification` wake-ups, and survives new `origin` kinds in later Claude Code versions.
- **Terminal `stop_reason`:** `end_turn`, `stop_sequence`, `max_tokens` end a turn; `tool_use`, `pause_turn`, `null`/absent do not. One reply spans several JSONL lines sharing a `message.id`, each with the final `stop_reason` — emit `TurnEnd` on the first, stay idle until the next turn-start.
- **Interrupt:** a turn-start while a turn is active emits `TurnEnd` for the old turn immediately before `TurnStart` for the new one.
- **Rotation vs truncation:** truncation = same file, smaller size → seek 0. Rotation = REMOVE / RENAME → close and re-open the path in a short bounded retry loop; retries exhausted → error on the error channel, never a panic.
- **Offline note:** `fsnotify` makes the companion's first build need module downloads (or a primed cache); committed `go.sum` / `go.work.sum` keep it reproducible. Vendoring is out of scope here.

## Verification

**Commands** (from repo root):
- `go work sync && git diff --exit-code` -- no diff
- `gofmt -l companion` -- no output
- `go build $(bash scripts/workspace_modules.sh)` and `go vet ./companion/...` -- exit 0
- `go test ./companion/...` -- parser + watcher suites pass
- `bash scripts/check_deps.sh` -- exit 0, verified edges unchanged from Story 1.2
- `bash scripts/workspace_modules.sh --check` -- exit 0, OK line
- `bash scripts/checks_test.sh` -- guard self-tests still PASS
- `shellcheck $(git ls-files '*.sh')` -- no output

**Manual checks:**
- Read `docs/transcript-format.md`: location, schema, an observed `version`, and stability caveats present; states the format is not a public contract.
- Open the PR: `ci.yml` `test` green on Linux + Windows; `lint` green (gofmt, `go work sync` no-op, `check_deps`, module guard).

## Suggested Review Order

**The turn-boundary rules (the core idea)**

- Entry point — how one raw JSONL line becomes zero, one, or two turn events; unknown/unparseable lines return nil.
  [`parser.go:127`](../../companion/internal/transcript/parser.go#L127)
- Turn-start: structural, not an `origin.kind` whitelist — non-`tool_result`, non-`isMeta`, non-`isSidechain` `user` line; open turn ⇒ interrupt (TurnEnd then TurnStart).
  [`parser.go:150`](../../companion/internal/transcript/parser.go#L150)
- Turn-end: first `assistant` line with a terminal `stop_reason` after a start; multi-line replies and subagent/meta lines can't end the turn.
  [`parser.go:173`](../../companion/internal/transcript/parser.go#L173)
- The terminal set — `end_turn` / `stop_sequence` / `max_tokens` / `refusal`; everything else (incl. `tool_use`, `pause_turn`, absent) keeps the turn open.
  [`parser.go:77`](../../companion/internal/transcript/parser.go#L77)
- `State()` — the one hook for a mid-file attach (Story 1.6 seeds from the tail and asks if a turn is still open).
  [`parser.go:98`](../../companion/internal/transcript/parser.go#L98)

**The pinned format note (the spike output)**

- Turn-start / turn-end rules as prose, and the "not a stable contract" framing they rest on.
  [`transcript-format.md:59`](../../docs/transcript-format.md#L59)
- File location + slug derivation, and why the parser never computes it (Story 1.7 passes the path in).
  [`transcript-format.md:11`](../../docs/transcript-format.md#L11)
- Caveats: `isSidechain`, version drift across `2.1.250`–`2.1.261`, growing field set.
  [`transcript-format.md:116`](../../docs/transcript-format.md#L116)

**The fsnotify tailer and its recovery paths**

- The run loop — fsnotify events, the 1s poll backstop, `ctx` cancellation; tuning knobs are snapshotted before the goroutine starts (race-free under test mutation).
  [`watch.go:123`](../../companion/internal/transcript/watch.go#L123)
- `readAppend` — offset tracking and truncation recovery (`size < offset` ⇒ fresh parser, re-read from 0).
  [`watch.go:194`](../../companion/internal/transcript/watch.go#L194)
- `reopen` — bounded retry for a rotated-away path; exhaustion delivers a fatal error and stops.
  [`watch.go:262`](../../companion/internal/transcript/watch.go#L262)
- `consume` — index-cursor line splitting with a buffered partial tail (linear on the seed path).
  [`watch.go:235`](../../companion/internal/transcript/watch.go#L235)
- `emitFatal` vs `emitErr` — fatal errors get a blocking send so a stop is never mistaken for a clean `ctx` cancel.
  [`watch.go:312`](../../companion/internal/transcript/watch.go#L312)
- `Watch` rejects a non-regular-file path up front (a directory would otherwise error every poll tick).
  [`watch.go:69`](../../companion/internal/transcript/watch.go#L69)

**Tests, fixtures, dependency**

- Parser table over every I/O-matrix row + the interrupt and sidechain-mid-turn fixtures.
  [`parser_test.go:51`](../../companion/internal/transcript/parser_test.go#L51)
- Watcher: fsnotify proven independent of the poll backstop (`pollInterval` set to 30s).
  [`watch_test.go:121`](../../companion/internal/transcript/watch_test.go#L121)
- Watcher: rotation recovery drives real `os.Rename` + a fresh file.
  [`watch_test.go:198`](../../companion/internal/transcript/watch_test.go#L198)
- The one new dependency, pinned.
  [`go.mod:7`](../../companion/go.mod#L7)
- README: companion dependency row + link to the format note.
  [`README.md:11`](../../README.md#L11)
