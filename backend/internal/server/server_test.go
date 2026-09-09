package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/backend"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// syncBuffer is an io.Writer safe for the concurrent writes slog does from
// multiple goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// harness is a running backend: a hub, an httptest server, and the captured log.
type harness struct {
	hub  *hub.Hub
	ts   *httptest.Server
	logs *syncBuffer
}

// newHarness stands up the backend through the real constructor (server.New),
// so its defaultHelloDeadline and nil-logger wiring are actually exercised.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h, logs := runHub(t)
	return startHarness(t, h, logs, New(h, loggerTo(logs)))
}

// newHarnessWithDeadline builds the handler from the struct literal so the hello
// deadline can be overridden for the timing-sensitive cases.
func newHarnessWithDeadline(t *testing.T, helloDeadline time.Duration) *harness {
	t.Helper()
	h, logs := runHub(t)
	srv := &server{hub: h, logger: loggerTo(logs), helloDeadline: helloDeadline}
	return startHarness(t, h, logs, srv.routes())
}

func loggerTo(logs *syncBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func runHub(t *testing.T) (*hub.Hub, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	h := hub.New()
	hubCtx, cancelHub := context.WithCancel(context.Background())
	hubDone := make(chan struct{})
	go func() {
		h.Run(hubCtx)
		close(hubDone)
	}()
	t.Cleanup(func() {
		cancelHub()
		<-hubDone
	})
	return h, logs
}

func startHarness(t *testing.T, h *hub.Hub, logs *syncBuffer, handler http.Handler) *harness {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &harness{hub: h, ts: ts, logs: logs}
}

func (h *harness) wsURL() string {
	return "ws" + strings.TrimPrefix(h.ts.URL, "http")
}

func (h *harness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, h.wsURL()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func writeMsg(t *testing.T, c *websocket.Conn, msg any) {
	t.Helper()
	b, err := proto.Encode(msg)
	if err != nil {
		t.Fatalf("encode %T: %v", msg, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write %T: %v", msg, err)
	}
}

func writeRaw(t *testing.T, c *websocket.Conn, raw string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		t.Fatalf("write raw: %v", err)
	}
}

func writeBinary(t *testing.T, c *websocket.Conn, b []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, b); err != nil {
		t.Fatalf("write binary: %v", err)
	}
}

// readMsg reads one frame and decodes it as a proto message.
func readMsg(t *testing.T, c *websocket.Conn) any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	msg, err := proto.Decode(data)
	if err != nil {
		t.Fatalf("decode frame %q: %v", string(data), err)
	}
	return msg
}

// readRaw reads one frame and returns its bytes undecoded, for byte-identity
// assertions on the wire form.
func readRaw(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return data
}

// expectOpenIdle asserts the socket is still open: a short read neither returns
// a frame nor a close, it simply times out on our own context. Side effect —
// coder/websocket closes the client conn once this read's context expires, so
// call it last and only after any hub.Count assertions.
func expectOpenIdle(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("expected the socket open and idle, but a frame arrived")
	}
	if ctx.Err() == nil {
		t.Fatalf("expected the socket open (idle read timeout), got a live error: %v", err)
	}
}

// expectClose asserts the next read fails with a clean close carrying want.
func expectClose(t *testing.T, c *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue // a queued data frame before the close; keep reading
		}
		if got := websocket.CloseStatus(err); got != want {
			t.Fatalf("close status = %d, want %d (err %v)", got, want, err)
		}
		return
	}
}

// expectCloseNoData asserts the very next frame is a clean close carrying want —
// with no data frame in between. Use after reading the single frame a path is
// allowed to send, to prove it sent exactly one.
func expectCloseNoData(t *testing.T, c *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("expected a close, got another data frame: %q", string(data))
	}
	if got := websocket.CloseStatus(err); got != want {
		t.Fatalf("close status = %d, want %d (err %v)", got, want, err)
	}
}

// expectClosed asserts the socket is closed, without pinning the close code
// (used where the spec says only "the connection is closed").
func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			t.Fatalf("socket still open after deadline: %v", err)
		}
		return
	}
}

