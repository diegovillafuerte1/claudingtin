---
title: 'Story 7.6: Remove the companion and pane placement'
type: 'chore'
created: '2026-10-27'
status: 'done'
baseline_commit: '93980454596650c71c08bc1bcdb53de11f7cfffd'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-7-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The mod (7.1–7.5) now does everything the companion did. The Go companion, the backend `/ws` transport, the session-start launcher and the binary release pipeline are still in the repo, so there are two clients and two transports to maintain.

**Approach:** Delete them all in one change and leave one client (the mod) and one transport (`/v1`). Give CI a job that runs `claude plugin validate --strict` and `claude plugin test`. Add the manifest test. Mark the companion-only rows in deferred-work obsolete.

## Boundaries & Constraints

**Always:**
- After the change, `go.work`, `scripts/workspace_modules.sh`, `check_deps.sh` and `checks_test.sh` know only `proto` and `backend`, with one allowed edge: `backend → proto`.
- The backend keeps exactly these routes: `/status`, `/v1/hello`, `/v1/events` and `/v1/send`. Every other path, `/ws` included, is a 404.
- Hub semantics don't change. The only hub edit is dropping the `Session.Conn` field and its import.
- Transport-agnostic coverage that exists only over `/ws` is ported to `/v1` rather than lost: the relay p90 test, and `/status` counting distinct keys.
- `backend/go.mod`/`go.sum` are tidy, with `coder/websocket` gone.
- The new CI job pins Claude Code to an exact version (`2.1.311`, the local one) and adds no new third-party Action.

**Ask First:** a `/proto` wire change (comment edits are fine), any hub behaviour change, a new third-party Action, or CI needing a secret to run `claude plugin test`.

**Never:**
- Change mod behaviour in `plugin/hooks/*.ts(x)`. Only tests, `hooks.json` and the `DEFAULT_BACKEND_URL` export may change there.
- Rewrite historical specs/retros under `_bmad-output`.
- Delete deferred-work rows. They are marked, never removed.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Old client | `GET /ws` (upgrade) | 404 | — |
| Session start | Claude Code loads the plugin | only the mod module loads; no SessionStart command runs | — |
| Default config | no `backendUrl` set | first fetch is `${DEFAULT_BACKEND_URL}/status` | — |
| Workspace guard | `scripts/workspace_modules.sh` | prints `./proto/... ./backend/...` (go.work order); `--check` OK | — |

</frozen-after-approval>

## Code Map

**Delete:**
- `companion/` (whole tree)
- `plugin/cmd/`, `plugin/go.mod`, `plugin/bin/`, `plugin/hooks/session-start.sh`
- The untracked local `plugin/session-start` and `plugin/session-start.exe` binaries
- `docs/transcript-format.md` (companion transcript spike)

**Edit:**
- `go.work` -- use only `./proto` and `./backend` (go.work.sum is ignored).
- `backend/internal/server/server.go`:
  - drop `handleWS`, `serveConn`, `writeFrame`, `helloDeadline`/`defaultHelloDeadline`, `frameWriteTimeout` and the `/ws` route;
  - rewrite the package doc (four routes);
  - the fallback comment.
- `backend/internal/server/server_test.go`:
  - keep `syncBuffer`, `harness`, `runHub`, `startHarness`, `loggerTo`, `waitCount`, the `/status` tests, the 404 test and the nil-logger test;
  - delete every ws helper and ws test;
  - `TestWS_NonGETIs405` becomes a `/ws`-is-404 test;
  - `TestStatus_CountsDistinctKeys` uses `v1Hello` (a second hello on k1 must not add);
  - port `TestRelayRoundTripP90UnderCeiling` to `matchPair`/`mustSend`/poll;
  - port `TestLogsCarryNoAccountKeyOrFrameText` only if `TestV1LogsCarryNoHandleKeyOrText` (v1_test.go:759) doesn't already cover it — otherwise delete it.
- `backend/internal/server/v1_test.go`:
  - `newV1Harness` L33 builds `server{}` without `helloDeadline`;
  - `TestV1RoutesServedByNewAndWSTakeoverRetiresHandle` L801 keeps the `New`-wired 405 and a hello→queued round trip, and drops the ws takeover. Rename it `TestV1RoutesServedByNew`.
