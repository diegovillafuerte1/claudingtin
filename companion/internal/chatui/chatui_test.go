package chatui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/diegovillafuerte1/claudingtin/proto"
)

// --- helpers ---------------------------------------------------------------

func viewOf(m *Model) string { return m.View().Content }

// assertNoTerminalControls fails if s carries any byte that could drive the
// terminal emulator: ESC, BEL, other C0 (bar '\n'/'\t' which the history never
// emits anyway), DEL, or a C1 code point.
func assertNoTerminalControls(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s carries control rune %#U:\n%q", what, r, s)
		}
	}
}

func step(t *testing.T, m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	mm, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	return mm, cmd
}

func typeString(t *testing.T, m *Model, s string) *Model {
	t.Helper()
	for _, r := range s {
		m, _ = step(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func pressEnter(t *testing.T, m *Model) (*Model, tea.Cmd) {
	t.Helper()
	return step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

// --- render: header, opener, four regions ---------------------------------

func TestOpenerIsTheFirstAndOnlyHistoryEntry(t *testing.T) {
	const opener = "what's the last thing that made you laugh?"
	m := New(proto.Matched{Opener: opener})

	if got := len(m.history); got != 1 {
		t.Fatalf("history has %d entries, want 1 (the opener)", got)
	}
	if m.history[0].from != fromOpener || m.history[0].text != opener {
		t.Fatalf("first history entry = %+v, want the opener", m.history[0])
	}
	v := viewOf(m)
	if !strings.Contains(v, opener) {
		t.Fatalf("view does not show the opener:\n%s", v)
	}
	if strings.Count(v, opener) != 1 {
		t.Fatalf("opener appears %d times in the view, want exactly once:\n%s", strings.Count(v, opener), v)
	}
}

func TestHeaderShowsPlaceholderWhenPseudonymBlank(t *testing.T) {
	m := New(proto.Matched{Pseudonym: "", Blurb: "", Opener: "hi there friend"})
	v := viewOf(m)
	if !strings.Contains(v, placeholderName) {
		t.Fatalf("blank pseudonym did not render the placeholder name %q:\n%s", placeholderName, v)
	}
	// No blurb line: the header is a single line.
	if got := m.headerHeight(); got != 1 {
		t.Fatalf("header height = %d with no blurb, want 1", got)
	}
}

func TestHeaderShowsPseudonymAndBlurb(t *testing.T) {
	m := New(proto.Matched{Pseudonym: "kestrel", Blurb: "collects maps", Opener: "hello there"})
	v := viewOf(m)
	if !strings.Contains(v, "kestrel") || !strings.Contains(v, "collects maps") {
		t.Fatalf("header missing pseudonym or blurb:\n%s", v)
	}
	if got := m.headerHeight(); got != 2 {
		t.Fatalf("header height = %d with a blurb, want 2", got)
	}
}

func TestHeaderBlurbRendersInert(t *testing.T) {
	m := New(proto.Matched{Pseudonym: "x", Blurb: "\x1b[31mred\x1b[0m blurb\x07", Opener: "hello there"})

	assertNoTerminalControls(t, "header text", m.headerText())

	v := viewOf(m)
	if strings.Contains(v, "\x1b[31m") || strings.Contains(v, "\x1b[0m") || strings.ContainsRune(v, 0x07) {
		t.Fatalf("a peer escape sequence survived into the view:\n%q", v)
	}
	if !strings.Contains(v, "red") || !strings.Contains(v, "blurb") {
		t.Fatalf("inert blurb text missing:\n%s", v)
	}
}

func TestAllFourRegionsPresentAndSurviveResize(t *testing.T) {
	m := New(proto.Matched{Opener: "opener line here"})

	for _, sz := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 40, Height: 10}, {Width: 132, Height: 60}} {
		m, _ = step(t, m, sz)
		v := viewOf(m)
		// header
		if !strings.Contains(v, placeholderName) {
			t.Fatalf("size %v: header missing:\n%s", sz, v)
		}
		// history (the opener)
		if !strings.Contains(v, "opener line here") {
			t.Fatalf("size %v: history/opener missing:\n%s", sz, v)
		}
		// input box
		if strings.TrimSpace(m.input.View()) == "" || !strings.Contains(v, strings.SplitN(m.input.View(), "\n", 2)[0]) {
			t.Fatalf("size %v: input region missing:\n%s", sz, v)
		}
		// controls
		for _, want := range []string{"block", "report", "my claude's back"} {
			if !strings.Contains(v, want) {
				t.Fatalf("size %v: controls missing %q:\n%s", sz, want, v)
			}
		}
	}
}

func TestControlsVisibleBeforeAnyResize(t *testing.T) {
	m := New(proto.Matched{Opener: "hello there"})
	v := viewOf(m)
	for _, want := range []string{"send", "block", "report", "my claude's back"} {
		if !strings.Contains(v, want) {
			t.Fatalf("controls bar missing %q before any resize:\n%s", want, v)
		}
	}
}

// --- peer text: inert, multi-line ---------------------------------------

func TestPeerEntryRendersInert(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m, _ = step(t, m, PeerMsg{
		ClientMsgID: "peer-1",
		Text:        "\x1b[31mhi\x1b[0m \x07\x1b]8;;http://x\x07link",
	})

	// The history region is the inert one: not a single control byte.
	assertNoTerminalControls(t, "rendered history", m.renderHistory())

	v := viewOf(m)
	if strings.ContainsRune(v, 0x07) {
		t.Fatalf("BEL reached the view:\n%q", v)
	}
	for _, seq := range []string{"\x1b[31m", "\x1b[0m", "\x1b]8;;"} {
		if strings.Contains(v, seq) {
			t.Fatalf("a peer escape sequence %q survived into the view:\n%q", seq, v)
		}
	}
	if !strings.Contains(v, "hi") || !strings.Contains(v, "link") {
		t.Fatalf("inert peer text missing its literal characters:\n%s", v)
	}
}

func TestMultiLinePeerEntry(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m, _ = step(t, m, PeerMsg{ClientMsgID: "p", Text: "line one\nline two"})
	v := viewOf(m)
	if !strings.Contains(v, "line one") || !strings.Contains(v, "line two") {
		t.Fatalf("multi-line peer entry did not render both lines:\n%s", v)
	}
}

// --- input cap ---------------------------------------------------------

func TestInputHardStopsAtCap(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	// Bulk to just under the cap, then hammer keystrokes past it.
	m, _ = step(t, m, tea.PasteMsg{Content: strings.Repeat("a", maxInputRunes-5)})
	for i := 0; i < 50; i++ {
		m, _ = step(t, m, tea.KeyPressMsg{Code: 'b', Text: "b"})
	}
	if n := utf8.RuneCountInString(m.input.Value()); n != maxInputRunes {
		t.Fatalf("input length = %d, want it clamped to %d", n, maxInputRunes)
	}
}

func TestPasteTruncatedToCap(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m, _ = step(t, m, tea.PasteMsg{Content: strings.Repeat("x", 5000)})
	if n := utf8.RuneCountInString(m.input.Value()); n != maxInputRunes {
		t.Fatalf("pasted input length = %d, want truncated to %d", n, maxInputRunes)
	}
}

func TestCapConstantIs2000(t *testing.T) {
	if maxInputRunes != 2000 {
		t.Fatalf("maxInputRunes = %d, want 2000", maxInputRunes)
	}
}

// --- send: optimistic echo, client_msg_id, nothing on the wire --------

func TestEnterAppendsOwnLineClearsInputSendsNothing(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m = typeString(t, m, "hey")

	m, cmd := pressEnter(t, m)

	last := m.history[len(m.history)-1]
	if last.from != fromSelf || last.text != "hey" {
		t.Fatalf("last history entry = %+v, want the user's own %q line", last, "hey")
	}
	if last.id == "" {
		t.Fatalf("optimistic entry has no client_msg_id")
	}
	if m.input.Value() != "" {
		t.Fatalf("input not cleared after send: %q", m.input.Value())
	}
	if !strings.Contains(viewOf(m), "hey") {
		t.Fatalf("sent line not shown in the view:\n%s", viewOf(m))
	}
	// Story 2.3 puts nothing on the wire: send raises no command at all.
	if cmd != nil {
		t.Fatalf("send returned a command %T, want nil (no relay in Story 2.3)", cmd())
	}
}

// --- send: WithSend relay callback (Story 2.4) -----------------------------

func TestEnterFiresWithSendOnceWithFreshIDAndTrimmedText(t *testing.T) {
	type call struct{ id, text string }
	var calls []call
	m := New(proto.Matched{Opener: "hi there"},
		WithSend(func(id, text string) { calls = append(calls, call{id, text}) }))

	m = typeString(t, m, "  hey there  ")
	m, _ = pressEnter(t, m)

	if len(calls) != 1 {
		t.Fatalf("WithSend fired %d times, want exactly 1", len(calls))
	}
	if calls[0].text != "hey there" {
		t.Fatalf("WithSend text = %q, want the trimmed %q", calls[0].text, "hey there")
	}
	last := m.history[len(m.history)-1]
	if last.from != fromSelf || last.text != "hey there" {
		t.Fatalf("optimistic self entry = %+v, want the trimmed own line", last)
	}
	if calls[0].id == "" || calls[0].id != last.id {
		t.Fatalf("WithSend id = %q, want the fresh id keyed on the self entry (%q)", calls[0].id, last.id)
	}

	// A second send fires it again with a different id.
	m = typeString(t, m, "again")
	m, _ = pressEnter(t, m)
	if len(calls) != 2 {
		t.Fatalf("second send fired WithSend %d times total, want 2", len(calls))
	}
	if calls[1].id == calls[0].id || calls[1].text != "again" {
		t.Fatalf("second call = %+v, want a fresh id and %q", calls[1], "again")
	}
}

func TestWhitespaceOnlyEnterDoesNotFireWithSend(t *testing.T) {
	fired := 0
	m := New(proto.Matched{Opener: "hi there"},
		WithSend(func(id, text string) { fired++ }))

	m = typeString(t, m, "   ")
	m, _ = pressEnter(t, m)
	m, _ = pressEnter(t, m) // bare Enter, empty box

	if fired != 0 {
		t.Fatalf("WithSend fired %d times on whitespace-only / empty Enter, want 0", fired)
	}
}

func TestPeerMsgStillAppendsThemEntryWithSendWired(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"}, WithSend(func(string, string) {}))
	m, _ = step(t, m, PeerMsg{ClientMsgID: "peer-9", Text: "hello from them"})

	last := m.history[len(m.history)-1]
	if last.from != fromPeer || last.text != "hello from them" || last.id != "peer-9" {
		t.Fatalf("last history entry = %+v, want the peer line keyed by peer-9", last)
	}
	if !strings.Contains(viewOf(m), "hello from them") {
		t.Fatalf("peer line not shown in the view:\n%s", viewOf(m))
	}
}

func TestEmptyEnterIsANoop(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	before := len(m.history)
	m, _ = pressEnter(t, m)
	m = typeString(t, m, "   ")
	m, _ = pressEnter(t, m)
	if len(m.history) != before {
		t.Fatalf("blank/whitespace Enter appended an entry: %d -> %d", before, len(m.history))
	}
	// A whitespace-only send still clears the box (matches the normal path).
	if m.input.Value() != "" {
		t.Fatalf("whitespace-only Enter left %q in the input box", m.input.Value())
	}
}

func TestClientMsgIDFreshPerSend(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m = typeString(t, m, "one")
	m, _ = pressEnter(t, m)
	m = typeString(t, m, "two")
	m, _ = pressEnter(t, m)

	var ids []string
	for _, e := range m.history {
		if e.from == fromSelf {
			ids = append(ids, e.id)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 self entries, got %d", len(ids))
	}
	if ids[0] == ids[1] || ids[0] == "" || ids[1] == "" {
		t.Fatalf("client_msg_ids not fresh/unique: %q", ids)
	}
}

// --- no attachment affordance, no read receipts -----------------------

func TestNoAttachmentAffordanceAnywhere(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})

	bindings := map[string]key.Binding{
		"send": m.keys.send, "block": m.keys.block, "report": m.keys.report, "leave": m.keys.leave,
	}
	banned := []string{"file", "attach", "image", "photo", "audio", "video", "upload", "media"}
	for name, b := range bindings {
		h := strings.ToLower(b.Help().Key + " " + b.Help().Desc)
		for _, bad := range banned {
			if strings.Contains(h, bad) {
				t.Fatalf("binding %q help mentions %q: %q", name, bad, h)
			}
		}
	}
	v := strings.ToLower(viewOf(m))
	for _, bad := range banned {
		if strings.Contains(v, bad) {
			t.Fatalf("view mentions an attachment affordance %q:\n%s", bad, v)
		}
	}
}

func TestNoReadReceiptState(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m = typeString(t, m, "hey")
	m, _ = pressEnter(t, m)
	m, _ = step(t, m, PeerMsg{ClientMsgID: "p", Text: "hello back"})

	v := strings.ToLower(viewOf(m))
	for _, bad := range []string{"seen", "delivered", "read receipt", "· read", "✓✓"} {
		if strings.Contains(v, bad) {
			t.Fatalf("view renders read-receipt state %q:\n%s", bad, v)
		}
	}
}

// --- block / report / leave intents ---------------------------------

func TestLeaveRaisesIntentAndQuits(t *testing.T) {
	var got []Intent
	m := New(proto.Matched{Opener: "hi there"}, WithNotify(func(i Intent) { got = append(got, i) }))

	_, cmd := step(t, m, ctrl('q'))

	if len(got) != 1 || got[0] != IntentLeave {
		t.Fatalf("leave key raised intents %v, want [leave]", got)
	}
	if cmd == nil {
		t.Fatal("leave returned no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("leave command produced %T, want tea.QuitMsg", cmd())
	}
}

func TestBlockAndReportRaiseIntentWithoutQuitting(t *testing.T) {
	var got []Intent
	m := New(proto.Matched{Opener: "hi there"}, WithNotify(func(i Intent) { got = append(got, i) }))

	m, cmd := step(t, m, ctrl('b'))
	if cmd != nil {
		t.Fatalf("block returned a command %T, want nil", cmd())
	}
	m, cmd = step(t, m, ctrl('r'))
	if cmd != nil {
		t.Fatalf("report returned a command %T, want nil", cmd())
	}
	_ = m

	if len(got) != 2 || got[0] != IntentBlock || got[1] != IntentReport {
		t.Fatalf("intents = %v, want [block report]", got)
	}
}

func TestIntentKeysNeverReachTheInputBox(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"}, WithNotify(func(Intent) {}))
	for _, k := range []tea.KeyPressMsg{ctrl('b'), ctrl('r')} {
		m, _ = step(t, m, k)
	}
	if m.input.Value() != "" {
		t.Fatalf("an intent keystroke leaked into the input box: %q", m.input.Value())
	}
}

// --- layout hardening -------------------------------------------------------

func TestOpenerOmittedWhenEmpty(t *testing.T) {
	m := New(proto.Matched{Opener: ""})
	if len(m.history) != 0 {
		t.Fatalf("empty opener seeded %d history entries, want 0: %+v", len(m.history), m.history)
	}
	v := viewOf(m)
	for _, want := range []string{placeholderName, "block", "report"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q with an empty opener:\n%s", want, v)
		}
	}
}

func TestPeerPseudonymNewlinesDoNotInflateHeader(t *testing.T) {
	m := New(proto.Matched{Pseudonym: "a\nb\nc", Blurb: "d\r\ne", Opener: "hi"})
	if strings.ContainsAny(m.pseudonym, "\r\n") || strings.ContainsAny(m.blurb, "\r\n") {
		t.Fatalf("pseudonym/blurb kept a line break: %q / %q", m.pseudonym, m.blurb)
	}
	if got := m.headerHeight(); got != 2 { // one name line + one blurb line
		t.Fatalf("header height = %d, want 2 (peer newlines must not add rows)", got)
	}
}

func TestControlsBarHeightIsCountedInLayout(t *testing.T) {
	m := New(proto.Matched{Opener: "hi there"})
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 30, Height: 20})

	if m.controlsHeight() < 2 {
		t.Fatalf("controls bar height at width 30 = %d, want it to have wrapped to >=2", m.controlsHeight())
	}
	// With the wrapped height budgeted, the whole surface fits the pane; the
	// old hard-coded "1 row" would overflow.
	if h := strings.Count(m.render(), "\n") + 1; h > m.height {
		t.Fatalf("rendered surface is %d rows, exceeds the %d-row pane", h, m.height)
	}
}