func waitCount(t *testing.T, h *hub.Hub, want int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if h.Count() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("hub.Count never reached %d (last %d)", want, h.Count())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func helloFor(key string, pv int) proto.Hello {
	return proto.Hello{AccountKey: key, ProtocolVersion: pv}
}

// readMatched reads frames from c until it sees a matched frame (a queued frame
// may legitimately arrive first) and returns it.
func readMatched(t *testing.T, c *websocket.Conn) proto.Matched {
	t.Helper()
	for i := 0; i < 3; i++ {
		switch m := readMsg(t, c).(type) {
		case proto.Matched:
			return m
		case proto.Queued:
			continue
		default:
			t.Fatalf("expected matched or queued, got %T", m)
		}
	}
	t.Fatal("never received a matched frame")
	return proto.Matched{}
}

// --- matrix rows ---------------------------------------------------------------

// Only the accepted path registers the key, so a rising Count already proves no
// please_update was sent; expectOpenIdle then proves the socket is still open.
func TestHelloSupported_CurrentVersion(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, helloFor("acct-1", proto.PROTOCOL_VERSION))

	waitCount(t, h.hub, 1)
	expectOpenIdle(t, c)
}

func TestHelloSupported_NewerVersionAccepted(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, helloFor("acct-future", proto.PROTOCOL_VERSION+5))

	waitCount(t, h.hub, 1)
	expectOpenIdle(t, c)
}

func TestHelloSupported_OneVersionOldAccepted(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, helloFor("acct-prev", proto.PROTOCOL_VERSION-1))

	waitCount(t, h.hub, 1)
	expectOpenIdle(t, c)
}

func TestHelloTooOld_PleaseUpdateThenClose(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, helloFor("acct-old", proto.PROTOCOL_VERSION-2))

	if _, ok := readMsg(t, c).(proto.PleaseUpdate); !ok {
		t.Fatal("expected a please_update frame")
	}
	// Exactly one please_update, then the close — no second frame.
	expectCloseNoData(t, c, websocket.StatusNormalClosure)

	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0 (too-old client must not register)", got)
	}
}

func TestBadFirstFrame_NotHello(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, proto.Ready{}) // decodable, wrong type

	msg := readMsg(t, c)
	errFrame, ok := msg.(proto.Error)
	if !ok {
		t.Fatalf("expected an error frame, got %T", msg)
	}
	if errFrame.Code != "expected_hello" {
		t.Fatalf("error code = %q, want expected_hello", errFrame.Code)
	}
	expectClose(t, c, websocket.StatusNormalClosure)
	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0", got)
	}
}

func TestBadFirstFrame_Undecodable(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeRaw(t, c, `{"type":`) // truncated JSON

	msg := readMsg(t, c)
	errFrame, ok := msg.(proto.Error)
	if !ok {
		t.Fatalf("expected an error frame, got %T", msg)
	}
	if errFrame.Code != "bad_frame" {
		t.Fatalf("error code = %q, want bad_frame", errFrame.Code)
	}
	expectClose(t, c, websocket.StatusNormalClosure)
	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0", got)
	}
}

func TestBadFirstFrame_UnknownType(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeRaw(t, c, `{"type":"no_such_type","v":1}`)

	msg := readMsg(t, c)
	errFrame, ok := msg.(proto.Error)
	if !ok {
		t.Fatalf("expected an error frame, got %T", msg)
	}
	if errFrame.Code != "bad_frame" {
		t.Fatalf("error code = %q, want bad_frame", errFrame.Code)
	}
	expectClose(t, c, websocket.StatusNormalClosure)
	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0", got)
	}
}

func TestHelloEmptyAccountKey_Rejected(t *testing.T) {
	h := newHarness(t)
	c := h.dial(t)

	writeMsg(t, c, helloFor("", proto.PROTOCOL_VERSION))

	msg := readMsg(t, c)
	errFrame, ok := msg.(proto.Error)
	if !ok {
		t.Fatalf("expected an error frame, got %T", msg)
	}
	if errFrame.Code != "bad_frame" {
		t.Fatalf("error code = %q, want bad_frame", errFrame.Code)
	}
	expectClose(t, c, websocket.StatusNormalClosure)
	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0 (empty key must not register)", got)
	}
}

