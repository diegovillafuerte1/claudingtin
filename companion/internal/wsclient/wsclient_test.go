package wsclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/proto"
)

// shrinkBackoff makes the reconnect delay tiny so the timing-sensitive cases
// run fast. Tests here never run in parallel, so mutating the package vars is
// safe.
func shrinkBackoff(t *testing.T) {
	t.Helper()
	ob, oc, od := backoffBase, backoffCap, dialTimeout
	backoffBase = 2 * time.Millisecond
	backoffCap = 20 * time.Millisecond
	dialTimeout = 2 * time.Second
	t.Cleanup(func() { backoffBase, backoffCap, dialTimeout = ob, oc, od })
}

// wsServer is a coder/websocket test server that records every frame it reads
// per accepted connection. onConn scripts a connection; dropAfterHello, when it
// returns true for a connection index, closes that connection the instant its
// hello is seen (deterministic — no sleeps). All hook fields are fixed at
// construction so no synchronisation is needed to read them.
type wsServer struct {
	ts *httptest.Server

	onConn         func(connIndex int, c *websocket.Conn)
	dropAfterHello func(connIndex int) bool

	mu       sync.Mutex
	conns    int
	received [][]any     // one slice of decoded frames per accepted connection
	helloAt  []time.Time // wall-clock time each connection's hello was read
}

func newWSServer(t *testing.T) *wsServer {
	t.Helper()
	return newWSServerCfg(t, &wsServer{})
}

func newWSServerWith(t *testing.T, onConn func(connIndex int, c *websocket.Conn)) *wsServer {
	t.Helper()
	return newWSServerCfg(t, &wsServer{onConn: onConn})
}

func newWSServerDropping(t *testing.T, dropAfterHello func(connIndex int) bool) *wsServer {
	t.Helper()
	return newWSServerCfg(t, &wsServer{dropAfterHello: dropAfterHello})
}

func newWSServerCfg(t *testing.T, s *wsServer) *wsServer {
	t.Helper()
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()

		s.mu.Lock()
		idx := s.conns
		s.conns++
		s.received = append(s.received, nil)
		s.mu.Unlock()

		done := make(chan struct{})
		if s.onConn != nil {
			go func() {
				s.onConn(idx, c)
				close(done)
			}()
		} else {
			close(done)
		}

		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				<-done
				return
			}
			msg, derr := proto.Decode(data)
			if derr != nil {
				continue
			}
			s.mu.Lock()
			s.received[idx] = append(s.received[idx], msg)
			if _, isHello := msg.(proto.Hello); isHello {
				s.helloAt = append(s.helloAt, time.Now())
			}
			s.mu.Unlock()

			if _, isHello := msg.(proto.Hello); isHello && s.dropAfterHello != nil && s.dropAfterHello(idx) {
				_ = c.Close(websocket.StatusNormalClosure, "")
				<-done
				return
			}
		}
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func (s *wsServer) helloTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Time, len(s.helloAt))
	copy(out, s.helloAt)
	return out
}

func (s *wsServer) url() string {
	return "ws" + strings.TrimPrefix(s.ts.URL, "http") + "/ws"
}

func (s *wsServer) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *wsServer) framesFor(idx int) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= len(s.received) {
		return nil
	}
	out := make([]any, len(s.received[idx]))
	copy(out, s.received[idx])
	return out
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func runClient(t *testing.T, c *Client) (context.CancelFunc, <-chan Result) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan Result, 1)
	done := make(chan struct{})
	go func() {
		res <- c.Run(ctx)
		close(done)
	}()
	// Cleanup must join the client goroutine before shrinkBackoff's own
	// cleanup restores the package knobs it reads.
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel, res
}

// drainEvents keeps c.Events() drained in the background for tests that don't
// inspect events but would otherwise wedge the client on the now-blocking emit
// once the channel buffer fills. Call it before runClient so its cleanup runs
// after the client has stopped.
func drainEvents(t *testing.T, c *Client) {
	t.Helper()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-c.Events():
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() { close(stop) })
}

func firstFrameIsHello(t *testing.T, frames []any, key string) {
	t.Helper()
	if len(frames) == 0 {
		t.Fatal("no frames received")
	}
	h, ok := frames[0].(proto.Hello)
	if !ok {
		t.Fatalf("first frame is %T, want proto.Hello", frames[0])
	}
	if h.AccountKey != key {
		t.Fatalf("hello account key = %q, want %q", h.AccountKey, key)
	}
	if h.ProtocolVersion != proto.PROTOCOL_VERSION {
		t.Fatalf("hello protocol version = %d, want %d", h.ProtocolVersion, proto.PROTOCOL_VERSION)
	}
}

func TestConnectSendsHelloFirst(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServer(t)
	c := New(Config{URL: s.url(), AccountKey: "acct-hello"})
	runClient(t, c)

	waitFor(t, "the hello frame", func() bool { return len(s.framesFor(0)) >= 1 })
	firstFrameIsHello(t, s.framesFor(0), "acct-hello")
}

