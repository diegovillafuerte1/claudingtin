// Package chatui is the companion's text chat surface: the Bubble Tea v2 model,
// update, and view that run launches on a `matched` frame and tears down when
// the session ends. It renders a peer header, a scrollable history seeded with
// the backend's opener, a length-capped input box, and an always-visible
// block / report / leave affordance. Every peer- or opener-supplied string is
// rendered through inert (this package's terminal-safe literal renderer).
//
// Scope note (Story 2.4): the relay is now wired. Enter still appends the user's
// own line to the history optimistically, keyed by a fresh client_msg_id, and
// then hands that id + trimmed text to the WithSend callback so run.loop can put
// one chat_msg on the wire (the Bubble Tea goroutine never writes the socket
// itself). Inbound peer lines arrive as PeerMsg. The server never echoes a
// chat_msg back to its sender, so there is no de-dup to do here. block / report
// / leave raise a typed Intent for run to log content-free. There is no file /
// image / audio affordance anywhere in the surface or its key map, and no
// read-receipt state is ever rendered.
//
// Scope note (Story 2.5): the *searching* spinner is a separate package
// (internal/searchui). The "spin" that belongs here is the intro flourish — one
// fixed row above the header that advances on a fixed tick, a fixed number of
// times (well under ~1s total), then clears for good with a one-time viewport
// growth. It lives entirely in the render path: it never gates key routing (the
// textarea is focused from New and Update always routes a KeyPressMsg to it) and
// it never touches message handling (a PeerMsg mid-flourish is appended
// normally). It is bounded by a frame count, not a wall clock, so a test drives
// it deterministically by feeding flourishTickMsg.
package chatui

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/diegovillafuerte1/claudingtin/proto"
)

// maxInputRunes is the client-side input cap. Keystrokes past it are refused
// (no bell); a paste is truncated to it. The authoritative cap is a backend
// concern in Epic 4 — this is a UX affordance only.
const maxInputRunes = 2000

// inputHeight is the fixed visible height of the input box, in rows.
const inputHeight = 3

// Fallback pane size used until the first tea.WindowSizeMsg arrives, so the
// surface renders sensibly on the very first frame.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// placeholderName stands in for a peer pseudonym until Epic 5 populates the
// real profile. Warm, lowercase, never names the machinery.
const placeholderName = "someone new"

// The intro "spin": a fixed row shown above the header for the first few frames
// after the surface opens, then gone for good. Bounded by the frame count, not
// a wall clock — flourishInterval * len(flourishFrames) is well under a second,
// and a test advances it deterministically by feeding flourishTickMsg.
const flourishInterval = 90 * time.Millisecond

var flourishFrames = []string{
	"◐  here we go",
	"◓  here we go",
	"◑  here we go",
	"◒  here we go",
	"◐  here we go",
	"◓  here we go",
}

// flourishTickMsg advances the intro flourish by one frame. It is scheduled only
// while the flourish is still playing and never again once it is done.
type flourishTickMsg struct{}

// Intent is a content-free signal that the user reached for block, report, or
// leave. Story 2.3 only surfaces it to run, which logs it; no proto frame is
// sent and no backend behaviour is wired until Epics 3–4.
type Intent int

const (
	// IntentBlock: the user pressed the block key.
	IntentBlock Intent = iota
	// IntentReport: the user pressed the report key.
	IntentReport
	// IntentLeave: the user pressed the leave key ("my Claude came back").
	IntentLeave
)

// String names the intent for a content-free log line.
func (i Intent) String() string {
	switch i {
	case IntentBlock:
		return "block"
	case IntentReport:
		return "report"
	case IntentLeave:
		return "leave"
	default:
		return "unknown"
	}
}

// PeerMsg appends a peer line to the history. Story 2.3 never emits one (the
// relay is Story 2.4); Update handles it so 2.4 composes on without reshaping
// the model.
type PeerMsg struct {
	ClientMsgID string
	Text        string
}

// OutboundMsg is the user's own line as it should go on the wire: the fresh
// client_msg_id the optimistic echo was keyed by, and the trimmed text. It is
// symmetric with PeerMsg. chatui does not send it — WithSend hands it to run,
// which owns the socket write.
type OutboundMsg struct {
	ClientMsgID string
	Text        string
}

// LogLine asks the surface to print one content-free operator line above the
// live frame, via tea.Println, coordinated with the renderer. run.loop delivers
// it with *tea.Program.Send — which is documented as a no-op once the program
// has exited, so unlike a direct *tea.Program.Println call it can never wedge
// run.loop during teardown (Epic 2 retrospective finding F6: a buffered
// block/report/leave intent processed after Run returned used to block the loop
// forever on Println).
type LogLine struct{ Text string }

// Option configures a Model at construction.
type Option func(*Model)

// WithNotify registers a callback invoked (synchronously, from Update) when the
// user reaches for block / report / leave. run passes one so it can log the
// intent content-free and, on leave, tear the surface down.
func WithNotify(fn func(Intent)) Option {
	return func(m *Model) { m.notify = fn }
}