func TestNoFirstFrame_ClosedAfterDeadline(t *testing.T) {
	h := newHarnessWithDeadline(t, 150*time.Millisecond)
	c := h.dial(t)

	// Send nothing. The server must close the socket once the deadline passes.
	expectClosed(t, c)
	if got := h.hub.Count(); got != 0 {
		t.Fatalf("hub.Count = %d, want 0", got)
	}
}

func TestTakeover_PriorConnEndedNewConnActive(t *testing.T) {
	h := newHarness(t)

	connA := h.dial(t)
	writeMsg(t, connA, helloFor("acct-K", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)

	connB := h.dial(t)
	writeMsg(t, connB, helloFor("acct-K", proto.PROTOCOL_VERSION))

	// connA is told its session ended exactly once, then closed normally.
	if _, ok := readMsg(t, connA).(proto.SessionEnded); !ok {
		t.Fatal("connA: expected a session_ended frame on takeover")
	}
	expectCloseNoData(t, connA, websocket.StatusNormalClosure)

	// The key is not double-counted, and connB stays open.
	if got := h.hub.Count(); got != 1 {
		t.Fatalf("hub.Count = %d, want 1 after takeover", got)
	}
	expectOpenIdle(t, connB)
}

func TestClientVanishes_KeyRemoved(t *testing.T) {
	h := newHarness(t)

	c := h.dial(t)
	writeMsg(t, c, helloFor("acct-drop", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)

	_ = c.CloseNow() // drop without a leave / clean close

	waitCount(t, h.hub, 0)
}

func TestPostHelloFramesDiscardedNotRegisteredTwice(t *testing.T) {
	h := newHarness(t)

	c := h.dial(t)
	writeMsg(t, c, helloFor("acct-chatty", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)

	// A second hello, a chat_msg from an unpaired conn (the hub relays it only
	// to a paired peer, so here it no-ops), a heartbeat, raw garbage text, and a
	// binary frame are all read and dropped — no state change, no reply.
	// (ready / busy do have meaning now and are covered by the match tests.)
	writeMsg(t, c, helloFor("acct-chatty", proto.PROTOCOL_VERSION))
	writeMsg(t, c, proto.ChatMsg{ClientMsgID: "m1", Text: "ignored"})
	writeMsg(t, c, proto.Heartbeat{})
	writeRaw(t, c, `{"type":`)
	writeBinary(t, c, []byte{0x00, 0x01, 0x02, 0xff})

	if got := h.hub.Count(); got != 1 {
		t.Fatalf("hub.Count = %d, want 1", got)
	}
	expectOpenIdle(t, c)
}

// --- FIFO queue + pairing over the wire --------------------------------------

func TestTwoReadyDialsMatchWithEqualSessionID(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	b := h.dial(t)
	writeMsg(t, b, helloFor("acct-b", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)

	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})

	ma := readMatched(t, a)
	mb := readMatched(t, b)

	if ma.SessionID == "" {
		t.Fatal("matched session_id is empty")
	}
	if ma.SessionID != mb.SessionID {
		t.Fatalf("session_id differs: %q vs %q", ma.SessionID, mb.SessionID)
	}
	if ma.Pseudonym != "" || ma.Blurb != "" {
		t.Fatalf("pseudonym/blurb must be empty in Epic 2: %#v", ma)
	}
	if ma.Opener == "" {
		t.Fatal("matched opener is empty")
	}
	if ma.Opener != mb.Opener {
		t.Fatalf("opener differs between peers over the wire: %q vs %q", ma.Opener, mb.Opener)
	}
	if !slices.Contains(backend.Openers(), ma.Opener) {
		t.Fatalf("opener %q is not in the curated set", ma.Opener)
	}
}

func TestThirdReadyDialGetsQueued(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	b := h.dial(t)
	writeMsg(t, b, helloFor("acct-b", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)
	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})
	readMatched(t, a)
	readMatched(t, b)

	c := h.dial(t)
	writeMsg(t, c, helloFor("acct-c", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 3)
	writeMsg(t, c, proto.Ready{})

	if _, ok := readMsg(t, c).(proto.Queued); !ok {
		t.Fatal("third dial: expected a queued frame")
	}
}

func TestQueuedDialClosingIsSilentAndUncounted(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)
	writeMsg(t, a, proto.Ready{})
	if _, ok := readMsg(t, a).(proto.Queued); !ok {
		t.Fatal("expected a queued frame")
	}

	_ = a.CloseNow() // drop while queued — must be silent

	waitCount(t, h.hub, 0)
}

func TestPairedDialClosingDeliversSessionEndedToPeer(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	b := h.dial(t)
	writeMsg(t, b, helloFor("acct-b", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)
	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})
	readMatched(t, a)
	readMatched(t, b)

	_ = a.CloseNow() // A's socket drops while paired

	if _, ok := readMsg(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A dropped")
	}
	waitCount(t, h.hub, 1)
}

