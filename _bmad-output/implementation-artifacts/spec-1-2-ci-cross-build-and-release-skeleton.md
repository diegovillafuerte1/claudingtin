---
title: 'Story 1.2 — CI cross-build and release skeleton'
type: 'feature'
created: '2026-09-05'
status: 'done'
review_loop_iteration: 0
baseline_commit: '88d9225b253cde7e82b3a1405df7a3efd5bd2d45'
context:
  - '{project-root}/_bmad-output/implementation-artifacts/epic-1-context.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** The monorepo builds and tests only when a developer runs the commands by hand. Nothing cross-compiles the companion for the four target platforms, builds the backend image, runs the suite on Windows, or enforces the dependency-direction invariant automatically — and there is no path from a version tag to shippable binaries.

**Approach:** Add two GitHub Actions workflows. `ci.yml` (pull request + push to `main`) builds/vets/tests the workspace on Linux and Windows, cross-builds the companion for the four `<os>-<arch>` targets, builds the backend image, and runs `gofmt` / `go work sync` / `shellcheck` / `scripts/check_deps.sh` / a workspace-module coverage guard. `release.yml` (`v*` tags) is a skeleton that attaches the four binaries to a GitHub Release and pushes the backend image to GHCR, with the `plugin/bin/` refresh left as a documented manual commit. A minimal `deploy/Dockerfile` and a `scripts/` helper that derives the module set from `go.work` support both.

## Boundaries & Constraints

**Always:**
- The workspace module set is derived once from `go.work` by a new `scripts/` helper; CI and `scripts/check_deps.sh` both consume it — no second hardcoded list. `check_deps.sh` behavior is unchanged; only the source of its module loop changes.
- The coverage guard fails CI when a top-level dir with a `go.mod` is missing from `go.work`, or a `go.work` `use` entry lacks a `go.mod`.
- PR builds are build-only: the backend image is built but not pushed, and a fork PR needs no secrets or registry credentials to pass.
- Registry is GHCR (`ghcr.io/diegovillafuerte1/claudingtin-backend`), auth via the workflow `GITHUB_TOKEN` + `packages: write`. Push only on `push` to `main` (tag `edge`) and `v*` tags (`<version>` + `latest`).
- Companion cross-build targets exactly `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `windows/amd64` (`.exe` on Windows); plain `go build`, `CGO_ENABLED=0`.
- Go tests run on `ubuntu-latest` and `windows-latest`. The Windows job is where the future `modernc.org/sqlite` build-and-open assertion will live; today it just runs the suite.
- Third-party Actions are pinned to a released major-version tag and limited to `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `docker/login-action`, `docker/build-push-action`, and one GitHub-release action. Go version comes from `go-version-file`, never a literal.
- `deploy/Dockerfile` is multi-stage, builds `./backend/cmd/serve` only, and yields a minimal static runtime image that launches `serve`.
- All `go` build/test/vet calls use the explicit multi-module pattern from Story 1.1 (no bare `./...` from the repo root).

**Ask First:**
- Adding a GitHub Action outside the list above, or adding a repository secret.
- Introducing any third-party Go dependency to make a check work.

**Never:**
- No `docker-compose.yml`, `fly.toml`, `keywords.txt`, or Fly.io deploy wiring — Epic 6.
- No real `serve` / hook / TUI / transcript logic — later Epic 1 stories; `serve` stays a stub the Dockerfile only needs to compile and launch.
- No SQLite schema or `modernc.org/sqlite` dependency yet — Story 1.4 / Epic 4.
- No actual release cut here; `release.yml` runs only on a real future `v*` tag.
- No CI auto-commit of `plugin/bin/` — that refresh stays manual and documented.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Pull request opened | branch pushed, PR vs `main` | `ci.yml`: Linux+Windows build/vet/test green, four companion artifacts uploaded, backend image builds; no GHCR push | job fails red on any build/test/lint failure |
| Push to `main` | commit merged | as above, plus push `ghcr.io/.../claudingtin-backend:edge` | image push failure fails the job |
| Version tag pushed | `git push origin v0.1.0` | `release.yml`: four binaries attached to the `v0.1.0` Release; image pushed `:0.1.0` + `:latest` | release/publish failure fails the job |
| New module not wired | new `foo/go.mod`, `go.work` not updated | coverage-guard step exits non-zero naming `foo` | CI red, explained |
| Forbidden import added | e.g. `proto` imports a sibling, any file incl. `_test.go` | `check_deps.sh` step exits non-zero | CI red |
| Shell-script regression | a `scripts/*.sh` / `plugin/hooks/*.sh` shellcheck violation | shellcheck step exits non-zero | CI red |

</frozen-after-approval>

## Code Map

`.github/workflows/` and `deploy/` currently hold only `.gitkeep`.

