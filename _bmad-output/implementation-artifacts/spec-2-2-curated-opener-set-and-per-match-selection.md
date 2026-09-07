---
title: 'Story 2.2 — Curated opener set and per-match selection'
type: 'feature'
created: '2026-09-06'
status: 'done'
review_loop_iteration: 0
baseline_commit: 'ada9aed3ba5f46cb46a11cac0cab337cc59051b5'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-2-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Story 2.1 finalized the `matched` wire shape but ships `opener: ""`. A freshly matched pair gets a blank chat and has to cold-open, which the product explicitly wants to avoid.

**Approach:** Add a contributor-editable curated opener file to the backend module, embedded at build time and parsed once behind a length-bound format check. The single-writer hub goroutine holds an in-memory rotation cursor and stamps one opener into every `proto.Matched` it mints, byte-identical for both peers, never repeating back-to-back.

## Boundaries & Constraints

**Always:**
- The opener set is the plain-text file `backend/openers.txt`, one opener per line. Blank lines and lines whose first non-whitespace character is `#` are ignored (the file carries a `#` header pointing at the guide). Entries are trimmed of surrounding whitespace before use.
- The file is embedded with `//go:embed` — no runtime file read, no new env var, no deployment change. Parsing happens once.
- A format check (a Go test over the real file) enforces every entry: valid UTF-8, no ASCII control bytes, 8–120 characters after trimming, no duplicate entries, and at least 2 entries total. A malformed file fails the test and (via a panic in the parse-once accessor) fails `backend serve` startup — it never ships a broken set silently.
- Opener selection happens only inside the `hub.Hub.Run` goroutine, in `drainQueue`, at the same point `session_id` is minted. The rotation cursor is a field on the goroutine-private `pairingState`; losing it on process restart is acceptable (AD-8, AD-10).
- Selection is a round-robin cursor: advance by one per match, wrap at the end. With ≥2 entries this guarantees the same opener is never used for two consecutive matches. Not random-with-repeats.
- Both peers of a pairing receive the exact same `opener` string in their `matched` frame (one `proto.Matched` value delivered to both `Outbound` channels, as today).
- `matched` still carries fully rendered opener **text**, never an index or id (spine DP-14). No `/proto` shape change — `Matched.Opener` already exists.
- The one-paragraph opener voice guide ships as `backend/openers.md`, sourced verbatim from the "Openers" section of `_bmad-output/specs/spec-claudingtin/voice.md`, with a short "how to add one" note. `openers.txt`'s header comment links to it.
- `go build` / `go vet` / `go test -race` over the `go.work` module set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh` (still only `backend → proto`), and `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Adding any third-party dependency. `embed` is stdlib. Anything else → HALT.
- Any `/proto` change (the story needs none) → HALT.
- If honoring the literal `backend/openers.txt` path forces something other than a small root-level `package backend` (embed cannot reach a parent directory) → HALT and discuss before relocating the file.
- Introducing randomness, a shuffle, or a persisted cursor instead of the plain round-robin → HALT.

**Never:**
- No opener *generation* per match, no templating, no per-user personalization.
- No companion changes. Story 2.5 renders the opener; here it only needs to arrive in `matched`. The Epic 1 companion still ignores the frame.
- No `chat_msg` / relay work (Story 2.4), no `pseudonym` / `blurb` population (Epic 5) — those stay `""`.
- No SQLite / persistence — the opener list is embedded, the cursor is in-memory.
- No new HTTP routes, no changes to `serveConn` frame routing or `writeFrame`.
- No opener content sanitization pass in the companion or backend beyond the format check (backend link-inerting / caps are Epic 4; opener text is trusted repo content).

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| First match | openers `[o0, o1, o2]`, cursor 0; A and B pair | both get `matched.opener == "o0"`, byte-identical; cursor advances to 1 | N/A |
| Consecutive matches | same set; three pairs form in succession | openers are `o0`, `o1`, `o2` in order; no two consecutive matches share an opener | N/A |
| Wrap | set of N, cursor at N-1; a pair forms | opener is entry N-1, cursor wraps to 0; the following match gets entry 0 (still not equal to N-1) | N/A |
| Multiple pairs in one drain | 4 sessions ready at once → 2 pairs formed in one `drainQueue` call | the two pairs get consecutive, distinct openers from the rotation | N/A |
| Comment / blank lines | `openers.txt` has a `#`-comment header and blank separators | those lines are not entries; only real openers are in the rotation | N/A |
| Entry out of bounds | a line is 3 chars, or 200 chars, or has a tab/control byte, or is a duplicate | the format test fails naming the offending entry; `Openers()` panics rather than returning a bad set | test failure / startup panic |
| Empty or 1-entry file | file parses to 0 or 1 entries | format test fails; `Openers()` panics | test failure / startup panic |
| Teardown then re-pair | a pairing is torn down, both re-`ready`, they re-pair | they get the next opener in rotation (cursor is not rewound by teardown) | N/A |

