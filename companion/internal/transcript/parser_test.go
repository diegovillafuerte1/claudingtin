package transcript

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// feedFixture reads testdata/<name>.jsonl and feeds every line (including a
// final line with no trailing newline) to a fresh Parser, returning the events
// in order and the parser's end state.
func feedFixture(t *testing.T, name string) ([]Event, State) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	p := NewParser()
	var got []Event
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		got = append(got, p.Feed(line)...)
	}
	return got, p.State()
}

func kinds(evs []Event) []EventKind {
	ks := make([]EventKind, len(evs))
	for i, e := range evs {
		ks[i] = e.Kind
	}
	return ks
}

func eqKinds(a, b []EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParserFixtures(t *testing.T) {
	cases := []struct {
		fixture  string
		want     []EventKind
		endState State
	}{
		{"normal_turn", []EventKind{TurnStart, TurnEnd}, StateIdle},
		{"turn_with_tools", []EventKind{TurnStart, TurnEnd}, StateIdle},
		{"multiline_reply", []EventKind{TurnStart, TurnEnd}, StateIdle},
		{"task_notification", []EventKind{TurnStart, TurnEnd}, StateIdle},
		{"unknown_types", nil, StateIdle},
		{"truncated_tail", []EventKind{TurnStart, TurnEnd}, StateIdle},
		{"idle_session", nil, StateIdle},
		{"interrupt", []EventKind{TurnStart, TurnEnd, TurnStart, TurnEnd}, StateIdle},
		{"sidechain_midturn", []EventKind{TurnStart, TurnEnd}, StateIdle},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			got, end := feedFixture(t, c.fixture)
			if !eqKinds(kinds(got), c.want) {
				t.Errorf("events = %v, want %v", kinds(got), c.want)
			}
			if end != c.endState {
				t.Errorf("end state = %v, want %v", end, c.endState)
			}
		})
	}
}

func TestParserTimestamps(t *testing.T) {
	got, _ := feedFixture(t, "normal_turn")
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	wantStart := time.Date(2026, 9, 5, 16, 45, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 5, 16, 45, 3, 0, time.UTC)
	if !got[0].At.Equal(wantStart) {
		t.Errorf("TurnStart.At = %v, want %v", got[0].At, wantStart)
	}
	if !got[1].At.Equal(wantEnd) {
		t.Errorf("TurnEnd.At = %v, want %v", got[1].At, wantEnd)
	}
}

// A mid-file attach: the parser is seeded from the tail of a transcript whose
// last user turn has no terminal stop_reason yet. State() must report in-turn.
func TestParserMidFileAttachInProgress(t *testing.T) {
	tail := [][]byte{
		[]byte(`{"type":"mode","mode":"","sessionId":"S"}`),
		[]byte(`{"type":"user","isSidechain":false,"message":{"role":"user","content":""},"uuid":"u1","timestamp":"2026-09-05T16:45:00.000Z","origin":{"kind":"human"}}`),
		[]byte(`{"type":"assistant","isSidechain":false,"message":{"role":"assistant","id":"m1","content":[{"type":"tool_use"}],"stop_reason":"tool_use"},"uuid":"a1","timestamp":"2026-09-05T16:45:01.000Z"}`),
		[]byte(`{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":""}]},"uuid":"u2","timestamp":"2026-09-05T16:45:02.000Z"}`),
	}
	p := NewParser()
	for _, l := range tail {
		p.Feed(l)
	}
	if got := p.State(); got != StateInTurn {
		t.Fatalf("State() = %v, want %v (turn still open, no terminal stop_reason)", got, StateInTurn)
	}

	// The eventual terminal assistant line closes it.
	p.Feed([]byte(`{"type":"assistant","isSidechain":false,"message":{"role":"assistant","id":"m2","content":[{"type":"text"}],"stop_reason":"end_turn"},"uuid":"a2","timestamp":"2026-09-05T16:45:09.000Z"}`))
	if got := p.State(); got != StateIdle {
		t.Fatalf("State() = %v, want %v after end_turn", got, StateIdle)
	}
}

func TestParserSkipsUnparseableAndUnknown(t *testing.T) {
	p := NewParser()
	for _, l := range []string{
		``,
		`not json`,
		`{"type":`,
		`{"type":"totally-unknown"}`,
		`{"type":"user"`, // truncated
		`[]`,
		`null`,
	} {
		if evs := p.Feed([]byte(l)); evs != nil {
			t.Errorf("Feed(%q) = %v, want nil", l, evs)
		}
	}
	if p.State() != StateIdle {
		t.Errorf("state drifted to %v on junk input", p.State())
	}
}

