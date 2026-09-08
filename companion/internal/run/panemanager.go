// paneManager owns the companion's two pane surfaces — the Bubble Tea chat
// program (internal/chatui) and the searching spinner (internal/searchui) — and
// every closure that used to drive them inline in loop: showPhase, launchChat,
// stopChat, launchSearch, stopSearch, the content-free breadcrumb router
// (chatLog), and the inbound-peer forward. loop keeps its single for { select }
// and every case in the same order; it just delegates pane work to pm.*.
//
// Lifted here for Epic 2 retrospective findings F4 (loop had grown to one
// ~370-line function every epic adds to) and F5 (launchSearch/stopSearch were
// near-verbatim copies of launchChat/stopChat). The F5 fold is the shared pane
// type below: one launch helper (pane.start) and one teardown helper
// (pane.teardown, holding the Quit -> chatQuitGrace -> Kill -> bounded-wait
// escalation) back both surfaces.
//
// Pure refactor — no observable behaviour changes. run_test.go's behaviour suite
// is the regression guard and passes unmodified; panemanager_test.go unit-tests
// this type directly.
package run

import (
	"context"
	"fmt"
	"time"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/chatui"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/statusline"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// teardownable is the slice of chatProgram / searchProgram that pane.teardown
// needs. Both interfaces already expose Quit() and Kill(), so each satisfies
// this with no interface change — that is what lets one teardown helper serve
// both surfaces (F5).
type teardownable interface {
	Quit()
	Kill()
}

// pane is the shared launch + teardown state for one Bubble Tea surface. The
// goroutine wiring (start) and the Quit -> grace -> Kill escalation (teardown)
// live here once and back both the chat program and the searching spinner (F5).
type pane struct {
	active bool
	done   chan struct{}
}

// start marks the pane active, makes a fresh done channel, and runs fn on a
// goroutine that closes done when fn returns. done is captured into a local so a
// later relaunch that reassigns p.done can never make this goroutine close the
// wrong channel — the same guard the old inline `go func(p, done)` had.
func (p *pane) start(fn func()) {
	p.active = true
	p.done = make(chan struct{})
	done := p.done
	go func() {
		fn()
		close(done)
	}()
}

// teardown is the one shared stop path for both surfaces (F5). It is a no-op
// (returns false) when the pane is not active. Otherwise it Quit()s the program,
// waits up to chatQuitGrace for the run goroutine to close done, and if that
// elapses escalates to Kill() plus a second bounded wait — then proceeds
// regardless, so a program that ignores both can never hang the loop. It clears
// active/done and returns true so the caller knows it owns the rest of the
// cleanup (niling its program handle, an optional resume repaint).
func (p *pane) teardown(prog teardownable) bool {
	if !p.active {
		return false
	}
	prog.Quit()
	select {
	case <-p.done:
	case <-time.After(chatQuitGrace):
		prog.Kill()
		// Bounded again: a wedged program that ignores Kill must not hang the
		// loop forever — proceed with teardown regardless.
		select {
		case <-p.done:
		case <-time.After(chatQuitGrace):
		}
	}
	p.active = false
	p.done = nil
	return true
}

// paneManager holds the chat + searching-spinner surface state and the
// lifecycle methods loop used to carry as closures. loop constructs one per run
// and routes every pane transition through it.
type paneManager struct {
	// ctx is carried from construction so launchChat / launchSearch can hand it
	// to the newChatProgram(pm.ctx, ...) / newSearchProgram(pm.ctx, ...) seam
	// calls — the surfaces bind their lifetime to loop's derived context.
	ctx context.Context
	cfg Config
	sl  *statusline.Renderer
	// resumePhase yields the status-line phase to repaint when a surface is torn
	// down with resume=true. loop passes
	// func() statusline.Phase { return phaseFor(desired) } so it reads loop's
	// live desired at teardown time — matching the old direct
	// sl.Show(phaseFor(desired)) in stopChat/stopSearch (ungated, because the
	// pane it belonged to is already down).
	resumePhase func() statusline.Phase

	// Chat surface. Launched on the first `matched`, live only while a match is
	// active; the status line is suppressed for its duration.
	chat        chatProgram
	chatPane    pane
	chatIntents chan chatui.Intent
	chatSends   chan chatui.OutboundMsg

	// Searching-spinner surface. Launched on an inbound `queued` frame while the
	// session is ready and unmatched; torn down on `matched`, the burst ending,
	// or any terminal path.
	search     searchProgram
	searchPane pane
}

func newPaneManager(ctx context.Context, cfg Config, sl *statusline.Renderer, resumePhase func() statusline.Phase) *paneManager {
	return &paneManager{ctx: ctx, cfg: cfg, sl: sl, resumePhase: resumePhase}
}

func (pm *paneManager) chatActive() bool   { return pm.chatPane.active }
func (pm *paneManager) searchActive() bool { return pm.searchPane.active }
func (pm *paneManager) anyActive() bool    { return pm.chatPane.active || pm.searchPane.active }

// showPhase is sl.Show gated on the two pane surfaces: while the chat program or
// the searching spinner owns the pane, the status line writes nothing.
func (pm *paneManager) showPhase(p statusline.Phase) {
	if pm.anyActive() {
		return
	}
	pm.sl.Show(p)
}

// launchChat builds the chat program through the newChatProgram seam and starts
// it. notify / send close over the freshly-made ci / cs locals, not the
// paneManager fields, so a lagging previous program that fires a callback after
// a relaunch writes a dead channel and the non-blocking select/default drops it.
func (pm *paneManager) launchChat(m proto.Matched) {
	pm.chatIntents = make(chan chatui.Intent, 8)
	ci := pm.chatIntents
	notify := func(i chatui.Intent) {
		// Non-blocking: if loop is mid-teardown and the buffer is full, a
		// dropped block/report keystroke is fine (leave also returns tea.Quit
		// from the model, so it is never lost).
		select {
		case ci <- i:
		default:
		}
	}
	pm.chatSends = make(chan chatui.OutboundMsg, 32)
	cs := pm.chatSends
	send := func(clientMsgID, text string) {
		// Fires synchronously inside the chat model's Update. Non-blocking: the
		// actual socket write is done by loop, off this goroutine, so a slow
		// socket can never stall the UI. A full buffer during teardown just
		// drops the line — the optimistic echo already showed it and v1 has no
		// ack.
		select {
		case cs <- chatui.OutboundMsg{ClientMsgID: clientMsgID, Text: text}:
		default:
		}
	}
	pm.chat = newChatProgram(pm.ctx, pm.cfg, m, notify, send)
	prog := pm.chat
	pm.chatPane.start(func() { _, _ = prog.Run() })
}

// stopChat tears the chat program down through the shared pane.teardown (Quit,
// then Kill on a grace timeout) and, when resume is true, hands the pane back to
// the status line.
func (pm *paneManager) stopChat(resume bool) {
	if !pm.chatPane.teardown(pm.chat) {
		return
	}
	pm.chat = nil
	pm.chatIntents = nil
	pm.chatSends = nil
	if resume {
		pm.sl.Show(pm.resumePhase())
	}
}

// launchSearch raises the searching spinner. Its one loop call site guards it to
// !anyActive() && desired == stateReady, so a redelivered `queued` after a
// reconnect, or one that lands after the burst already ended, never starts a
// second spinner or a stray flash.
func (pm *paneManager) launchSearch() {
	pm.search = newSearchProgram(pm.ctx, pm.cfg)
	prog := pm.search
	pm.searchPane.start(func() { _, _ = prog.Run() })
}

// stopSearch tears the spinner down through the shared pane.teardown, mirroring
// stopChat, and hands the pane back to the status line when resume is true.
func (pm *paneManager) stopSearch(resume bool) {
	if !pm.searchPane.teardown(pm.search) {
		return
	}
	pm.search = nil
	if resume {
		pm.sl.Show(pm.resumePhase())
	}
}

// stopAll tears down whichever surface is up with resume:false — the
// terminal-path teardown (ctx.Done, any clientDone result). Safe no-op when
// neither is active. Order matches the old inline stopSearch(false);
// stopChat(false) pairs.
func (pm *paneManager) stopAll() {
	pm.stopSearch(false)
	pm.stopChat(false)
}

// log writes a content-free operator breadcrumb. While a chat surface is up it
// goes through chat.Send (a no-op if the program has already exited, so it never
// blocks the loop) and the model prints it above the live frame; a breadcrumb
// dropped because the surface just went away is acceptable — the frame it would
// have annotated is gone. With no surface up it goes to cfg.Err. It never calls
// chat.Println: that blocks forever once Run has returned, which a buffered
// intent in the teardown window can reach (Epic 2 retrospective F6).
func (pm *paneManager) log(line string) {
	if pm.chatPane.active && pm.chat != nil {
		pm.chat.Send(chatui.LogLine{Text: line})
		return
	}
	fmt.Fprintln(pm.cfg.Err, line)
}

// sendPeer forwards an inbound peer line to the live chat surface as a PeerMsg.
// With no chat active it is dropped — no panic, nothing to cfg.Out.
func (pm *paneManager) sendPeer(clientMsgID, text string) {
	if pm.chatPane.active && pm.chat != nil {
		pm.chat.Send(chatui.PeerMsg{ClientMsgID: clientMsgID, Text: text})
	}
}

// chatDone / searchDone / intents / sends expose the channels loop's select
// reads. Each returns the underlying field verbatim — nil while the pane is
// down, so the select case stays dormant exactly as the bare variable did. The
// accessor is re-evaluated each select pass; no behaviour rides on the call
// itself.
func (pm *paneManager) chatDone() <-chan struct{}        { return pm.chatPane.done }
func (pm *paneManager) searchDone() <-chan struct{}      { return pm.searchPane.done }
func (pm *paneManager) intents() <-chan chatui.Intent    { return pm.chatIntents }
func (pm *paneManager) sends() <-chan chatui.OutboundMsg { return pm.chatSends }
