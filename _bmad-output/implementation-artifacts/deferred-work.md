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

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: Concurrent regeneration of an already-corrupt `account-key` is not race-free — two companions starting at the same instant with a pre-corrupt file each run `regenerate()` (no `O_EXCL`, no lock) and can return divergent keys for that session (self-heals on the next start). First-run concurrency is handled; corrupt-file concurrency is not.
  evidence: blind-hunter + edge-case-hunter. Spec's frozen convergence guarantee is explicitly scoped to "racing first-run processes"; a lock-file design is out of scope for Epic 1 and AD-11 records "evasion accepted". Rare compound condition, self-healing.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: `resolveExisting` aborts on a transient non-ENOENT read error during the convergence poll (`return "", err`) instead of retrying within the window and only bailing after attempts are exhausted.
  evidence: edge-case-hunter. Low severity — a transient read failure on a local file you just lost an `O_EXCL` race for is unlikely — but it makes first-run `Load` brittle under it.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: On Windows, `os.Rename` in `regenerate` can fail with `ERROR_SHARING_VIOLATION` when another companion process has the key file open for reading during a concurrent regeneration; there is no retry. Consider a bounded retry loop on the sharing-violation errno on Windows.
  evidence: edge-case-hunter. Windows is a target platform; the triggering condition is a rare compound (Windows + corrupt file + simultaneous start + one mid-read). Not fault-injectable on the dev platform.
  resolved: RESOLVED on the Story 1.7 branch — `regenerate`'s `os.Rename` and `inspect`'s `os.Open` now go through `companion/internal/fsretry`, which bounded-retries `ERROR_SHARING_VIOLATION` / `ERROR_ACCESS_DENIED` on Windows.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: A `SIGKILL` between `os.CreateTemp` and `os.Rename` in `regenerate` leaves an orphan `.account-key-*` temp file in the config dir; nothing sweeps stale temp files on startup. They accumulate across crashes during regeneration.
  evidence: edge-case-hunter + blind-hunter. Self-limiting in practice (regeneration only runs on a corrupt file) and the files are hidden dotfiles; a startup glob-sweep would be the fix.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: `Load` returns `(string, error)` only and gives the caller no signal about which path was taken (reused / first-created / regenerated-from-corruption). A regeneration is abuse-relevant; a later story (Epic 4 enforcement / telemetry) may want a hook or a second return value.
  evidence: blind-hunter. Frozen surface is exactly `Load(configDir string) (string, error)`, so any change needs intent renegotiation; deferred as a future need, not a current defect.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: `Load` does not tighten an already-existing `configDir` whose mode is broader than `0700` (only a freshly created dir gets `0700`, and `tightenPerms` only touches the key file). The key file is `0600` regardless, so exposure is limited to the filename being listable.
  evidence: blind-hunter. Spec's frozen "Always" only mandates `0700` on a *created* configDir; a dir-mode tighten mirroring `tightenPerms` would be a cheap, consistent hardening.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: CI (`.github/workflows/ci.yml`) still runs `go test` without `-race`; the `go test -race` acceptance criterion in the 1.3 / 1.4 / 1.5 specs is only met by a manual invocation. `identity`'s concurrent `Load` path would regress undetected in CI. (Same repo-wide gap already logged for spec-1-1 / spec-1-4.)
  evidence: verification-gap. `go test -race ./companion/...` passes locally; the gap is that nothing enforces it going forward.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-5-account-key-at-a-reinstall-stable-path.md`
  summary: No test for a symlink at the `account-key` path. `firstCreate`'s `O_EXCL` refuses to create through a dangling symlink, but `regenerate`'s `os.Rename` silently replaces a symlink and `inspect` / `reread` follow one (an attacker-planted symlink to a valid-looking UUID file elsewhere would be reused).
  evidence: blind-hunter. Requires local write access to the config dir (already game-over for identity integrity); low value, listed for completeness.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-6-companion-launch-transcript-watch-ready-busy-over-the-websoc.md`
  summary: The companion accepts `ws://` (cleartext) to any host from the `server-url` arg or `SERVER_URL`; the account key then travels in the `hello` frame unencrypted. The architecture spine mandates TLS/WSS for all client↔server traffic, but nothing warns or refuses on a `ws://` URL to a non-loopback host.
  evidence: blind-hunter. Needs a deliberate warn-vs-refuse decision that also accounts for self-host deploys terminating TLS at a proxy; the compiled default is `ws://127.0.0.1:8080/ws` (loopback dev) and changing it is already Ask-First.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-6-companion-launch-transcript-watch-ready-busy-over-the-websoc.md`
  summary: `wsclient.Run` resets the reconnect backoff (`attempt = 0`) the instant the `hello` write returns nil, before `serve` runs. A backend that accepts the socket then immediately drops it never lets the client accrue backoff, so N companions can hammer a crash-looping or draining backend roughly every `backoffBase` (~0.5s). Consider gating the reset on a minimum post-`hello` connection uptime.
  evidence: verification-gap + blind-hunter. Matches the spec's literal wording ("backoff reset after a successful hello"), so tightening it is a spec renegotiation, not a patch; low impact at v1 scale (one small instance) but a real thundering-herd path.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-6-companion-launch-transcript-watch-ready-busy-over-the-websoc.md`
  summary: Inbound `proto.Error` frames are silently discarded by `wsclient.serve` (only `please_update` / `session_ended` are recognised). If a future backend rejects the `hello` with an `error` frame + normal close, the companion reconnects forever re-sending the same rejected hello with zero operator feedback. Unreachable in Epic 1 (the only `error`-on-first-frame paths are bad/empty-key frames, which `identity.Load` + the encoder preclude), and the spec Code Map explicitly says "all other inbound frames discarded".
  evidence: blind-hunter + edge-case-hunter. Becomes relevant when later epics add backend-side rejection paths; fix is to surface a scrubbed `Code`/`Msg` line to stderr while still honouring "an error is never a transport close".