func TestBusyWhilePairedDeliversSessionEndedToPeer(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	b := h.dial(t)
	writeMsg(t, b, helloFor("acct-b", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)
	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})
	readMatched(t, a)
	readMatched(t, b)

	writeMsg(t, a, proto.Busy{})

	if _, ok := readMsg(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A went busy")
	}
	// Both sockets stay open; nobody is re-enqueued or evicted.
	if got := h.hub.Count(); got != 2 {
		t.Fatalf("hub.Count = %d, want 2", got)
	}
}

// TestSessionEndedBytesIdenticalAcrossCauses drives every exercisable
// backend-side end cause and asserts the peer's session_ended frame is
// byte-identical across all of them and equal to the canonical no-session_id /
// no-cause frame.
func TestSessionEndedBytesIdenticalAcrossCauses(t *testing.T) {
	const canonical = `{"type":"session_ended","v":1}`

	enc, err := proto.Encode(proto.SessionEnded{})
	if err != nil {
		t.Fatalf("encode SessionEnded: %v", err)
	}
	if string(enc) != canonical {
		t.Fatalf("proto.Encode(SessionEnded{}) = %q, want canonical %q", string(enc), canonical)
	}

	frames := map[string][]byte{}

	// Peer goes busy.
	func() {
		h := newHarness(t)
		a, b := matchedPair(t, h, "acct-a", "acct-b")
		writeMsg(t, a, proto.Busy{})
		frames["busy"] = readRaw(t, b)
	}()

	// Peer disconnects.
	func() {
		h := newHarness(t)
		a, b := matchedPair(t, h, "acct-a", "acct-b")
		_ = a.CloseNow()
		frames["disconnect"] = readRaw(t, b)
	}()

	// Peer leaves.
	func() {
		h := newHarness(t)
		a, b := matchedPair(t, h, "acct-a", "acct-b")
		writeMsg(t, a, proto.Leave{})
		frames["leave"] = readRaw(t, b)
	}()

	// Connection takeover: A (key acct-K) is paired with B; a new hello for
	// acct-K takes A over, and B — A's peer — is notified via teardownPair.
	func() {
		h := newHarness(t)
		_, b := matchedPair(t, h, "acct-K", "acct-b")
		a2 := h.dial(t)
		writeMsg(t, a2, helloFor("acct-K", proto.PROTOCOL_VERSION))
		frames["takeover"] = readRaw(t, b)
	}()

	for cause, got := range frames {
		if string(got) != canonical {
			t.Fatalf("%s: session_ended bytes = %q, want %q", cause, string(got), canonical)
		}
	}
}

