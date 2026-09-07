// Package statusline is the companion's ambient UI: one human-readable line on
// an io.Writer that changes as the connection and the model's think-time move
// between a handful of phases. From Story 2.3 a Bubble Tea chat surface
// (internal/chatui) takes over the pane while a match is live and run suppresses
// these writes for its duration; this line covers every other moment. Nothing
// here ever writes to a Claude Code stream.
//
// All copy lives in this file so the voice — warm, lowercase-friendly, and
// never naming the machinery ("waiting", never "queue") — stays in one place.
package statusline

import (
	"fmt"
	"io"
)

// Phase is a coarse state the companion can be in. The renderer prints one line
// per phase and only when the phase actually changes.
type Phase int

const (
	// PhaseConnecting: the websocket is being opened for the first time.
	PhaseConnecting Phase = iota
	// PhaseWaiting: connected, but the model is not thinking right now, so
	// there is nobody to talk to yet — the user is waiting for the next quiet
	// moment. Maps to the wire "busy" state.
	PhaseWaiting
	// PhaseFreeToChat: the model is thinking; the user is free to chat with
	// whoever else is around. Maps to the wire "ready" state.
	PhaseFreeToChat
	// PhaseReconnecting: the connection dropped and is being re-established.
	PhaseReconnecting
	// PhaseUpdateNeeded: the server asked this companion to update; nothing
	// else will happen until the user installs a newer build.
	PhaseUpdateNeeded
	// PhaseInert: the first-run screen was not accepted, so nothing is
	// connected and nothing will be until the user comes back to it. No guilt,
	// no nagging. Appended last on purpose — the earlier constants must keep
	// their values.
	PhaseInert
)

// line is the copy for a phase. Kept deliberately short and unpolished.
func line(p Phase) string {
	switch p {
	case PhaseConnecting:
		return "connecting…"
	case PhaseWaiting:
		return "you're back with claude — waiting for the next quiet moment"
	case PhaseFreeToChat:
		return "claude's thinking — you're free to chat"
	case PhaseReconnecting:
		return "lost the thread for a moment — picking it back up"
	case PhaseUpdateNeeded:
		return "this companion is out of date — grab the latest build to keep going"
	case PhaseInert:
		return "nothing's connected — you can turn this on whenever you like"
	default:
		return "…"
	}
}

// Renderer writes phase lines to an io.Writer, suppressing repeats.
type Renderer struct {
	w    io.Writer
	last Phase
	have bool
}

// New returns a Renderer that writes to w.
func New(w io.Writer) *Renderer {
	return &Renderer{w: w}
}

// Show writes the line for p, but only if p differs from the phase last shown
// (the very first call always writes). A write error is swallowed — the status
// line is best-effort and must never take down the companion.
func (r *Renderer) Show(p Phase) {
	if r.have && r.last == p {
		return
	}
	r.have = true
	r.last = p
	_, _ = fmt.Fprintln(r.w, line(p))
}