// WithSend registers a callback invoked (synchronously, from Update) right after
// the optimistic self line is appended on Enter, carrying that line's fresh
// client_msg_id and its trimmed text. run passes one so run.loop — never this
// Bubble Tea goroutine — does the actual socket write. A whitespace-only Enter
// appends nothing and does not call it.
func WithSend(fn func(clientMsgID, text string)) Option {
	return func(m *Model) { m.send = fn }
}

type speaker int

const (
	fromOpener speaker = iota
	fromPeer
	fromSelf
)

type entry struct {
	id   string
	from speaker
	text string
}

type keymap struct {
	send   key.Binding
	block  key.Binding
	report key.Binding
	leave  key.Binding
}

func defaultKeymap() keymap {
	return keymap{
		send:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send")),
		block:  key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "block")),
		report: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "report")),
		// esc is the advertised leave key; ctrl+q is kept as an alias but a
		// terminal with legacy flow control (ixon) swallows it before the
		// program sees it, so it cannot be the only binding (Epic 2 retro F9).
		leave: key.NewBinding(key.WithKeys("esc", "ctrl+q"), key.WithHelp("esc", "my claude's back")),
	}
}

// historyKeyMap is the viewport's key map while the input box has focus: only
// non-text keys scroll the history, so typing an "f", "b", "u", "d", "j", "k",
// or space still goes to the input box (the viewport's DefaultKeyMap binds all
// of those). Mouse-wheel scrolling is handled separately and stays on.
func historyKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
	}
}

// singleLine collapses CR/LF to spaces so a peer-controlled pseudonym or blurb
// cannot add rows to the header and shrink the history viewport.
func singleLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

// Model is the chat surface. Construct it with New and hand it to tea.NewProgram.
type Model struct {
	pseudonym string
	blurb     string

	history []entry
	vp      viewport.Model
	input   textarea.Model
	keys    keymap

	notify func(Intent)
	send   func(clientMsgID, text string)

	// flourishFrame is the current intro-flourish frame; flourishDone latches
	// true once the fixed frame count is reached and the row is gone for good.
	flourishFrame int
	flourishDone  bool

	width  int
	height int
}

// New builds the surface from a matched payload, seeding the history with the
// opener as its first entry (unless the payload carries none).
func New(matched proto.Matched, opts ...Option) *Model {
	ta := textarea.New()
	ta.CharLimit = maxInputRunes
	ta.ShowLineNumbers = false
	ta.Prompt = "> "
	ta.Placeholder = "say something"
	ta.SetHeight(inputHeight)
	ta.Focus()

	vp := viewport.New()
	vp.SoftWrap = true
	vp.KeyMap = historyKeyMap()

	var history []entry
	if matched.Opener != "" {
		history = append(history, entry{from: fromOpener, text: matched.Opener})
	}

	m := &Model{
		pseudonym: singleLine(matched.Pseudonym),
		blurb:     singleLine(matched.Blurb),
		history:   history,
		vp:        vp,
		input:     ta,
		keys:      defaultKeymap(),
		width:     defaultWidth,
		height:    defaultHeight,
	}
	for _, o := range opts {
		o(m)
	}
	m.relayout()
	return m
}

// Init satisfies tea.Model; it starts the input cursor blinking and kicks off
// the bounded intro "spin" flourish.
func (m *Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.flourishTick()) }

// flourishTick schedules the next intro-flourish frame.
func (m *Model) flourishTick() tea.Cmd {
	return tea.Tick(flourishInterval, func(time.Time) tea.Msg { return flourishTickMsg{} })
}

// Update satisfies tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.leave):
			if m.notify != nil {
				m.notify(IntentLeave)
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.block):
			if m.notify != nil {
				m.notify(IntentBlock)
			}
			return m, nil
		case key.Matches(msg, m.keys.report):
			if m.notify != nil {
				m.notify(IntentReport)
			}
			return m, nil
		case key.Matches(msg, m.keys.send):
			m.appendSelf()
			return m, nil
		case m.isHistoryScrollKey(msg):
			// pgup / pgdn / ctrl+u / ctrl+d scroll the history; everything else
			// is text for the input box.
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case LogLine:
		// A content-free operator breadcrumb from run.loop. tea.Println prints it
		// above the live frame and is a no-op if the program is already quitting,
		// so it is safe from any run.loop arm (F6).
		return m, tea.Println(msg.Text)

	case PeerMsg:
		// Follow the newest line only if the reader was already at the bottom;
		// don't yank them down from wherever they had scrolled. The flourish
		// state has no bearing here — a peer line mid-flourish is appended
		// normally and is still in history once the flourish clears.
		atBottom := m.vp.AtBottom()
		m.history = append(m.history, entry{
			id:   msg.ClientMsgID,
			from: fromPeer,
			text: msg.Text,
		})
		m.syncHistory(atBottom)
		return m, nil

	case flourishTickMsg:
		// Purely cosmetic: advance the intro row, and re-arm until the fixed
		// frame count is spent. It never gates key routing or message handling.
		if m.flourishDone {
			return m, nil
		}
		m.flourishFrame++
		if m.flourishFrame < len(flourishFrames) {
			return m, m.flourishTick()
		}
		// Done for good: the row is dropped and the viewport grows by that one
		// row, once. No further flourish ticks are scheduled.
		m.flourishDone = true
		m.relayout()
		return m, nil
	}

	// Paste, cursor-blink ticks, mouse wheel — hand to both sub-components so the
	// input keeps working and the history stays scrollable.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// isHistoryScrollKey reports whether msg is one of the non-text keys bound to