// --- history scrolling ----------------------------------------------------

// fillHistory drives enough peer lines through the model to overflow the
// viewport, leaving it pinned at the bottom.
func fillHistory(t *testing.T, m *Model, n int) *Model {
	t.Helper()
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})
	for i := 0; i < n; i++ {
		m, _ = step(t, m, PeerMsg{ClientMsgID: "p", Text: "history line"})
	}
	return m
}

func TestHistoryScrollsWithPageKeys(t *testing.T) {
	m := New(proto.Matched{Opener: "the opener"})
	m = fillHistory(t, m, 60)

	if !m.vp.AtBottom() {
		t.Fatal("precondition: viewport should start pinned to the bottom")
	}
	y0 := m.vp.YOffset()

	m, _ = step(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})

	if m.vp.YOffset() >= y0 {
		t.Fatalf("page-up did not scroll the history: YOffset %d -> %d", y0, m.vp.YOffset())
	}
	if m.vp.AtBottom() {
		t.Fatal("page-up left the viewport at the bottom")
	}
}

func TestPageKeyDoesNotTypeIntoTheInput(t *testing.T) {
	m := New(proto.Matched{Opener: "the opener"})
	m = fillHistory(t, m, 60)
	m, _ = step(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.input.Value() != "" {
		t.Fatalf("a page-up keystroke leaked into the input box: %q", m.input.Value())
	}
}

func TestPeerMessageDoesNotForceScrollWhenReaderScrolledUp(t *testing.T) {
	m := New(proto.Matched{Opener: "the opener"})
	m = fillHistory(t, m, 60)

	m.vp.SetYOffset(0) // reader scrolled to the very top
	if m.vp.AtBottom() {
		t.Skip("viewport not scrollable at this size; nothing to assert")
	}

	m, _ = step(t, m, PeerMsg{ClientMsgID: "p", Text: "a new line from them"})

	if m.vp.YOffset() != 0 {
		t.Fatalf("an incoming peer line yanked the reader off the top: YOffset now %d", m.vp.YOffset())
	}
}

func TestPeerMessageSticksToBottomWhenAlreadyThere(t *testing.T) {
	m := New(proto.Matched{Opener: "the opener"})
	m = fillHistory(t, m, 60)

	if !m.vp.AtBottom() {
		t.Fatal("precondition: viewport should be at the bottom")
	}
	m, _ = step(t, m, PeerMsg{ClientMsgID: "p", Text: "another line"})
	if !m.vp.AtBottom() {
		t.Fatal("a peer line did not keep a bottom-anchored reader at the bottom")
	}
}