// TestSessionEndedIsFinalFrameWithRacedChatMsg races a chat_msg against the end
// for busy, leave, and peer-disconnect: the peer may receive that chat_msg
// before session_ended or not at all, but never after, and session_ended is the
// last frame on that session.
func TestSessionEndedIsFinalFrameWithRacedChatMsg(t *testing.T) {
	ends := map[string]func(t *testing.T, a *websocket.Conn){
		"busy":       func(t *testing.T, a *websocket.Conn) { writeMsg(t, a, proto.Busy{}) },
		"leave":      func(t *testing.T, a *websocket.Conn) { writeMsg(t, a, proto.Leave{}) },
		"disconnect": func(t *testing.T, a *websocket.Conn) { _ = a.CloseNow() },
	}
	for name, endFn := range ends {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			a, b := matchedPair(t, h, "acct-a", "acct-b")

			writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c1", Text: "last words"})
			endFn(t, a)

			sawEnd := false
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
				_, data, err := b.Read(ctx)
				cancel()
				if err != nil {
					break // idle timeout or socket close: no more frames
				}
				msg, derr := proto.Decode(data)
				if derr != nil {
					t.Fatalf("decode %q: %v", string(data), derr)
				}
				switch msg.(type) {
				case proto.ChatMsg:
					if sawEnd {
						t.Fatal("chat_msg arrived AFTER session_ended")
					}
				case proto.SessionEnded:
					if sawEnd {
						t.Fatal("received a second session_ended")
					}
					sawEnd = true
				default:
					t.Fatalf("unexpected frame %T", msg)
				}
			}
			if !sawEnd {
				t.Fatal("never received session_ended")
			}
		})
	}
}

// TestTakeoverVictimThatIsAlsoPeerGetsExactlyOneSessionEnded covers the handler
// dedupe: a connection that is taken over while it is also the peer of a session
// ending concurrently writes exactly one session_ended, then closes.
func TestTakeoverVictimThatIsAlsoPeerGetsExactlyOneSessionEnded(t *testing.T) {
	h := newHarness(t)

	// victim (key acct-K) is paired with peer.
	victim, peer := matchedPair(t, h, "acct-K", "acct-P")

	// Concurrently: the peer ends the session AND a new connection takes over
	// acct-K. The peer write runs on its own goroutine (no *testing.T use there).
	busyFrame, err := proto.Encode(proto.Busy{})
	if err != nil {
		t.Fatalf("encode Busy: %v", err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = peer.Write(ctx, websocket.MessageText, busyFrame)
	}()

	a2 := h.dial(t)
	writeMsg(t, a2, helloFor("acct-K", proto.PROTOCOL_VERSION))

	// Exactly one session_ended, then a clean close with no second data frame.
	if _, ok := readMsg(t, victim).(proto.SessionEnded); !ok {
		t.Fatal("victim: expected a session_ended frame")
	}
	expectCloseNoData(t, victim, websocket.StatusNormalClosure)

	// The replacement proceeds to the queue normally.
	writeMsg(t, a2, proto.Ready{})
	if _, ok := readMsg(t, a2).(proto.Queued); !ok {
		t.Fatal("replacement: expected a queued frame")
	}
}

// TestSessionEndedGuardReArmsOnRematch covers the `case proto.Matched: sentEnd
// = false` arm in serveConn: once a session has ended on a socket, a fresh match
// on that same socket must re-arm the per-session guard so the NEXT end still
// delivers a session_ended. This fails if that re-arm line is removed.
func TestSessionEndedGuardReArmsOnRematch(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	// First end: A leaves; B drains its session_ended (arms B's sentEnd guard).
	writeMsg(t, a, proto.Leave{})
	if _, ok := readMsg(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected session_ended after A left the first session")
	}

	// Re-ready BOTH on the same connections; they re-match each other.
	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})
	readMatched(t, a)
	readMatched(t, b)

	// End the SECOND session the same way: B must receive a SECOND
	// session_ended on that same socket — proving the guard re-armed on the
	// intervening proto.Matched.
	writeMsg(t, a, proto.Leave{})
	if _, ok := readMsg(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a second session_ended after the re-match ended — the sentEnd guard did not re-arm on proto.Matched")
	}
}

