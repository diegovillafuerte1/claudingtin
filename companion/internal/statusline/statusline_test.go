package statusline

import (
	"strings"
	"testing"
)

func TestShowWritesOneLinePerPhase(t *testing.T) {
	cases := []struct {
		name  string
		phase Phase
		want  string
	}{
		{"connecting", PhaseConnecting, "connecting…"},
		{"waiting", PhaseWaiting, "you're back with claude — waiting for the next quiet moment"},
		{"free to chat", PhaseFreeToChat, "claude's thinking — you're free to chat"},
		{"reconnecting", PhaseReconnecting, "lost the thread for a moment — picking it back up"},
		{"update needed", PhaseUpdateNeeded, "this companion is out of date — grab the latest build to keep going"},
		{"inert", PhaseInert, "nothing's connected — you can turn this on whenever you like"},
		{"claude back", PhaseClaudeBack, "looks like their claude's back — catch you later"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			// A fresh renderer per case: the first Show always writes.
			New(&b).Show(tc.phase)
			got := strings.TrimRight(b.String(), "\n")
			if got != tc.want {
				t.Fatalf("Show(%v) wrote %q, want %q", tc.phase, got, tc.want)
			}
		})
	}
}

func TestRepeatedPhaseIsANoOp(t *testing.T) {
	var b strings.Builder
	r := New(&b)

	r.Show(PhaseWaiting)
	first := b.String()
	if first == "" {
		t.Fatal("first Show wrote nothing")
	}

	r.Show(PhaseWaiting)
	r.Show(PhaseWaiting)
	if b.String() != first {
		t.Fatalf("repeated Show wrote again: %q", b.String())
	}

	// A different phase writes; going back writes again too.
	r.Show(PhaseFreeToChat)
	r.Show(PhaseWaiting)
	if got := strings.Count(b.String(), "\n"); got != 3 {
		t.Fatalf("expected 3 lines after waiting→free→waiting, got %d:\n%s", got, b.String())
	}
}

func TestInertPhaseRepeatIsANoOp(t *testing.T) {
	var b strings.Builder
	r := New(&b)

	r.Show(PhaseInert)
	first := b.String()
	if !strings.Contains(first, "whenever you like") {
		t.Fatalf("first Show(PhaseInert) wrote %q", first)
	}

	r.Show(PhaseInert)
	r.Show(PhaseInert)
	if b.String() != first {
		t.Fatalf("repeated Show(PhaseInert) wrote again: %q", b.String())
	}
}

func TestFirstShowAlwaysWritesEvenForZeroPhase(t *testing.T) {
	var b strings.Builder
	// PhaseConnecting is the zero value; the first call must still write.
	New(&b).Show(PhaseConnecting)
	if !strings.Contains(b.String(), "connecting") {
		t.Fatalf("first Show(PhaseConnecting) wrote %q", b.String())
	}
}

// TestNoAlarmLanguage: no phase line — least of all an end line — carries
// rejection or alarm vocabulary. The companion's whole exit story is "their
// Claude came back", never "you were left / disconnected / blocked" (Story 3.2,
// voice.md "Exit and disconnect copy").
func TestNoAlarmLanguage(t *testing.T) {
	banned := []string{
		"left", "disconnect", "connection lost",
		"rejected", "blocked", "reported", "are you sure",
	}
	// Every defined phase, plus the fallback.
	for p := PhaseConnecting; p <= PhaseClaudeBack; p++ {
		got := strings.ToLower(line(p))
		for _, bad := range banned {
			if strings.Contains(got, bad) {
				t.Errorf("line(%v) = %q contains banned copy %q", p, got, bad)
			}
		}
	}
}

func TestUnknownPhaseDoesNotPanic(t *testing.T) {
	var b strings.Builder
	New(&b).Show(Phase(999))
	if b.String() == "" {
		t.Fatal("expected a fallback line for an unknown phase")
	}
}
