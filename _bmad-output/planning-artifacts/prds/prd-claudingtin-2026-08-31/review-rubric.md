# PRD Quality Review — Claude Think-Time Chat Roulette

## Overall verdict

This is a strong fast-path draft that knows exactly what it is: a hobby-scale, open-source
project sized with deliberate, well-placed rigor where the risk actually lives (trust/safety/legal,
section 8) and deliberate restraint everywhere else (no growth metrics, two personas, lean NFRs).
It makes hard calls and states them as calls — text-only, FIFO-only, self-attested 18+ with a
documented residual-risk acceptance — rather than smoothing to neutral. What holds up: the thesis,
the scope discipline, the honest triage of blocking vs. non-blocking open questions. What is at
risk downstream: there is no glossary on a PRD that explicitly feeds architecture, UX, and epics;
a cluster of FR cross-references broke during renumbering; and the single most load-bearing FR
(FR5, the chat surface) is an open technical risk rather than a spec — correctly flagged, but
architecture cannot spec the core surface until OQ-1 resolves.

## Decision-readiness — strong

The PRD reads as a set of decisions, not considerations. Section 1 carries a "Framing decisions
already locked" block; FR23 ("Persist SHALL ship default-on") and FR44 ("Decision (Diego):
self-attestation is the v1 approach — no ID checks") name the decider and the trade accepted;
OQ-A and OQ-C are marked "DECIDED" with the residual risk spelled out ("self-attestation is
increasingly held inadequate ... Diego's call is to accept this risk"). Section 11 struck-through
resolved items (OQ-2, OQ-4, OQ-6, OQ-9) with their rationale rather than deleting them, which
preserves the audit trail.

Trade-offs are named with what was given up. The `[NOTE FOR PM]` at FR23 is placed at a genuine
tension, not a safe checkpoint: persist default-on breaks the "no decision required to end it"
property from UJ-1 and G2, and the note says so, lists options (a)/(b)/(c), and records "Left as
(a) per Diego unless flagged." Open questions are actually open — OQ-1 (feasibility), OQ-3 (auth)
— and separated from the non-blocking set. The section 4 metrics resist the "everything balances"
red flag by naming explicit counter-metrics (C1–C3) and directly addressing the tension between
M2 ("people keep using it") and NG1 ("not optimizing for retention").

### Findings
- **low** OQ-B has no named owner (§8, "Open legal questions") — OQ-A and OQ-C each end with
  "*Owner: Diego*" / "*drafting owner: Diego*", but OQ-B (matching-as-defective-product) only
  says "the maintainer should get the risk assessed." *Fix:* add an owner line consistent with
  its siblings.

## Substance over theater — strong

No persona theater: two personas (§3), both load-bearing. "The lonely power user" drives the
entire exit convention (F4) and the metrics stance; "the maintainer / self-hoster" drives NFR13
(cost), NFR14 (one-command self-host), NFR16, G4, and G5. Neither is furniture.

No vision theater: the "Why it exists" paragraph in §1 ("idle think-time becomes a moment of
human contact instead of a moment of waiting") is specific to this product and could not be
pasted into another PRD in the category.

NFRs are mostly earned, not boilerplate — NFR7 (`< ~500ms p90`), NFR9 ("low hundreds of
concurrent users"), NFR13 ("under ~$20/month") carry product-specific thresholds, and the softer
ones are honestly tagged `[ASSUMPTION]`. The "text-only stranger chat is an underserved niche"
claim (§8) is sourced in addendum §1 and tied to a safety rationale rather than standing as a
differentiation section for its own sake.

## Strategic coherence — strong

The PRD has a thesis and bets on it: think-time dead time is "both frequent and empty," and
liquidity is make-or-break — M1 states "If the spinner reliably resolves inside a think-time
burst, the product works. If it doesn't, nothing else matters." Feature prioritization follows:
v1 is pairing plus the safety primitives that make stranger chat survivable; the commitment ramp,
tags, pull-in, and the webapp are all v2 (§10), gated on "once liquidity is proven." Section 9
(cold-start) is entirely in service of the thesis — making a thin pool feel non-empty.

Success metrics validate the thesis rather than measuring activity: M1 is liquidity, M2 is repeat
use as an outcome proxy with the reasoning stated, and counter-metrics C1 (session length
trending up) and C2 (DAU becoming a design focus) explicitly guard against drift into an
engagement product — matching the "not a growth play" framing in §1. The MVP scope kind is
coherent: a problem-solving MVP (loneliness in the think-time gap) with an experience spine (the
"my Claude came back" no-rejection fiction), and the scope logic matches — minimal surface,
safety primitives, everything else deferred.

## Done-ness clarity — adequate

Most FRs carry at least one testable consequence, often sharply. FR6 ("MUST never block, delay,
or error the user's Claude Code session"), FR10 ("no queue position, no ETA, no 'N people
online'"), FR15 (memory-only relay, no disk), FR19 ("No event SHALL ever tell a user they were
left, rejected, blocked, or reported"), FR41 (`hash(Claude account id + server salt)`), and FR43
(enumerated first-run disclosure content) are all verifiable as written. Assumption-tagged
numeric bounds (FR38 `N≈20`, FR34 `~1 minute` / `~24h`, NFR7 `~500ms p90`) give story creation
something concrete to test against even where flagged for confirmation. There is no separate
Acceptance Criteria section, but for a PRD at this scale the FR consequences largely carry that
weight.

The one serious gap is FR5. "Terminal-native and adjacent," "Intended form: a split pane ...
degrading to a small persistent corner companion widget," and "Exact mechanism is an open
technical risk — see OQ-1" describe a direction, not a spec. This is the core UX surface; UX and
architecture work cannot proceed on it until OQ-1 resolves. It is correctly and prominently
flagged as phase-blocking with a named owner (the architect / Winston), so this is handled
honestly — but downstream it is a hole, not a spec.

### Findings
- **medium** FR5 chat-surface mechanism is unspecified (§6 F1, FR5; §11 OQ-1) — "Exact mechanism
  is an open technical risk." Correctly flagged, but UX cannot spec the product's primary surface
  and architecture cannot size it until OQ-1 closes. *Fix:* treat OQ-1 resolution as a gate that
  feeds back a rewritten FR5 (concrete mechanism, fallback behavior, cross-terminal behavior)
  before UX spec begins.
- **low** Adjectival NFRs — NFR6 ("no perceptible latency") and NFR8 ("good enough for a hobby
  project" availability). Both specify degradation behavior, which limits the damage, but neither
  gives a bound. *Fix:* add a hook-return budget to NFR6 (e.g. hook exits in `< 50ms`).
- **low** FR36 rate-limiting leaves two of three dimensions unbounded — "max match rate, max
  messages/second" have no numbers; only "max concurrent sessions (1)" is set. *Fix:* add rough
  ceilings or mark the two open dimensions as OQ-owned.

## Scope honesty — strong

Omissions are explicit and stated twice: NG1–NG6 in §2 (NG1 tied directly to the counter-metrics,
NG6 explaining the Claude-Code-only limitation with its reason) and a full "Out of scope (v1)"
list in §12. Deferred work is not silently dropped — it is collected in the §10 roadmap (R1–R6)
with `[v2]` markers on the relevant FRs. Rejected directions (micro-consult marketplace,
minigames, per-match generated openers) are named with rationale via the addendum §3 reference.

Assumptions are tagged inline (`[ASSUMPTION]`, ~21 occurrences) and the `[NOTE FOR PM]` at FR23
sits at the one real unresolved tension. Open-items density is well-triaged for the stakes:
section 11 splits "Phase-blocking — resolve before architecture / build" (only OQ-1 and OQ-3 are
truly live) from "Non-blocking — decide during build, owner noted" (OQ-5, 7, 8, 10). Two blockers
on a v1 green-light for a hobby project is honest and proportionate.

### Findings
- **low** The Assumptions Index (§11, "Assumptions to confirm") summarizes rather than enumerates
  — it calls out five load-bearing assumptions and otherwise says "All `[ASSUMPTION]`-tagged
  items above." A reader has to grep the body to get the full list. *Fix:* enumerate the inline
  tags, or state explicitly that the grep-based roundtrip is the intended index for this PRD.

## Downstream usability — adequate

This PRD is chain-top — §1 references "Framing decisions already locked," §11 OQ-1 names the
architect (Winston) as the owner who "must validate feasibility ... before build," and the intent
is to feed UX, architecture, and epics. The ID space supports that: FR1–FR46 are contiguous and
unique; UJ, M, C, G, NG, NFR, CS, R, and OQ series are all clean; UJ-1 through UJ-3 have named
protagonists (Sam, Priya, plus "Dev") carrying context inline.

Two things weaken source-extraction. First, there is no glossary, and the core domain nouns are
not anchored — "stable account key" appears also as "stable key," "stable (hashed, salted)
account key," and (addendum) "stable account token"; "think-time burst" alternates with "burst"
and "think-time"; "auto-cooldown," "cooldown," and "temporary cooldown" are used for the same
mechanism. Each term is individually clear from context, but a chain-top PRD feeding three
workflows should pin them once. Second, a cluster of FR cross-references broke during renumbering
(see mechanical notes) — an engineer chasing the persist definition from FR2 lands on the wrong
FR.

### Findings
- **high** No Glossary on a chain-top PRD (§1; §11 OQ-1 names Winston/architecture as consumer).
  Core nouns — "stable account key," "think-time burst," "persist," "auto-cooldown," "session
  ID," "pull-in," "commitment ramp," "the exit convention" — have no anchored definitions, and
  several drift in phrasing across the document. *Fix:* add a short glossary; pin "stable account
  key" to one phrasing and use it verbatim in FR32, FR41, NFR2, NFR3, and the §8 abuse table.
- **medium** Broken FR cross-references from renumbering. FR2 cites "unless **persist** is
  enabled for that user (FR20)" — persist is FR22/FR23; FR20 is the re-queue rule. FR8 cites
  "block list (FR30)" — block is FR32; FR30 is openers — and "an active cooldown window with them
  (FR32)" — cooldown is FR34. *Fix:* correct the three references (FR2 → FR22/FR23; FR8 → FR32
  and FR34).
- **low** UJ-5 has no named protagonist ("New user"); UJ-1 and UJ-4 use "Dev," which reads
  ambiguously as a role rather than a name. *Fix:* name the UJ-5 protagonist and disambiguate
  "Dev."

## Shape fit — strong

The PRD has not been forced into a shape that fights the product. It is a consumer-facing
stranger-chat product with a solo, open-source maintainer, and it is sized accordingly: rigor
light overall (~4–6 pages, two personas, lean NFRs), with the substance bar still met. The
meaningful-UX element — the "my Claude came back" no-rejection framing, which the addendum calls
the "load-bearing insight" — is carried by UJs with named protagonists (UJ-1 through UJ-5), which
is the right instrument for it.

Section weight tracks risk. The trust/safety/legal section (§8) is heavier than a typical hobby
project would carry, and the PRD justifies that directly: "the closest precedents (Omegle,
Chatroulette) failed on exactly these issues." Growth metrics are deliberately absent and M1 is
operational (liquidity), matching the "for-fun ... not a startup, not a growth play" stance in
§1. Nothing is over-formalized; nothing load-bearing is under-formalized except FR5, covered
above.

## Mechanical notes

- **Glossary drift.** "stable account key" / "stable key" / "stable (hashed, salted) account
  key" / "stable account token" (addendum) / "ban / block key" — one concept, four to five
  phrasings. "think-time burst" / "burst" / "think-time" used loosely. "auto-cooldown" /
  "cooldown" / "temporary cooldown" interchangeable. No glossary to anchor any of them.
- **ID continuity.** FR1–FR46 contiguous and unique. UJ-1–5, M1–M3, C1–C3, G1–G5, NG1–NG6,
  NFR1–NFR17, CS1–CS5, R1–R6, OQ-1/3/5/7/8/10 plus OQ-A/B/C — all clean, no gaps or duplicates.
- **Broken cross-references.** FR2 → "(FR20)" should be FR22/FR23 (persist). FR8 → "block list
  (FR30)" should be FR32. FR8 → "cooldown window ... (FR32)" should be FR34. All three are
  consistent with an off-by shift from a renumber; no other cross-refs affected (F4/F7 group
  references, FR23→C1/UJ-1/G2, FR32→FR41, FR44→OQ-A all resolve correctly).
- **Assumptions Index roundtrip.** Partial. Section 11 "Assumptions to confirm" names five
  load-bearing assumptions and otherwise points at the inline tags rather than listing all ~21.
  Every summarized item does appear inline; the reverse (every inline tag in the index) does not
  hold.
- **UJ protagonist naming.** UJ-2 (Sam), UJ-3 (Priya) named cleanly. UJ-1/UJ-4 ("Dev") ambiguous
  between name and role. UJ-5 ("New user") unnamed.
- **Required sections.** All present for the agreed stakes and product type: Overview,
  Goals/Non-goals, Target users, Success metrics + counter-metrics, User journeys, FRs, NFRs,
  Trust/safety/legal, Cold-start, v2 roadmap, Open questions/assumptions, Out of scope.