func TestBusyFrameFromQueuedDialIsSilent(t *testing.T) {
	h := newHarness(t)

	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)
	writeMsg(t, a, proto.Ready{})
	if _, ok := readMsg(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected a queued frame")
	}

	// A bare busy with no prior ready is also harmless.
	b := h.dial(t)
	writeMsg(t, b, helloFor("acct-b", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)
	writeMsg(t, b, proto.Busy{})

	// A leaves the queue via busy: no error, no frame, socket stays open.
	writeMsg(t, a, proto.Busy{})
	if got := h.hub.Count(); got != 2 {
		t.Fatalf("hub.Count = %d, want 2 (busy must not evict either connection)", got)
	}

	// A fresh dial that readies now only gets queued — proving A was removed
	// from the queue, not sitting there waiting to be matched.
	c := h.dial(t)
	writeMsg(t, c, helloFor("acct-c", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 3)
	writeMsg(t, c, proto.Ready{})
	if _, ok := readMsg(t, c).(proto.Queued); !ok {
		t.Fatal("C: expected a queued frame (A must have left the queue)")
	}

	// Both earlier dials are still open and silent.
	expectOpenIdle(t, b)
	expectOpenIdle(t, a)
}

// --- chat relay over the wire ----------------------------------------------

// matchedPair dials two clients, readies both, drains their matched frames, and
// returns the two conns.
func matchedPair(t *testing.T, h *harness, keyA, keyB string) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	a := h.dial(t)
	writeMsg(t, a, helloFor(keyA, proto.PROTOCOL_VERSION))
	b := h.dial(t)
	writeMsg(t, b, helloFor(keyB, proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 2)
	writeMsg(t, a, proto.Ready{})
	writeMsg(t, b, proto.Ready{})
	readMatched(t, a)
	readMatched(t, b)
	return a, b
}

// expectNoFrame fails if any frame arrives within d; a plain read timeout on our
// own context is the pass.
func expectNoFrame(t *testing.T, c *websocket.Conn, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("expected no frame, got %q", string(data))
	}
	if ctx.Err() == nil {
		t.Fatalf("expected an idle read timeout, got a live error: %v", err)
	}
}

func TestChatMsgRelayedToPeerNeverEchoedToSender(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c1", Text: "hey"})

	got, ok := readMsg(t, b).(proto.ChatMsg)
	if !ok {
		t.Fatalf("B: expected a chat_msg, got %#v", got)
	}
	if got.ClientMsgID != "c1" || got.Text != "hey" {
		t.Fatalf("B received %#v, want {c1, hey}", got)
	}

	// The sender gets nothing back.
	expectNoFrame(t, a, 300*time.Millisecond)
}

func TestChatMsgNotEchoedOverFiveSends(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	for i := 0; i < 5; i++ {
		writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c", Text: "line"})
	}
	for i := 0; i < 5; i++ {
		if _, ok := readMsg(t, b).(proto.ChatMsg); !ok {
			t.Fatalf("B: expected chat_msg %d of 5", i+1)
		}
	}
	expectNoFrame(t, a, 300*time.Millisecond)
}

// TestChatMsgRelayPreservesOrderAndContent: A sends 10 distinct chat lines; B
// receives exactly those 10, in order, byte-identical — the "in-order
// single-hop best-effort" promise, which the identical-payload tests cannot see.
func TestChatMsgRelayPreservesOrderAndContent(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	const n = 10
	for i := 0; i < n; i++ {
		writeMsg(t, a, proto.ChatMsg{ClientMsgID: fmt.Sprintf("m%d", i), Text: fmt.Sprintf("line %d", i)})
	}
	for i := 0; i < n; i++ {
		got, ok := readMsg(t, b).(proto.ChatMsg)
		if !ok {
			t.Fatalf("B: frame %d is not a chat_msg: %#v", i, got)
		}
		wantID, wantText := fmt.Sprintf("m%d", i), fmt.Sprintf("line %d", i)
		if got.ClientMsgID != wantID || got.Text != wantText {
			t.Fatalf("B: frame %d = {%q, %q}, want {%q, %q}", i, got.ClientMsgID, got.Text, wantID, wantText)
		}
	}
	expectNoFrame(t, a, 300*time.Millisecond)
}

