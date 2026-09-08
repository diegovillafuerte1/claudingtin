// Package run is the companion's orchestrator: it wires the Story 1.3 transcript
// watcher to the Story 1.4 websocket handshake so that the model's turn
// boundaries become ready / busy frames on the wire, and maps the handful of
// terminal conditions (server asked to update, another connection took over,
// the watcher died, the context was cancelled) to the documented exits.
//
// It owns one desired state (ready/busy) that every watcher event updates, a
// sent state, and a short debounce so a seed replay or a fast burst of
// transitions collapses to just the settled state. After every (re)connect the
// desired state is re-announced unconditionally — that is the whole of "resume
// from the current transcript state".
//
// Two surfaces can take the pane from the status line. An inbound `queued`
// frame raises the searching spinner (internal/searchui); an inbound `matched`
// stops it — within the one select-case, so a chat_msg buffered behind the
// matched waits rather than drops — and opens the chat surface
// (internal/chatui), whose intro "spin" flourish plays as it settles. The
// status line writes nothing while either surface owns the pane. A burst that
// ends while searching just stops the spinner locally here; real
// return-to-spinner + re-enqueue semantics are Epic 3.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/chatui"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/safety"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/searchui"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/statusline"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/transcript"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/wsclient"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// debounceInterval coalesces a burst of turn transitions (a seed replay, or
// tool-call churn) so only the settled state reaches the wire. Package-level
// var, not a const, only so tests can shrink it.
var debounceInterval = 250 * time.Millisecond

// watchDrainWait bounds how long loop waits for a fatal watcher error to arrive
// after the Events channel closes, on the off chance ordering surprised us.
var watchDrainWait = 100 * time.Millisecond

// errWatchStopped is returned when the transcript watcher stops without having
// delivered a specific error. It maps to a non-zero exit.
var errWatchStopped = errors.New("transcript watch stopped")