// scroll the history viewport (see historyKeyMap).
func (m *Model) isHistoryScrollKey(msg tea.KeyPressMsg) bool {
	return key.Matches(msg, m.vp.KeyMap.PageUp, m.vp.KeyMap.PageDown,
		m.vp.KeyMap.HalfPageUp, m.vp.KeyMap.HalfPageDown)
}

// View satisfies tea.Model. MouseModeCellMotion is set so the history viewport
// receives wheel events.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// appendSelf optimistically appends the current input as the user's own line,
// keyed by a fresh client_msg_id, clears the input, then hands the same id and
// text to the WithSend callback (if any) so run.loop can put one chat_msg on the
// wire. A whitespace-only Enter appends nothing and sends nothing.
func (m *Model) appendSelf() {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		// A whitespace-only send still clears the box, matching the normal path.
		m.input.Reset()
		return
	}
	id := newClientMsgID()
	m.history = append(m.history, entry{
		id:   id,
		from: fromSelf,
		text: text,
	})
	m.input.Reset()
	m.syncHistory(true)
	if m.send != nil {
		m.send(id, text)
	}
}

// relayout re-flows every region to the current pane size, keeping the history
// pinned to the bottom if it already was.
func (m *Model) relayout() {
	if m.width <= 0 {
		return
	}
	m.input.SetWidth(m.width)

	vpH := m.height - m.headerHeight() - m.controlsHeight() - inputHeight - m.flourishHeight()
	if vpH < 1 {
		vpH = 1
	}
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpH)
	m.syncHistory(m.vp.AtBottom())
}

// syncHistory re-renders the history into the viewport. When stick is true the
// view is pinned to the newest line.
func (m *Model) syncHistory(stick bool) {
	m.vp.SetContent(m.renderHistory())
	if stick {
		m.vp.GotoBottom()
	}
}

func (m *Model) renderHistory() string {
	lines := make([]string, 0, len(m.history))
	for _, e := range m.history {
		switch e.from {
		case fromSelf:
			lines = append(lines, "you  "+inert(e.text))
		case fromPeer:
			lines = append(lines, "them  "+inert(e.text))
		default: // fromOpener
			lines = append(lines, inert(e.text))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) headerText() string {
	name := strings.TrimSpace(inert(m.pseudonym))
	if name == "" {
		name = placeholderName
	}
	h := "you're chatting with " + name
	if b := strings.TrimSpace(inert(m.blurb)); b != "" {
		h += "\n" + b
	}
	return h
}

func (m *Model) headerHeight() int {
	return strings.Count(m.headerText(), "\n") + 1
}

// flourishHeight is the one row the intro "spin" occupies above the header while
// it plays, and zero once it is done — relayout budgets for it so the viewport
// grows by exactly that row, once, when the flourish clears.
func (m *Model) flourishHeight() int {
	if m.flourishDone {
		return 0
	}
	return 1
}

// flourishRow is the current intro-flourish line. Callers must only use it while
// flourishHeight() is 1.
func (m *Model) flourishRow() string {
	if m.flourishFrame < 0 || m.flourishFrame >= len(flourishFrames) {
		return flourishFrames[0]
	}
	return flourishFrames[m.flourishFrame]
}

// controlsText is the always-visible affordance, rendered from the key bindings
// so there is one source of truth. It names block, report, and leave and never
// mentions files, attachments, or read receipts.
func (m *Model) controlsText() string {
	parts := make([]string, 0, 4)
	for _, b := range []key.Binding{m.keys.send, m.keys.block, m.keys.report, m.keys.leave} {
		h := b.Help()
		parts = append(parts, h.Key+" "+h.Desc)
	}
	return strings.Join(parts, "    ")
}

// controlsHeight is the row count of the controls bar once wrapped to the pane
// width — it wraps to 2+ rows on a narrow pane, and relayout must budget for
// that or the viewport height is wrong.
func (m *Model) controlsHeight() int {
	if m.width <= 0 {
		return 1
	}
	return lipgloss.Height(lipgloss.NewStyle().Width(m.width).Render(m.controlsText()))
}

func (m *Model) render() string {
	rows := make([]string, 0, 5)
	if !m.flourishDone {
		rows = append(rows, m.flourishRow())
	}
	rows = append(rows,
		m.headerText(),
		m.vp.View(),
		m.input.View(),
		m.controlsText(),
	)
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// newClientMsgID mints the id an optimistic local echo is keyed by: 9 bytes
// from crypto/rand, base64 URL-safe with no padding (~12 chars). Same shape as
// backend/internal/hub/id.go. A crypto/rand failure is unrecoverable here.
func newClientMsgID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("chatui: crypto/rand read failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
