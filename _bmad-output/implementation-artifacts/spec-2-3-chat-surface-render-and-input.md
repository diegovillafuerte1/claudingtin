---
title: 'Story 2.3 — Chat surface: render and input'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: '7b480ca0643912fea381b5f7950d2ad177678cfe'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-2-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The companion's entire UI is one status line (`internal/statusline`). Story 2.1 makes the backend send `matched` (with the Story 2.2 opener), but the companion ignores that frame — a paired user has no surface to talk in.

**Approach:** Add a Bubble Tea v2 chat surface (`companion/internal/chatui`) that `run` launches on `matched`: a peer header, a scrollable history seeded with the opener, a length-capped input box, and always-visible block / report / leave controls, with peer text rendered through an inert-literal sanitizer. The real message relay is Story 2.4; the searching spinner and searching→matched "spin" are Story 2.5.

## Boundaries & Constraints

**Always:**
- New package `companion/internal/chatui` holds the Bubble Tea v2 model, view, and the `inert` sanitizer; it imports only `charmbracelet/bubbletea/v2`, `charmbracelet/bubbles/v2`, `charmbracelet/lipgloss/v2`, `proto`, and stdlib.
- The view shows, at all times, none scrolling away or hidden by focus: a header with the peer `pseudonym` and, when non-empty, `blurb` (both from the `matched` payload — empty until Epic 5, so a neutral placeholder name renders when blank); a scrollable history whose first, seeded entry is the `opener`; an input box; and a persistently visible affordance naming block, report, and leave ("my Claude came back").
- Peer and opener text render only through `inert(s string) string`: keep printable runes and `\n`; neutralize every C0 byte except `\n` (0x00–0x1F), DEL (0x7F), C1 (0x80–0x9F), and ESC so no ANSI/CSI/OSC sequence reaches the terminal; map invalid UTF-8 to U+FFFD; do no markdown, HTML, or link interpretation and emit no OSC 8 hyperlink.
- The user's own typed text appends to the history optimistically on send, keyed by a fresh `client_msg_id` (stdlib `crypto/rand` → short base64, the shape of `backend/internal/hub/id.go`). Story 2.3 puts nothing on the wire and receives no `chat_msg`.
- Input is capped client-side at `const maxInputRunes = 2000`: keystrokes past the cap are refused without repeated bell; paste is truncated to the cap.
- No file / image / audio / video / attachment affordance exists anywhere in the surface or its key map. No read-receipt / "seen" / "delivered" state is ever rendered.
- `block` / `report` / `leave` are key-bound and always shown but here only emit a typed intent `run` logs content-free (no message text, no account key) — no `proto.Block` / `Report` / `Leave` frame is sent. `leave`, `session_ended`, and ctx-cancel each tear the program down and return control to the status line; a clean end keeps exit code 0.
- `wsclient` surfaces the inbound `proto.Matched` frame to `run` while keeping its `please_update` / `session_ended` handling and its "ignore every other frame" default unchanged.
- `run.loop` launches the chat program on the first `matched` (a `tea.Program` over `cfg.In` / `cfg.Out`) and suppresses `statusline` writes while it runs; every existing pre-match branch keeps its behavior, and every current `run` / `statusline` test stays green.
- All copy follows `voice.md` — warm, lowercase-friendly, never names the machinery; leave carries the "my Claude came back" fiction with no apology and no "are you sure?".
- `go build`, `go vet`, `go test -race` over the `go.work` set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` all pass; `check_deps.sh` still reports only `companion → proto` and `backend → proto`.

**Ask First:**
- Any `/proto` change — the story needs none (`matched` already carries the fields; no `typing` type is added) → HALT.
- A UI / markdown / ANSI third-party dependency other than the three Charm v2 modules at the `ARCHITECTURE-SPINE.md:258–260` majors → HALT.
- If rendering `matched` cleanly turns out to require replacing the status line's pre-match phases with Bubble Tea rather than launching the chat program alongside it → HALT (reshapes Story 2.5).
- Any input cap other than 2000 runes → HALT.

**Never:**
- No message relay, no `chat_msg` send/receive, no round-trip — Story 2.4.
- No searching spinner, no "spin", no `queued`-state rendering — Story 2.5.
- No typing indicator, no `typing` proto message — deferred; only "no read receipts" is in scope.
- No backend behavior for block/report/leave, nothing on the wire for them — Epics 3–4.
- No `pseudonym` / `blurb` population — Epic 5; render placeholders.
- No link-inerting or authoritative length caps as a backend concern — Epic 4.
- No SQLite / persistence; no chat content to disk or logs; nothing pushed into the Claude Code TUI or written to `cfg.Out` outside the Bubble Tea frame.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| Match arrives | `matched{pseudonym:"", blurb:"", opener:"o"}` while status shows waiting / free-to-chat | chat program starts; header shows the placeholder name, no blurb line; history holds `o` as its only entry; input focused and empty | N/A |
| Peer text with ANSI | entry `"\x1b[31mhi\x1b[0m \x07\x1b]8;;http://x\x07link"` | rendered inert — ESC/CSI/BEL/OSC dropped or shown as harmless literals, never interpreted; no colour, no bell, no hyperlink | invalid UTF-8 → U+FFFD |
| Multi-line peer text | `"line one\nline two"` | two visual lines within one history entry | N/A |
| Input at cap | user holds a key or pastes 5000 chars, cap 2000 | input stops at 2000 runes; paste truncated to 2000; no repeated bell | N/A |
| Send | user types `"hey"`, presses Enter | `"hey"` appended immediately as the user's own entry keyed by a fresh `client_msg_id`; nothing on the wire; input clears | N/A |
| Attachment probe | user hunts the key map for a "send file" binding | no such binding; nothing happens | N/A |
| Leave | user triggers the leave control | program exits; `run` logs a content-free leave intent; status line resumes; no `leave` frame sent | N/A |
| session_ended mid-chat | backend sends `proto.SessionEnded` while chat is open | program exits cleanly; status line resumes; exit code stays 0 | N/A |
| Resize | pane resized while chat open | header, history, input, controls all stay visible and re-flow; history keeps its near-bottom scroll position | N/A |

</frozen-after-approval>

## Code Map

- `companion/internal/chatui/chatui.go` — **new.** Bubble Tea v2 `Model`: peer `pseudonym`/`blurb`/`opener`; a `bubbles/v2/viewport` history; a `bubbles/v2/textarea` (or small custom input) capped at `maxInputRunes`; a `key.Binding` set for send / block / report / leave (no attachment binding). `Update` handles `tea.KeyPressMsg`, `tea.WindowSizeMsg`, an append-history `tea.Msg`, and emits `blockIntentMsg` / `reportIntentMsg` / `leaveIntentMsg` (leave also returns `tea.Quit`). `View` composes header + `inert`-rendered history + input + controls bar via `lipgloss/v2`. All user-facing strings here, `voice.md`-compliant. `const maxInputRunes = 2000`.
- `companion/internal/chatui/inert.go` — **new.** `inert(s string) string` per the Always rule; mirror the control-byte handling of `backend/openers.go:48`.
- `companion/internal/chatui/chatui_test.go` — **new.** Drive `Model.Update`/`View` directly (no PTY): opener seeded once at top; header with/without blurb; ANSI/BEL/ESC/OSC-8 in an entry render inert (assert on `View()`, `%q`); multi-line entry; input hard-stops at the cap and paste truncates; Enter appends the user line keyed by a `client_msg_id`, clears input, sends nothing; no attachment binding; leave yields the intent / `tea.Quit`; `WindowSizeMsg` keeps all four regions present.
- `companion/internal/chatui/inert_test.go` — **new.** Table: CSI/SGR, bare ESC, BEL, DEL, C1 byte, OSC 8 hyperlink, `<b>` / `[md](x)` literal pass-through, invalid UTF-8 → U+FFFD, `\n` preserved, plain text unchanged.
- `companion/internal/wsclient/wsclient.go` — **edit.** `serve` (L222–261) discards non-terminal frames at L246–248. Add: on `proto.Matched`, deliver the payload to the caller — a new buffered `Matched chan proto.Matched` on `Client` drained by `run.loop`, or a new `EventKind` carrying it. Keep `PleaseUpdate` / `SessionEnded` and the ignore-default intact. Package doc "the two inbound frames that matter" (L1–11) → three.
- `companion/internal/wsclient/wsclient_test.go` — **edit.** New case: server writes a `proto.Matched`; the client surfaces it with fields intact; a non-matched non-terminal frame is still ignored; nothing logged.
- `companion/internal/run/run.go` — **edit.** `stateClient` (L122–126) gains the matched accessor mirroring `Events()`. `loop` (L155–300): a new `case` for the first `matched` launches `chatui.New(...)` inside a `tea.NewProgram` (over `cfg.In` / `cfg.Out`) on its own goroutine and suppresses `sl.Show` while it runs; on `leaveIntent`, `ResultSessionEnded`, or `ctx.Done()` it stops the program (`p.Quit()`, `p.Kill()` fallback), logs a content-free line to `cfg.Err`, resumes the status line. Every existing branch keeps its behavior.
- `companion/internal/run/run_test.go` — **edit.** Extend `fakeClient` with a `matched` channel + nil-safe `Matched()`. New tests: a `matched` starts the chat surface (assert through a seam — construction / a content-free "matched" log line, no PTY); `leaveIntent` and `ResultSessionEnded` each tear it down and return to the status line; existing pre-match tests unchanged.
- `companion/internal/statusline/statusline.go` — **read-only** except the stale "no chat surface yet" sentence in the package doc (L1–4) — refresh that one line.
- `companion/go.mod` / `companion/go.sum` — **edit.** Add the three Charm v2 modules at the spine majors (`ARCHITECTURE-SPINE.md:258–260`: bubbletea v2.0.x, bubbles v2.2.x, lipgloss v2.0.x); `go mod tidy` + `go work sync`.
- `companion/main.go` — **read-only.** `run.Run` signature unchanged; `cfg.In`/`Out`/`Err` already threaded (L89–92).
- `README.md` — **edit.** `## Companion` section: on `matched` the companion now shows a text chat surface (header, opener-seeded history, capped input, always-visible block/report/leave) and renders peer text inert; relay + spin are later stories.
- `scripts/check_deps.sh` / `.github/workflows/ci.yml` — **read-only.** The Charm deps are third-party under `companion`, which `check_deps.sh` permits (it restricts only `proto`); `companion → proto` unchanged. CI matrix already runs `go test -race`.

