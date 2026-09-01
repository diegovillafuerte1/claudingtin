# Spine Review — Good-Spine Checklist

**Target:** `ARCHITECTURE-SPINE.md` (claude-think-time-chat-roulette, initiative altitude, build-substrate)
**Driving PRD:** `prd-claudingtin-2026-08-31/prd.md` + `addendum.md`
**Bar applied:** deliberately hobby-scale open-source project, "build substrate, spine only." Judged against that bar, not enterprise.
**Reviewer:** rubric lens
**Date:** 2026-09-01

---

## Overall verdict

**Strong. Ship as the substrate after closing a short list of additive gaps — no structural rework needed.**

This is an above-average spine for its altitude and scope. The invariants are mostly concrete
and code-reviewable (AD-4 "no cause field," AD-5 "current + previous minor," AD-8 "exactly one
goroutine," AD-13 endpoint enumeration are exemplary). The stack is coherent (pure-Go SQLite +
single static binary + single Fly machine + Docker parity). The capability map covers F1–F11.
Crucially, the operational/environmental envelope — the dimension most spines drop — is
genuinely handled here: three environments (dev machine / Fly public instance / self-host),
release flow, provider choice with rationale, an explicit single-instance constraint, and a
decided admin surface.

The gaps that remain are real but narrow: one load-bearing PRD invariant has no AD, one
safety-keystone behavior has no owning unit, one diagram is likely broken, and the persistent
store's schema-evolution story is silent. All are fixable by addition.

---

## Checklist findings

### 1. Fixes the real divergence points for the level below, misses none

**Mostly. Three genuine misses.**

Covered well: state ownership (AD-1), policy-enforcement location (AD-2), wire schema (AD-3),
exit-message shape (AD-4), version negotiation (AD-5), think-time authority (AD-6),
plugin→companion handshake (AD-7), queue concurrency (AD-8), profile canonical source (AD-9),
data-at-rest (AD-10), identity (AD-11), config mechanism (AD-12), admin surface (AD-13). That
is a well-chosen set.

Misses:

- **[Medium] The "never block / never error the Claude Code session" guarantee is not an
  invariant.** This is the single most-repeated guarantee in the PRD — FR6 (stated twice),
  NFR6 (≤50 ms, fail open), NFR8 (degrade to a calm state, never an error dump), and it is
  load-bearing for G5. It binds `plugin` and `companion`. AD-7 only touches it obliquely
  ("prevents hook timing limits becoming load-bearing"). A downstream implementer can satisfy
  every current AD and still ship a `SessionStart` hook that blocks, delays, or errors the CC
  session on a slow spawn or an unreachable backend. This deserves its own AD: *plugin hooks
  return within a fixed budget, swallow all errors, and a companion/backend fault is always a
  silent no-op for the CC session.*

- **[Medium] Content-sanitization ownership is unassigned.** The addendum calls "text-only +
  inert links" *the* safety keystone (§1, and PRD §11 "Also foundational"). FR14 (strip /
  inert links) and NFR11 (no remote-markup rendering, input length caps) need an owning unit.
  AD-2 enumerates policy decisions — "identity acceptance, cooldown, block, ban, rate-limit,
  and keyword-filter" — and pointedly omits link handling and length caps. Nothing says
  whether `companion` or `backend` strips links, caps length, or neutralizes markup. Both
  could assume the other does it and links pass through. This is a real two-unit divergence on
  a safety-critical path. Fix: fold link-inertness + length caps + no-markup into AD-2's rule
  (backend-authoritative, companion may additionally render inert), or add a short AD.

- **[Medium] DB schema evolution is silent** (see item 6).

Not misses (correctly out of spine scope): pane placement / tmux split (FR5) is
`companion`-internal; opener wording, repo docs (NFR20), voice guide are content not
structure.

### 2. Every AD's Rule is enforceable and actually prevents its stated divergence

**Yes.** Every AD-N rule is checkable by code review or by inspecting a type/endpoint list.
AD-3/AD-4/AD-5/AD-8/AD-13 are model examples of enforceability. AD-6's "MAY provide a
secondary nudge but are not authoritative" and AD-2's "never gates on policy locally" are
review-enforceable design rules. No rule is aspirational-only.

- **[Low] AD status tags are inconsistent.** Only AD-1 and AD-11 carry `[ADOPTED]`. AD-2
  through AD-10, AD-12, AD-13 carry no status marker, and the front-matter says
  `status: draft`. As written this reads as "two decisions are firm, eleven are not," which is
  presumably not the intent. Tag all ADs or none.