- `.github/workflows/ci.yml` -- **new.** `pull_request` + `push` to `main`. Jobs: `test` (matrix `ubuntu-latest`/`windows-latest`; setup-go `go-version-file: go.work`; build/vet/test the derived module set), `lint` (`ubuntu-latest`; `gofmt -l .`, `go work sync` + `git diff --exit-code`, `shellcheck scripts/*.sh plugin/hooks/*.sh`, `scripts/check_deps.sh`, `scripts/workspace_modules.sh --check`), `cross-build` (matrix of the four `GOOS/GOARCH`; `go build` companion; `upload-artifact`), `backend-image` (`build-push-action`, `deploy/Dockerfile`, `push: ${{ github.event_name == 'push' }}`, login step gated the same).
- `.github/workflows/release.yml` -- **new.** `push` tags `v*`. Cross-build + image jobs like `ci.yml` but always push: binaries → GitHub Release via the release action; image → `:<version>` + `:latest`. Header comment documents the manual `plugin/bin/<os>-<arch>/` refresh and that each plugin release pins one companion binary version.
- `deploy/Dockerfile` -- **new.** `FROM golang:1.27` build stage → `CGO_ENABLED=0 go build -o /out/serve ./backend/cmd/serve` → `scratch`/distroless runtime, `ENTRYPOINT ["/serve"]`, `EXPOSE 8080`. `COPY . .` brings `go.work` so `proto` resolves in workspace mode.
- `.dockerignore` -- **new.** Drops `_bmad*/`, `.git/`, `.github/`, `plugin/bin/`, editor cruft from the build context.
- `scripts/workspace_modules.sh` -- **new.** Parses the `use ( … )` block in `go.work`; default output = `./<dir>/...` globs (Story 1.1 explicit pattern); `--check` asserts every `use` entry has a `go.mod` and every top-level `*/go.mod` is listed, exiting non-zero with an explanation otherwise. bash 3.2, `set -euo pipefail`.
- [`scripts/check_deps.sh:70`](../../scripts/check_deps.sh#L70) -- **edit.** Replace the literal `for m in proto backend companion plugin` with the helper's output (strip `./` and `/...`); update the header comment to say the set is `go.work`-derived.
- [`go.work:3`](../../go.work#L3) -- read-only; the `use (...)` block is the single source of truth.
- `README.md` -- **edit.** Add a "CI & releases" section: what each workflow does, the four targets, the GHCR image name, the manual `plugin/bin/` refresh.
- [`backend/cmd/serve/main.go:11`](../../backend/cmd/serve/main.go#L11) -- read-only; the Dockerfile compiles this stub unchanged.

## Tasks & Acceptance

**Execution:**
- [x] `scripts/workspace_modules.sh` -- new helper: emit `go.work`-derived module globs; `--check` guard for unwired/malformed modules -- resolves deferred finding #2
- [x] `scripts/check_deps.sh` -- source the helper for its module loop; update header comment -- one source of truth for the module set
- [x] `deploy/Dockerfile` + `.dockerignore` -- minimal multi-stage build of `backend/cmd/serve`, trimmed context
- [x] `.github/workflows/ci.yml` -- `test` (Linux+Windows), `lint` (gofmt/work-sync/shellcheck/check_deps/coverage-guard), `cross-build` (4 targets → artifacts), `backend-image` (build always, push only on `push` to `main`) -- resolves deferred finding #1
- [x] `.github/workflows/release.yml` -- `v*`-tag skeleton: four binaries → Release, image → GHCR `:<version>`/`:latest`; documented manual `plugin/bin/` refresh
- [x] `README.md` -- "CI & releases" section
- [x] Run the local checks in Verification against the current tree before the first CI run

**Acceptance Criteria:**
- Given the current clean tree, when `bash scripts/check_deps.sh` and `bash scripts/workspace_modules.sh --check` run, then both exit 0 and `check_deps.sh` prints the same verified-edges output as Story 1.1.
- Given `shellcheck` is available, when it runs over `scripts/*.sh` and `plugin/hooks/*.sh`, then it reports nothing.
- Given `deploy/Dockerfile`, when `docker build -f deploy/Dockerfile -t claudingtin-backend .` runs, then the image builds and `docker run --rm claudingtin-backend` starts and exits like the current `serve` stub.
- Given `ci.yml` / `release.yml`, when `actionlint` runs (or they are inspected), then both are valid, every third-party Action is pinned to a major-version tag on the allowed list, and the Go version comes from `go-version-file`.
- Given a pull request, when `ci.yml` runs, then the Linux and Windows test jobs pass, exactly four companion artifacts upload (`companion-darwin-arm64`, `-darwin-amd64`, `-linux-amd64`, `-windows-amd64.exe`), the backend image builds, and no GHCR push step runs.
- Given a temporary `tmpmod/go.mod` not added to `go.work`, when the coverage-guard step runs, then it exits non-zero naming `tmpmod`.

## Design Notes

- **One source of truth for the module set.** `workspace_modules.sh` parses `go.work`; `check_deps.sh` and every CI `go` call consume its output, so adding a module to `go.work` automatically extends build/test/vet/dep-check and forgetting `go.work` is caught by `--check`. This is the concrete fix for deferred-work finding #2.
- **Fork-PR safety.** `backend-image` sets `push: ${{ github.event_name == 'push' }}` and gates `docker/login-action` on the same expression, so a fork PR (no secrets, no `packages: write`) still passes a build-only image job.
- **No macOS test runner yet** — the companion is cross-*compiled* for Darwin, not run; add a macOS `test` job when Darwin-specific paths (fsnotify, tmux) land in Story 1.6. Note this in the `ci.yml` header.
- **`release.yml` is a skeleton** — structurally complete but only ever triggered by a real `v*` tag (none exist). Do not create a tag to test it in this story.

## Verification

**Commands** (from repo root):
- `bash scripts/workspace_modules.sh` -- prints `./backend/... ./companion/... ./plugin/... ./proto/...` (order per `go.work`)
- `bash scripts/workspace_modules.sh --check` -- exit 0, prints an OK line
- `bash scripts/check_deps.sh` -- exit 0, verified-edges output unchanged from Story 1.1
- `bash scripts/checks_test.sh` -- all guard self-tests PASS (negative-path regressions for `workspace_modules.sh --check` and `check_deps.sh`)
- `shellcheck $(git ls-files '*.sh')` -- no output (covers `scripts/*.sh` and `plugin/hooks/*.sh`)
- `go build $(bash scripts/workspace_modules.sh)` and `go test $(bash scripts/workspace_modules.sh)` -- still green (helper output is a drop-in for the explicit module list)
- `docker build -f deploy/Dockerfile -t claudingtin-backend .` -- image builds; `docker run --rm claudingtin-backend` exits cleanly
- `actionlint` (if installed) -- no findings on `.github/workflows/*.yml`

**Manual checks:**
- Push the story branch, open a draft PR: confirm `ci.yml` runs, four artifacts attach, Windows+Linux jobs pass, no GHCR push on the PR; confirm the `:edge` push succeeds on merge to `main`.

## Suggested Review Order

**Single source of truth for the module set (the core idea)**

- Entry point — parses `go.work`'s `use` block; default prints `./<dir>/...` globs, `--check` guards the tree.
  [`workspace_modules.sh:29`](../../scripts/workspace_modules.sh#L29)
- `--check` negative paths: unwired `use` entry, and a top-level `*/go.mod` missing from `go.work`.
  [`workspace_modules.sh:62`](../../scripts/workspace_modules.sh#L62)
- `check_deps.sh` now enumerates its module loop from the helper instead of a literal list.
  [`check_deps.sh:78`](../../scripts/check_deps.sh#L78)
- Honesty guard: a module with no hardcoded direction rules now fails loudly, never silently passes.
  [`check_deps.sh:83`](../../scripts/check_deps.sh#L83)

**CI pipeline (`ci.yml`)**

- `test` matrix (ubuntu + windows); each step captures the helper output and fails if it is empty.
  [`ci.yml:24`](../../.github/workflows/ci.yml#L24)
- `lint` job: gofmt, `go work sync` no-op, robust `git ls-files` shellcheck, dep check, module guard, guard self-tests.
  [`ci.yml:59`](../../.github/workflows/ci.yml#L59)
- `cross-build` — the four `GOOS/GOARCH` legs → upload-artifact.
  [`ci.yml:92`](../../.github/workflows/ci.yml#L92)
- `backend-image` — fork-PR safe: login gated on `push`, `push:` follows `github.event_name == 'push'`.
  [`ci.yml:122`](../../.github/workflows/ci.yml#L122)

**Release skeleton (`release.yml`) — structural only, never run yet**

- `v*` trigger; `binaries` job (`max-parallel: 1`) attaches each companion build to the tag's Release.
  [`release.yml:33`](../../.github/workflows/release.yml#L33)
- `backend-image` job derives the bare version from the tag and pushes `:<version>` + `:latest` to GHCR.
  [`release.yml:66`](../../.github/workflows/release.yml#L66)

**Supporting: image + tests + docs**

- Multi-stage Dockerfile: workspace build of `./backend/cmd/serve` → `scratch` runtime + CA roots.
  [`Dockerfile:11`](../../deploy/Dockerfile#L11)
- Guard regression tests — 5 assertions over fixture copies, real tree never mutated.
  [`checks_test.sh:34`](../../scripts/checks_test.sh#L34)
- `.dockerignore` trims the build context to the Go workspace.
  [`.dockerignore:1`](../../.dockerignore#L1)
- README "CI & releases" section — workflows, targets, GHCR image, the manual `plugin/bin/` refresh.
  [`README.md:27`](../../README.md#L27)