- `backend/internal/hub/hub.go:19,25-46` -- remove the `websocket` import, the `Conn` field and its doc mentions ("its websocket connection").
- `backend/internal/hub/hub_test.go:15` -- update the comment.
- `backend/cmd/serve/main.go:1,60` -- update comments (the long-lived connections are now held polls).
- `proto/messages.go:24,176` and `proto/protocol.go:4` -- update comments only to drop `/ws`.
- `backend/go.mod`/`go.sum` -- `go mod tidy`.
- `scripts/check_deps.sh`:
  - rules for `proto` and `backend` only;
  - remove the companion and plugin rules and edges;
  - the header doc.
- `scripts/checks_test.sh:92,105` -- update the expected globs to `./proto/... ./backend/...` (check the real go.work order). The fixture go.work uses `proto`, `backend` and `frontend`.
- `plugin/hooks/hooks.json` -- `{"description": <mod-only>, "modules": ["./register.tsx"]}`, with no `hooks` key.
- `plugin/hooks/register.tsx:10` -- `export const DEFAULT_BACKEND_URL`.
- `plugin/hooks/register.test.tsx:118` -- the manifest test: with no options, the first fetch equals `` `${DEFAULT_BACKEND_URL}/status` `` (that's the manifest default against the code default), and the band draws, which proves `hooks.json` loaded the module. Name it to say so.
- `plugin/.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json` -- descriptions without "companion".
- `.github/workflows/ci.yml`:
  - delete the `cross-build` job and the companion comments;
  - add a `plugin` job on ubuntu-latest:
    - `npm install -g @anthropic-ai/claude-code@2.1.311`;
    - `jq -e '.modules == ["./register.tsx"] and (has("hooks") | not)' plugin/hooks/hooks.json`;
    - `claude plugin validate --strict plugin`;
    - `claude plugin test plugin`.
- `.github/workflows/release.yml` -- delete the `binaries` job and update the header. Only `backend-image` remains.
- `.gitignore`, `.dockerignore` -- drop the companion, session-start and `plugin/bin` lines.
- `deploy/Dockerfile:8-9` -- fix the stale comment.
- `README.md`:
  - replace the "Companion" section with a short "Mod" section (band, chat Pane, first-run gate, account key path, `userConfig.backendUrl`);
  - drop `/ws`;
  - module table: two modules plus `plugin/` (TS mod, not a Go module);
  - Development commands: two globs plus the two `claude plugin` commands;
  - CI & releases: no cross-build, no `plugin/bin` manual step.
- `_bmad-output/implementation-artifacts/deferred-work.md`:
  - prefix the `summary:` of each row whose code is deleted here (companion, launcher/placement, `/ws`, binary releases; e.g. the spec-1-3/1-6/1-7/3-2 companion rows and the spec-7-2 `/ws` lapse row) with `**Obsolete (7.6):** `;
  - in the triage block L299–300, append ` — done in 7.6` to the two "→ Story 7.6" lines.
- `_bmad-output/implementation-artifacts/sprint-status.yaml:76` -- `7-5` → done (its PR #38 merged), and drop the stale "ships in one PR" comment.

## Tasks & Acceptance

**Execution:**
- [x] Delete the trees and files listed under **Delete**.
- [x] `go.work`, `scripts/*.sh` -- two-module workspace and guards.
- [x] `backend/internal/server/*`, `backend/internal/hub/*`, `backend/cmd/serve/main.go`, `proto/*.go` comments, `backend/go.mod` -- remove `/ws` and `coder/websocket`, and port the two tests.
- [x] `plugin/hooks/hooks.json`, `register.tsx`, `register.test.tsx`, manifests -- mod-only manifest plus the manifest test.
- [x] `.github/workflows/*.yml` -- drop the binaries and add the plugin job.
- [x] `.gitignore`, `.dockerignore`, `deploy/Dockerfile`, `README.md` -- doc and ignore cleanup.
- [x] `deferred-work.md`, `sprint-status.yaml` -- mark rows.

**Acceptance Criteria:**
- Given the repo, when `git grep -n -i -E 'companion|coder/websocket|session-start|plugin/bin'` runs over everything outside `_bmad*` and `.claude`, then no hits remain except deliberate history notes. The mod's "byte-compatible with the companion's files" comments are allowed.
- Given the tree, when the Verification commands run, then all pass.

## Design Notes

- **No tracked binaries remain.** The `!plugin/bin/**` re-include goes, and so does the README's manual refresh step.
- **Old key carry-over stays.** The mod still reads the companion's `account-key`/`safety-ack` paths, so existing users keep their key. Those comments are history, not dead code.
- **Module order is `./backend/... ./proto/...`.** `go work sync` sorts `go.work`'s `use` block and CI requires sync to be a no-op, so the matrix row's "(go.work order)" is what holds; the order it spells out is a typo.
- **`v1 taken over` logging stays** in `v1.go`, even though `/ws` was its only trigger. It is the hub-evict path, and removing it is a hub-semantics question for later.

## Verification

**Commands:**
- `export PATH="/opt/homebrew/bin:$PATH"; go build $(bash scripts/workspace_modules.sh) && go vet $(bash scripts/workspace_modules.sh) && go test -race $(bash scripts/workspace_modules.sh)` -- expected: pass.
- `gofmt -l .` -- expected: empty. `go work sync && git diff --exit-code` -- expected: clean.
- `bash scripts/workspace_modules.sh --check && bash scripts/check_deps.sh && bash scripts/checks_test.sh` -- expected: OK.
- `shellcheck $(git ls-files '*.sh')` -- expected: clean (if installed).
- `claude plugin validate --strict plugin && claude plugin test plugin` -- expected: no errors, and all pass.
- `docker build -f deploy/Dockerfile .` -- expected: builds (if Docker is available).

**Manual checks:**
- The PR's CI `plugin` job passes on GitHub. If it needs auth or network, HALT per Ask First.

## Suggested Review Order

**One transport: `/ws` is gone**

- Entry point: four routes remain; `/ws` falls through to the 404 fallback.
  [`server.go:33`](../../backend/internal/server/server.go#L33)

- Hub `Session` loses its unused `*websocket.Conn`; hub semantics are untouched.
  [`hub.go:42`](../../backend/internal/hub/hub.go#L42)

- The evict arm is now defensive only (deferred: unreachable without `/ws`).
  [`v1.go:195`](../../backend/internal/server/v1.go#L195)

**Coverage moved from `/ws` to `/v1`**

- Relay p90 latency guard, now over the long-poll path.
  [`server_test.go:101`](../../backend/internal/server/server_test.go#L101)

- A real upgrade handshake to `/ws` gets a 404.
  [`server_test.go:201`](../../backend/internal/server/server_test.go#L201)

- Version gate edges: previous and newer protocol versions are welcomed.
  [`v1_test.go:294`](../../backend/internal/server/v1_test.go#L294)

- Peer `session_ended` is the same bare frame for busy, leave and supersede.
  [`v1_test.go:535`](../../backend/internal/server/v1_test.go#L535)

- Ordered, multi-byte relay round trip.
  [`v1_test.go:521`](../../backend/internal/server/v1_test.go#L521)

**One client: the mod only**

- The manifest test: the manifest default equals the code default, and the band proves the module loaded.
  [`register.test.tsx:122`](../../plugin/hooks/register.test.tsx#L122)

- The exported default that the test pins.
  [`register.tsx:10`](../../plugin/hooks/register.tsx#L10)

- CI finally runs validate and test, with Claude Code pinned and no new Action.
  [`ci.yml:93`](../../.github/workflows/ci.yml#L93)

**Workspace and guards**

- One allowed edge left: backend → proto.
  [`check_deps.sh:134`](../../scripts/check_deps.sh#L134)

- Self-test pins the `go work sync`-canonical module order.
  [`checks_test.sh:92`](../../scripts/checks_test.sh#L92)

**Peripherals**

- Release ships only the backend image.
  [`release.yml:25`](../../.github/workflows/release.yml#L25)

- README's new mod section replaces the companion docs.
  [`README.md:47`](../../README.md#L47)

- Companion-only deferred rows are marked, never deleted.
  [`deferred-work.md:282`](deferred-work.md#L282)
