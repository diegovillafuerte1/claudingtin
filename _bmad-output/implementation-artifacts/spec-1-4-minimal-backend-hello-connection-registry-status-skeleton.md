---
title: 'Story 1.4 — Minimal backend: hello, connection registry, /status skeleton'
type: 'feature'
created: '2026-09-05'
status: 'done'
review_loop_iteration: 0
baseline_commit: '2e611c8d8fe6e75b90d580d55e58d70f18e3fe94'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The `backend` module is compile-only stubs. Epic 1's companion (Story 1.6) needs a hub to open a websocket to, announce its protocol version via `hello`, and be tracked as connected — plus a `GET /status` for the maintainer view — before any matching exists.

**Approach:** Implement `backend serve`: a websocket upgrade endpoint that reads the first `hello` frame, gates the protocol version, and registers the account key in a channel-driven single-writer connection registry (AD-8); a second `hello` for a live key takes over and ends the prior connection; a `GET /status` returning JSON with `concurrent_users`. Structured JSON logs to stdout carry no account key and no frame text.

## Boundaries & Constraints

**Always:**
- All connection-registry mutation happens on one goroutine that owns the map and consumes a command channel; connection handlers touch it only by sending on that channel (AD-8). Network I/O (sending `session_ended`, closing) runs on handler goroutines, never the registry goroutine.
- Every websocket frame is a `proto` type via `proto.Encode` / `proto.Decode` — no message shape defined outside `/proto`.
- Version gate on `hello.protocol_version` (pv): accept `pv >= proto.PROTOCOL_VERSION - 1`, including pv newer than the backend. If `pv < proto.PROTOCOL_VERSION - 1`, send exactly one `please_update` frame then a normal close — never an `error` frame, never a bare close without the frame.
- One active connection per account key: a second `hello` for a connected key becomes active; the prior connection receives a `session_ended` frame then a normal close. `/status` `concurrent_users` counts distinct connected keys.
- `serve` reads `PORT` (default `8080` when unset/empty). The only HTTP routes are the websocket upgrade (`/ws`) and `GET /status`; every other path is 404, non-GET `/status` is 405 (AD-13).
- Logs are structured JSON (`log/slog` JSON handler) to stdout. No log line contains an account key or any frame text. Client-facing failures after the socket is open are a `proto` `error` frame, never a transport-close code (except the AD-5 `please_update`).
- `go build` / `go vet` / `go test -race` over the `go.work`-derived module set, `gofmt -l .`, `go work sync` + `git diff --exit-code`, `bash scripts/check_deps.sh`, and `bash scripts/checks_test.sh` all pass.

**Ask First:**
- Adding any third-party module beyond `github.com/coder/websocket` (pinned `v1.8.15`, the architecture Stack choice).
- Any change to the `/proto` package (e.g. a `Decode` variant that also returns the envelope).

