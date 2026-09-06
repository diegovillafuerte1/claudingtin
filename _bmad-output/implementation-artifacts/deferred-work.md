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

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: Transcript parser fails unsafe on an un-enumerated terminal `stop_reason` or a turn whose final line is lost (CLI crash / mid-write truncation) — `Parser` stays `StateInTurn` with no timeout or backstop, the opposite of its "unknown line → degrade quietly" stance. Consider a stuck-turn watchdog (likely in Story 1.6's ready/busy layer) or treating any non-`tool_use`/`pause_turn` non-empty `stop_reason` as terminal.
  evidence: edge-case-hunter + blind-hunter. `terminalStopReasons` is a closed allowlist; `refusal` was already missing (patched). No path closes a turn except a known terminal reason or the next turn-start.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: Mid-turn rotation/truncation drops an open turn silently — `tailer.reset()` wipes parser state while `State() == StateInTurn` without signalling the consumer; recovery depends on the replacement file replaying the in-progress turn. Consider a synthetic `TurnEnd` (or a distinct reset event) on mid-turn reset.
  evidence: edge-case-hunter. `reset()` is called from both the truncation branch and `reopen()`; neither checks in-turn state.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: `readAppend` seeds via `io.ReadAll` (whole file into memory on attach) and only treats `size < offset` as truncation — a same-path in-place rewrite that keeps or grows the size is undetected and makes the tailer `Seek` into the middle of fresh content (parser self-resyncs at the next newline but may miss/duplicate events). Consider seeking near EOF on large initial files and an inode/ctime identity check for rewrites.
  evidence: edge-case-hunter + blind-hunter. Real transcripts observed up to ~2.3 MB today; Claude Code compaction currently rotates by rename (covered), so in-place rewrite is low-probability.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: `tailer.partial` line buffer is unbounded — a newline-free file dropped at the watched path grows memory without limit via `append`. Add a max-line-size guard that resynchronises past an over-long line.
  evidence: edge-case-hunter + blind-hunter. Requires a malformed/wrong file at the path; transcript lines are large but bounded in practice.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: `rawLine` is unmarshalled as one struct, so any single-field JSON type drift in a future Claude Code version (e.g. numeric `timestamp`, non-object `message`) discards the whole line and silently stops all turn detection. Consider decoding the mapped fields individually via `json.RawMessage`.
  evidence: edge-case-hunter. No such drift observed across versions 2.1.250–2.1.261; speculative but the failure mode is total and silent.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: `reopen()` window is fixed (~5s, no backoff/jitter) and terminal — once exhausted the tailer is deaf for the rest of the session with no auto-restart, only an error on `Errors`. Consider a longer/configurable window or a re-arm path.
  evidence: blind-hunter. The I/O matrix specs "retries exhausted → error on the error channel, no panic", so terminal-after-~5s is current intended behaviour; a slow compaction could exceed it.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: fsnotify event-name match is exact (`filepath.Clean(ev.Name) != t.path`); on case-insensitive filesystems (macOS/Windows) a case difference between the hook-supplied path and the on-disk name would drop every event, silently degrading to the 1s poll backstop. Consider a case-folded comparison on those platforms (weighed against matching case-only-different siblings).
  evidence: edge-case-hunter. Low probability — the Story 1.7 hook passes the real path — but the degradation is silent.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: No `macos-latest` in the CI `test` matrix even though the kqueue-backed fsnotify tailer and its macOS `EvalSymlinks` workaround ship in this story — macOS rename/rotation/symlink behaviour is exercised nowhere in CI. Revisit the `ci.yml` header comment that defers the macOS runner to Story 1.6.
  evidence: verification-gap + blind-hunter. Spec listed adding `macos-latest` under "Ask First"; the implementer correctly stayed in-bounds, so this is surfaced for a deliberate decision.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-4-minimal-backend-hello-connection-registry-status-skeleton.md`
  summary: On SIGINT/SIGTERM the backend does not drain live websockets — `http.Server.Shutdown` ignores hijacked conns and `hub.Run` drops its map on `ctx.Done()` without closing any `Evict` channel, so connected clients get no `session_ended` and no close frame, just process exit. Needs a hub broadcast/`Range` API and a shutdown path that ends live sessions cleanly. Belongs with AD-17 (reconnect grace) / the deploy-hardening story.
  evidence: blind-hunter + edge-case-hunter. Spec scoped shutdown to `http.Server.Shutdown` only; acceptable for a skeleton with no matched sessions to preserve, but a real gap once matching lands.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-4-minimal-backend-hello-connection-registry-status-skeleton.md`
  summary: No connection-liveness check after `hello` — no post-`hello` read deadline, no `heartbeat` handling, and coder/websocket sends no automatic pings. A half-open/dead TCP connection stays in the registry and inflates `/status` `concurrent_users` until an OS-level timeout. Belongs with the story that implements `heartbeat` and/or AD-17.
  evidence: blind-hunter. Spec "Never" defers post-`hello` frame handling to later epics; surfaced here because `/status` accuracy now depends on it.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-4-minimal-backend-hello-connection-registry-status-skeleton.md`
  summary: CI's `test` job runs `go test` without `-race`, but this story's Verification section and the frozen "Always" bullet both require `go test -race`, and this story adds the first genuinely concurrent code (the AD-8 single-writer hub). Add `-race` to the `go test` step in `.github/workflows/ci.yml` (deliberately left read-only by spec-1-4; weigh the Windows-runner cost / cgo requirement).
  evidence: verification-gap. `go test -race` passes locally; the gap is that CI does not enforce it going forward.
