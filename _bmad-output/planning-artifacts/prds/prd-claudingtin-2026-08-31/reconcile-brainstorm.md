# Brainstorm ↔ PRD Reconciliation

**Input reconciled:** `_bmad-output/brainstorming/brainstorm-claude-thinking-chat-roulette-2026-08-28/brainstorm-intent.md`
**Compared against:** `prd.md`, `addendum.md` (prd-claudingtin-2026-08-31)
**Date:** 2026-08-31

## Summary judgment

Coverage of the brainstorm's *mechanics* is strong. Nearly every concept, guardrail,
and synthesis insight is present, often verbatim: FIFO pairing and its rationale,
ephemeral-by-default, "my Claude came back" as load-bearing social fiction, silent
re-queue with no "they left" message, tiny optional profile, pre-written (not
generated) openers, no minigames, not-a-help-marketplace, the block primitive
outranking tags/criteria, the ~1-min-then-24h auto-cooldown, the persist → session
ID → missed-connections commitment ramp as one continuous mechanism, pull-in as the
cold-start fix, the light dating-adjacent framing accepted rather than fought, and
the "for-fun, open-source, not a startup" posture (which the PRD actually
*strengthens* via counter-metrics C1–C2).

The gaps are about **emphasis, tone, and one genuine tension** — the things a
requirements structure drops silently.

---

## 1. Drift / tension — Persist default-on vs "no decision required to end it"

**Brainstorm intent:** Two paired statements under Core Product —
(a) "Ephemeral by default, tied to model think-time. When your Claude finishes, the
chat ends and you move on — clean, no decision required."
(b) "Persist toggle, defaultable ON. A user setting: when persist is on, the chat
keeps going after think-time ends…"

The most faithful reading of (b) is a *per-user* option the user can set as their
own default — subordinate to (a), which is stated as *the* product default and is
echoed in the concept line ("the chat can just end").

**PRD:** FR23 ships persist **ON for every new user**. The PRD's own `[NOTE FOR PM]`
under FR23 concedes this: the default user "must actively tap 'my Claude came back'
to end every chat — the 'no decision required to end it' property (UJ-1, G2) only
holds for users who turn persist off," and it makes counter-metric C1 (session
length) more load-bearing. Resolution recorded as "accept it (option a) per Diego."

**Why it matters:** This is the one place the PRD's build target works against a
headline property of the brainstorm — the *effortless, decision-free exit* that the
whole "socially safe exit built into the core interaction" idea rests on. It is
consciously chosen and flagged, not an oversight, but the finalize pass should
confirm Diego still wants global default-on given that it inverts "ephemeral by
default" for the median user. Options b (auto-close N seconds after model returns
with no new message) and c (default-off) are already enumerated in the NOTE.

---

## 2. Silently down-weighted — the playful / warm / serendipitous feel

**Brainstorm intent:** The emotional framing is light and warm: "turn idle
think-time into serendipitous human connection," a *roulette* (spin, low stakes,
spin again), "a small hit of genuine human connection," fun. The word "roulette"
and the for-fun spirit are the product's charm.

**PRD:** The spirit is *stated* (Overview "for-fun… not a growth play"; §12) but has
**no teeth in the functional requirements.** The PRD's center of gravity is
trust/safety/legal: §8 is the longest section, plus cold-start (§9) and three legal
open questions. A reader of the FR body alone would take this for a risk-management
project. Nothing governs:
- the *voice* of the opener prompts (brainstorm: "generic pre-written conversation
  openers," light — FR30 specifies only that they exist and rotate);
- the *warmth* of the "my Claude came back" / "your Claude is back" copy — FR19
  mandates the neutral no-rejection *framing* but not that it stay charming rather
  than clinical;
- the small delight of a match landing inside a think-time burst.

NFR8's "calm 'no one around right now'" state is the one place tone survives as a
requirement. Recommend a short "Voice & tone" note or NFR so the finalize pass
carries the serendipity/warmth forward as something buildable and reviewable, not
just as framing prose.

---

## 3. Weakened — lightweight nudges as the sanctioned answer to dead time

**Brainstorm intent (Key Design Principles):** "No minigames. Resist feature bloat.
Dead beats get lightweight tooltips/nudges only (e.g. 'why not edit your profile') —
nothing more." This is a *positive* instruction: nudges are the allowed response to
dead time.

**PRD:** Survives only as an **exception clause in the out-of-scope list** (§12:
"…any 'activity' beyond text chat and tooltip nudges") and in addendum §3. There is
no positive FR sanctioning the nudge behavior. UJ-4's "leave a note for the next
person" is a different mechanic (empty-queue seed for the v2 board), not the
edit-your-profile style nudge. Minor, but the brainstorm's one permitted
dead-time affordance has no requirement to point to.

---

## 4. Scope narrowed — "integrated with Claude" → "Claude Code only"

**Brainstorm intent:** "A for-fun, open-source plugin integrated with Claude"
(broad); Concept: "A Claude plugin."

**PRD:** NG6 and §12 exclude claude.ai and Claude Desktop from v1; v1 is a **Claude
Code plugin** specifically. Well-justified by addendum §2 (lifecycle hooks exist
only in Claude Code / Desktop, and the think-time approximation + companion pane
depend on them). This is a sound narrowing, but it is a real reduction from the
original vision and deserves to be an explicit, visible decision in the finalize
pass rather than only a non-goal line — especially since addendum §2 suggests
Desktop *might* also support hooks, yet §12 excludes it.

---

## 5. Minimized — the opposite-gender match preference as a stated user truth

**Brainstorm intent (Problem / Motivation):** "The target user is the AI-pilled
Claude user who is starved of human connection. Anyone will do as a match, though
many would prefer an opposite-gender match." Stated as a real, current user desire.

**PRD:** The persona line preserves "would prefer an opposite-gender match but will
take anyone." But the *feature* handling is reduced to FR29 — a single optional
"prefer to be matched with" hint, **stored for v2 pull-in, with explicitly no effect
on v1 FIFO.** Correct given FIFO-only, yet the acknowledged user need is now almost
invisible: it reads as a dormant storage field rather than a recognized truth the
product is deliberately choosing not to serve in v1. A one-line acknowledgement
("v1 knowingly does not address the opposite-gender preference; FIFO is gender-blind
by design") would keep the intent honest.

---

## Minor / not material

- **v2 missed-connections reconnect** evolved from the brainstorm's sketch ("post
  your session ID; the other person searches for it, finds your contact info, and a
  built-in connect flow lets you actually connect" — effectively one-directional)
  toward **mutual opt-in required** (FR25). This is a research-backed refinement,
  documented in addendum §1 ("opt-in re-connect that converts: user-initiated
  beats auto-triggered… only on a mutual signal"), so it is a deliberate
  improvement, not a distortion — noting only so the change of shape is visible.
- **"Instant" / "fast rotation" openers:** brainstorm stresses openers are *instant*
  and rotate fast (freshness, low repeat). PRD FR30 captures "curated rotating set"
  and the not-generated rationale; the instantaneity/freshness quality is implied
  but not stated as a requirement. Trivial.
- **"AI-pilled"** softened to "AI-heavy / AI-forward" — register change only,
  meaning preserved.

---

## No contradictions found beyond item 1

Item 1 (persist default-on) is the only place the PRD's stated build target pulls
against an explicit brainstorm intent, and the PRD flags it itself. Everything else
is faithful, additive, or a documented refinement.
