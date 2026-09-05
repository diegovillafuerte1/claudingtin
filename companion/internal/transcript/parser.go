// Package transcript turns the Claude Code session transcript (a newline-
// delimited JSON file) into a stream of turn boundaries: exactly one TurnStart
// and one TurnEnd per user turn, no matter how many tool calls happen inside it.
//
// The transcript file's location and line schema are not a documented, stable
// contract — see docs/transcript-format.md. Everything here is defensive: an
// unknown line type or an unparseable line is skipped, never fatal.
//
// Parser is pure: it is fed one raw JSONL line at a time and returns any turn
// events that line produced. It performs no I/O. The fsnotify tailer that reads
// the file and feeds Parser lives in watch.go.
package transcript

import (
	"encoding/json"
	"time"
)

// EventKind distinguishes the two turn boundaries Parser emits.
type EventKind int

const (
	// TurnStart marks the beginning of a user turn: a human prompt, a queued
	// prompt, or a task-notification wake-up.
	TurnStart EventKind = iota
	// TurnEnd marks the model going idle at the end of a turn: the first
	// assistant line with a terminal stop_reason after a TurnStart.
	TurnEnd
)

// String renders an EventKind for tests and logs.
func (k EventKind) String() string {
	switch k {
	case TurnStart:
		return "TurnStart"
	case TurnEnd:
		return "TurnEnd"
	default:
		return "EventKind(?)"
	}
}

// Event is a single turn boundary. At is the transcript line's timestamp, or the
// zero time if the line carried no parseable timestamp.
type Event struct {
	Kind EventKind
	At   time.Time
}

// State is the parser's turn state, exposed for a mid-file attach: after seeding
// Parser from the tail of an existing transcript, State reports whether the last
// user turn is still in progress.
type State int

const (
	// StateIdle: no turn is in progress; the model is not thinking.
	StateIdle State = iota
	// StateInTurn: a TurnStart has been seen with no matching TurnEnd yet.
	StateInTurn
)

// String renders a State for tests and logs.
func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateInTurn:
		return "in-turn"
	default:
		return "State(?)"
	}
}

// terminalStopReasons end a turn: generation halted and the model is idle.
// tool_use, pause_turn, and a null/absent stop_reason do not — the turn
// continues.
var terminalStopReasons = map[string]bool{
	"end_turn":      true,
	"stop_sequence": true,
	"max_tokens":    true,
	"refusal":       true,
}

// Parser is the turn-boundary state machine. The zero value is ready to use and
// starts idle.
//
// Not safe for concurrent use: the tailer in watch.go owns one Parser and feeds
// it from a single goroutine.
type Parser struct {
	inTurn bool
}

// NewParser returns an idle Parser. The zero value works too; this is here for
// readability at call sites.
func NewParser() *Parser { return &Parser{} }

// State reports whether a turn is currently in progress.
func (p *Parser) State() State {
	if p.inTurn {
		return StateInTurn
	}
	return StateIdle
}

// rawLine is the subset of a transcript line the parser cares about. Every other
// field — and there are many, see docs/transcript-format.md — is ignored.
type rawLine struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		ID         string          `json:"id"`
	} `json:"message"`
}

// Feed consumes one raw JSONL line (without the trailing newline) and returns
// the turn events it produced, in order. Most lines produce none. A line that
// does not parse as JSON, or whose type is unknown, is skipped silently and
// returns nil — never an error, never a panic.
//
// Buffering of an incomplete final line is the tailer's job, not the parser's:
// Feed expects whole lines.
func (p *Parser) Feed(line []byte) []Event {
	var l rawLine
	if err := json.Unmarshal(line, &l); err != nil {
		return nil
	}

	switch l.Type {
	case "user":
		return p.feedUser(l)
	case "assistant":
		return p.feedAssistant(l)
	default:
		// mode, permission-mode, atis-latch, bridge-session, last-prompt,
		// ai-title, agent-name, queue-operation, cost-state, file-history-*,
		// attachment, system, and whatever later Claude Code versions add.
		return nil
	}
}

// feedUser handles a "user" line. It is a turn boundary when its content is not
// a tool_result and it is not a meta or subagent line. Structural detection, not
// an origin.kind whitelist: one rule covers human prompts, queued prompts, and
// task-notification wake-ups, and survives new origin kinds.
func (p *Parser) feedUser(l rawLine) []Event {
	if l.IsMeta || l.IsSidechain {
		return nil
	}
	if isToolResultContent(l.Message.Content) {
		return nil
	}

	at := parseAt(l.Timestamp)
	if p.inTurn {
		// Interrupt: a new turn starts before the previous one ended. Close the
		// old turn immediately, then open the new one. inTurn stays true.
		return []Event{{Kind: TurnEnd, At: at}, {Kind: TurnStart, At: at}}
	}
	p.inTurn = true
	return []Event{{Kind: TurnStart, At: at}}
}

// feedAssistant handles an "assistant" line. Only the first terminal-stop_reason
// line after a TurnStart produces a TurnEnd; once idle, further assistant lines
// (including the sibling lines of a multi-line reply that share message.id) are
// ignored. Subagent (isSidechain) and injected (isMeta) lines never end the
// main turn.
func (p *Parser) feedAssistant(l rawLine) []Event {
	if !p.inTurn || l.IsSidechain || l.IsMeta {
		return nil
	}
	if !terminalStopReasons[l.Message.StopReason] {
		return nil
	}
	p.inTurn = false
	return []Event{{Kind: TurnEnd, At: parseAt(l.Timestamp)}}
}

// isToolResultContent reports whether a "user" line's message.content is the
// array form carrying a tool_result block. A plain string prompt (the common
// case) unmarshals as a string, not an array, and returns false.
func isToolResultContent(content json.RawMessage) bool {
	if len(content) == 0 {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return false // string content, or something unexpected — not a tool_result
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// parseAt parses a transcript timestamp (ISO 8601 / RFC 3339, e.g.
// "2026-09-05T16:45:00.123Z"). RFC3339Nano parses timestamps both with and
// without fractional seconds. An empty or unparseable value yields the zero
// time rather than an error — the timestamp is informational.
func parseAt(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	return time.Time{}
}
