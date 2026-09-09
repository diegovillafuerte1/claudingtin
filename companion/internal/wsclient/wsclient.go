// Package wsclient owns the companion's single websocket to the backend: it
// dials, sends the version + account-key hello as the first frame after every
// (re)connect, carries ready/busy state frames and outbound chat_msg frames
// out, watches for the five inbound frames that matter (queued, matched,
// chat_msg, please_update, session_ended) — queued is surfaced on Queued() for
// run.loop to raise the searching spinner, matched on Matched() to open the
// chat surface, chat_msg on ChatMsgs() to render as a peer line, and
// session_ended on SessionEnded() as a calm chat-end — and reconnects with an
// exponentially backed-off, fully jittered delay after any drop — forever,
// until the context is cancelled or the server asks the client to update.
//
// session_ended is a chat-end, not a connection-end: it is surfaced and serving
// continues, because a peer merely left and the socket is still ours. It ends
// Run (as ResultSessionEnded) only when the server also closes the socket
// within sessionEndedCloseGrace of the frame — a connection takeover or a
// server shutdown. please_update is the one frame that ends Run unconditionally.
//
// It speaks nothing on the wire that is not a proto message, mirrors the
// backend's framing (text frames, proto.Encode/Decode, a normal close on a
// clean exit and CloseNow on the drop path), and never logs: the account key
// must not leak, so diagnostics are the caller's job.
package wsclient

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/proto"
)

// Backoff / timeout knobs. Package-level vars, not consts, only so the tests can
// shrink them; nothing outside this package touches them (mirrors
// internal/transcript).
var (
	backoffBase       = 500 * time.Millisecond
	backoffCap        = 30 * time.Second
	dialTimeout       = 10 * time.Second
	frameWriteTimeout = 5 * time.Second
	// sessionEndedCloseGrace bounds how soon after a session_ended frame a
	// socket close still counts as "the server ended this connection" (a
	// takeover or a shutdown) rather than an unrelated later drop. The backend
	// writes session_ended and closes synchronously on that path, so a small
	// window is enough; a network blip seconds after a peer left reconnects
	// normally instead.
	sessionEndedCloseGrace = 2 * time.Second
)

// Result is how Run ended.
type Result int

const (
	// ResultContextDone: the caller cancelled the context. Clean shutdown.
	ResultContextDone Result = iota
	// ResultPleaseUpdate: the server sent please_update. Run stops retrying;
	// the caller keeps the process alive so the user can read the notice.
	ResultPleaseUpdate
	// ResultSessionEnded: the server closed the socket right after a
	// session_ended frame — another connection for this account key took over,
	// or the server is shutting down. A session_ended whose socket stays open
	// is a peer leaving a chat and does not end Run.
	ResultSessionEnded
)

// EventKind is a connection-lifecycle notification pushed to Events.
type EventKind int

const (
	// Connected: a hello has just been written and accepted; the caller should
	// (re)push the current ready/busy state now.
	Connected EventKind = iota
	// Reconnecting: a live connection dropped and a backoff delay is starting.
	Reconnecting
)

// Event is one connection-lifecycle notification.
type Event struct {
	Kind EventKind
}

// ErrNotConnected is returned by SendState when there is no live connection. It
// is not fatal: the caller's state is re-pushed on the next Connected event.
var ErrNotConnected = errors.New("wsclient: not connected")

// Config is the immutable configuration of a Client.
type Config struct {
	// URL is the fully-resolved websocket URL, path included (e.g.
	// "wss://host/ws").
	URL string
	// AccountKey travels in proto.Hello.AccountKey and nowhere else.
	AccountKey string
}

// Client is one reconnecting websocket. Construct it with New, run it with Run,
// push state through SendState, and observe (re)connects on Events.
type Client struct {
	url        string
	accountKey string

	events       chan Event
	queued       chan proto.Queued
	matched      chan proto.Matched
	chatMsg      chan proto.ChatMsg
	sessionEnded chan proto.SessionEnded

	mu   sync.Mutex
	conn *websocket.Conn

	writeMu sync.Mutex
}

// New returns a Client that will dial cfg.URL.
func New(cfg Config) *Client {
	return &Client{
		url:          cfg.URL,
		accountKey:   cfg.AccountKey,
		events:       make(chan Event, 8),
		queued:       make(chan proto.Queued, 1),
		matched:      make(chan proto.Matched, 1),
		chatMsg:      make(chan proto.ChatMsg, 64),
		sessionEnded: make(chan proto.SessionEnded, 1),
	}
}

// Events delivers Connected / Reconnecting notifications. Delivery is
// guaranteed: emit blocks until the consumer takes the event (or the context is
// cancelled), so a Connected — which is the caller's cue to re-announce state
// after a reconnect — is never dropped. The consumer (run.loop) always returns
// to its select within one frameWriteTimeout, so this cannot stall the client
// for long.
func (c *Client) Events() <-chan Event { return c.events }

