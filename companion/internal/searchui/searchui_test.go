package searchui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// assertNoTerminalControls fails if s carries any byte that could drive the
// terminal emulator: ESC, BEL, other C0 (bar '\n'/'\t'), DEL, or a C1 code
// point. Mirrors chatui_test.assertNoTerminalControls.
func assertNoTerminalControls(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s carries control rune %#U:\n%q", what, r, s)
		}
	}
}

func viewOf(m *Model) string { return m.View().Content }

func step(t *testing.T, m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	mm, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	return mm, cmd
}

func TestInitReturnsATickCommand(t *testing.T) {
	m := New()
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil, want the spinner tick command")
	}
	if _, ok := cmd().(spinner.TickMsg); !ok {
		t.Fatalf("Init command produced %T, want spinner.TickMsg", cmd())
	}
}

func TestTickAdvancesFrameAndReArms(t *testing.T) {
	m := New()
	before := viewOf(m)

	m, cmd := step(t, m, spinner.TickMsg{})
	if cmd == nil {
		t.Fatal("a spinner tick did not re-arm the animation (nil command)")
	}
	if _, ok := cmd().(spinner.TickMsg); !ok {
		t.Fatalf("re-arm command produced %T, want spinner.TickMsg", cmd())
	}

	after := viewOf(m)
	if before == after {
		t.Fatalf("a spinner tick did not change the rendered frame:\n%q", after)
	}
}

func TestKeyPressIsInert(t *testing.T) {
	m := New()
	before := viewOf(m)

	m, cmd := step(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		t.Fatalf("a keypress produced a command %T, want nil (keys are inert)", cmd())
	}
	if got := viewOf(m); got != before {
		t.Fatalf("a keypress changed the view:\nbefore %q\nafter  %q", before, got)
	}
}

func TestViewIsOneCalmLineNoCounts(t *testing.T) {
	m := New()
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	v := viewOf(m)

	for _, r := range v {
		if r >= '0' && r <= '9' {
			t.Fatalf("searching view carries a digit %q — no counts allowed:\n%q", r, v)
		}
	}
	for _, bad := range []string{"queue", "ETA", "eta", "online", "position", "waiting in"} {
		if strings.Contains(strings.ToLower(v), strings.ToLower(bad)) {
			t.Fatalf("searching view names the machinery (%q):\n%q", bad, v)
		}
	}
	if !strings.Contains(v, waitingLine) {
		t.Fatalf("searching view is missing the calm line %q:\n%q", waitingLine, v)
	}
}

func TestViewHasNoTerminalControls(t *testing.T) {
	m := New()
	assertNoTerminalControls(t, "searching view (no size)", viewOf(m))

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 48, Height: 12})
	m, _ = step(t, m, spinner.TickMsg{})
	assertNoTerminalControls(t, "searching view (sized, spun)", viewOf(m))
}
