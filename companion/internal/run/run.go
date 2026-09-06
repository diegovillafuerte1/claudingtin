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
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/safety"
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
		sl.Show(phaseFor(desired))
	}

	var lastWatchErr error

	for {
		select {
		case <-ctx.Done():
			<-clientDone
			return nil

		case res := <-clientDone:
			switch res {
			case wsclient.ResultPleaseUpdate:
				// One notice, stop reconnecting, stay alive so the user can
				// read it. No key, no transcript content.
				fmt.Fprintln(cfg.Err, "companion: the server asked this build to update; it will not reconnect until you install a newer companion")
				sl.Show(statusline.PhaseUpdateNeeded)
				<-ctx.Done()
				return nil
			case wsclient.ResultSessionEnded:
				return nil
			default: // ResultContextDone
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
				sl.Show(phaseFor(desired))
			case wsclient.Reconnecting:
				sl.Show(statusline.PhaseReconnecting)
			}
		}
	}
}
