---
title: 'Story 1.1 — Monorepo scaffold and the /proto wire contract'
type: 'feature'
created: '2026-09-01'
status: 'done'
review_loop_iteration: 0
baseline_commit: '06d3189f1a67fd96c3b13bd3cf9a58568d49647c'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The repository is empty. Every later Epic 1 story needs a monorepo layout with enforced dependency direction and one shared definition of every websocket message, or each story invents its own wire shapes and module boundaries.

**Approach:** Lay out a Go workspace with four modules (`proto`, `backend`, `companion`, `plugin`) and compile-only entrypoint stubs, then implement `proto` in full: the `{type, v, ...payload}` envelope, a `PROTOCOL_VERSION` constant, a `PascalCase` struct with a `snake_case` `type` discriminator for every v1 message, and encode/decode helpers with a round-trip test over the whole set. Add a dependency-direction check script.

## Boundaries & Constraints

**Always:**
- Go workspace (`go.work`) over `./proto ./backend ./companion ./plugin`; each its own module; Go 1.27; standard library only (no third-party deps this story).
- Dependency direction: `companion → proto`, `backend → proto`. `proto` imports no sibling and no third-party package. Nothing imports `plugin`; `plugin` imports no sibling (it execs the companion binary by path).
- `proto` is the only place a message shape is defined; `backend` and `companion` both import it.
- Envelope is exactly `{ "type": <snake_case string>, "v": <int>, ...payload }`, flat (not nested); `v` is stamped to `PROTOCOL_VERSION` on encode; message structs never carry `v`.
- Every v1 message round-trips: `Encode` → `Decode` yields a value deep-equal to the original with its `type` discriminator intact.
- `go build ./...`, `go test ./...`, `go vet ./...` all pass on a fresh clone with no network.

**Ask First:**
- None.

**Never:**
- No real `serve` / hook / TUI / transcript / negotiation logic (Stories 1.2, 1.4, 1.6, 1.7) — entrypoints are `func main() {}` stubs.
- No CI workflow content, no `deploy/` content, no SQLite schema, no third-party libraries — later stories.
- `proto` defines shapes and (de)serialisation only — no message handling.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Round-trip a known message | populated `Hello` value | `Encode` returns flat JSON with `"type":"hello"` and `"v":<PROTOCOL_VERSION>`; `Decode` returns a `Hello` deep-equal to the original | N/A |
| Every v1 message type | each struct in the client→server and server→client set | marshal → unmarshal deep-equals the original; discriminator matches the type's constant | N/A |
| Unknown discriminator | `{"type":"bogus","v":1}` | `Decode` returns a non-nil error naming the unknown type | error value, no panic |
| Missing / empty `type` | `{"v":1}` | `Decode` returns a non-nil error | error value, no panic |
| Malformed JSON | `{"type":` | `Decode` returns a non-nil error | error value, no panic |

</frozen-after-approval>

## Code Map

Greenfield — repo contains only `_bmad/`, `_bmad-output/`, `.claude/`. All paths below are created here.

- `go.work` -- `use ./proto ./backend ./companion ./plugin`
- `proto/` -- module `github.com/diegovillafuerte1/claudingtin/proto`. `protocol.go`: `PROTOCOL_VERSION` const + `Envelope{Type string, V int}`. `messages.go`: one struct + discriminator const per v1 message, plus `Encode(any) ([]byte, error)`, `Decode([]byte) (any, error)`, and a discriminator→constructor registry. `messages_test.go`: table-driven round-trip + error paths (the I/O matrix).
- `backend/` -- module `.../backend`, requires `.../proto`. `cmd/serve`, `cmd/ban`, `cmd/reports` stub `main`s; `serve` references a `proto` symbol so the edge is real.
- `companion/` -- module `.../companion`, requires `.../proto`. `main.go` stub referencing `proto`.
- `plugin/` -- module `.../plugin`, no sibling require. `cmd/session-start/main.go` stub (arg parse + print); `hooks/session-start.sh` placeholder; `bin/.gitkeep`.
- `deploy/.gitkeep`, `.github/workflows/.gitkeep` -- source-tree placeholders.
- `scripts/check_deps.sh` -- `go list`-based dependency-direction assertion.
- `.gitignore` (Go artifacts), `README.md` (one paragraph + module layout table).