// Non-terminal stop_reasons keep the turn open; only a terminal one ends it.
func TestParserStopReasons(t *testing.T) {
	nonTerminal := []string{"tool_use", "pause_turn", "", "null"}
	for _, sr := range nonTerminal {
		p := NewParser()
		p.Feed([]byte(`{"type":"user","message":{"role":"user","content":""},"timestamp":"2026-09-05T16:45:00Z","origin":{"kind":"human"}}`))
		p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","id":"m1","content":[],"stop_reason":"` + sr + `"}}`))
		if p.State() != StateInTurn {
			t.Errorf("stop_reason %q ended the turn; want still in-turn", sr)
		}
	}
	for _, sr := range []string{"end_turn", "stop_sequence", "max_tokens", "refusal"} {
		p := NewParser()
		p.Feed([]byte(`{"type":"user","message":{"role":"user","content":""},"timestamp":"2026-09-05T16:45:00Z","origin":{"kind":"human"}}`))
		evs := p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","id":"m1","content":[],"stop_reason":"` + sr + `"}}`))
		if len(evs) != 1 || evs[0].Kind != TurnEnd {
			t.Errorf("stop_reason %q: events = %v, want one TurnEnd", sr, kinds(evs))
		}
		if p.State() != StateIdle {
			t.Errorf("stop_reason %q: state = %v, want idle", sr, p.State())
		}
	}
}

// An assistant terminal line with no preceding TurnStart produces nothing.
func TestParserIdleAssistantIgnored(t *testing.T) {
	p := NewParser()
	evs := p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","id":"m1","content":[],"stop_reason":"end_turn"}}`))
	if evs != nil {
		t.Errorf("idle assistant produced %v, want nil", kinds(evs))
	}
	if p.State() != StateIdle {
		t.Errorf("state = %v, want idle", p.State())
	}
}

// An isMeta user line while idle is not a turn-start.
func TestParserIdleMetaUserIgnored(t *testing.T) {
	p := NewParser()
	evs := p.Feed([]byte(`{"type":"user","isMeta":true,"turnCompanion":true,"message":{"role":"user","content":[{"type":"text"}]},"uuid":"um1","timestamp":"2026-09-05T16:45:00.000Z"}`))
	if evs != nil {
		t.Errorf("idle meta user produced %v, want nil", kinds(evs))
	}
	if p.State() != StateIdle {
		t.Errorf("state = %v, want idle", p.State())
	}
}

// Mid-turn, an isSidechain assistant end_turn and an isMeta assistant end_turn
// are both ignored: the turn stays open until a real terminal line.
func TestParserSidechainAndMetaAssistantMidTurn(t *testing.T) {
	p := NewParser()
	if evs := p.Feed([]byte(`{"type":"user","message":{"role":"user","content":""},"timestamp":"2026-09-05T16:45:00Z","origin":{"kind":"human"}}`)); len(evs) != 1 || evs[0].Kind != TurnStart {
		t.Fatalf("prompt: events = %v, want one TurnStart", kinds(evs))
	}
	if evs := p.Feed([]byte(`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","id":"sc1","content":[],"stop_reason":"end_turn"}}`)); evs != nil {
		t.Errorf("sidechain assistant end_turn produced %v, want nil", kinds(evs))
	}
	if p.State() != StateInTurn {
		t.Fatalf("state = %v after sidechain line, want in-turn", p.State())
	}
	if evs := p.Feed([]byte(`{"type":"assistant","isMeta":true,"message":{"role":"assistant","id":"mt1","content":[],"stop_reason":"end_turn"}}`)); evs != nil {
		t.Errorf("meta assistant end_turn produced %v, want nil", kinds(evs))
	}
	if p.State() != StateInTurn {
		t.Fatalf("state = %v after meta line, want in-turn", p.State())
	}
	if evs := p.Feed([]byte(`{"type":"assistant","message":{"role":"assistant","id":"m1","content":[],"stop_reason":"end_turn"}}`)); len(evs) != 1 || evs[0].Kind != TurnEnd {
		t.Errorf("real assistant end_turn: events = %v, want one TurnEnd", kinds(evs))
	}
	if p.State() != StateIdle {
		t.Errorf("state = %v, want idle", p.State())
	}
}

// parseAt yields the zero time for an absent or unparseable timestamp.
func TestParserZeroTimestamp(t *testing.T) {
	p := NewParser()
	evs := p.Feed([]byte(`{"type":"user","message":{"role":"user","content":""},"origin":{"kind":"human"}}`))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want one", kinds(evs))
	}
	if !evs[0].At.IsZero() {
		t.Errorf("At = %v, want zero time for a line with no timestamp", evs[0].At)
	}

	p2 := NewParser()
	evs = p2.Feed([]byte(`{"type":"user","message":{"role":"user","content":""},"timestamp":"not-a-timestamp","origin":{"kind":"human"}}`))
	if len(evs) != 1 || !evs[0].At.IsZero() {
		t.Errorf("At = %v, want zero time for a garbage timestamp", evs[0].At)
	}
}

func TestEventKindAndStateStrings(t *testing.T) {
	if TurnStart.String() != "TurnStart" || TurnEnd.String() != "TurnEnd" {
		t.Errorf("EventKind.String mismatch: %s / %s", TurnStart, TurnEnd)
	}
	if StateIdle.String() != "idle" || StateInTurn.String() != "in-turn" {
		t.Errorf("State.String mismatch: %s / %s", StateIdle, StateInTurn)
	}
}