func TestChatMsgMultiByteTextArrivesIntact(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	const text = "héllo 😀 مرحبا"
	writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c1", Text: text})
	got := readMsg(t, b).(proto.ChatMsg)
	if got.Text != text {
		t.Fatalf("peer received %q, want %q", got.Text, text)
	}
}

func TestChatMsgAfterPeerLeftIsDroppedWithNoError(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	_ = b.CloseNow() // B drops; A gets session_ended
	if _, ok := readMsg(t, a).(proto.SessionEnded); !ok {
		t.Fatal("A: expected session_ended after B dropped")
	}
	waitCount(t, h.hub, 1)

	// A sends into the ended pairing: nothing is delivered, no error frame.
	writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c1", Text: "still there?"})
	expectNoFrame(t, a, 300*time.Millisecond)
}

func TestUnpairedChatMsgIsIgnored(t *testing.T) {
	h := newHarness(t)
	a := h.dial(t)
	writeMsg(t, a, helloFor("acct-a", proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)

	writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c1", Text: "nobody home"})

	// Count stays 1 whether or not the relay has been processed yet (an unpaired
	// chat_msg is a no-op either way), so this needs no happens-before. It must
	// come before expectNoFrame, though: that call's read-timeout makes
	// coder/websocket close the client conn, which asynchronously drops the count.
	if got := h.hub.Count(); got != 1 {
		t.Fatalf("hub.Count = %d, want 1 (an unpaired chat_msg changes nothing)", got)
	}
	expectNoFrame(t, a, 300*time.Millisecond)
}

// TestRelayRoundTripP90UnderCeiling is a coarse latency check: ~50 relayed
// messages A→B, each timed send-to-receive, p90 well under a loose ceiling. A
// single in-process hop with non-blocking sends and no disk/CPU work.
func TestRelayRoundTripP90UnderCeiling(t *testing.T) {
	h := newHarness(t)
	a, b := matchedPair(t, h, "acct-a", "acct-b")

	const n = 50
	lat := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		writeMsg(t, a, proto.ChatMsg{ClientMsgID: "c", Text: "ping"})
		if _, ok := readMsg(t, b).(proto.ChatMsg); !ok {
			t.Fatalf("B: expected chat_msg %d", i)
		}
		lat = append(lat, time.Since(start))
	}
	slices.SortFunc(lat, func(x, y time.Duration) int { return cmp.Compare(x, y) })
	p90 := lat[int(float64(n)*0.9)-1]
	if p90 > 500*time.Millisecond {
		t.Fatalf("relay p90 = %v, want well under 500ms", p90)
	}
}

// --- /status -----------------------------------------------------------------

