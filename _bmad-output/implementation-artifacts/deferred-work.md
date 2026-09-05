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