### 3. Nothing under Deferred could let two units diverge

**Correct.** The Deferred section explicitly reasons about single-unit vs. multi-unit
divergence and gets it right:

- Transcript-parse edge cases (interrupts, `SubagentStop`, format shifts) — only `companion`
  parses the transcript, so divergence stays inside one unit. Safe to defer, and correctly
  also surfaced as an Open Question.
- Rate-limit values and keyword-list contents — enforced `backend`-only (AD-2, AD-8,
  `KEYWORDS_PATH`). Single unit. Safe.
- Multi-instance / federation, v2 webapp, pull-in / tags — out of v1 scope; spine only avoids
  precluding them (opaque backend-issued session IDs). Fine.

- **[Low] One invariant is mis-bucketed as tuning.** FR36's "exactly 1 concurrent chat
  session per account key" is a structural rule, not a numeric knob, but it currently sits
  under Deferred → "rate-limit numeric values — operational tuning." Promote the *shape*
  (all limits bounded; exactly one concurrent session) to a rule; leave only the numbers
  deferred. The conventions table already hints at this ("exactly 1 concurrent chat") — make
  it load-bearing.

### 4. Named tech is verified-current (plausibility / coherence)

**Coherent, no red flags.**

- Go 1.26.x — plausible for 2026-09 (Go 1.26 ~Aug 2026 cadence).
- `charmbracelet/bubbletea` v1.3.10, bubbles/lipgloss "current" — bubbletea v1.x line is the
  stable one; plausible.
- `coder/websocket` v1.8.15 — real package (ex `nhooyr.io/websocket`), v1.8.x plausible.
- `fsnotify`, `modernc.org/sqlite` (pure-Go, no cgo) — real, and the pure-Go choice is exactly
  right for a single cross-compiled static binary and a bundled companion.
- Fly.io `shared-cpu-1x`, one machine + one volume — coherent with NFR9 (low hundreds
  concurrent) and NFR13 (<$20/mo).
- Docker + `docker-compose.yml`, same image — coherent with NFR14.

The seed note ("verified current 2026-09-01 … pin exact minors at project init") sets the
right expectation for the items marked "current."

Overall the stack tells one consistent story: static Go binaries, no cgo, one process, one
file, one machine, Docker parity for self-host.

### 5. Covers the driving PRD's capability areas (F1–F11, key NFRs)

**F1–F11: yes**, via the Capability → Architecture Map, each pointed at units and ADs.

**NFRs — mostly, with the gaps already noted plus two minor ones:**

| NFR | Status |
| --- | --- |
| NFR1 no content persisted | Covered — AD-10 |
| NFR6 hook ≤50 ms, fail open | **Gap — no AD** (item 1) |
| NFR8 degrade cleanly, no error dump | Partially — same fail-open family, no AD |
| NFR10 TLS/WSS | Shown in both deployment diagrams; not an AD. Acceptable at this bar (Fly terminates TLS; self-host proxy) but a one-line rule would close it |
| NFR11 untrusted content / inert links / length caps | **Gap — no owning unit** (item 1) |
| NFR12 account-key UUID as identity, no handshake | Covered — AD-11 |
| NFR13 / NFR14 cost + one-command self-host | Covered — Stack + Structural Seed |
| NFR15 maintainer status view | Covered — `GET /status`, AD-13 |
| NFR2 no IP logs / 24 h expiry | **[Low]** Logging convention says "no message content" but is silent on IPs / PII. Rate-limiting is per-account-key and in-memory, so IP logs may not exist at all — but say so explicitly |
| NFR3 account key never in client-visible payload | **[Low]** Implied by AD-9 (backend is sole origin of peer-visible fields) but not stated as an invariant. One line: "no wire message delivered to a peer contains an account key" |

### 6. Every dimension this initiative-altitude spine owns is decided / deferred / an open question — especially the operational/environmental envelope

**Operational envelope is well-covered** — better than most spines at this altitude:

- Deployment & environments: dev machine, Fly public instance, self-host — all three drawn
  and described. ✔
- Infra / provider strategy: Fly.io named, single machine + volume, single-instance "by
  design" with the reason (one SQLite file). ✔
