---
title: 'Story 1.5 — Account key at a reinstall-stable path'
type: 'feature'
created: '2026-09-05'
status: 'done'
review_loop_iteration: 0
baseline_commit: '0f5d210983340d1ff7713e60da9f81767ed74842'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The companion has no identity to present to the backend. Hook input
carries no Claude-account identifier, and a key stored inside the plugin
directory would be lost on every reinstall/update — so blocks and bans keyed to a
user could not survive a reinstall (AD-11, PRD NFR3/NFR4).

**Approach:** Add a stdlib-only `companion/internal/identity` package that, given
a config directory, loads or creates a random UUIDv4 stored at `account-key`
(mode `0600`), regenerates it when the file is empty or corrupt, and never emits
the key to logs or errors. Ship a `DefaultConfigDir()` helper over
`os.UserConfigDir()`; the real directory arrives as a launch argument in a later
story. `companion/main.go` stays the Story 1.6 stub — no wiring here.

## Boundaries & Constraints

**Always:**
- Surface is exactly `Load(configDir string) (string, error)` and
  `DefaultConfigDir() (string, error)`. `Load` touches
  `filepath.Join(configDir, "account-key")` only — it never resolves paths
  itself or reaches into the plugin directory.
- New key material is a UUIDv4 from `crypto/rand` (16 bytes, version nibble
  `0x40`, variant bits `0x80`, canonical `8-4-4-4-12` lowercase hex).
- Every write of the key file is mode `0600`; a missing `configDir` is created
  with `MkdirAll` mode `0700`. First create uses `O_CREATE|O_EXCL`; regeneration
  writes a sibling temp file then `os.Rename`. After any create/regenerate,
  re-read the file and return the on-disk value so racing first-run processes
  converge.
- A file that is missing, zero-length, whitespace-only, or not a canonical
  UUIDv4 string is treated as absent and (re)generated. A valid key is returned
  verbatim after trimming surrounding whitespace, file otherwise untouched.
- The key never appears in a returned `error`, a panic message, or any package
  output; the package writes no logs.