## Tasks & Acceptance

**Execution:**
- [x] `go.work` + `proto/go.mod`, `backend/go.mod`, `companion/go.mod`, `plugin/go.mod` -- workspace + four modules, Go 1.27 (`backend`/`companion` also carry `replace ../proto` for offline resolution outside workspace mode)
- [x] `proto/protocol.go` -- `PROTOCOL_VERSION = 1`, `Envelope`
- [x] `proto/messages.go` -- structs + `type` consts for client→server (`hello`, `ready`, `busy`, `chat_msg`, `leave`, `block`, `report`, `profile_put`, `forget_me`, `note_put`, `heartbeat`) and server→client (`queued`, `matched`, `chat_msg`, `session_ended`, `blocklist`, `profile_ack`, `error`, `please_update`); `Encode` / `Decode` / registry + init-built reverse map
- [x] `proto/messages_test.go` -- round-trip every type; assert unknown / missing / empty / malformed / non-object `type` errors; flat-wire, registry-coverage, discriminator-snake_case checks
- [x] `backend/cmd/{serve,ban,reports}/main.go` -- compile-only stubs; `serve` touches `proto`
- [x] `companion/main.go` -- compile-only stub touching `proto`
- [x] `plugin/cmd/session-start/main.go` + `plugin/hooks/session-start.sh` -- stub launcher + placeholder hook, no sibling import
- [x] `plugin/bin/.gitkeep`, `deploy/.gitkeep`, `.github/workflows/.gitkeep`
- [x] `scripts/check_deps.sh` -- exits non-zero if `proto` imports a sibling/third-party, anything imports `plugin`, `plugin` imports a sibling, or a `→proto` edge is dead
- [x] `.gitignore`, `README.md`

**Acceptance Criteria:**
- Given a fresh clone with no network, when `go build` and `go test` run at the repo root over the explicit module set (`./proto/... ./backend/... ./companion/... ./plugin/...`), then both exit 0 and the `proto` suite exercises every v1 message type. (Bare `./...` is not used — a Go workspace does not span its modules from a non-module root; see Verification.)
- Given the `proto` package, when its exported surface is inspected, then `PROTOCOL_VERSION` and `Envelope` are present and every message struct has a `snake_case` `type` discriminator constant matching its wire name.
- Given any v1 message value, when passed through `Encode` then `Decode`, then the result is deep-equal to the input and the envelope carries `"v": <PROTOCOL_VERSION>`.
- Given `Decode` input with an unknown, missing, or malformed `type`, when it runs, then it returns a non-nil error and does not panic.
- Given `bash scripts/check_deps.sh` on the current tree, when it runs, then it exits 0 and prints the verified edges.

## Spec Change Log

- **2026-09-05 — review pass 1 (no loopback; patch-only).** Finding: the frozen "Always" bullet and an AC named bare `go build ./...` / `go test ./...` at the repo root, which a Go workspace cannot satisfy from a non-module root, while the same frozen block mandates a four-module-only workspace (self-contradiction). Amended (non-frozen only): the AC and the Verification section now name the explicit `./proto/... ./backend/... ./companion/... ./plugin/...` module set; the frozen bullet's `./...` is read as shorthand for "the project builds/tests/vets clean offline," which the explicit form satisfies. Avoids a green-looking but literally-unrunnable acceptance command. KEEP: four project modules and no repo-root module; `go.work` member order is left to `go work sync` (alphabetical) since that command is in the suite.

## Design Notes

- **Module path** is `github.com/diegovillafuerte1/claudingtin` (repo home confirmed post-review).
- **Decode:** read the envelope, switch on `type` via the registry to the concrete struct, unmarshal the whole object into it, return as `any`; callers type-assert. Keep the registry and the round-trip test in lockstep. Shape:
  ```go
  const TypeHello = "hello"
  type Hello struct {
      AccountKey      string `json:"account_key"`
      ProtocolVersion int    `json:"protocol_version"`
  }
  ```