</frozen-after-approval>

## Code Map

- `backend/openers.txt` — **new.** The curated set, one opener per line. Leading `#` header comment (2–4 lines) points contributors at `openers.md`. Seed with ~24 openers written to the `voice.md` guide: playful, disarming, low-stakes human curiosity; never survey/interview-like. All entries 8–120 chars, no duplicates.
- `backend/openers.md` — **new.** The one-paragraph opener voice guide, copied verbatim from the "Openers" subsection of `_bmad-output/specs/spec-claudingtin/voice.md` (lines 30–35), plus a 2–3 line "To add an opener: append a line to `openers.txt`; keep it 8–120 chars; run `go test ./...`."
- `backend/openers.go` — **new.** `package backend` (root of the backend module — the only place `//go:embed openers.txt` can see the file). `//go:embed openers.txt` into a `string`. `const openerMinLen = 8`, `openerMaxLen = 120`. `parseOpeners(raw string) ([]string, error)`: split on `\n`, drop blank and `#`-prefixed (post-trim) lines, trim each remaining line, validate (utf8.ValidString, no `r < 0x20`, len in `[min,max]`), reject duplicates, require `len >= 2`; return the slice or a descriptive error. `Openers() []string`: `sync.Once` + `parseOpeners`; on error `panic` with the message (embedded file is a build-time artifact — a bad one is a repo bug, and failing `serve` startup is correct). Returns a fresh copy so callers can't mutate the shared slice.
- `backend/openers_test.go` — **new.** Table/subtests: (1) the real embedded file parses without error and yields `>= 2` entries; (2) every entry passes the bound + charset + UTF-8 rules (guards future contributions); (3) no duplicates; (4) `parseOpeners` unit cases — ignores `#` and blank lines, trims, rejects a too-short / too-long / control-char / duplicate entry, rejects a 0- and 1-entry input.
- `backend/internal/hub/hub.go` — **edit.** Package doc (L1–9) and `pairingState` doc (L158–164) mention the hub owns the queue, pairings, and `session_id` mint — add the opener rotation cursor to that list.
  - `pairingState` struct (L161–164): add `openers []string` and `openerCursor int`.
  - `Run` (L81–83): build `p := &pairingState{pairings: ..., openers: claudingtin.Openers()}` (import `github.com/diegovillafuerte1/claudingtin/backend` — intra-module, `check_deps.sh` unaffected; pick a clean import alias, e.g. `backendpkg` or dot-free `claudingtin`).
  - `drainQueue` (L217–241): where `m := proto.Matched{SessionID: newSessionID()}` is built (L235), also set `Opener: p.nextOpener()`.
  - new method `nextOpener() string` on `*pairingState`: `if len(p.openers) == 0 { return "" }` (defensive; `Run` always populates it); `o := p.openers[p.openerCursor]; p.openerCursor = (p.openerCursor + 1) % len(p.openers); return o`.
- `backend/internal/hub/hub_test.go` — **edit.**
  - `newSession` / `startHub` (L12–30): hub tests build `pairingState` via `Run`, which now calls `claudingtin.Openers()` — the real embedded file is used, no test wiring needed. Confirm `startHub` still works unchanged.
  - `TestTwoReadyMatchWithEqualSessionID` (L201–224): L220–221 currently *fails* if `ma.Opener != ""`. Change to: `ma.Opener` is non-empty, `ma.Opener == mb.Opener`, and it is one of `claudingtin.Openers()`. Keep the `Pseudonym`/`Blurb` empty assertions.
  - `TestFIFOOrderWithThreeWaiters` (L249+) and other `matchedFrame` callers (L264–265, L369–370, L390–391, L464): they don't assert on `Opener`; leave as-is.
  - **new** `TestOpenerRotationNeverRepeatsBackToBack`: form `len(Openers())+2` pairings in sequence (register + ready pairs, drain each), collect each pair's opener, assert (a) every opener is in the set, (b) no two consecutive are equal, (c) the sequence walks the set in file order and wraps.