**Never:**
- No SQLite / persistence / `DB_PATH`, no ban re-read loop, no `SIGHUP` — Epic 4. No queue, matching, `ready`/`busy` handling, `matched`, or message relay — Epics 2–3. Post-`hello` frames are read and discarded in v1.
- No merging `cmd/serve`, `cmd/ban`, `cmd/reports` into one subcommand binary; only `cmd/serve` gains behavior here.
- No HTTP surface beyond `/ws` and `/status`. No in-process TLS (Fly / the operator's proxy terminates it).
- No account-key fingerprint or hash in logs in this story.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|---|---|---|---|
| `hello`, supported | first frame `hello`, pv in `{PROTOCOL_VERSION-1, PROTOCOL_VERSION}` or newer | socket stays open, no `please_update`, key registered; `GET /status` `concurrent_users` includes it | N/A |
| `hello`, too old | first frame `hello`, `pv < PROTOCOL_VERSION - 1` | exactly one `please_update` frame, then normal close; key not registered | close after frame |
| bad first frame | first frame is not `hello`, or undecodable (`{"type":` / unknown type) | one `error` frame (`code` `expected_hello` or `bad_frame`), then close; nothing registered | error frame, no panic |
| no first frame | client sends nothing before the hello deadline (~10s) | connection closed, nothing registered | close, no panic |
| takeover | key `K` active on conn A; conn B sends `hello` for `K` | conn A gets `session_ended` then a normal close; conn B active; `concurrent_users` for `K` unchanged | graceful, no double-count |
| client vanishes | conn A drops without `leave` | `K` removed from the registry when A's read loop ends; `concurrent_users` drops | no panic |
| `GET /status` | `N` distinct keys connected | `200`, `Content-Type: application/json`, body `{"concurrent_users": N}` | N/A |
| `/status` non-GET / unknown path | `POST /status` / `GET /nope` | `405` / `404` | N/A |
| log scrub | a connection carrying account key `acct-secret` completes any row above | captured stdout log never contains `acct-secret` or frame text | N/A |

</frozen-after-approval>

## Code Map

- `backend/go.mod` + `backend/go.sum` — **edit / new.** Add `require github.com/coder/websocket v1.8.15` (`go mod download`); keep `replace .../proto => ../proto`.
- `go.work.sum` — **new / updated** by `go work sync`; commit it (CI `lint` runs `go work sync` then `git diff --exit-code`).
- `backend/internal/hub/hub.go` — **new.** The AD-8 single writer. `New() *Hub`; `Run(ctx)` owns `map[string]*Session` + a command channel; `Register(*Session)` / `Unregister(*Session)` / `Count() int` only send commands (Count reads a reply). `Register` for a key already mapped to a *different* session closes the displaced session's `evict` channel — the hub goroutine does no I/O. `Unregister` removes only if the map still points to that exact `*Session` (post-takeover safety).
- `backend/internal/hub/hub_test.go` — **new.** register→count; takeover closes prior `evict` and holds count stable; stale `Unregister` after takeover is a no-op; `-race` clean under concurrent `Register` / `Count`.
- `backend/internal/server/server.go` — **new.** `New(h *hub.Hub, logger *slog.Logger) http.Handler`. `/ws`: `websocket.Accept`, read first frame under a ~10s context, `proto.Decode`; `hello` → version gate (`please_update` + close, else `Session{key, conn, evict}`, `h.Register`, then a read loop that discards frames, returning on client close or `<-evict` — sending `session_ended` first); non-`hello` / undecodable → `error` frame + close. `GET /status` → `{"concurrent_users": h.Count()}`. Guards for 405 / 404.
- `backend/internal/server/server_test.go` — **new.** `httptest.Server` + `websocket.Dial` over every matrix row; `slog` JSON handler into a `bytes.Buffer` for the log-scrub check.
- `backend/cmd/serve/main.go` — **replace stub.** Read `PORT` (default `8080`); `slog` JSON logger on stdout; `hub.Run` on a cancellable context; `http.Server` with `server.New`; graceful `Shutdown` on `SIGINT` / `SIGTERM`. Drop `_ = proto.PROTOCOL_VERSION` (the server package now makes the `backend → proto` edge real).
- `README.md` — **edit.** "Backend" subsection: `backend serve`, `PORT` (default 8080), `GET /status`, `/ws` upgrade, JSON-logs / no-secrets note.
- `scripts/check_deps.sh`, `.github/workflows/ci.yml` — **read-only.** `backend` may import third-party; `backend → proto` stays real via `server.go`; the CI `test` job fetches `coder/websocket` (runner has network).

## Tasks & Acceptance

**Execution:**
- [x] `backend/go.mod` + `backend/go.sum` — add `github.com/coder/websocket v1.8.15`; `go mod download`
- [x] `backend/internal/hub/hub.go` — channel-driven registry with takeover eviction (AD-8)
- [x] `backend/internal/hub/hub_test.go` — registry unit tests incl. takeover + stale-unregister + `-race`
- [x] `backend/internal/server/server.go` — `/ws` upgrade + `hello` gate + read loop; `GET /status`
- [x] `backend/internal/server/server_test.go` — the I/O & Edge-Case Matrix, incl. log-scrub
- [x] `backend/cmd/serve/main.go` — `PORT`, `slog` JSON logger, hub + `http.Server`, graceful shutdown
- [x] `README.md` — Backend section
- [x] `go work sync` — clean (Go keeps the new checksums in `backend/go.sum`; no `go.work.sum` is generated, nothing extra to commit)

**Acceptance Criteria:**
- Given `backend serve` with `PORT` set and a client that opens `/ws` and sends `hello` with a supported `protocol_version`, when the server processes it, then the socket stays open, no `please_update` is sent, and the key shows in `GET /status` `concurrent_users`.
- Given a client whose `hello.protocol_version` is below `PROTOCOL_VERSION - 1`, when the server processes it, then the client receives exactly one `please_update` frame and the socket is then closed, and the key is not registered.
- Given a key with a live connection, when a second `hello` for that key arrives, then the prior connection receives `session_ended` and is closed, the new connection is active, and `concurrent_users` does not double-count the key.
- Given the server is running with `N` distinct keys connected, when `GET /status` is called, then it returns `200` JSON with `concurrent_users` equal to `N`, and no stdout log line contains an account key or frame text.
- Given `bash scripts/check_deps.sh`, `bash scripts/checks_test.sh`, `gofmt -l .`, `go vet`, and `go work sync` + `git diff --exit-code` on the finished tree, when they run, then all pass.

## Spec Change Log

## Design Notes

- **Single-writer shape.** Hub goroutine `select`s over one `commands` channel; commands are small structs (`register`, `unregister`, `count`). `Session` = `key string`, `conn *websocket.Conn`, `evict chan struct{}`. Takeover = `close(old.evict)`; the old handler goroutine, `select`-ing on `<-evict` vs. its read, sends `session_ended` then `conn.Close(websocket.StatusNormalClosure, "")`. No network call on the hub goroutine.
- **Version fields.** `hello.protocol_version` (the payload int, AD-5) is authoritative. The envelope `v` stamped by `proto.Encode` is not consulted; the deferred-work item about a `Decode` envelope variant stays deferred — do not touch `/proto`. `PROTOCOL_VERSION` is 1, so "oldest supported" is `0` and the `please_update` branch is tested with `protocol_version: -1`; the gate is `pv >= PROTOCOL_VERSION - 1` so it survives a bump.
- **`please_update` vs `error`.** `please_update` is its own frame + normal close. Every other post-open rejection is a `proto.Error` frame with a stable machine `code` (`expected_hello`, `bad_frame`) then close.
- **`log/slog`** JSON handler (stdlib). Log lifecycle (open, please_update, takeover, close) and `/status` hits with fields like `remote`, `pv`, `concurrent_users` — never `account_key`, never frame text.

## Verification

**Commands** (from repo root; `mods="$(bash scripts/workspace_modules.sh)"`):
- `go build $mods` / `go vet $mods` — exit 0, clean
- `go test -race $mods` — all pass; new `hub` and `server` suites green
- `gofmt -l .` — no output; `go work sync && git diff --exit-code` — no changes after commit
- `bash scripts/check_deps.sh` (still prints `backend → proto`) and `bash scripts/checks_test.sh` — exit 0
- `PORT=8080 go run ./backend/cmd/serve`, then: `curl -s localhost:8080/status` → `{"concurrent_users":0}`; `POST /status` → 405; `GET /nope` → 404

## Suggested Review Order

**The single-writer registry (AD-8) — the heart of the change**

- Entry point: one goroutine owns the map, everyone else sends a command; no network I/O here.
  [`hub.go:55`](../../backend/internal/hub/hub.go#L55)
- Takeover = the hub closes the displaced session's `Evict` channel and nothing more.
  [`hub.go:65`](../../backend/internal/hub/hub.go#L65)
- Identity-checked removal: a stale `Unregister` after a takeover can't evict the replacement.
  [`hub.go:74`](../../backend/internal/hub/hub.go#L74)
- `send` also selects on `stopped`, so callers never block on a hub whose `Run` has returned.
  [`hub.go:90`](../../backend/internal/hub/hub.go#L90)
- `Session` carries only key + conn + evict; the hub touches just `Evict`.
  [`hub.go:19`](../../backend/internal/hub/hub.go#L19)

**The hello handshake and version gate**

- `serveConn`: read the first frame under a bounded context, then route on type.
  [`server.go:113`](../../backend/internal/server/server.go#L113)
- Version gate: accept `pv >= PROTOCOL_VERSION-1` incl. newer; older gets one `please_update` then a normal close.
  [`server.go:161`](../../backend/internal/server/server.go#L161)
- Empty account key is rejected like any bad first frame — otherwise all keyless clients collide on `""`.
  [`server.go:147`](../../backend/internal/server/server.go#L147)
- Post-register: discard-loop on its own goroutine; the handler waits on client-close vs. eviction, cancelling the conn ctx on the evict path.
  [`server.go:193`](../../backend/internal/server/server.go#L193)
- One frame writer for every proto message; failures are logged, not surfaced (caller is already closing).
  [`server.go:208`](../../backend/internal/server/server.go#L208)

**HTTP surface**

- Exactly two routes; everything else 404, non-GET `/status` 405.
  [`server.go:47`](../../backend/internal/server/server.go#L47)
- `/status` → `{"concurrent_users": N}`, `no-store`, logged at Debug so probes don't flood stdout.
  [`server.go:62`](../../backend/internal/server/server.go#L62)
- Upgrade with `InsecureSkipVerify` — a native client has no Origin; identity is the `hello` key.
  [`server.go:89`](../../backend/internal/server/server.go#L89)

**Process wiring**

- `PORT` (default 8080), slog JSON logger, hub + `http.Server`, and a shutdown the process actually waits for.
  [`main.go:28`](../../backend/cmd/serve/main.go#L28)
- Only `ReadHeaderTimeout` is set — a whole-request timeout would kill `/ws`.
  [`main.go:48`](../../backend/cmd/serve/main.go#L48)

**Tests & docs (peripherals)**

- Every I/O-matrix row over `httptest` + `websocket.Dial`, built through the real `server.New`.
  [`server_test.go:247`](../../backend/internal/server/server_test.go#L247)
- Takeover: exactly one `session_ended` then a close, count held at 1.
  [`server_test.go:385`](../../backend/internal/server/server_test.go#L385)
- Log scrub: a full lifecycle with a known key + chat text, asserted absent from the JSON logs.
  [`server_test.go:543`](../../backend/internal/server/server_test.go#L543)
- Concurrency stress plus a deterministic register-N / unregister-N / count-0 post-condition.
  [`hub_test.go:160`](../../backend/internal/hub/hub_test.go#L160)
- `## Backend` section: `/ws` handshake, takeover, `GET /status`, no-secrets logging.
  [`README.md:14`](../../README.md#L14)