## Tasks & Acceptance

**Execution:**
- [x] `companion/go.mod` / `go.sum` — add bubbletea/bubbles/lipgloss v2 at the spine majors; `go mod tidy`, `go work sync`
- [x] `companion/internal/chatui/inert.go` — terminal-safe literal renderer (C0-except-`\n` / DEL / C1 / ESC neutralized, U+FFFD for bad UTF-8, no markup, no OSC 8)
- [x] `companion/internal/chatui/inert_test.go` — table over CSI/ESC/BEL/DEL/C1/OSC-8/HTML/markdown/invalid-UTF-8/newline/plain
- [x] `companion/internal/chatui/chatui.go` — Bubble Tea v2 model/update/view: header, opener-seeded history, capped input, block/report/leave bindings, no attachment affordance, no read receipts, `voice.md` copy
- [x] `companion/internal/chatui/chatui_test.go` — `Update`/`View` unit tests for every UI-local I/O-matrix row
- [x] `companion/internal/wsclient/wsclient.go` + `_test.go` — surface inbound `proto.Matched`; keep terminal-frame handling; test
- [x] `companion/internal/run/run.go` + `_test.go` — launch the chat program on first `matched`, tear it down on leave / `session_ended` / ctx-cancel, resume the status line; content-free logs; extend `fakeClient`
- [x] `companion/internal/statusline/statusline.go` — refresh the stale "no chat surface yet" doc sentence
- [x] `README.md` — Companion section: the on-match chat surface and inert rendering