- source_spec: `_bmad-output/implementation-artifacts/spec-1-7-sessionstart-hook-fail-open-launch.md`
  summary: The per-session_id lock file (`<tmp>/claudingtin-<id>.lock`) has no TTL, liveness check, or cleanup path — it accumulates one file per session in a temp dir that is not reliably reaped (macOS `/var/folders/...`), and a companion that crashed on startup or a `cmd.Start()` that errored is never relaunched for that `session_id` again (a later `resume` finds the lock held and silently no-ops).
  evidence: blind-hunter + edge-case-hunter + verification-gap. The spec Design Notes explicitly accept the stale-lock tradeoff and flag "a later refinement"; the frozen I/O matrix codifies "lock left in place" on spawn error. Real but bounded (a fresh Claude Code session gets a fresh id). Fix candidates: record the child PID in the lock and relaunch when not alive, a mtime-based TTL, or release the lock on a pre-start failure.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-7-sessionstart-hook-fail-open-launch.md`
  summary: `parse` does `io.ReadAll` on the hook stdin with no read deadline. A Claude Code that opens the hook's stdin but does not send EOF promptly makes the launcher block until the `hooks.json` `timeout: 10`, at which point Claude Code kills the hook and starts the session anyway — a up-to-10s delay in the companion launching (and a brief SessionStart stall), not a hard block.
  evidence: edge-case-hunter. Trigger is effectively a Claude Code bug; `timeout: 10` is the designed backstop. Consider reading stdin in a goroutine with a short timer and proceeding with whatever arrived.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-7-sessionstart-hook-fail-open-launch.md`
  summary: There is no native-Windows hook entry. `hooks/hooks.json` registers only the POSIX `hooks/session-start.sh`; a Windows Claude Code without a POSIX `sh` on PATH (native PowerShell, no Git Bash) gets a silent no-op even though a working `session-start-windows-amd64.exe` is committed and shipped. Consider a `.cmd`/`.ps1` wrapper plus a Windows-matched `hooks.json` entry.
  evidence: blind-hunter + edge-case-hunter. The spec's Design Notes name native-Windows-without-sh an accepted degradation, so closing it is a scope decision, not a patch. (Story 1.7 does add MINGW/MSYS/CYGWIN mapping to the wrapper so Git Bash users are covered.)