- **Flat wire output:** `Encode` must emit `{type,v,...payload}` at one level — marshal the payload to a map and add the two keys, or embed `Envelope` in each struct; do not nest the payload under a key.
- Stub `main`s are `func main() {}` with a `// Story 1.x fills this in` comment.

## Verification

**Commands** (run from repo root; `GOPROXY=off` to prove no network is needed):
- `go build ./proto/... ./backend/... ./companion/... ./plugin/...` -- exit 0, no output
- `go test ./proto/... ./backend/... ./companion/... ./plugin/...` -- all pass; `proto` round-trip + error-path suite green (18 message types + flat-wire + registry-coverage + discriminator-match + 5 error cases)
- `go vet ./proto/... ./backend/... ./companion/... ./plugin/...` -- clean
- `bash scripts/check_deps.sh` -- exit 0; prints `companion→proto`, `backend→proto`, `proto→∅`, `plugin` unreferenced
- `go work sync` -- exit 0, no changes
- `gofmt -l .` -- no output

Note: bare `go build ./...` from the repo root is not used — a Go workspace does not expand `./...` across its modules unless the working directory is itself a module, and the repo root is deliberately not a module (four project modules only). The explicit multi-module pattern above is the canonical build/test/vet command.

## Suggested Review Order

**Wire contract — the payload of this story**

- Entry point: how a value becomes a flat `{type, v, ...payload}` frame; note the typed-nil-pointer guard.
  [`messages.go:204`](../../proto/messages.go#L204)
- The inverse: envelope-first decode, registry lookup, returns the struct value (not pointer); forward-compatible (unknown fields ignored).
  [`messages.go:247`](../../proto/messages.go#L247)
- The single source of truth every message routes through; reverse map built in `init` so the two can't drift.
  [`messages.go:166`](../../proto/messages.go#L166)
- The invariant envelope and the negotiation constant it stamps.
  [`protocol.go:20`](../../proto/protocol.go#L20)

**Contract tests — what locks the wire shape**

- Round-trips every v1 message: deep-equal, `v` stamped, discriminator intact, flat wire.
  [`messages_test.go:62`](../../proto/messages_test.go#L62)
- Reflection guard added in review: every payload field needs a snake_case json tag that doesn't collide with `type`/`v`.
  [`messages_test.go:222`](../../proto/messages_test.go#L222)
- The I/O matrix error rows: unknown / missing / empty / malformed / non-object / nil — error, never panic.
  [`messages_test.go:180`](../../proto/messages_test.go#L180)

**Dependency-direction enforcement**

- `go list -deps -test` per module (test-file imports counted); a `go list` failure is now a loud, explained error.
  [`check_deps.sh:38`](../../scripts/check_deps.sh#L38)
- The forbidden-edge checks: `proto` has no siblings/third-party; nothing imports `plugin`; the `→proto` edges are alive.
  [`check_deps.sh:100`](../../scripts/check_deps.sh#L100)

**Workspace & module boundaries**

- Four modules, root is not a module; order left to `go work sync`.
  [`go.work:3`](../../go.work#L3)
- `require` + local `replace` so `backend` resolves `proto` offline even outside workspace mode.
  [`go.mod:5`](../../backend/go.mod#L5)

**Peripherals — compile-only stubs & hygiene**

- Deliberate `proto` reference makes the `backend → proto` edge real for the check.
  [`serve/main.go:11`](../../backend/cmd/serve/main.go#L11)
- Silent, fail-open stub — models the real launcher's "nothing on stdout/stderr" contract.
  [`session-start/main.go:14`](../../plugin/cmd/session-start/main.go#L14)
- `!plugin/bin/**` negation so the committed cross-built binaries (incl. `*.exe`) stay addable.
  [`.gitignore:11`](../../.gitignore#L11)
