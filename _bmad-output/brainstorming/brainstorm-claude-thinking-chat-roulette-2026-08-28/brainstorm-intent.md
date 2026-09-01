---
title: Brainstorm Intent — Claude Think-Time Chat Roulette
date: 2026-08-28
source: .memlog.md (canonical brainstorming record)
status: ready for product-brief / PRD
---

# Concept

A Claude plugin that pairs you with another Claude user for a live one-to-one
chat during the dead time while your model is thinking. Each chat is ephemeral by
default and scoped to think-time: when your Claude comes back, the chat can just
end. The aim is to turn idle think-time into serendipitous human connection, with
a lightweight, socially safe exit built into the core interaction.

# Problem / Motivation

- Claude think-time creates frequent short stretches of dead time with nothing to do.
- The target user is the AI-pilled Claude user who is starved of human connection.
  Anyone will do as a match, though many would prefer an opposite-gender match.
- Think-time bursts can be very short, so any heavy matching step would cost more
  time than the chat itself is worth.

# Core Product (MVP)

Pure, minimal FIFO chat roulette delivered as a plugin:

- **FIFO pairing.** No matching algorithm at all — first in queue meets next in
  queue. This is both the right call for short think-time and the
  lowest-hanging-fruit MVP.
- **Ephemeral by default, tied to model think-time.** When your Claude finishes,
  the chat ends and you move on — clean, no decision required.
- **Persist toggle, defaultable ON.** A user setting: when persist is on, the chat
  keeps going after think-time ends and continues until you choose to stop, at
  any time.
- **Tiny profile.** Small and optional; edited from within the plugin.
- **Instant pre-written opener prompts.** A fast rotation of generic pre-written
  conversation openers shown at the start of each match. (Not per-match generated
  — see Open Questions.)
- **"My Claude came back" exit convention.** Leaving at any moment is framed as
  "my Claude came back," so bailing never reads as rejection. This social fiction
  is the sanctioned way to end any chat.
- **Disconnect returns you to the spinner.** On disconnect, just show the
  "searching" spinner again and silently re-enter the queue. No "they left"
  message, ever.

# Chosen Framing

- A **for-fun, open-source plugin integrated with Claude**. Not a startup.
- Accept the concept's recurring gravitational pull toward a dating app: a **light
  dating-match element is acceptable** rather than something to fight.

# Post-MVP / v2

A companion webapp "half" that adds a commitment path on top of the ephemeral chat:

- **Shareable session IDs.** Every ephemeral chat has a session ID you can share.
- **"Missed connections" board.** You can't DM anyone. You post your session ID;
  the other person searches for it, finds your contact info, and a built-in
  connect flow lets you actually connect.
- **Optional interest hashtags/tags** in the profile that lightly bias matching
  toward people who share a tag, so you occasionally re-meet a small recurring cast.
- **Optional profile-matched "pull-in."** An active, non-waiting user can be
  pulled into a match if their profile fits what a searcher is looking for
  (opt-in match criteria, e.g. a/s/l — age/sex/location). FIFO stays the baseline.

# Key Design Principles / Guardrails

- **No minigames.** Resist feature bloat. Dead beats get lightweight
  tooltips/nudges only (e.g. "why not edit your profile") — nothing more.
- **Not a help/expertise marketplace.** The "micro-consult / work help" direction
  was explicitly killed: it needs too much personal and situational context, and
  real human-help needs are too domain-specific — "everything breaks." The product
  stays scoped to light serendipitous social chat.
- **One per-person block primitive that outranks tags and match criteria.**
  Blocking a specific person must never cost you an interest tag or force you to
  change your match criteria to escape someone.
- **Auto-block cooldown to prevent small-pool repeats.** Rate-limit re-matches
  with the same person — e.g. ~1 minute together, then auto-block for a day
  (temporary cooldown) to force variety.

# Open Questions (for downstream)

- Anti-repeat / cooldown rules for pull-in and for small pools generally — needs
  careful design beyond the rough "1 minute then day-long auto-block" sketch.
- Per-match generated icebreakers (built from both profiles) are likely too slow
  and costly to compute at match time; pre-written openers are the current
  fallback. Revisit if generation gets cheap/fast enough.

# Synthesis Insight (non-obvious)

- The **"my Claude came back" social fiction is load-bearing** for every risky
  feature. It is what makes ephemeral exits, disconnects, pull-in, and dating-ish
  matching feel safe rather than like rejection.
- The **persist toggle → session ID → missed-connections board form a single
  commitment ramp**: each step lets a user voluntarily escalate from "forget this
  person" to "actually connect," without ever forcing the choice.
- **Pull-in doubles as the cold-start fix**: matching against active non-waiting
  users keeps the queue from feeling empty when the pool is small.
