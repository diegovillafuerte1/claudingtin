---
title: 'Story 4.1: Persisted policy store and single-writer load'
type: 'feature'
created: '2026-10-29'
status: 'done'
baseline_commit: 'bf7b2f750f097f507198f912cee6947f2cd2a97a'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-4-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The backend has no durable state. Blocks, cooldowns and bans (Epic 4) would be lost on every restart, and the hub's `eligible` seam only rejects self-pairing.

**Approach:**
- Add a SQLite store (`modernc.org/sqlite`, pure Go) at `DB_PATH`, opened WAL with `busy_timeout`, with embedded forward-only migrations gated by `schema_version`.
- `serve` migrates and loads policy (blocks, live cooldowns, bans) before the hub starts.
- The hub's `eligible` reads that policy from memory only.
- Policy writes go to memory first, then write through to SQLite. The writes have no callers yet: 4.2, 4.4 and 4.9 add them.

## Boundaries & Constraints

**Always:**
- Pure Go, `CGO_ENABLED=0` everywhere, including the Windows CI leg.
- Migrations are ordered `NNNN_name.sql` files embedded from `backend/migrations/`. Pending ones are applied in one `BEGIN IMMEDIATE` transaction (two processes starting together cannot both migrate), and `schema_version` is set to the highest applied. There is no down path.
- Six tables: `account`, `profile`, `block`, `cooldown`, `ban`, `report` (the AC's names), with the columns from the architecture erDiagram. Times are epoch-ms `INTEGER`.
- `cooldown` stores the pair in canonical order (`key_a < key_b`).
- Matching never touches SQLite. `eligible(a,b)` = not self, not blocked in either direction, no live cooldown (`expires_ms > now`), neither key banned.
- A write-through error leaves the in-memory state applied. It is reported through an injected callback, which `serve` logs with no keys.
- `serve` refuses to start (exit 1, a clear log line) when the DB cannot open, migrate or load.
- Logs never carry account keys.
- `DB_PATH` defaults to `claudingtin.db`. The image sets `DB_PATH=/data/claudingtin.db`.

**Ask First:**
- any `/proto` or wire change;
- any dependency other than `modernc.org/sqlite`;
- changing the existing `cmd/ban` / `cmd/reports` binary layout.

**Never:**
- block, cooldown, report or ban behaviour on the wire (4.2–4.9);
- ban re-read on SIGHUP or an interval (4.9);
- persisting message content, queue or pairings;
- down-migrations;
- a policy read from SQLite on the matching path.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Fresh DB | missing file at `DB_PATH` | all migrations applied, `schema_version` = latest, 6 tables exist, `journal_mode` = wal, `busy_timeout` > 0 | — |
| Partly migrated | `schema_version` = 1 of N | only 2..N run | a failing migration rolls back the whole batch, `serve` exits 1 |
| Up to date | version = latest | nothing runs | — |
| Newer DB | version > latest known | refuse to start | exit 1, logs both versions |
| Two openers | serve's store + a second `Open` on the same file, concurrent writes | both succeed, no `database is locked` | — |
| Concurrent first start | two `Open`s on a fresh file at once | one migrates, the other sees latest | — |
| Load | rows in block/cooldown/ban | hub memory holds them; expired cooldowns are skipped | — |
| Eligibility | A blocked B, or B blocked A, or a live cooldown, or either banned | not paired; the head waits or takes the next eligible successor | — |
| Write-through | in-hub `addBlock` / `addCooldown` / `addBan` | memory updated, row persisted, visible after re-`Open` + load | writer error → memory kept, callback fired |
| Bad path | `DB_PATH` in a missing dir | `serve` exits 1 | — |

</frozen-after-approval>

## Code Map

- `backend/go.mod` -- add `modernc.org/sqlite v1.60.1`. It's the newest release at least two weeks old (v1.61.0 is from 2026-10-21); the spine's v1.57.0 is superseded. Run `go work sync` and commit `go.sum`/`go.work.sum`.
- `backend/migrations/` (new) -- `migrations.go` (`package migrations`, `//go:embed *.sql`, `FS embed.FS`) plus `0001_init.sql` with the six tables and `schema_version(version INTEGER NOT NULL)`.
- `backend/internal/store/` (new) -- `Open(ctx, path) (*Store, error)`:
  - DSN pragmas `journal_mode(WAL)`, `busy_timeout(5000)`;
  - migrates on open;
  - `LoadPolicy(ctx) (Policy, error)`, where `Policy{Blocks [][2]string; Cooldowns []Cooldown; Bans []string}`;
  - `PutBlock`, `PutCooldown` (upsert, canonical pair), `PutBan` (upsert);
  - `Close`.
- `backend/internal/hub/hub.go`:
  - `New()` L107 becomes `New(opts ...Option)`, with `WithPolicy(store.Policy, Writer)`, `WithClock(func() time.Time)` and `WithWriteError(func(error))`. `New()` with no options behaves exactly as today, so the existing tests stay green.
  - The `eligible` closure L133 ANDs the predicates over a hub-goroutine-owned `policyState`.
  - `policyState.addBlock`, `addCooldown`, `addBan` write memory then the `Writer`.
  - The scan (`firstEligibleSuccessor` L474) is unchanged.
- `backend/cmd/serve/main.go` L53 -- read `DB_PATH`, `store.Open`, `LoadPolicy`, `hub.New(WithPolicy…)`, close on shutdown, exit 1 on error (mirror the openers check at L46). Update the package doc comment.
- `deploy/Dockerfile` -- a `/data` dir in the scratch image and `ENV DB_PATH=/data/claudingtin.db`.
- `scripts/check_deps.sh` -- read only. Backend third-party imports are allowed (only proto is restricted). Confirm it stays green.
- `README.md` -- document `DB_PATH` in the backend config list.

## Tasks & Acceptance

**Execution:**
- [x] `backend/go.mod`, `go.work.sum` -- the sqlite dependency.
- [x] `backend/migrations/` -- the embed and `0001_init.sql`.
- [x] `backend/internal/store/store.go` + `store_test.go` -- open, migrate, load and put. Tests:
  - fresh, partial, up-to-date and newer-version DBs;
  - pragmas;
  - a failed migration rolls back;
  - two `Open`s doing concurrent writes;
  - a concurrent fresh start;
  - expired cooldowns skipped;
  - canonical pair order.
- [x] `backend/internal/hub/hub.go` + tests -- options, `policyState`, predicates and write-through. Tests:
  - each predicate keeps a pair apart while the head takes the next eligible successor;
  - a cooldown expiring via `WithClock` makes the pair eligible;
  - the writer gets the rows;
  - a writer error keeps memory and fires the callback;
  - a fake `Writer` proves no read happens on the match path.
- [x] `backend/cmd/serve/main.go` -- wiring and the fail-closed startup.
- [x] `deploy/Dockerfile`, `README.md` -- `DB_PATH`.

**Acceptance Criteria:**
- Given the full suite, when `go test -race ./...` runs on ubuntu, macOS and Windows CI, then everything passes, and the existing hub/server tests still pass using `hub.New()`.
- Given `scripts/check_deps.sh`, the workspace guard and `go work sync`, when run, then all are clean.
- Given the backend image, when it builds with `CGO_ENABLED=0`, then `serve` starts and creates `/data/claudingtin.db`.

## Design Notes

- **Why the hub imports `store` types:** the hub owns in-memory policy (AD-8), and `store.Policy` is plain data. `Writer` is a small interface in the hub that `*store.Store` satisfies, so hub tests use a fake.
- **Synchronous write-through on the hub goroutine:** policy writes are rare (block, cooldown, ban) and take milliseconds in WAL. Doing them in order keeps memory and disk changing in the same order. Revisit if 4.4's cooldown volume shows up in latency.
- **Bans are enforced at enqueue too:** a banned key never joins the queue (`pairingState.admit`), so it cannot sit at the head and stall the queue. It also stays in `allows`.
- **`schema_version` is created by the store, not by 0001:** it has to exist before any migration can be gated. The store reads `MAX(version)`.
- **Open hardening (from review):**
  - a path containing `?` or `#` is refused;
  - WAL is verified after open;
  - a fresh-file `SQLITE_BUSY` during the WAL switch is retried within the busy timeout;
  - policy loads in one read transaction;
  - a misnamed migration is an error;
  - write-through runs on a detached context with a 2 s bound, and the hub is joined before the store closes.
- **CLI shape:** the separate `cmd/ban` / `cmd/reports` binaries stay. 4.1 proves the two-process WAL sharing with two `Open` handles (file-level locking is identical), and 4.9 decides the CLI form.

## Verification

**Commands:**
- `go test -race ./backend/... ./proto/...` -- expected: all pass.
- `bash scripts/check_deps.sh && bash scripts/workspace_modules.sh >/dev/null && go work sync && git diff --exit-code go.work` -- expected: clean.
- `CGO_ENABLED=0 GOOS=windows go build ./backend/...` -- expected: builds.
- `docker build -f deploy/Dockerfile .` (if Docker is available) -- expected: builds.

## Suggested Review Order

**Matching reads policy from memory only**

- Entry point: the one eligibility check now asks the in-memory policy.
  [`hub.go:144`](../../backend/internal/hub/hub.go#L144)

- Not self, no block either way, no live cooldown, neither banned.
  [`policy.go:104`](../../backend/internal/hub/policy.go#L104)

- A banned key never enqueues, so it can't stall the queue from the head.
  [`hub.go:362`](../../backend/internal/hub/hub.go#L362)

**Write-through**

- Detached from the hub's context, bounded at 2 s; errors go to the callback.
  [`policy.go:126`](../../backend/internal/hub/policy.go#L126)

- Cooldowns only extend; bad keys are ignored.
  [`policy.go:157`](../../backend/internal/hub/policy.go#L157)

**The store**

- Forward-only migrations in one BEGIN IMMEDIATE, gated by `MAX(version)`.
  [`store.go:167`](../../backend/internal/store/store.go#L167)

- Open: path guard, WAL plus busy timeout via the DSN, WAL verified.
  [`store.go:59`](../../backend/internal/store/store.go#L59)

- A fresh-file WAL switch can fail SQLITE_BUSY at once: retried within the timeout.
  [`store.go:99`](../../backend/internal/store/store.go#L99)

- Policy loads as one read-transaction snapshot; expired cooldowns are skipped.
  [`store.go:238`](../../backend/internal/store/store.go#L238)

- Migration files are named strictly, with no gaps or silent skips.
  [`store.go:133`](../../backend/internal/store/store.go#L133)

- The schema: six tables; cooldown pairs are canonical, enforced by CHECK.
  [`0001_init.sql:24`](../../backend/migrations/0001_init.sql#L24)

**Startup and shutdown**

- Fail-closed: open, migrate and load, then build the hub with the store as writer.
  [`main.go:124`](../../backend/cmd/serve/main.go#L124)

- The hub is joined before the store closes.
  [`main.go:70`](../../backend/cmd/serve/main.go#L70)

**Peripherals**

- Image: `/data` plus `DB_PATH`; CI now runs it and checks the DB file exists.
  [`Dockerfile:23`](../../deploy/Dockerfile#L23)
  [`ci.yml:136`](../../.github/workflows/ci.yml#L136)

- Two handles writing concurrently, with no "database is locked".
  [`store_test.go:245`](../../backend/internal/store/store_test.go#L245)

- Each predicate keeps a pair apart while the head takes the next eligible successor.
  [`policy_test.go:50`](../../backend/internal/hub/policy_test.go#L50)

- Persisted ban and block are enforced by serve's hub.
  [`main_test.go:62`](../../backend/cmd/serve/main_test.go#L62)
