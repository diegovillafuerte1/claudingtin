# Claude Code session transcript format

**Status: observed, not contracted.** This note pins what the Claude Code
session transcript looked like during the Story 1.3 spike so the companion's
think-time detector (`companion/internal/transcript`) has something to code
against. It is a snapshot of undocumented, unstable behaviour. Nothing here is a
promise from Anthropic, and every field, path rule, and line type below can
change without notice in any Claude Code release. Treat a parse miss as normal
and degrade quietly — never as a contract violation.

## File location

For each project directory, Claude Code keeps one JSONL file per session under:

```
~/.claude/projects/<slug>/<sessionId>.jsonl
```

- `<slug>` is the session's absolute working directory with every `/` and `.`
  replaced by `-`. A leading `/` therefore becomes a leading `-`
  (`/Users/k/Documents/Github/claudingtin` →
  `-Users-k-Documents-Github-claudingtin`).
- `<sessionId>` is a UUIDv4. It also appears inside the lines as `sessionId`
  (and, on some line types, `session_id`).
- The companion does **not** derive this path. Story 1.7's `SessionStart` hook
  passes the transcript path in as an argument, so there is no OS-specific path
  logic in the parser or the tailer — the path is an input.

The file is appended to line-by-line as the session runs. It can be rewritten
(observed as a shrink in size — treated as truncation) and, across sessions or
compaction, replaced (rename / remove of the path — treated as rotation).

## Line schema

Newline-delimited JSON: one JSON object per line, `\n`-terminated. The last line
may lack its terminating newline while it is still being written — it is
incomplete and must be buffered until the rest arrives.

Every line has a string `type`. Two `type` values carry turns:

### `user`

A `user` line wraps an Anthropic Messages API user message plus Claude Code
bookkeeping:

| Field | Notes |
|-------|-------|
| `type` | `"user"` |
| `message.role` | `"user"` |
| `message.content` | a plain string for a real prompt; an **array** for tool results (`[{ "type": "tool_result", "tool_use_id", "content", "is_error"? }]`) and for meta blocks (`[{ "type": "text", ... }]`) |
| `uuid`, `parentUuid` | line identity / linkage; `parentUuid` is `null` for the first line of a session |
| `timestamp` | ISO 8601 / RFC 3339 UTC, e.g. `2026-09-05T16:45:00.000Z` |
| `isSidechain` | `true` marks a subagent (Task tool) turn — see caveats |
| `isMeta` | `true` on injected context lines (command output, caveats, `turnCompanion` blocks); absent otherwise |
| `origin` | `{ "kind": "human" }` for a typed prompt, `{ "kind": "task-notification" }` for a wake-up, absent/`null` for tool-result lines. Observed kinds are not exhaustive. |
| `promptSource` | `"typed"`, `"system"`, or absent |
| `sessionId`, `version`, `cwd`, `gitBranch`, `userType`, `entrypoint` | session context, present on most (not all) `user` lines |

**Turn-start rule (structural, not a whitelist):** a `user` line begins a turn
when its `message.content` is **not** a `tool_result` array **and** the line is
**not** `isMeta` and **not** `isSidechain`. This one rule covers human prompts,
queued prompts, and `task-notification` wake-ups, and it survives new `origin`
kinds in later Claude Code versions. A turn-start while a turn is already open is
an interrupt: the open turn ends immediately, then the new one starts.

### `assistant`

An `assistant` line wraps an Anthropic Messages API assistant message:

| Field | Notes |
|-------|-------|
| `type` | `"assistant"` |
| `message.id` | Anthropic message id. **One assistant reply is written as several `assistant` lines that share this id** — e.g. a `thinking` line then a `text` line, each carrying the same final `stop_reason`. |
| `message.role` | `"assistant"` |
| `message.content` | array of blocks: `thinking`, `text`, `tool_use`, … |
| `message.stop_reason` | `end_turn`, `stop_sequence`, `max_tokens`, `refusal` are **terminal** (generation halted, turn is over). `tool_use`, `pause_turn`, and `null`/absent are **not** — the turn continues. |
| `message.usage`, `message.model`, `requestId`, `apiBlockIndex`, `effort` | metadata, ignored by the parser |
| `uuid`, `parentUuid`, `timestamp`, `isSidechain`, `sessionId`/`session_id`, `version`, `cwd`, `gitBranch` | as for `user` |

**Turn-end rule:** the first `assistant` line with a terminal `stop_reason`
after a turn-start ends the turn, unless the line is `isSidechain` or `isMeta`
(injected — never a real turn boundary). The parser then stays idle until the
next turn-start, so the sibling lines of a multi-line reply (same `message.id`,
same terminal `stop_reason`) collapse to a single turn-end. `N` tool calls
inside a turn produce `N` non-terminal (`tool_use`) `assistant` lines and never
end it.

**Consumed by Story 1.6:** the companion maps these boundaries to the `ready` /
`busy` wire messages — per the epic, `ready` within ~1s of a turn-start (model
thinking, user available to match), `busy` within ~1s of a turn-end. That
mapping, the websocket, and the wire messages are all Story 1.6's; this package
only emits the boundaries.

## Other line types (ignored)

Every other `type` is skipped silently. Observed during the spike:

```
mode  permission-mode  atis-latch  bridge-session  last-prompt  ai-title
agent-name  queue-operation  cost-state  file-history-snapshot
file-history-delta  attachment  system  pr-link
```

This list is not exhaustive and grows across versions — an unknown `type` is
expected, not an error.

## Observed versions

- Claude Code CLI: `2.1.261` (the machine running the spike).
- `version` field values across the transcripts sampled: **`2.1.250`, `2.1.252`,
  `2.1.261`** (range `2.1.250`–`2.1.261`).
- Sanitized structural fixtures derived from these transcripts live in
  `companion/internal/transcript/testdata/` — line shapes only, all conversation
  content (`thinking`, `text`, `tool_result` bodies, `usage`, tokens) stripped.

## Caveats

- **Not a public contract.** Undocumented, unstable, version-dependent. The
  parser and tailer are deliberately defensive: unknown `type`, unparseable
  line, missing field, truncation, rename, rotation → recover and continue,
  never panic, never treat it as fatal.
- **`isSidechain` marks subagent turns; `isMeta` marks injected lines.** In the
  transcripts sampled, Task subagents wrote to their own separate session files
  (no `isSidechain: true` lines appeared in the parent file), but the parser
  still filters both `isSidechain` and `isMeta` on `user` **and** `assistant`
  lines, so an interleaved subagent turn or an injected line can never open or
  close a main-session turn.
- **Field set grows across versions.** New top-level keys and new nested
  `message` fields appear between releases; the parser reads a small named
  subset and ignores the rest.
- **`origin.kind` is open-ended.** New kinds will appear; the structural
  turn-start rule above does not enumerate them.
- **Timestamps are informational.** An absent or unparseable `timestamp` yields
  the zero time, not an error.