func TestStatus_EmptyRegistry(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Get(h.ts.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != `{"concurrent_users":0}` {
		t.Fatalf("body = %q, want {\"concurrent_users\":0}", string(body))
	}
}

func TestStatus_CountsDistinctKeys(t *testing.T) {
	h := newHarness(t)

	for _, key := range []string{"k1", "k2", "k3"} {
		c := h.dial(t)
		writeMsg(t, c, helloFor(key, proto.PROTOCOL_VERSION))
	}
	// A duplicate of k1 must not add to the count.
	dup := h.dial(t)
	writeMsg(t, dup, helloFor("k1", proto.PROTOCOL_VERSION))

	waitCount(t, h.hub, 3)

	resp, err := http.Get(h.ts.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	defer resp.Body.Close()

	var got struct {
		ConcurrentUsers int `json:"concurrent_users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ConcurrentUsers != 3 {
		t.Fatalf("concurrent_users = %d, want 3", got.ConcurrentUsers)
	}
}

func TestStatus_NonGETIs405(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Post(h.ts.URL+"/status", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want 405", resp.StatusCode)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Get(h.ts.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status code = %d, want 404", resp.StatusCode)
	}
}

func TestWS_NonGETIs405(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Post(h.ts.URL+"/ws", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /ws: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want 405", resp.StatusCode)
	}
}

func TestNew_NilLoggerDoesNotPanic(t *testing.T) {
	h, _ := runHub(t)
	handler := New(h, nil)
	if handler == nil {
		t.Fatal("New(h, nil) returned nil")
	}
}

// --- log scrub -------------------------------------------------------------

func TestLogsCarryNoAccountKeyOrFrameText(t *testing.T) {
	h := newHarness(t)

	const secretKey = "acct-secret-abc123"
	const secretText = "please-do-not-log-this-frame-text"
	const secretKey2 = "acct-secret-def456"
	const secretKey3 = "acct-secret-ghi789"

	// A full connection lifecycle: hello, a chat frame (discarded), a takeover,
	// a ready -> match -> busy teardown, a /status hit, then a drop.
	connA := h.dial(t)
	writeMsg(t, connA, helloFor(secretKey, proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 1)
	writeMsg(t, connA, proto.ChatMsg{ClientMsgID: "m1", Text: secretText})

	connB := h.dial(t)
	writeMsg(t, connB, helloFor(secretKey, proto.PROTOCOL_VERSION))
	if _, ok := readMsg(t, connA).(proto.SessionEnded); !ok {
		t.Fatal("connA: expected session_ended")
	}
	expectClose(t, connA, websocket.StatusNormalClosure)

	// A too-old client, to exercise the please_update log path.
	connOld := h.dial(t)
	writeMsg(t, connOld, helloFor(secretKey, proto.PROTOCOL_VERSION-9))
	_ = readMsg(t, connOld)

	// A match between two more secret keys, then a teardown, to exercise the
	// matched / session_ended log paths.
	connC := h.dial(t)
	writeMsg(t, connC, helloFor(secretKey2, proto.PROTOCOL_VERSION))
	connD := h.dial(t)
	writeMsg(t, connD, helloFor(secretKey3, proto.PROTOCOL_VERSION))
	waitCount(t, h.hub, 3)
	writeMsg(t, connC, proto.Ready{})
	writeMsg(t, connD, proto.Ready{})
	readMatched(t, connC)
	readMatched(t, connD)
	// A real relayed chat line between the matched pair: its text must never
	// reach the logs either.
	writeMsg(t, connC, proto.ChatMsg{ClientMsgID: "relayed-1", Text: secretText})
	if _, ok := readMsg(t, connD).(proto.ChatMsg); !ok {
		t.Fatal("connD: expected the relayed chat_msg")
	}
	writeMsg(t, connC, proto.Busy{})
	if _, ok := readMsg(t, connD).(proto.SessionEnded); !ok {
		t.Fatal("connD: expected session_ended after connC went busy")
	}

	resp, _ := http.Get(h.ts.URL + "/status")
	if resp != nil {
		resp.Body.Close()
	}

	_ = connB.CloseNow()
	_ = connC.CloseNow()
	_ = connD.CloseNow()
	waitCount(t, h.hub, 0)

	// Give the disconnect log line time to land.
	time.Sleep(50 * time.Millisecond)

	out := h.logs.String()
	if out == "" {
		t.Fatal("no logs captured")
	}
	for _, k := range []string{secretKey, secretKey2, secretKey3} {
		if strings.Contains(out, k) {
			t.Fatalf("logs contain an account key (%s):\n%s", k, out)
		}
	}
	if strings.Contains(out, secretText) {
		t.Fatalf("logs contain frame text:\n%s", out)
	}
	// Sanity: the log must actually be JSON lines with expected fields, and the
	// match / teardown paths did log something.
	if !strings.Contains(out, `"msg":"ws connected"`) {
		t.Fatalf("expected a 'ws connected' log line, got:\n%s", out)
	}
	if !strings.Contains(out, `"msg":"ws matched"`) {
		t.Fatalf("expected a 'ws matched' log line, got:\n%s", out)
	}
	if !strings.Contains(out, `"msg":"ws session ended"`) {
		t.Fatalf("expected a 'ws session ended' log line, got:\n%s", out)
	}
}