**Acceptance Criteria:**
- Given a running companion that has received `matched`, when the chat surface renders, then it simultaneously shows the peer pseudonym/blurb header, a scrollable history with the opener as its first entry, an input box, and visible block, report, and leave controls.
- Given a peer or opener string with ANSI/CSI/OSC escape sequences, control bytes, HTML, or markdown, when it renders, then it appears as inert literal text with no escape sequence reaching the terminal and no markup or link interpretation, verified by a test asserting on `View()` output.
- Given the chat surface, when the key map and view are inspected, then no file/image/audio/attachment affordance exists anywhere and typed input stops at the 2000-rune client cap with paste truncated.
- Given any history message, when it renders, then no read-receipt / "seen" / "delivered" state is shown for it.
- Given the user triggers leave, or the backend sends `session_ended`, or the context is cancelled, when the chat program stops, then control returns to the status line, the exit code is unchanged (0 on a clean end), and no `leave` / `block` / `report` frame was put on the wire.
- Given the finished tree, when `gofmt -l .`, `go vet`, `go test -race` over the `go.work` set, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` run, then all pass and `check_deps.sh` still reports only `companion → proto` and `backend → proto`.

## Spec Change Log

## Design Notes

**Launch alongside the status line, not replace it.** The Epic 1 `run.loop` + `statusline` plain-line contract is pinned by ~20 tests asserting exact copy on an `io.Writer`. Story 2.5 owns the `queued` state and the searching→matched transition — that is where a unified Bubble Tea program subsuming the status line belongs. Story 2.3 keeps blast radius to "additive surface, shown only during a match" so 2.4 (relay) and 2.5 (spin) compose onto it.

**Inert rendering is the security core** (spine §200–202: the backend inerts links / caps, the companion renders literal). In a terminal the threat is ESC/CSI/OSC bytes in peer text hijacking the pane — colour, cursor moves, clipboard via OSC 52, hyperlinks via OSC 8. `inert` is a whitelist: keep printable runes plus `\n`, replace everything else; its own file plus a table test makes the guarantee auditable.

**No PTY in tests.** Exercise `chatui.Model` through `Update`/`View` directly — deterministic, race-clean. `run`'s launch/teardown is asserted through a seam (construction plus a content-free log line), not a real terminal.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` and `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass, incl. new `chatui` and updated `wsclient` / `run`
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — clean after commit
- `bash scripts/check_deps.sh` — prints only `companion → proto`, `backend → proto`; `bash scripts/checks_test.sh` — exit 0

**Manual check:**
- Build the companion, run it in a tmux pane against a local `backend serve` with two ready clients; on match the pane shows the chat surface with the opener at the top and a focused input box; type past 2000 characters and confirm input stops; press the leave key and confirm the pane returns to the status line with the process still alive.

## Suggested Review Order

**Chat surface lifecycle (the wiring)**

- Entry point — the first `matched` spins the Bubble Tea program on its own goroutine; `notify` is non-blocking so a full buffer never freezes the UI.
  [`run.go:226`](../../companion/internal/run/run.go#L226)
- Teardown — `Quit` → bounded grace → `Kill` → bounded again; `resume` gates whether the status line comes back.
  [`run.go:249`](../../companion/internal/run/run.go#L249)
- Only the first `matched` opens a surface; a stray second one while it is up is ignored (Epic 3 owns re-match).
  [`run.go:408`](../../companion/internal/run/run.go#L408)
- block / report / leave are logged content-free through `chat.Println` — a raw `cfg.Err` write would land mid-frame on the same tty.
  [`run.go:416`](../../companion/internal/run/run.go#L416)
- `session_ended` tears the surface down with `resume:false` — the process returns immediately, so a status repaint would only flash.
  [`run.go:325`](../../companion/internal/run/run.go#L325)
- The `chatProgram` seam gains `Println` so `*tea.Program` satisfies it and the fake can capture the lines.
  [`run.go:139`](../../companion/internal/run/run.go#L139)

**Inbound `matched` plumbing**

- `serve` hands `proto.Matched` to the caller and keeps serving; the send is non-blocking so a terminal frame behind it is never delayed.
  [`wsclient.go:256`](../../companion/internal/wsclient/wsclient.go#L256)
- `Matched()` accessor — buffered by one, never closed, drop-on-undrained semantics documented.
  [`wsclient.go:121`](../../companion/internal/wsclient/wsclient.go#L121)

**Terminal-safe rendering (the security core)**

- `inert` — whitelist on `unicode.IsGraphic` plus the zero-width joiner; every C0/C1/DEL and other format code point (bidi overrides included) is dropped.
  [`inert.go:35`](../../companion/internal/chatui/inert.go#L35)
- Why the ZWJ is the one kept exception (emoji sequences) and ZWSP / word-joiner / bidi are not.
  [`inert.go:13`](../../companion/internal/chatui/inert.go#L13)

**Chat model — render and input**

- `New` — opener seeded only when non-empty; peer pseudonym/blurb collapsed to one line; viewport gets the restricted scroll key map.
  [`chatui.go:163`](../../companion/internal/chatui/chatui.go#L163)
- `Update` — intent keys, then scroll keys, then text; a peer line sticks to the bottom only if the reader was already there.
  [`chatui.go:202`](../../companion/internal/chatui/chatui.go#L202)
- `historyKeyMap` / `isHistoryScrollKey` — pgup/pgdn/ctrl+u/ctrl+d scroll the history; bare letters stay text for the input box.
  [`chatui.go:130`](../../companion/internal/chatui/chatui.go#L130)
- `View` sets `MouseModeCellMotion` so the wheel reaches the viewport.
  [`chatui.go:273`](../../companion/internal/chatui/chatui.go#L273)
- `relayout` budgets the *wrapped* controls-bar height, not a hard-coded row, so a narrow pane does not push content off-screen.
  [`chatui.go:300`](../../companion/internal/chatui/chatui.go#L300)
- `controlsText` is rendered from the key bindings — one source of truth for the hint copy.
  [`chatui.go:358`](../../companion/internal/chatui/chatui.go#L358)
- `appendSelf` — a whitespace-only send still clears the input box.
  [`chatui.go:282`](../../companion/internal/chatui/chatui.go#L282)

**Docs & tests**

- statusline package doc: the ambient line, suppressed while the chat owns the pane.
  [`statusline.go:1`](../../companion/internal/statusline/statusline.go#L1)
- `inert` table — ZWJ/VS/skin-tone kept, ZWSP/word-joiner/bidi dropped, plus the existing ESC/CSI/OSC coverage.
  [`inert_test.go:34`](../../companion/internal/chatui/inert_test.go#L34)
- Layout + scroll tests: empty opener, peer-newline header, wrapped controls, page-key scroll, no-force-scroll.
  [`chatui_test.go:369`](../../companion/internal/chatui/chatui_test.go#L369)
- Loop tests: status suppressed while active, second `matched` ignored, `Kill` path when `Quit` is ignored, content-free intent logs.
  [`run_test.go:1284`](../../companion/internal/run/run_test.go#L1284)
- Over-the-wire: a `proto.Matched` is surfaced intact and serving continues.
  [`wsclient_test.go:352`](../../companion/internal/wsclient/wsclient_test.go#L352)