// Config is everything Run needs. In, Out and Err default to os.Stdin /
// os.Stdout / os.Stderr when nil.
type Config struct {
	TranscriptPath string
	ServerURL      string
	AccountKey     string
	// ConfigDir is the resolved OS-config directory (the same one identity uses).
	// The first-run acknowledgement lives at <ConfigDir>/safety-ack.
	ConfigDir string
	// In is the reader the first-run gate reads its one line from.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run starts the transcript watch and the websocket client and blocks until a
// terminal condition. A nil return is a clean exit (code 0); a non-nil error is
// a fatal one (non-zero exit). The account key never appears in the error.
//
// cfg.ConfigDir is required: it is where the first-run acknowledgement
// (<ConfigDir>/safety-ack) is read and written. Run returns an error if it is
// empty rather than touching a relative path in the process working directory.
func Run(ctx context.Context, cfg Config) error {
	if cfg.In == nil {
		cfg.In = os.Stdin
	}
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.Err == nil {
		cfg.Err = os.Stderr
	}
	if cfg.ConfigDir == "" {
		return errors.New("run: ConfigDir is required")
	}

	// Every terminal path (session_ended, please_update, a fatal watch error,
	// a clean context cancel) must tear the transcript watcher and its fsnotify
	// handle down — not leave them running until the caller's context ends. A
	// derived context cancelled on return does exactly that.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The first-run precondition: nothing is watched and no socket is dialed
	// until the local 18+/safety screen is cleared.
	switch outcome, err := safety.Gate(ctx, cfg.In, cfg.Out, cfg.ConfigDir); {
	case err != nil:
		return fmt.Errorf("first-run gate: %w", err)
	case outcome == safety.OutcomeAccepted, outcome == safety.OutcomeAlreadyAccepted:
		// Cleared — fall through to the watch + client path below.
	case outcome == safety.OutcomeDeclined:
		// Stay running, connected to nothing, until the session ends. Nothing
		// was recorded, so the screen returns next run.
		statusline.New(cfg.Out).Show(statusline.PhaseInert)
		<-ctx.Done()
		return nil
	case outcome == safety.OutcomeAborted:
		// ctx cancelled while waiting for the line — a clean exit.
		return nil
	default:
		// This switch guards a safety gate: an unrecognised Outcome must fail
		// closed, never fall through to "connect".
		return fmt.Errorf("first-run gate: unexpected outcome %d", outcome)
	}

	w, err := transcript.Watch(ctx, cfg.TranscriptPath)
	if err != nil {
		return fmt.Errorf("start transcript watch: %w", err)
	}

	client := wsclient.New(wsclient.Config{
		URL:        cfg.ServerURL,
		AccountKey: cfg.AccountKey,
	})

	return loop(ctx, cfg, w.Events, w.Errors, client, statusline.New(cfg.Out))
}

// stateClient is the slice of *wsclient.Client that loop depends on, so tests
// can substitute a fake.
type stateClient interface {
	Run(ctx context.Context) wsclient.Result
	SendState(ctx context.Context, msg any) error
	Events() <-chan wsclient.Event
	Queued() <-chan proto.Queued
	Matched() <-chan proto.Matched
	ChatMsgs() <-chan proto.ChatMsg
	SendChat(ctx context.Context, m proto.ChatMsg) error
}

// chatProgram is the slice of *tea.Program that loop drives, behind a seam so
// run_test.go can substitute a fake and never stand up a real terminal.
//
// loop reaches the running surface only through Send: it is documented as a
// no-op once the program has exited, so it can never wedge loop during teardown.
// *tea.Program.Println is deliberately not on this seam — it is a bare blocking
// send with no escape once Run has returned, and the teardown window makes that
// reachable from a buffered block/report/leave intent (Epic 2 retrospective F6).
// A content-free operator line goes through Send as a chatui.LogLine (the model
// emits tea.Println), or to cfg.Err when no surface is up.
type chatProgram interface {
	Run() (tea.Model, error)
	Send(tea.Msg)
	Quit()
	Kill()
}

// newChatProgram builds the real Bubble Tea chat program for a match. It is a
// package var so tests can replace it. Signals stay with the process's own
// signal.NotifyContext (WithoutSignalHandler) so a SIGINT still cancels ctx and
// unwinds everything, chat included.
// newChatModel builds the chat surface model with its callbacks wired. It is a
// thin package var so a test can substitute a real *chatui.Model while capturing
// the send closure run.loop handed it — exercising the real WithNotify/WithSend
// wiring without a PTY, which the wholesale newChatProgram stub cannot.
var newChatModel = func(m proto.Matched, notify func(chatui.Intent), send func(clientMsgID, text string)) tea.Model {
	return chatui.New(m, chatui.WithNotify(notify), chatui.WithSend(send))
}

var newChatProgram = func(ctx context.Context, cfg Config, m proto.Matched, notify func(chatui.Intent), send func(clientMsgID, text string)) chatProgram {
	return tea.NewProgram(
		newChatModel(m, notify, send),
		tea.WithContext(ctx),
		tea.WithInput(cfg.In),
		tea.WithOutput(cfg.Out),
		tea.WithoutSignalHandler(),
	)
}

// chatQuitGrace bounds how long teardown waits for a Quit()'d chat program to
// return before Kill()ing it. Package var so tests can shrink it. The searching
// spinner's teardown reuses the same grace.
var chatQuitGrace = 2 * time.Second

// searchProgram is the slice of *tea.Program that loop drives for the searching
// spinner, behind a seam so run_test.go can substitute a fake and never stand
// up a real terminal. The spinner takes no callbacks and speaks nothing on the
// wire — it is purely a pane animation — so the seam is smaller than
// chatProgram's.
type searchProgram interface {
	Run() (tea.Model, error)
	Quit()
	Kill()
}

// newSearchProgram builds the real Bubble Tea searching-spinner program. It is
// a package var so tests can replace it. Like the chat program it keeps the
// process's own signal handling (WithoutSignalHandler) so a SIGINT still
// cancels ctx and unwinds everything.
var newSearchProgram = func(ctx context.Context, cfg Config) searchProgram {
	return tea.NewProgram(
		searchui.New(),
		tea.WithContext(ctx),
		tea.WithInput(cfg.In),
		tea.WithOutput(cfg.Out),
		tea.WithoutSignalHandler(),
	)
}

// wireState is the companion's view of the model's think-time, as it maps to the
// wire.
type wireState int

const (
	// stateBusy is the default: no turn seen, or the last turn ended.
	stateBusy wireState = iota
	// stateReady: a turn is in progress, the model is thinking.
	stateReady
)

func frameFor(s wireState) any {
	if s == stateReady {
		return proto.Ready{}
	}
	return proto.Busy{}
}

func phaseFor(s wireState) statusline.Phase {
	if s == stateReady {
		return statusline.PhaseFreeToChat
	}
	return statusline.PhaseWaiting
}

// loop is the event core, split out so run_test.go can drive it with a fake
// watcher and a fake client.
func loop(
	ctx context.Context,
	cfg Config,
	events <-chan transcript.Event,
	watchErrs <-chan error,
	client stateClient,
	sl *statusline.Renderer,
) error {
	sl.Show(statusline.PhaseConnecting)

	clientDone := make(chan wsclient.Result, 1)
	go func() { clientDone <- client.Run(ctx) }()

	desired := stateBusy
	sent := stateBusy
	haveSent := false

	// Chat surface state. It is launched on the first `matched` and lives only
	// while a match is active; the status line is suppressed for its duration.
	var (
		chat        chatProgram
		chatActive  bool
		chatDone    chan struct{}
		chatIntents chan chatui.Intent
		chatSends   chan chatui.OutboundMsg
	)

	// Searching-spinner state. It is launched on an inbound `queued` frame while
	// the session is ready and unmatched, and torn down on `matched`, the burst
	// ending, or any terminal path.
	var (
		search       searchProgram
		searchActive bool
		searchDone   chan struct{}
	)

	// showPhase is sl.Show gated on the two pane surfaces: while the chat
	// program or the searching spinner owns the pane, the status line writes
	// nothing.
	showPhase := func(p statusline.Phase) {
		if chatActive || searchActive {
			return
		}
		sl.Show(p)
	}

	launchChat := func(m proto.Matched) {
		chatIntents = make(chan chatui.Intent, 8)
		ci := chatIntents
		notify := func(i chatui.Intent) {
			// Non-blocking: if loop is mid-teardown and the buffer is full, a
			// dropped block/report keystroke is fine (leave also returns
			// tea.Quit from the model, so it is never lost).
			select {
			case ci <- i:
			default:
			}
		}
		chatSends = make(chan chatui.OutboundMsg, 32)
		cs := chatSends
		send := func(clientMsgID, text string) {
			// Fires synchronously inside the chat model's Update. Non-blocking:
			// the actual socket write is done by loop below, off this goroutine,
			// so a slow socket can never stall the UI. A full buffer during
			// teardown just drops the line — the optimistic echo already showed
			// it and v1 has no ack.
			select {
			case cs <- chatui.OutboundMsg{ClientMsgID: clientMsgID, Text: text}:
			default:
			}
		}
		chat = newChatProgram(ctx, cfg, m, notify, send)
		chatActive = true
		chatDone = make(chan struct{})
		go func(p chatProgram, done chan struct{}) {
			_, _ = p.Run()
			close(done)
		}(chat, chatDone)
	}

	// stopChat tears the chat program down (Quit, then Kill on a grace timeout)
	// and, when resume is true, hands the pane back to the status line.
	stopChat := func(resume bool) {
		if !chatActive {
			return
		}
		chat.Quit()
		select {
		case <-chatDone:
		case <-time.After(chatQuitGrace):
			chat.Kill()
			// Bounded again: a wedged program that ignores Kill must not hang
			// loop forever — proceed with teardown regardless.
			select {
			case <-chatDone:
			case <-time.After(chatQuitGrace):
			}
		}
		chatActive = false
		chat = nil
		chatIntents = nil
		chatSends = nil
		chatDone = nil
		if resume {
			sl.Show(phaseFor(desired))
		}
	}

	// chatLog writes a content-free operator breadcrumb. While a chat surface is
	// up it goes through chat.Send (a no-op if the program has already exited, so
	// it never blocks the loop) and the model prints it above the live frame; a
	// breadcrumb dropped because the surface just went away is acceptable — the
	// frame it would have annotated is gone. With no surface up it goes to
	// cfg.Err. It never calls chat.Println: that blocks forever once Run has
	// returned, which a buffered intent in the teardown window can reach (F6).
	chatLog := func(line string) {
		if chatActive && chat != nil {
			chat.Send(chatui.LogLine{Text: line})
			return
		}
		fmt.Fprintln(cfg.Err, line)
	}

	// launchSearch raises the searching spinner. Guarded by its one call site to
	// !chatActive && !searchActive && desired == stateReady, so a redelivered
	// `queued` after a reconnect, or one that lands after the burst already
	// ended, never starts a second spinner or a stray flash.
	launchSearch := func() {
		search = newSearchProgram(ctx, cfg)
		searchActive = true
		searchDone = make(chan struct{})
		go func(p searchProgram, done chan struct{}) {
			_, _ = p.Run()
			close(done)
		}(search, searchDone)
	}

	// stopSearch tears the spinner down (Quit, then Kill on a grace timeout),
	// mirroring stopChat, and hands the pane back to the status line when resume
	// is true.
	stopSearch := func(resume bool) {
		if !searchActive {
			return
		}
		search.Quit()
		select {
		case <-searchDone:
		case <-time.After(chatQuitGrace):
			search.Kill()
			select {
			case <-searchDone:
			case <-time.After(chatQuitGrace):
			}
		}
		searchActive = false
		search = nil
		searchDone = nil
		if resume {
			sl.Show(phaseFor(desired))
		}
	}

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	debouncePending := false
	arm := func() {
		if debouncePending && !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(debounceInterval)
		debouncePending = true
	}

	// pushState sends the frame for the current desired state and only records
	// it as sent when the write actually succeeds — a failed send (no live
	// connection, or a half-dead socket) must leave sent/haveSent alone so the
	// next Connected re-announces.
	pushState := func() {
		if err := client.SendState(ctx, frameFor(desired)); err == nil {
			sent = desired
			haveSent = true
		}
	}

	flush := func() {
		if !haveSent || desired != sent {
			pushState()
		}
		// A burst that ends while searching just drops the spinner locally and
		// returns the pane to the waiting phrase — Epic 3 owns real
		// return-to-spinner + re-enqueue.
		if searchActive && desired == stateBusy {
			stopSearch(true)
		}
		showPhase(phaseFor(desired))
	}

	var lastWatchErr error

	for {
		select {
		case <-ctx.Done():
			stopSearch(false)
			stopChat(false)
			<-clientDone
			return nil

		case res := <-clientDone:
			switch res {
			case wsclient.ResultPleaseUpdate:
				// One notice, stop reconnecting, stay alive so the user can
				// read it. No key, no transcript content.
				stopSearch(false)
				stopChat(false)
				fmt.Fprintln(cfg.Err, "companion: the server asked this build to update; it will not reconnect until you install a newer companion")
				sl.Show(statusline.PhaseUpdateNeeded)
				<-ctx.Done()
				return nil
			case wsclient.ResultSessionEnded:
				// The process returns nil right after, so there is no status
				// line to hand back to — resume:false avoids a stale flash.
				stopSearch(false)
				stopChat(false)
				return nil
			default: // ResultContextDone
				stopSearch(false)
				stopChat(false)
				return nil
			}

		case ev, ok := <-events:
			if !ok {
				// A clean context cancel (SIGINT/SIGTERM) makes transcript.Watch
				// close Events and Errors with nothing on Errors. That close, the
				// closed-Errors receive, and <-ctx.Done() can all be ready in the
				// same select; if this branch is the one picked, it is still a
				// normal shutdown — exit 0, never errWatchStopped.
				if ctx.Err() != nil {
					stopSearch(false)
					stopChat(false)
					<-clientDone
					return nil
				}
				// The watcher stopped on its own. On the fatal path it delivers
				// its error on watchErrs (blocking) before closing Events, so
				// lastWatchErr is already set; the short drain is belt-and-braces.
				if lastWatchErr == nil && watchErrs != nil {
					select {
					case e := <-watchErrs:
						lastWatchErr = e
					case <-time.After(watchDrainWait):
					}
				}
				if lastWatchErr != nil {
					// main prints the returned error to stderr; no key or file
					// content is in it (the watcher's errors carry only the path).
					return fmt.Errorf("transcript watch failed: %w", lastWatchErr)
				}
				return errWatchStopped
			}
			// A normal event means the watcher is healthy again: forget any
			// earlier transient error so it can never be reported as the fatal
			// cause if the watcher later stops for some other reason.
			lastWatchErr = nil
			switch ev.Kind {
			case transcript.TurnStart:
				desired = stateReady
			case transcript.TurnEnd:
				desired = stateBusy
			}
			arm()

		case err, ok := <-watchErrs:
			if !ok {
				watchErrs = nil
				continue
			}
			if err != nil {
				// A watcher error. It may be transient (the watcher keeps
				// running) or the fatal one delivered just before the channels
				// close — this branch cannot tell which, so the wording does not
				// prejudge it. The string carries the transcript path (a launch
				// arg), never file content.
				lastWatchErr = err
				fmt.Fprintln(cfg.Err, "companion: transcript watch error:", err)
			}

		case <-debounce.C:
			debouncePending = false
			flush()

		case cev := <-client.Events():
			switch cev.Kind {
			case wsclient.Connected:
				// AD-1: a spoke recovers by reconnecting and re-announcing. The
				// client blocks on this send until loop drains it, so a
				// Connected is never lost even while loop is busy in a slow
				// SendState — the re-announce below always runs.
				pushState()
				showPhase(phaseFor(desired))
			case wsclient.Reconnecting:
				showPhase(statusline.PhaseReconnecting)
			}

		case <-client.Queued():
			// An inbound `queued` frame: the session is ready and left waiting.
			// Raise the searching spinner — but only if nothing else owns the
			// pane and the burst has not already ended. A redelivered `queued`
			// after a reconnect finds searchActive already true and is a no-op.
			if !chatActive && !searchActive && desired == stateReady {
				launchSearch()
			}

		case m := <-client.Matched():
			// Story 2.3: the first matched opens the chat surface. A stray
			// second one while it is already up is ignored (Epic 3 owns
			// re-enqueue and re-match). The spinner (if any) is torn down here,
			// in the same select-case as launchChat, so a chat_msg buffered
			// behind this matched waits rather than drops.
			if !chatActive {
				stopSearch(false)
				launchChat(m)
			}

		case cm := <-client.ChatMsgs():
			// An inbound peer line. Forward it to the live chat surface as a
			// PeerMsg; if no chat is active it is dropped (no panic, nothing to
			// cfg.Out).
			if chatActive && chat != nil {
				chat.Send(chatui.PeerMsg{ClientMsgID: cm.ClientMsgID, Text: cm.Text})
			}

		case om := <-chatSends:
			// The chat surface echoed the user's own line optimistically and
			// asked us to put it on the wire. This is the only goroutine that
			// writes the socket for chat. On failure, one content-free
			// breadcrumb — no text, no id, nothing raw to the tty.
			if err := client.SendChat(ctx, proto.ChatMsg{ClientMsgID: om.ClientMsgID, Text: om.Text}); err != nil {
				chatLog("companion: a chat line could not be sent")
			}

		case i := <-chatIntents:
			// block / report / leave are logged content-free here — no message
			// text, no account key, nothing on the wire (Epics 3–4). leave also
			// tears the surface down: it does so first, then writes the
			// breadcrumb to the now-free tty and repaints the status line (the
			// same order the please_update path uses).
			switch i {
			case chatui.IntentLeave:
				stopChat(false)
				fmt.Fprintln(cfg.Err, "companion: leave requested from the chat surface")
				showPhase(phaseFor(desired))
			case chatui.IntentBlock:
				chatLog("companion: block requested from the chat surface")
			case chatui.IntentReport:
				chatLog("companion: report requested from the chat surface")
			default:
				chatLog("companion: unrecognized chat intent")
			}

		case <-chatDone:
			// The chat program exited on its own (its context ended, or an
			// internal error). Hand the pane back unless we are shutting down.
			stopChat(ctx.Err() == nil)

		case <-searchDone:
			// The spinner program exited on its own (its context ended, or an
			// internal error). Hand the pane back unless we are shutting down.
			stopSearch(ctx.Err() == nil)
		}
	}
}