// Queued delivers the inbound proto.Queued frame to the caller. It is buffered
// by one and never closed; run.loop drains it to raise the searching spinner.
// queued is not a terminal frame — Run keeps serving after one. The reader
// never blocks on it: the serve goroutine's send is non-blocking, so a queued
// that arrives while the buffer is full is dropped — a redelivered queued after
// a reconnect is a harmless no-op once the spinner is already up.
func (c *Client) Queued() <-chan proto.Queued { return c.queued }

// Matched delivers the inbound proto.Matched frame (fields intact) to the
// caller. It is buffered by one and never closed; run.loop drains it to launch
// the chat surface. matched is not a terminal frame — Run keeps serving after
// one, so a session that ends and re-matches within the same Run works. The
// reader never blocks on it: a second matched that arrives before run.loop
// drains the first is dropped (run.loop ignores a second matched while a chat
// is already up), so a session_ended or please_update queued behind it is
// still processed without delay.
func (c *Client) Matched() <-chan proto.Matched { return c.matched }

// ChatMsgs delivers each inbound peer proto.ChatMsg (fields intact) to the
// caller. It is buffered (64) and never closed; run.loop drains it and forwards
// each line to the live chat surface, dropping it if no chat is active. Like
// Matched it is not terminal — Run keeps serving after one — and the reader
// never blocks on it: the serve goroutine's send is non-blocking, so a chat
// line that arrives while the buffer is full is dropped. v1 has no ack, so
// drop-on-full is acceptable.
func (c *Client) ChatMsgs() <-chan proto.ChatMsg { return c.chatMsg }

// SessionEnded delivers the inbound proto.SessionEnded frame to the caller. It
// is buffered by one and never closed; run.loop drains it to show the calm
// "their Claude came back" state and route the pane. session_ended is not a
// terminal frame here — a peer left a chat, the socket is still ours, and Run
// keeps serving so the session can be re-matched. The reader never blocks on
// it: the serve goroutine's send is non-blocking, so a duplicate frame that
// arrives while the buffer is full is dropped (run.loop's routing is
// idempotent). Run still ends with ResultSessionEnded when the server closes
// the socket within sessionEndedCloseGrace of this frame (takeover / shutdown).
func (c *Client) SessionEnded() <-chan proto.SessionEnded { return c.sessionEnded }

func (c *Client) emit(ctx context.Context, k EventKind) {
	select {
	case c.events <- Event{Kind: k}:
	case <-ctx.Done():
	}
}

// Run drives the connect / hello / serve / reconnect loop until the context is
// cancelled (ResultContextDone) or the server ends the client
// (ResultPleaseUpdate / ResultSessionEnded). It blocks; call it on its own
// goroutine. It is not safe to call Run twice on one Client.
func (c *Client) Run(ctx context.Context) Result {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ResultContextDone
		}

		conn, err := c.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ResultContextDone
			}
			if !c.sleep(ctx, attempt) {
				return ResultContextDone
			}
			attempt++
			continue
		}

		if err := c.writeFrame(ctx, conn, proto.Hello{
			AccountKey:      c.accountKey,
			ProtocolVersion: proto.PROTOCOL_VERSION,
		}); err != nil {
			conn.CloseNow()
			if ctx.Err() != nil {
				return ResultContextDone
			}
			if !c.sleep(ctx, attempt) {
				return ResultContextDone
			}
			attempt++
			continue
		}

		// The hello is on the wire: the connection is as good as it gets on our
		// side, so the backoff resets here (AD-1 / spec "backoff reset after a
		// successful hello").
		attempt = 0
		c.setConn(conn)
		c.emit(ctx, Connected)

		res := c.serve(ctx, conn)
		c.setConn(nil)

		switch res {
		case serveContextDone:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return ResultContextDone
		case servePleaseUpdate:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return ResultPleaseUpdate
		case serveSessionEnded:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return ResultSessionEnded
		default: // serveDropped
			conn.CloseNow()
			c.emit(ctx, Reconnecting)
			if !c.sleep(ctx, attempt) {
				return ResultContextDone
			}
			attempt++
		}
	}
}

// SendState writes msg (a proto.Ready / proto.Busy value) as one text frame on
// the live connection. With no live connection it returns ErrNotConnected and
// the caller relies on the next Connected event to re-push.
func (c *Client) SendState(ctx context.Context, msg any) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	return c.writeFrame(ctx, conn, msg)
}

// SendChat writes m as one text frame on the live connection. With no live
// connection it returns ErrNotConnected; v1 does not queue or retry the line
// (the optimistic local echo already showed it), so the caller just logs a
// content-free failure. The backend relays it to the paired peer only and never
// echoes it back.
func (c *Client) SendChat(ctx context.Context, m proto.ChatMsg) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	return c.writeFrame(ctx, conn, m)
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dctx, c.url, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// serveResult is the reason serve returned.
type serveResult int

