- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Wire scripts/check_deps.sh into CI (with `go list -deps -test` and a shellcheck step) in Story 1.2 so the dependency-direction invariant runs automatically, not only when a developer remembers.
  evidence: Three review layers independently flagged that the only enforcement of the enforced dependency direction is a shell script nothing in the normal build/test/vet path invokes; CI is Story 1.2 by design, leaving a gap between 1.1 and 1.2.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Add a mechanism (marker interface + reflective enumeration, or codegen) so a v1 message struct declared but omitted from proto's `registry` fails a test.
  evidence: TestRegistryFullyCovered / TestDiscriminatorConstantsMatchWireNames cross-check registry<->roundTripSamples<->wireNames with `registry` as pivot; nothing enumerates the message structs actually declared, so a later story adding a struct+const but forgetting `registry` passes the whole suite while Encode/Decode silently reject that type. All 18 current types are consistent (latent, not active).

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Clarify the roles of the envelope `v` field vs `Hello.protocol_version` in Story 1.4 (version negotiation) and in the architecture; consider a Decode variant that also returns the parsed Envelope so 1.4 can act on `v`.
  evidence: Decode currently discards env.V; there are two version notions on the hello frame (envelope `v` and `Hello.protocol_version`) with no documented distinction. Story 1.1 is shapes-only so this is out of scope here, but 1.4 needs it resolved.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: ChatMsg.Text does not round-trip for invalid UTF-8 (encoding/json rewrites to U+FFFD); decide and test the UTF-8 handling contract when chat relay is implemented (Story 2.x).
  evidence: The proto round-trip sample for ChatMsg is ASCII only; arbitrary user chat text with invalid UTF-8 bytes would not survive Encode->Decode unchanged.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Add a FuzzDecode target asserting "never panics on arbitrary bytes" for proto.Decode.
  evidence: Decode parses untrusted network input; current robustness coverage is five hand-picked strings plus a panic-recover. Fuzzing is the natural hardening check.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Publish a human-readable v1 message catalog (direction + fields) and a protocol CHANGELOG alongside PROTOCOL_VERSION; revisit the PROTOCOL_VERSION identifier if staticcheck/ST1003 is adopted.
  evidence: For a "wire contract" deliverable the message set exists only in Go source; PROTOCOL_VERSION ships with "any breaking wire change bumps it" but nothing records what each version means. Doc-set work is Story 6.4.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-1-monorepo-scaffold-and-the-proto-wire-contract.md`
  summary: Verification/README commands hardcode the four module paths; when a fifth module is added to go.work nothing flags that the command list needs updating (go work sync does not).
  evidence: Every documented build/test/vet command is `./proto/... ./backend/... ./companion/... ./plugin/...`; a new workspace module's tests would simply never run until someone edits the docs.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: CI hygiene pass — add `concurrency` groups (cancel superseded PR runs; serialize the mutable `:edge` and `:latest` image pushes), `timeout-minutes` on every job, and `retention-days` on the PR cross-build artifact uploads.
  evidence: blind-hunter + edge-case-hunter. Not correctness bugs, but two `main` pushes racing both move `:edge`, and jobs currently inherit the 6h default timeout. Standard for any real pipeline.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: Release supply-chain hardening (do before the first real `v*` tag) — pin third-party Actions to commit SHAs + add `.github/dependabot.yml` for Action bumps; add `govulncheck` and `actions/dependency-review-action`; add image provenance/SBOM and consider cosign signing; publish `SHA256SUMS` for the release binaries; document that macOS binaries are unsigned/unnotarized (Gatekeeper).
  evidence: blind-hunter. Jobs hold `contents: write` / `packages: write`; Actions are on mutable major tags. Overlaps Epic 6 "run it in public".

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: `release.yml` work needed before its first real use — gate on the test suite (or require the tagged commit to have a green `ci.yml`); replace the copy-pasted companion cross-build with a `workflow_call` reusable workflow shared with `ci.yml`; promote the CI-tested binaries as assets instead of rebuilding; make asset upload atomic (draft → verify 4 files → publish) so a mid-matrix failure cannot publish a partial release; anchor the tag filter to semver and handle `-rc`/`-beta` (`prerelease:`, do not move `:latest`); add `workflow_dispatch` for dry-runs and `generate_release_notes: true`.
  evidence: all three review layers. The spec deliberately scoped `release.yml` as an untested skeleton; these are the items to close before Epic 6 cuts a real release.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: `scripts/workspace_modules.sh` robustness — parse `go.work` via `go work edit -json` instead of the hand-rolled awk (handles quoted paths, comments, a root `use .`); make the `--check` reverse scan recurse (`find . -name go.mod`) so an unwired *nested* module is also caught; update the README/spec wording that currently says only "top-level module". Partially supersedes the Story 1.1 deferred item about hardcoded module paths (the enumeration is now go.work-derived; `check_deps.sh`'s per-direction rules are still keyed to the four known module names).
  evidence: all three review layers. Low priority — repo convention is top-level modules only and the current parser handles every normal `go.work`.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: Backend image, revisit when `serve` gains real behavior (Story 1.4+) — non-root `USER`, `/tmp`, `tzdata`/`/etc/passwd` as needed, `ENV PORT=8080` tied to the actual listener, `HEALTHCHECK`; digest-pin the `golang:1.27` builder and the runtime base; add OCI labels via `docker/metadata-action` so the GHCR package links to the repo + commit; add a `docker run --rm` start smoke-test step to the `backend-image` job; add Docker layer caching (`cache-from`/`cache-to: type=gha`) and a `COPY go.work */go.mod` + `go mod download` layer once dependencies exist.
  evidence: blind-hunter. The current `scratch` image runs as root with no writable tmp; fine for a no-op stub, not for a real server.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-2-ci-cross-build-and-release-skeleton.md`
  summary: Repo-wide hygiene — `-race` on `go test` in CI; a per-module `go mod tidy` / `go mod verify` check; version stamping (`-ldflags -X` or `-buildvcs`) for the companion and `serve`; `.gitattributes` enforcing LF and add `dist/` to `.gitignore`; use `${{ github.repository_owner }}` for the GHCR image name instead of the hardcoded account (revisit the spec constraint that locks the literal name); enable the Go build cache in CI once a `go.sum` exists.
  evidence: blind-hunter. None are correctness bugs today; all are standard once the project has dependencies and contributors.