- Operations: release flow (CI cross-build, container publish, GitHub release, plugin pins one
  companion version), maintainer CLI subcommands for ban / reports (AD-13),
  `REPORT_WEBHOOK` for out-of-band reports, `GET /status` for metrics. ✔
- Config surface: fully enumerated (AD-12). ✔

**Silent sub-dimensions:**

- **[Medium] Database schema migration.** The spine is meticulous about *wire* versioning
  (AD-3, AD-5: negotiate on connect, accept current + previous minor, bump on breaking
  change) but says nothing about how the schema of the persistent SQLite file — which it
  mandates and which lives on a long-lived Fly volume across many backend releases — evolves.
  No migration tool, no "schema version" row, no "additive-only" rule, not even a deferral.
  For a store that outlives the process and the deploy, this is a real owned dimension left
  blank. Minimum: a one-line rule (e.g. "additive migrations only, applied on boot; a
  `schema_version` row gates startup") or an explicit Open Question.

- **[Low] Volume durability / backup.** Single SQLite file, single volume, no snapshot or
  backup posture. The data is low-value and largely reconstructable (profiles, blocks,
  cooldowns, bans, report rows) so this is minor — but it should be a deliberate one-liner
  ("no backups in v1; data loss on volume failure is accepted, bans are the only painful
  loss") rather than unstated.

No staging environment — correct omission at this bar, not a finding.

### 7. Diagrams are valid mermaid and convey real structure

**Two of three are fine. One is likely broken.**

- **Diagram 1 (`graph LR`, unit/dependency):** valid. Conveys real structure. Mixes
  compile-time edges (`imports`) with runtime edges (`websocket`, `tail`, `spawns once`) and
  a data edge (`file`) without visual distinction, but the prose immediately below
  ("Dependency direction: …") rescues it. Cosmetic only.

- **Diagram 2 (`graph TB`, containers & environments):** valid (`subgraph id["Title"]`,
  `-.label.->` dotted edges, cross-subgraph edges all supported). Conveys the deployment
  topology clearly — this is the diagram doing the most work and it earns its place.

- **[Medium] Diagram 3 (`erDiagram`, persisted entities): likely does not render.** The
  `PROFILE` block is well-formed (one `type name [key]` per line). The other four blocks pack
  every attribute onto a single line:
  `BLOCK { string blocker_key FK  string blocked_key  int created_ms }`. Mermaid's erDiagram
  grammar is newline-delimited inside `{ }` — one attribute per line — so these compressed
  bodies parse as a single malformed attribute and error out. Fix: expand `BLOCK`,
  `COOLDOWN`, `BAN`, `REPORT` to one attribute per line like `PROFILE`. (Modeling nit while
  you're there: `COOLDOWN` has two FKs to `PROFILE` — `key_a`, `key_b` — that the single
  crow's-foot relationship can't express; acceptable, but a comment would help.)

---

## Summary table

| # | Checklist item | Verdict | Severity of worst gap |
| --- | --- | --- | --- |
| 1 | Fixes real divergence points, misses none | Mostly — 3 misses | Medium |
| 2 | Rules enforceable & prevent their divergence | Yes | Low (status tags) |
| 3 | Deferred can't let units diverge | Correct | Low (1 mis-bucketed invariant) |
| 4 | Named tech verified-current / coherent | Yes | — |
| 5 | Covers F1–F11, key NFRs | F1–F11 yes; a few NFR gaps | Medium (inherits items 1) |
| 6 | Every owned dimension decided / deferred / OQ | Ops envelope strong; 2 silent sub-dimensions | Medium (DB migration) |
| 7 | Diagrams valid mermaid, real structure | 2 of 3 good | Medium (erDiagram likely broken) |

## Recommended fixes before build (all additive)

1. **Add an AD for fail-open / never-block-CC** binding `plugin` + `companion` (item 1).
2. **Assign content sanitization** — link inertness, length caps, no remote markup — to a unit,
   folded into AD-2 or a new AD (item 1).
3. **Fix the erDiagram** — one attribute per line for `BLOCK` / `COOLDOWN` / `BAN` / `REPORT`
   (item 7).
4. **State a DB schema-migration rule** (or an explicit Open Question) (item 6).
5. **Normalize AD status tags** — all or none (item 2).
6. Minor: promote "exactly 1 concurrent session / all limits bounded" out of Deferred;
   add one-liners for NFR2 (IP logging), NFR3 (no account key in peer payloads), and volume
   backup posture.