const (
	serveDropped serveResult = iota
	serveContextDone
	servePleaseUpdate
	serveSessionEnded
)

// serve reads frames on a dedicated goroutine (so an outbound write and an
// inbound session_ended can never deadlock, the same shape as the backend's
// serveConn) until the context is cancelled, a read fails, or a terminal
// inbound frame arrives. A proto.Queued / proto.Matched / proto.ChatMsg /
// proto.SessionEnded is handed to the caller on its channel and serving
// continues; every other non-terminal inbound frame is discarded.
//
// session_ended is surfaced but not terminal: a peer left a chat, the socket
// is still ours. It only ends serve (as serveSessionEnded) when a read error
// follows within sessionEndedCloseGrace — the server closed the socket right
// after the frame, i.e. a takeover or a shutdown. A later unrelated drop is an
// ordinary serveDropped and reconnects; a fresh queued/matched clears the arm.
func (c *Client) serve(ctx context.Context, conn *websocket.Conn) serveResult {
	readErr := make(chan struct{}, 1)
	terminal := make(chan serveResult, 1)

	// Captured once here so the read goroutine — which can outlive serve's
	// return, unblocking from conn.Read only after ctx is cancelled — never
	// races a test mutating the package var during teardown.
	grace := sessionEndedCloseGrace

	go func() {
		// endedAt is set when a session_ended is surfaced and arms a window in
		// which a following socket close is read as "the server ended this
		// connection" (takeover / shutdown) rather than an ordinary drop. Any
		// later frame — decodable or not — proves the socket still live and
		// disarms it; a fresh session_ended re-arms.
		var endedAt time.Time
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				if ctx.Err() == nil && !endedAt.IsZero() && time.Since(endedAt) < grace {
					// The server closed the socket right after a session_ended:
					// a takeover or a shutdown. End Run, do not reconnect. (A
					// ctx cancellation is a clean shutdown and takes the
					// serveContextDone path instead.)
					terminal <- serveSessionEnded
					return
				}
				select {
				case readErr <- struct{}{}:
				default:
				}
				return
			}
			endedAt = time.Time{}
			msg, derr := proto.Decode(data)
			if derr != nil {
				continue // undecodable frame — discard
			}
			switch m := msg.(type) {
			case proto.Queued:
				// Not terminal: hand it to run.loop and keep serving. The send
				// is non-blocking — a redelivered queued after a reconnect is a
				// no-op once the spinner is up, and a terminal frame queued
				// behind it must not wait.
				select {
				case c.queued <- m:
				default:
				}
			case proto.Matched:
				// Not terminal: hand it to run.loop and keep serving. The send
				// is non-blocking — the channel is buffered by one, and if
				// run.loop has not drained a prior matched yet, dropping this
				// one is correct (run.loop ignores a second matched while a chat
				// is up). The reader must not stall here or a terminal frame
				// behind it would wait.
				select {
				case c.matched <- m:
				default:
				}
			case proto.ChatMsg:
				// Not terminal: hand the peer line to run.loop and keep
				// serving. Non-blocking for the same reason as matched — a
				// terminal frame queued behind it must not wait, and v1 has no
				// ack so drop-on-full is acceptable.
				select {
				case c.chatMsg <- m:
				default:
				}
			case proto.PleaseUpdate:
				terminal <- servePleaseUpdate
				return
			case proto.SessionEnded:
				// Not terminal on its own: surface it and keep serving. If the
				// server closes the socket next (within the grace), the read
				// error above turns this into serveSessionEnded.
				endedAt = time.Now()
				select {
				case c.sessionEnded <- m:
				default:
				}
			default:
				// every other v1 frame is ignored in this epic
			}
		}
	}()

	select {
	case <-ctx.Done():
		return serveContextDone
	case <-readErr:
		return serveDropped
	case r := <-terminal:
		return r
	}
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) writeFrame(ctx context.Context, conn *websocket.Conn, msg any) error {
	b, err := proto.Encode(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, frameWriteTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

// sleep waits out one backoff delay. It returns false if the context is
// cancelled first.
func (c *Client) sleep(ctx context.Context, attempt int) bool {
	d := backoffDelay(attempt)
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// backoffDelay is min(cap, base*2^attempt) with full jitter: a uniform random
// pick in [0, that ceiling).
func backoffDelay(attempt int) time.Duration {
	ceil := backoffBase
	for i := 0; i < attempt; i++ {
		ceil *= 2
		if ceil >= backoffCap {
			ceil = backoffCap
			break
		}
	}
	if ceil > backoffCap {
		ceil = backoffCap
	}
	if ceil <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(ceil)))
}