- `backend/internal/server/server_test.go` — **edit.** `readMatched` (L245–258) unchanged. `Test... ` at L474–484: L483–484 currently fails if `ma.Opener != ""` — change to assert `ma.Opener` non-empty and `ma.Opener == mb.Opener`. Other `readMatched` callers (L498–499, L537–538, L558–559, L748–749) don't touch `Opener`; leave them. Optionally add one over-the-wire assertion that two paired dials read the identical `opener` text.
- `proto/messages.go` — **edit (doc only).** `Matched` doc comment (L114–118): "Opener ... (empty until Story 2.2 adds selection)" → "Opener is the rotating pre-written conversation opener the backend selects per match." No struct/tag/registry change.
- `proto/messages_test.go` — **read-only.** `roundTripSamples` L28 already populates `Matched{... Opener: "what are you avoiding right now?"}`; no change.
- `backend/cmd/serve/main.go` — **read-only.** `hub.New()` + `go h.Run(hubCtx)` unchanged; the embedded openers are pulled in transitively via `hub` → root package. A malformed file panics here at first match-drain… actually at `Run` start (`claudingtin.Openers()` in `Run`) — startup, which is the intended fail-closed behavior.
- `README.md` — **edit.** `## Backend` section (the `/ws` bullet, ~L46): update "(`pseudonym` / `blurb` / `opener` are empty until later stories)" → `opener` now carries a rotating curated opener; add one sentence: the set is `backend/openers.txt` (contributor-editable, format-bound by `backend/openers_test.go`), guide in `backend/openers.md`.
- `scripts/check_deps.sh` / `.github/workflows/ci.yml` — **read-only.** New root `package backend` is intra-module; `check_deps.sh` keys on the `backend` module name and skips self-imports. CI already runs `go test -race` over the matrix.

## Tasks & Acceptance

**Execution:**
- [x] `backend/openers.md` — add the voice guide (verbatim "Openers" section from `voice.md`) + a short "how to add one" note
- [x] `backend/openers.txt` — seed ~24 on-voice openers, one per line, with a `#` header linking to `openers.md`
- [x] `backend/openers.go` — `package backend`; `//go:embed openers.txt`; `parseOpeners` with the 8–120 / UTF-8 / no-control / no-dup / `>=2` rules; `Openers()` via `sync.Once`, panic on parse error, return a copy
- [x] `backend/openers_test.go` — real-file parse + per-entry bound/charset/dup checks; `parseOpeners` unit cases (ignore `#`/blank, trim, reject short/long/control/dup, reject 0- and 1-entry)
- [x] `backend/internal/hub/hub.go` — `pairingState.openers` + `openerCursor`; populate in `Run` from `claudingtin.Openers()`; `nextOpener()`; stamp `Opener` in `drainQueue`; update the two doc comments
- [x] `backend/internal/hub/hub_test.go` — fix `TestTwoReadyMatchWithEqualSessionID` opener assertion; add `TestOpenerRotationNeverRepeatsBackToBack`; matrix rows "multiple pairs in one drain" (`TestMultiplePairsInOneDrainGetConsecutiveOpeners`) and "teardown then re-pair" (`TestTeardownDoesNotRewindOpenerCursor`)
- [x] `backend/internal/server/server_test.go` — fix the `ma.Opener`/`mb.Opener` assertion at L483–484; optional over-the-wire identical-opener check
- [x] `proto/messages.go` — update the `Matched.Opener` doc comment
- [x] `README.md` — Backend section: `opener` now rotates a curated set; name `openers.txt` / `openers.md` / the format test
- [x] `go work sync` — no diff after commit

**Acceptance Criteria:**
- Given the running `backend serve` process and two clients that send `ready`, when they are paired, then both `matched` frames carry the same non-empty `opener` string drawn from `backend/openers.txt`.
- Given N successive pairings on one process, when their openers are collected in order, then no two consecutive openers are equal and each is a verbatim entry of the embedded set (round-robin, file order, wrapping).
- Given a contributor edits `backend/openers.txt`, when an entry is empty, shorter than 8 or longer than 120 characters, contains a control byte, or duplicates another entry, then `go test ./...` fails and names the offending entry, and `backend serve` panics on startup rather than serving a malformed set.
- Given the opener rotation cursor, when it is mutated, then the mutation happens only inside `hub.Hub.Run` (in `drainQueue`), verified by `go test -race` staying clean and by inspection that `openerCursor` is referenced only within `pairingState` methods called from `Run`.
- Given `gofmt -l .`, `go vet`, `go test -race` over the `go.work` module set, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` on the finished tree, when they run, then all pass and `check_deps.sh` still reports only `backend → proto`.

## Spec Change Log

## Design Notes

**Why a root `package backend`.** `//go:embed` cannot reference a parent directory, so the file it embeds must sit in the same directory as (or below) the `.go` file that embeds it. Honoring the chosen path `backend/openers.txt` means a small package at the backend module root (`backend/openers.go`, importable as `github.com/diegovillafuerte1/claudingtin/backend`). This is a legitimate Go layout (root package alongside `cmd/` and `internal/`) and keeps the opener file maximally discoverable next to `go.mod`. `internal/hub` importing the module root is intra-module and does not touch the `backend → proto` dependency edge.

**Round-robin, not shuffle.** The requirement is only "never back-to-back" and "not random-with-repeats". A cursor that advances by one and wraps is the minimal thing that satisfies both, is trivially assertable in a test, and needs no RNG. Starting at index 0 every process start is fine — the cursor is explicitly allowed to be lost on restart (epic context / AD-10), and "back-to-back" is a within-process property.

