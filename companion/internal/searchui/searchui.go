// Package searchui is the companion's "searching" pane: a minimal Bubble Tea v2
// model that run.loop launches when the backend sends `queued` (a `ready`
// session left waiting for a match) and tears down the instant the wait
// resolves — `matched`, the burst ending, or any terminal path.
//
// It shows one calm line and an animated spinner and nothing else: no queue
// position, no ETA, no "N online" count, no nudge (Epic 5 owns nudges). Keys are
// inert — there is no quit key and no minigame; ctrl+c still cancels the process
// context through the signal handler run installs. All copy is static in this
// file so the voice — warm, lowercase, never naming the machinery ("looking for
// someone", not "in the queue") — stays in one place. This package logs nothing.
//
// Scope note: the spinner here is the *waiting* animation. The brief
// searching→matched "spin" flourish is chatui's intro, not this package.
package searchui

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// waitingLine is the single line shown above the spinner. Warm, lowercase, and
// it never names the queue or hints at a count.
const waitingLine = "looking for someone"

// Model is the searching pane. Construct it with New and hand it to
// tea.NewProgram.
type Model struct {
	spinner spinner.Model

	width  int
	height int
}

// New builds the searching pane with a MiniDot spinner.
func New() *Model {
	return &Model{
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

// Init starts the spinner animation.
func (m *Model) Init() tea.Cmd { return m.spinner.Tick }

// Update advances the spinner on its own tick, records the pane size, and
// ignores every key — the searching pane has no interactive affordance.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		// Inert: no quit key, no minigame. ctrl+c is handled by the process
		// signal handler, not here.
		return m, nil
	}
	return m, nil
}

// View renders the calm line and the spinner frame, roughly centred in the
// pane. No counts, no position, no ETA.
func (m *Model) View() tea.View {
	body := m.spinner.View() + "  " + waitingLine
	if m.width > 0 && m.height > 0 {
		body = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
	}
	return tea.NewView(body)
}
