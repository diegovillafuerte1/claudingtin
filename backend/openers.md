# Openers

- The curated opener set lives in the open-source repo so contributors can extend it (CAP-7).
- Playful and disarming, skewed toward low-stakes human curiosity. Never survey-like or interview-like. ("What's the last thing that made you laugh?" — yes. "What are your hobbies?" — no.)
- The repo ships this file's Openers section as the one-paragraph voice guide contributed openers are checked against.

## To add an opener

Append a line to `openers.txt`; keep it 8–120 characters, plain text (no tabs
or control characters), and not a duplicate of an existing line. Run
`go test ./...` — the format check in `openers_test.go` verifies every entry and
fails naming any line that breaks a rule.

- Match the house style of the existing entries: lowercase, and phrased as a
  question.
- Keep the set comfortably above the two-entry minimum — it is a curated set to
  rotate through, not a fallback.