**`nextOpener` sketch:**
```go
func (p *pairingState) nextOpener() string {
	if len(p.openers) == 0 {
		return ""
	}
	o := p.openers[p.openerCursor]
	p.openerCursor = (p.openerCursor + 1) % len(p.openers)
	return o
}
```

**Fail closed on a bad file.** `Openers()` panics on a parse error instead of returning `nil` / a partial set. The file is a compiled-in artifact; a malformed one is a repository bug that the format test catches in CI, and if it somehow reaches production, a crash at `serve` startup is safer than silently pairing people with a blank or garbage opener.

**Format check doubles as a contribution gate.** Iterating the real embedded entries in `openers_test.go` (not just synthetic `parseOpeners` inputs) means a contributor's new line is checked by `go test ./...` — the CI matrix already runs it.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` and `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `backend` openers tests, updated `hub` opener assertion + rotation test, updated `server` opener assertion green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no changes after commit
- `bash scripts/check_deps.sh` — still prints only `backend → proto`; `bash scripts/checks_test.sh` — exit 0

**Manual check:**
- `PORT=8080 go run ./backend/cmd/serve`; with two scripted ws clients on `/ws` each sending `hello` then `ready`, both receive a `matched` frame whose `opener` is the same non-empty string and appears in `backend/openers.txt`; a third and fourth client that then pair receive the next opener in file order, different from the first.
- Temporarily add a 3-character line to `backend/openers.txt` → `go test ./backend/...` fails naming that entry; `go run ./backend/cmd/serve` panics on startup. Revert.

## Suggested Review Order

**Opener set: parse, validate, embed**

- Entry point — the format contract every entry must meet; error names the offending line.
  [`openers.go:33`](../../backend/openers.go#L33)
- Rune-count length bound, BOM strip, and C0/DEL/C1 rejection — the hardening the review added.
  [`openers.go:34`](../../backend/openers.go#L34)
- Fail-closed accessor: parse once, panic on a bad embedded file, hand back a copy.
  [`openers.go:77`](../../backend/openers.go#L77)
- `//go:embed` is why this package sits at the module root (cannot reach a parent dir).
  [`openers.go:17`](../../backend/openers.go#L17)

**Per-match selection in the single-writer loop**

- Round-robin cursor: advance-and-wrap, never back-to-back for ≥2 openers; defensive empty guard.
  [`hub.go:256`](../../backend/internal/hub/hub.go#L256)
- The one call site — opener stamped into `Matched` beside the `session_id` mint, inside `drainQueue`.
  [`hub.go:243`](../../backend/internal/hub/hub.go#L243)
- Cursor lives on the goroutine-private `pairingState`; loaded once in `Run` from `backend.Openers()`.
  [`hub.go:87`](../../backend/internal/hub/hub.go#L87)

**Curated content + contributor guide**

- The 24 seed openers and the ignored `#` header; contributors append here.
  [`openers.txt:1`](../../backend/openers.txt#L1)
- Voice guide (verbatim from `voice.md`) plus the house-style + minimum-count conventions.
  [`openers.md:7`](../../backend/openers.md#L7)

**Wire contract & docs**

- `Matched.Opener` doc: rotates through the set, never repeats back-to-back, identical for both peers.
  [`messages.go:118`](../../proto/messages.go#L118)
- README Backend section: opener behaviour, the editable file, and the format test.
  [`README.md:34`](../../README.md#L34)

**Tests**

- Story 2.1's "opener must be empty" flipped to "non-empty, equal, in the set".
  [`hub_test.go:204`](../../backend/internal/hub/hub_test.go#L204)
- Rotation: file order, wrapping, no consecutive repeat over `len(set)+2` pairings.
  [`hub_test.go:293`](../../backend/internal/hub/hub_test.go#L293)
- Matrix row "multiple pairs in one drain" — a blocked head hides two waiters, then one re-drain forms both.
  [`hub_test.go:347`](../../backend/internal/hub/hub_test.go#L347)
- Matrix row "teardown then re-pair" — cursor is not rewound by a teardown.
  [`hub_test.go:417`](../../backend/internal/hub/hub_test.go#L417)
- `parseOpeners` units: comment/blank/trim, length boundaries, control bytes, BOM, dedup, <2 entries.
  [`openers_test.go:36`](../../backend/openers_test.go#L36)
- Real embedded file parses clean (named error, not a panic, when a contributor breaks it).
  [`openers_test.go:13`](../../backend/openers_test.go#L13)
- Over-the-wire: two dials get a byte-identical opener that is a member of the curated set.
  [`server_test.go:464`](../../backend/internal/server/server_test.go#L464)
