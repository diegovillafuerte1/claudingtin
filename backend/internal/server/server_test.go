package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

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

	// A second hello, other proto frames, raw garbage text, and a binary frame
	// are all read and discarded — never decoded, never acted on.
	writeMsg(t, c, helloFor("acct-chatty", proto.PROTOCOL_VERSION))
	writeMsg(t, c, proto.Ready{})
	writeMsg(t, c, proto.Heartbeat{})
	writeRaw(t, c, `{"type":`)
	writeBinary(t, c, []byte{0x00, 0x01, 0x02, 0xff})

	if got := h.hub.Count(); got != 1 {
		t.Fatalf("hub.Count = %d, want 1", got)
	}
	expectOpenIdle(t, c)
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

	// A full connection lifecycle: hello, a chat frame (discarded), a takeover,
	// a /status hit, then a drop.
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

	resp, _ := http.Get(h.ts.URL + "/status")
	if resp != nil {
		resp.Body.Close()
	}

	_ = connB.CloseNow()
	waitCount(t, h.hub, 0)

	// Give the disconnect log line time to land.
	time.Sleep(50 * time.Millisecond)

	out := h.logs.String()
	if out == "" {
		t.Fatal("no logs captured")
	}
	if strings.Contains(out, secretKey) {
		t.Fatalf("logs contain the account key:\n%s", out)
	}
	if strings.Contains(out, secretText) {
		t.Fatalf("logs contain frame text:\n%s", out)
	}
	// Sanity: the log must actually be JSON lines with expected fields.
	if !strings.Contains(out, `"msg":"ws connected"`) {
		t.Fatalf("expected a 'ws connected' log line, got:\n%s", out)
	}
}