- source_spec: `_bmad-output/implementation-artifacts/spec-1-7-sessionstart-hook-fail-open-launch.md`
  summary: No CI guard that `plugin/bin/<target>/session-start` exists alongside every `plugin/bin/<target>/companion` (and vice versa). A partial manual binary refresh — the documented post-release step — would ship with a missing launcher or companion for some target and the wrapper would just hit `[ -x ] || exit 0` silently.
  evidence: blind-hunter. Pre-existing class of gap (there is no committed-binary completeness check for the companion either; `plugin/bin/` currently holds only `.gitkeep`). Cheap to add once the first release populates `plugin/bin/`.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-7-sessionstart-hook-fail-open-launch.md`
  summary: `pluginRoot`'s `os.Executable()` fallback does not `filepath.EvalSymlinks` the launcher path before walking three directories up. A plugin install symlinked into place (common under `~/.claude/plugins`) that also lacks `${CLAUDE_PLUGIN_ROOT}` in the hook env would resolve `../../..` against the wrong tree and not find the companion.
  evidence: blind-hunter. Low probability — Claude Code sets `${CLAUDE_PLUGIN_ROOT}` (checked first after the Story 1.7 patch) and Linux `os.Executable()` already resolves `/proc/self/exe`; the exposure is macOS + env-unset + symlinked install.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-3-transcript-format-spike-and-defensive-parser.md`
  summary: `TestWatcherRecoversFromRotation` (`companion/internal/transcript/watch_test.go`) flaked on `windows-latest` with `ERROR_SHARING_VIOLATION` — the watcher's `readAppend` opened `session.jsonl` while a peer renamed a fresh file over it, which Windows refuses (Unix allows rename-with-open-handle). A real Claude Code log rotation racing the tailer on Windows could surface the same.
  evidence: Observed as a CI failure on PR #7 (Story 1.7, which does not itself touch `transcript/`).
  resolved: RESOLVED on the Story 1.7 branch — `transcript.readAppend` now opens via `companion/internal/fsretry`, which bounded-retries `ERROR_SHARING_VIOLATION` / `ERROR_ACCESS_DENIED` on Windows.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-9-first-run-screen-and-18-gate-as-a-local-precondition.md`
  summary: `safety.Gate` has no deadline on its one stdin line read. A launcher that wires the companion's stdin to an open pipe that never delivers a line and is never closed (no EOF) would leave `Gate` — and therefore `run.Run` — blocked forever, with the screen printed but no status output and no connection. No launcher in the repo does this today: Story 1.7's `detachedSpawn` opens `/dev/null` (immediate EOF ⇒ decline) and the Story 1.8 tmux pane is a real PTY.
  evidence: edge-case-hunter. The spec deliberately keeps the gate a single code path with no TTY probing (human-approved), so a read deadline is a scope decision, not a patch. Fix candidate: a `select` timeout branch defaulting to `OutcomeDeclined` when the read neither completes nor EOFs within a short window on a non-interactive stdin.

- source_spec: `_bmad-output/implementation-artifacts/spec-1-9-first-run-screen-and-18-gate-as-a-local-precondition.md`
  summary: First-run screen copy is not validated against operational reality or jurisdiction. It says "report hands the last few messages to a human to look at" while no report handling exists until Epic 4, and asserts a flat "18 or older" gate with no allowance for differing ages of majority / data-consent ages. For a consent surface these should be a recorded product-copy decision checked against the Epic 4 report semantics and the PRD's OQ-A age-assurance stance.
  evidence: blind-hunter. The frozen spec only requires the screen to cover "how block and report work" and an affirmative 18+ control — it does; the precise wording and legal nuance are a product-owner call, not a code defect.

- source_spec: `_bmad-output/implementation-artifacts/spec-2-1-fifo-queue-and-single-writer-pairing-loop.md`
  summary: The hub mints one `session_id` per pairing, delivers it in `matched`, and does not retain it — `pairings map[*Session]*Session` stores only peer pointers. Epic 3 (in-memory relay, `leave` handling, Story 3.7 "session-id keepsake on persisted chat end") and any per-pairing logging will need that id server-side; it would otherwise be re-minted or threaded back from a client.
  evidence: blind-hunter + edge-case-hunter. No consumer exists in Story 2.1, so storing it now is dead state; the natural change is to widen the pairing table (e.g. `map[*Session]*pairing{peer, sessionID}`) in the first Epic 3 story that reads it.

- source_spec: `_bmad-output/implementation-artifacts/spec-2-1-fifo-queue-and-single-writer-pairing-loop.md`
  summary: `hub.deliver()` does a non-blocking send and silently drops the frame if a `Session.Outbound` is full or nil. A dropped `matched` would leave a recorded pairing one peer never learned about; a dropped `session_ended` would leave a survivor believing it is still paired — with no log line, counter, or retry.
  evidence: edge-case-hunter + blind-hunter. Not reachable in Story 2.1 (the connection handler always allocates `Outbound` with cap 4, and at most ~2 frames are ever buffered to one session before it is drained), but frame volume rises when the Story 2.4 relay lands. Fix candidate: have `deliver` report send success; on failure for a match, do not record the pairing (re-queue or drop both); on failure for a teardown, mark the survivor for a forced close by its handler.