- stdlib only — `companion`'s third-party deps stay exactly `proto` + `fsnotify`.
  `go build`/`go vet`/`go test -race` over the `scripts/workspace_modules.sh`
  set, `gofmt -l .`, `go work sync` + `git diff --exit-code`,
  `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Adding any third-party module (e.g. `github.com/google/uuid`) instead of the
  `crypto/rand` helper.
- Any change to `companion/main.go` or to the `/proto` package.

**Never:**
- No wiring into `main.go`, no CLI arg parsing, no websocket / `hello` send —
  Stories 1.6 / 1.7.
- No hashing, fingerprinting, or logging of the key anywhere.
- No enforcement (blocks, cooldowns, bans, SQLite rows) — Epic 4.
- No config file or env var for the path; `DefaultConfigDir` is
  `os.UserConfigDir()` + `claudingtin` and nothing else.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| First run | `configDir` absent or present, no `account-key` file | `MkdirAll` `0700` if needed, generate UUIDv4, write `account-key` mode `0600`, return it | wrapped error, no key, if mkdir/create fails |
| Reuse | `account-key` holds a valid UUIDv4 (± trailing whitespace) | return that UUID trimmed; file bytes unchanged | N/A |
| Reinstall stability | key file persists while the plugin dir is deleted/recreated | `Load` returns the same UUID (path is outside the plugin dir) | N/A |
| Empty or corrupt file | 0 bytes, whitespace-only, or not a canonical UUIDv4 (garbage, truncated, wrong version/variant char) | detected as absent, regenerated via temp+rename mode `0600`, new UUID returned | N/A |
| Broader perms | existing valid key file is e.g. `0644` | best-effort `os.Chmod` to `0600`; key still returned | chmod failure non-fatal |
| Concurrent first run | two processes call `Load` on a missing file at once | both return a valid UUIDv4; `O_EXCL` loser reads the winner's file; post-write re-read makes callers converge on one key | `os.IsExist` → read path |
| Unwritable `configDir` | dir exists, not writable | wrapped error naming the failed op; no partial `account-key` left behind | error, no panic |
| Log/err scrub | any row above | no returned error string and no package output contains the UUID | N/A |
| `DefaultConfigDir` | `os.UserConfigDir()` resolves / fails | returns `<userConfigDir>/claudingtin` / propagates the error wrapped | wrapped error |

</frozen-after-approval>

## Code Map

- `companion/internal/identity/identity.go` — **new.** `DefaultConfigDir`,
  `Load`, unexported `newUUIDv4() (string, error)` (`crypto/rand`),
  `looksLikeUUIDv4(string) bool`, first-create + atomic-write helpers.
  `keyFileName = "account-key"`, `configSubdir = "claudingtin"` as unexported
  consts. Follow the `internal/transcript` style: full doc comments,
  `fmt.Errorf("identity: <op>: %w", err)` wrapping, no key in any message.
- `companion/internal/identity/identity_test.go` — **new.** Table-driven over the
  I/O & Edge-Case Matrix; `t.TempDir()` for `configDir`; perms asserted via
  `os.Stat(...).Mode().Perm()`; reinstall simulated by deleting a separate
  "plugin dir" between two `Load` calls on one `configDir`; concurrent `Load`
  under `-race`; scrub test greps the returned key out of every error string.
- `companion/main.go` — **read-only.** Stays the Story 1.6 stub; Story 1.6 calls
  `identity.Load` with the config-dir launch arg from Story 1.7.
- `companion/go.mod` / `companion/go.sum` — **read-only.** No new dependency.
- `README.md` — **edit.** Companion section: one short "Account key" note —
  UUIDv4 at `os.UserConfigDir()/claudingtin/account-key`, mode `0600`, outside
  the plugin dir, regenerated if empty/corrupt, never logged.
- `scripts/check_deps.sh`, `scripts/workspace_modules.sh` — **read-only.** New
  package is inside `companion`; the `companion → proto` edge is unaffected.
- Reference: `_bmad-output/planning-artifacts/architecture/architecture-claudingtin-2026-09-01/ARCHITECTURE-SPINE.md`
  AD-11; `epic-1-context.md` "Account key" bullet.

## Tasks & Acceptance

**Execution:**
- [x] `companion/internal/identity/identity.go` — implement `DefaultConfigDir` and
  `Load` (load-or-create, empty/corrupt detection + regenerate, `0600`, `O_EXCL`
  first create, temp+rename regenerate with post-write re-read, `crypto/rand`
  UUIDv4, best-effort perms tighten)
- [x] `companion/internal/identity/identity_test.go` — table-driven tests covering
  every I/O & Edge-Case Matrix row, plus the reinstall-stability simulation,
  concurrent `Load` under `-race`, and the key-scrub assertion
- [x] `README.md` — add the "Account key" note to the Companion section

**Acceptance Criteria:**
- Given a config dir with no key file, when `Load` runs, then a sibling "plugin
  dir" is deleted, then `Load` runs again, the two calls return the identical
  UUIDv4 and the key file's mode is `0600`.
- Given `bash scripts/check_deps.sh`, `bash scripts/checks_test.sh`,
  `gofmt -l .`, `go vet`, and `go work sync` + `git diff --exit-code` on the
  finished tree, when they run, then all pass and `companion`'s dependency set is
  still `proto` + `fsnotify`.
- Given `go test -race` over the workspace module set, when concurrent `Load`
  calls target a missing key file, then every call returns a canonical UUIDv4 and
  no data race is reported.

## Spec Change Log

## Design Notes

- **UUIDv4 without a dependency:** read 16 bytes from `crypto/rand`, set
  `b[6] = b[6]&0x0f | 0x40` and `b[8] = b[8]&0x3f | 0x80`, format the canonical
  byte groups as lowercase hex. Keeps `companion` at two third-party deps —
  matching the repo's stdlib-first stance (Story 1.4 gated even
  `coder/websocket` behind Ask First).
- **Convergence on first run:** first create is
  `os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)`; on `os.IsExist`
  fall through to the read path. After any create or regenerate, re-read the file
  and return the on-disk value.
- **Corruption check is canonical-form only:** length 36, `-` at indices
  8/13/18/23, hex elsewhere, char 14 is `4`, char 19 in `{8,9,a,b}`. Anything
  else is regenerated — no repair attempt.
- Filename `account-key` and subdir `claudingtin` are unexported consts, not
  tunables — `t.TempDir()` is enough for the tests.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `identity` suite green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no changes after commit
- `bash scripts/check_deps.sh` — exit 0, still prints `companion → proto`
- `bash scripts/checks_test.sh` — exit 0
- `go list -m all` in `companion/` — lists only `proto` and `fsnotify` (+ its `golang.org/x/sys` indirect); no new module

## Suggested Review Order

**Entry point — the load-or-create decision**

- The whole contract in one switch: MkdirAll `0700`, classify the file, then reuse / first-create / regenerate.
  [`identity.go:85`](../../companion/internal/identity/identity.go#L85)

**File classification**

- Bounded read (`LimitReader` 512B) then canonical-form check; missing vs. corrupt vs. valid, oversized ⇒ corrupt.
  [`identity.go:127`](../../companion/internal/identity/identity.go#L127)
- Canonical UUIDv4 test — lowercase hex, version `4`, variant `8/9/a/b`; anything else is regenerated, never repaired.
  [`identity.go:290`](../../companion/internal/identity/identity.go#L290)

**First create & the concurrency-convergence path**

- `O_CREATE|O_EXCL` write; on `ErrExist` defer to `resolveExisting`, on write/sync failure remove the partial file.
  [`identity.go:154`](../../companion/internal/identity/identity.go#L154)
- The `O_EXCL` loser polls the winner's file across the window; only a dead winner triggers a regenerate.
  [`identity.go:200`](../../companion/internal/identity/identity.go#L200)
- write → `Sync` → `Close`, each wrapped and op-named; the fsync is what makes the key survive power loss.
  [`identity.go:181`](../../companion/internal/identity/identity.go#L181)

**Regeneration & read-back**

- Sibling temp file + fsync + atomic `os.Rename`; deferred cleanup leaves nothing behind on failure.
  [`identity.go:218`](../../companion/internal/identity/identity.go#L218)
- Post-write re-read through `inspect` so racing callers converge on the on-disk value.
  [`identity.go:253`](../../companion/internal/identity/identity.go#L253)

**Default path resolution**

- `os.UserConfigDir()` + `claudingtin`, error wrapped and op-named — no env var, no override.
  [`identity.go:60`](../../companion/internal/identity/identity.go#L60)

**Tests — matrix coverage & the tricky ones**

- First run: `0700` dir, `0600` key, returned value equals on-disk.
  [`identity_test.go:47`](../../companion/internal/identity/identity_test.go#L47)
- 16 goroutines on a missing file converge on one key; runs under `-race`.
  [`identity_test.go:249`](../../companion/internal/identity/identity_test.go#L249)
- Forces the `resolveExisting` wait loop: zero-byte file, delayed writer, caller must return the written key.
  [`identity_test.go:491`](../../companion/internal/identity/identity_test.go#L491)
- Empty / whitespace / garbage / wrong version-variant / uppercase / oversized ⇒ regenerated `0600`, no leftover temp file.
  [`identity_test.go:152`](../../companion/internal/identity/identity_test.go#L152)
- Error scrub: a forced regenerate failure leaks neither the existing key nor any UUID-shaped substring.
  [`identity_test.go:322`](../../companion/internal/identity/identity_test.go#L322)
- `DefaultConfigDir` failure branch: `HOME`/`XDG_CONFIG_HOME` cleared, error wrapped and op-named.
  [`identity_test.go:444`](../../companion/internal/identity/identity_test.go#L444)

**Docs**

- User-facing note: concrete per-OS paths, regeneration triggers, "managed file — don't edit", Windows advisory perms.
  [`README.md:40`](../../README.md#L40)