func TestConnectedEventFires(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServer(t)
	c := New(Config{URL: s.url(), AccountKey: "k"})
	runClient(t, c)

	select {
	case ev := <-c.Events():
		if ev.Kind != Connected {
			t.Fatalf("first event = %v, want Connected", ev.Kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no Connected event")
	}
}

func TestSendStateFramesArrive(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServer(t)
	c := New(Config{URL: s.url(), AccountKey: "k"})
	runClient(t, c)

	<-c.Events() // Connected

	if err := c.SendState(context.Background(), proto.Ready{}); err != nil {
		t.Fatalf("SendState(Ready): %v", err)
	}
	if err := c.SendState(context.Background(), proto.Busy{}); err != nil {
		t.Fatalf("SendState(Busy): %v", err)
	}

	waitFor(t, "ready then busy frames", func() bool {
		f := s.framesFor(0)
		if len(f) < 3 {
			return false
		}
		_, ready := f[1].(proto.Ready)
		_, busy := f[2].(proto.Busy)
		return ready && busy
	})
}

func TestSendStateWithoutConnection(t *testing.T) {
	c := New(Config{URL: "ws://127.0.0.1:0/ws", AccountKey: "k"})
	if err := c.SendState(context.Background(), proto.Ready{}); err != ErrNotConnected {
		t.Fatalf("SendState with no connection = %v, want ErrNotConnected", err)
	}
}

func TestReconnectResendsHello(t *testing.T) {
	shrinkBackoff(t)
	// Drop connection 0 the instant its hello arrives — deterministic.
	s := newWSServerDropping(t, func(idx int) bool { return idx == 0 })
	c := New(Config{URL: s.url(), AccountKey: "acct-rc"})
	runClient(t, c)

	waitFor(t, "a second connection", func() bool { return s.connCount() >= 2 })
	waitFor(t, "hello on the second connection", func() bool { return len(s.framesFor(1)) >= 1 })
	firstFrameIsHello(t, s.framesFor(0), "acct-rc")
	firstFrameIsHello(t, s.framesFor(1), "acct-rc")
}

func TestReconnectEmitsReconnectingEvent(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServerDropping(t, func(idx int) bool { return idx == 0 })
	c := New(Config{URL: s.url(), AccountKey: "k"})
	runClient(t, c)

	deadline := time.After(3 * time.Second)
	sawReconnecting := false
	for !sawReconnecting {
		select {
		case ev := <-c.Events():
			if ev.Kind == Reconnecting {
				sawReconnecting = true
			}
		case <-deadline:
			t.Fatal("never saw a Reconnecting event")
		}
	}
}

func TestPleaseUpdateStopsRetries(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServerWith(t, func(idx int, conn *websocket.Conn) {
		b, _ := proto.Encode(proto.PleaseUpdate{})
		_ = conn.Write(context.Background(), websocket.MessageText, b)
	})
	c := New(Config{URL: s.url(), AccountKey: "k"})
	_, res := runClient(t, c)

	select {
	case got := <-res:
		if got != ResultPleaseUpdate {
			t.Fatalf("Run returned %v, want ResultPleaseUpdate", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after please_update")
	}

	// No further reconnect attempts. With the shrunk backoff (cap 20ms) a
	// reconnect would show well within this window.
	after := s.connCount()
	time.Sleep(200 * time.Millisecond)
	if s.connCount() != after {
		t.Fatalf("client kept reconnecting after please_update: %d → %d", after, s.connCount())
	}
}

func TestSessionEndedEndsClient(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServerWith(t, func(idx int, conn *websocket.Conn) {
		b, _ := proto.Encode(proto.SessionEnded{})
		_ = conn.Write(context.Background(), websocket.MessageText, b)
	})
	c := New(Config{URL: s.url(), AccountKey: "k"})
	_, res := runClient(t, c)

	select {
	case got := <-res:
		if got != ResultSessionEnded {
			t.Fatalf("Run returned %v, want ResultSessionEnded", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after session_ended")
	}
}

// TestMatchedSurfacedToCaller: the server sends a proto.Matched; the client
// delivers it on Matched() with every field intact, keeps serving afterwards
// (a following non-terminal frame is still ignored, Run does not return), and
// nothing is logged (wsclient has no logger by construction).
func TestMatchedSurfacedToCaller(t *testing.T) {
	shrinkBackoff(t)

	want := proto.Matched{
		SessionID: "sess-abc123",
		Pseudonym: "kestrel",
		Blurb:     "collects maps",
		Opener:    "what's the last thing that made you laugh?",
	}
	s := newWSServerWith(t, func(idx int, conn *websocket.Conn) {
		mb, _ := proto.Encode(want)
		_ = conn.Write(context.Background(), websocket.MessageText, mb)
		// A later non-terminal frame that must still be ignored.
		qb, _ := proto.Encode(proto.Queued{})
		_ = conn.Write(context.Background(), websocket.MessageText, qb)
	})
	c := New(Config{URL: s.url(), AccountKey: "k"})
	drainEvents(t, c)
	_, res := runClient(t, c)

	select {
	case got := <-c.Matched():
		if got != want {
			t.Fatalf("Matched() delivered %+v, want %+v", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no value on Matched() after the server sent one")
	}

	// Run keeps going: the Queued frame did not end it.
	select {
	case r := <-res:
		t.Fatalf("Run returned %v after a matched + queued; it must keep serving", r)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestContextCancelStopsRun(t *testing.T) {
	shrinkBackoff(t)
	s := newWSServer(t)
	c := New(Config{URL: s.url(), AccountKey: "k"})
	cancel, res := runClient(t, c)

	<-c.Events() // Connected
	cancel()

	select {
	case got := <-res:
		if got != ResultContextDone {
			t.Fatalf("Run returned %v, want ResultContextDone", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestContextCancelDuringBackoff(t *testing.T) {
	shrinkBackoff(t)
	// Nothing is listening: every dial fails and the client sits in backoff.
	c := New(Config{URL: "ws://127.0.0.1:1/ws", AccountKey: "k"})
	cancel, res := runClient(t, c)

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case got := <-res:
		if got != ResultContextDone {
			t.Fatalf("Run returned %v, want ResultContextDone", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel during backoff")
	}
}

func TestBackoffDelayBounds(t *testing.T) {
	ob, oc := backoffBase, backoffCap
	backoffBase = 500 * time.Millisecond
	backoffCap = 30 * time.Second
	defer func() { backoffBase, backoffCap = ob, oc }()

	for attempt := 0; attempt < 12; attempt++ {
		ceil := backoffBase << attempt
		if ceil > backoffCap || ceil <= 0 {
			ceil = backoffCap
		}
		for i := 0; i < 50; i++ {
			d := backoffDelay(attempt)
			if d < 0 || d >= ceil {
				t.Fatalf("attempt %d: delay %v outside [0, %v)", attempt, d, ceil)
			}
		}
	}
}

// TestBackoffDefaults pins the shipped knobs so a units typo (e.g. 500*Second)
// fails a test rather than silently changing behaviour.
func TestBackoffDefaults(t *testing.T) {
	if backoffBase != 500*time.Millisecond {
		t.Fatalf("shipped backoffBase = %v, want 500ms", backoffBase)
	}
	if backoffCap != 30*time.Second {
		t.Fatalf("shipped backoffCap = %v, want 30s", backoffCap)
	}
}

// TestBackoffResetsAfterHello proves the backoff attempt counter is reset after
// every successful hello, not carried across drops: with the counter reset,
// every inter-hello gap is a single backoffDelay(0) ∈ [0, backoffBase) plus
// dial time and never grows with the number of prior drops. Without the reset,
// each successive gap's ceiling would double.
func TestBackoffResetsAfterHello(t *testing.T) {
	ob, oc, od := backoffBase, backoffCap, dialTimeout
	backoffBase = 30 * time.Millisecond
	backoffCap = 10 * time.Second // never clamps during this test
	dialTimeout = 2 * time.Second
	t.Cleanup(func() { backoffBase, backoffCap, dialTimeout = ob, oc, od })

	const drops = 6
	s := newWSServerDropping(t, func(idx int) bool { return idx < drops })
	c := New(Config{URL: s.url(), AccountKey: "k"})
	drainEvents(t, c) // 13 lifecycle events > buffer; must be drained
	runClient(t, c)

	waitFor(t, "all reconnects", func() bool { return len(s.helloTimes()) >= drops+1 })
	ts := s.helloTimes()

	limit := 5 * backoffBase // generous headroom for CI scheduling
	for i := 1; i < len(ts); i++ {
		if gap := ts[i].Sub(ts[i-1]); gap > limit {
			t.Fatalf("hello gap %d→%d = %v, want < %v (backoff not reset after hello?)", i-1, i, gap, limit)
		}
	}
}

// TestEventsNotDroppedUnderSlowDrain proves emit delivery is guaranteed: even
// when the consumer drains Events() slowly, a burst of reconnects that exceeds
// the channel buffer loses no Connected event (the old best-effort emit would
// silently drop the overflow).
func TestEventsNotDroppedUnderSlowDrain(t *testing.T) {
	ob, oc, od := backoffBase, backoffCap, dialTimeout
	backoffBase = 2 * time.Millisecond
	backoffCap = 10 * time.Millisecond
	dialTimeout = 2 * time.Second
	t.Cleanup(func() { backoffBase, backoffCap, dialTimeout = ob, oc, od })

	const drops = 5 // 5 Connected + 5 Reconnecting = 10 events > buffer (8)
	s := newWSServerDropping(t, func(idx int) bool { return idx < drops })
	c := New(Config{URL: s.url(), AccountKey: "k"})
	runClient(t, c)

	connected := 0
	deadline := time.After(3 * time.Second)
	for connected < drops+1 { // drops reconnects + the final stable connect
		select {
		case ev := <-c.Events():
			if ev.Kind == Connected {
				connected++
			}
			time.Sleep(15 * time.Millisecond) // slow drain
		case <-deadline:
			t.Fatalf("only saw %d Connected events, want %d — emit dropped some", connected, drops+1)
		}
	}
}
