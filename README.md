# claudingtin

A Claude Code plugin that turns model think-time into a chat roulette: while Claude is working, the companion quietly connects you to another person who is also waiting, and drops the connection the moment your session needs you back. The companion never writes to the Claude Code TUI, and nothing leaves your machine until a local first-run safety and 18+ screen has been cleared.

This repository is a Go workspace (`go.work`) over four modules — `proto`, `backend`, `companion`, `plugin` — with an enforced dependency direction: `proto` is the single definition of every websocket message, and both `backend` and `companion` import it while `plugin` links no sibling at all. The repository root is deliberately not itself a module.

| Module | Path | Import path | Depends on | Role |
|--------|------|-------------|------------|------|
| `proto` | `./proto` | `github.com/diegovillafuerte1/claudingtin/proto` | — (stdlib only) | Wire contract: the `{type, v, ...payload}` envelope, `PROTOCOL_VERSION`, one struct + `snake_case` discriminator per v1 message, and `Encode`/`Decode`. |
| `backend` | `./backend` | `github.com/diegovillafuerte1/claudingtin/backend` | `proto` | Server: `cmd/serve` (websocket + `GET /status`), `cmd/ban`, `cmd/reports`. Minimal in Epic 1. |
| `companion` | `./companion` | `github.com/diegovillafuerte1/claudingtin/companion` | `proto`, `fsnotify` | Local TUI that tails the Claude Code transcript and speaks `ready`/`busy` over the websocket. Transcript turn-boundary parser + fsnotify tailer live in `internal/transcript`; the format it targets is pinned in [`docs/transcript-format.md`](docs/transcript-format.md). |
| `plugin` | `./plugin` | `github.com/diegovillafuerte1/claudingtin/plugin` | — (execs the companion by path) | `cmd/session-start` launcher and the `hooks/` SessionStart script. Committed cross-built companion binaries live under `bin/<os>-<arch>/`. |

## Backend

`backend serve` (`./backend/cmd/serve`) is the minimal Epic 1 server. It reads
one environment variable — `PORT`, default `8080` when unset or empty — and
exposes exactly two HTTP routes:

- **`/ws`** — websocket upgrade. The client's first frame must be a `hello`
  (`proto` type) carrying its `protocol_version` and account key. The version is
  gated: `protocol_version >= PROTOCOL_VERSION - 1` is accepted (including a
  version newer than the backend); anything older gets exactly one
  `please_update` frame followed by a normal close. A bad first frame (not
  `hello`, or undecodable) gets one `error` frame (`expected_hello` /
  `bad_frame`) then a close. On a valid `hello` the account key is registered in
  a single-writer connection registry; a later `hello` for a live key takes over
  and the displaced connection receives `session_ended` then a normal close.
  Post-`hello` frames are read and discarded in this epic.
- **`GET /status`** — returns `200` with `application/json` body
  `{"concurrent_users": N}`, where `N` is the number of distinct connected
  account keys. A non-GET `/status` is `405`; every other path is `404`.

Logs are structured JSON (`log/slog`) to stdout and deliberately carry **no
account key and no frame text**. The process shuts down gracefully on `SIGINT` /
`SIGTERM`. TLS is terminated by the operator's proxy, never in-process.

## Development

Requires Go 1.27.x. The only third-party dependency is `github.com/fsnotify/fsnotify` (pinned `v1.10.1`, used by the companion's transcript tailer); `proto`, `backend`, and `plugin` stay stdlib-only. The first build needs module downloads (or a primed module cache) to fetch it; `companion/go.sum` keeps that reproducible. After that, no network access is needed to build or test.

From the repository root (Go's `./...` does not span workspace modules on its own, so name them):

```sh
go build ./proto/... ./backend/... ./companion/... ./plugin/...   # compile every module
go test  ./proto/... ./backend/... ./companion/... ./plugin/...   # incl. the proto wire round-trip over every v1 message
go vet   ./proto/... ./backend/... ./companion/... ./plugin/...   # static checks
go work sync                                                      # keep the workspace build list in sync
bash scripts/check_deps.sh                                        # assert the dependency direction
gofmt -l .                                                        # formatting (expect no output)
```

The module list above is also emitted by `scripts/workspace_modules.sh` (so
`go build $(bash scripts/workspace_modules.sh)` is equivalent). CI and
`scripts/check_deps.sh` both enumerate the module set from `go.work` via that
helper, so `go.work` is the single source of truth for *which* modules exist:
`go build` / `go vet` / `go test` in CI pick up a new module automatically.
`scripts/check_deps.sh` still needs a dependency-direction rule written for each
module by name — a module added to `go.work` under an unknown name makes it fail
loudly until rules are added. `scripts/workspace_modules.sh --check` fails if a
`go.work` entry has no `go.mod`, or a top-level module is missing from `go.work`.

## CI & releases

Two GitHub Actions workflows live in `.github/workflows/`.

**`ci.yml`** runs on every pull request and on push to `main`:

- **test** — `go build` / `go vet` / `go test` the `go.work`-derived module set
  on `ubuntu-latest` and `windows-latest`. (No macOS runner yet — the companion
  is cross-*compiled* for Darwin but not exercised until Story 1.6 adds
  Darwin-specific paths.)
- **lint** — `gofmt -l .`, `go work sync` must be a no-op, `shellcheck` over
  `scripts/*.sh` and `plugin/hooks/*.sh`, `scripts/check_deps.sh`
  (dependency direction), and `scripts/workspace_modules.sh --check`
  (every `go.work` entry has a `go.mod`, every top-level module is wired in).
- **cross-build** — plain `go build` (`CGO_ENABLED=0`) of the companion for the
  four targets `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `windows/amd64`,
  each uploaded as a build artifact.
- **backend-image** — builds `deploy/Dockerfile`. On push to `main` it is pushed
  to `ghcr.io/diegovillafuerte1/claudingtin-backend:edge`; on a pull request it
  is built only — no registry login, no push — so fork PRs need no secrets.

**`release.yml`** runs on a `v*` tag (e.g. `git push origin v0.1.0`):

- attaches the four companion binaries — `companion-darwin-arm64`,
  `companion-darwin-amd64`, `companion-linux-amd64`,
  `companion-windows-amd64.exe` — to the GitHub Release for the tag;
- pushes the backend image to `ghcr.io/diegovillafuerte1/claudingtin-backend`
  tagged with the bare version (`v0.1.0` → `0.1.0`) and `latest`.

### Manual step: refreshing `plugin/bin/`

CI never commits binaries. After a release, copy the freshly built companion
binaries into `plugin/bin/<os>-<arch>/` and commit them as a separate reviewed
change. Each plugin release pins exactly one companion binary version.
