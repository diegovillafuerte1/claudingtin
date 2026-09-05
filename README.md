# claudingtin

A Claude Code plugin that turns model think-time into a chat roulette: while Claude is working, the companion quietly connects you to another person who is also waiting, and drops the connection the moment your session needs you back. The companion never writes to the Claude Code TUI, and nothing leaves your machine until a local first-run safety and 18+ screen has been cleared.

This repository is a Go workspace (`go.work`) over four modules — `proto`, `backend`, `companion`, `plugin` — with an enforced dependency direction: `proto` is the single definition of every websocket message, and both `backend` and `companion` import it while `plugin` links no sibling at all. The repository root is deliberately not itself a module.

| Module | Path | Import path | Depends on | Role |
|--------|------|-------------|------------|------|
| `proto` | `./proto` | `github.com/diegovillafuerte1/claudingtin/proto` | — (stdlib only) | Wire contract: the `{type, v, ...payload}` envelope, `PROTOCOL_VERSION`, one struct + `snake_case` discriminator per v1 message, and `Encode`/`Decode`. |
| `backend` | `./backend` | `github.com/diegovillafuerte1/claudingtin/backend` | `proto` | Server: `cmd/serve` (websocket + `GET /status`), `cmd/ban`, `cmd/reports`. Minimal in Epic 1. |
| `companion` | `./companion` | `github.com/diegovillafuerte1/claudingtin/companion` | `proto` | Local TUI that tails the Claude Code transcript and speaks `ready`/`busy` over the websocket. |
| `plugin` | `./plugin` | `github.com/diegovillafuerte1/claudingtin/plugin` | — (execs the companion by path) | `cmd/session-start` launcher and the `hooks/` SessionStart script. Committed cross-built companion binaries live under `bin/<os>-<arch>/`. |

## Development

Requires Go 1.27.x. No third-party dependencies and no network access are needed to build or test.

From the repository root (Go's `./...` does not span workspace modules on its own, so name them):

```sh
go build ./proto/... ./backend/... ./companion/... ./plugin/...   # compile every module
go test  ./proto/... ./backend/... ./companion/... ./plugin/...   # incl. the proto wire round-trip over every v1 message
go vet   ./proto/... ./backend/... ./companion/... ./plugin/...   # static checks
go work sync                                                      # keep the workspace build list in sync
bash scripts/check_deps.sh                                        # assert the dependency direction
gofmt -l .                                                        # formatting (expect no output)
```
