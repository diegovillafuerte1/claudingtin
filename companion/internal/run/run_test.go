package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	tea "charm.land/bubbletea/v2"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/chatui"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/safety"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/statusline"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/transcript"
	"github.com/diegovillafuerte1/claudingtin/companion/internal/wsclient"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// withDebounce sets debounceInterval for the duration of a test. Tests here
// never run in parallel, so mutating the package var is safe.
func withDebounce(t *testing.T, d time.Duration) {
	t.Helper()
	old := debounceInterval
	debounceInterval = d
	t.Cleanup(func() { debounceInterval = old })
}

// shrinkDebounce is the fast default used by the e2e tests.
func shrinkDebounce(t *testing.T) { withDebounce(t, 10*time.Millisecond) }

// uuidLike catches any account-key-shaped string leaking into a returned error.
var uuidLike = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// ackedConfigDir returns a fresh temp dir with a pre-recorded first-run
// acknowledgement, so a Run under test clears the gate as "already accepted"
// (no screen, no stdin read) and exercises the watch + client path as before.
func ackedConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := safety.Record(dir); err != nil {
		t.Fatalf("pre-record safety ack: %v", err)
	}
	return dir
}

// fakeClient stands in for *wsclient.Client in the loop tests.
type fakeClient struct {
	events   chan wsclient.Event
	matched  chan proto.Matched
	chatMsgs chan proto.ChatMsg

	mu          sync.Mutex
	sent        []any
	chatSent    []proto.ChatMsg // payloads passed to SendChat
	sendErr     error           // returned by SendState when set
	sendChatErr error           // returned by SendChat when set
	sendGate    chan struct{}   // if non-nil, SendState blocks receiving from it
	sendEntry   chan struct{}   // SendState signals here (non-blocking) on entering the gate
	result      wsclient.Result
	stop        chan struct{} // close to make Run return f.result
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		events:   make(chan wsclient.Event, 8),
		matched:  make(chan proto.Matched, 1),
		chatMsgs: make(chan proto.ChatMsg, 8),
		stop:     make(chan struct{}),
	}
}

func (f *fakeClient) Run(ctx context.Context) wsclient.Result {
	select {
	case <-ctx.Done():
		return wsclient.ResultContextDone
	case <-f.stop:
		return f.result
	}
}

func (f *fakeClient) SendState(_ context.Context, msg any) error {
	f.mu.Lock()
	gate, entry := f.sendGate, f.sendEntry
	f.mu.Unlock()
	if gate != nil {
		if entry != nil {
			select {
			case entry <- struct{}{}:
			default:
			}
		}
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeClient) setGate(gate, entry chan struct{}) {
	f.mu.Lock()
	f.sendGate, f.sendEntry = gate, entry
	f.mu.Unlock()
}

func (f *fakeClient) setSendErr(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

func (f *fakeClient) setSendChatErr(err error) {
	f.mu.Lock()
	f.sendChatErr = err
	f.mu.Unlock()
}

func (f *fakeClient) Events() <-chan wsclient.Event { return f.events }

// Matched is nil-safe: a fakeClient built without a matched channel simply
// never surfaces a match.
func (f *fakeClient) Matched() <-chan proto.Matched { return f.matched }

// ChatMsgs is nil-safe: a fakeClient built without a chatMsgs channel simply
// never surfaces a peer line.
func (f *fakeClient) ChatMsgs() <-chan proto.ChatMsg { return f.chatMsgs }

func (f *fakeClient) SendChat(_ context.Context, m proto.ChatMsg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendChatErr != nil {
		return f.sendChatErr
	}
	f.chatSent = append(f.chatSent, m)
	return nil
}

func (f *fakeClient) chatSentFrames() []proto.ChatMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]proto.ChatMsg, len(f.chatSent))
	copy(out, f.chatSent)
	return out
}

func (f *fakeClient) endRunWith(r wsclient.Result) {
	f.result = r
	close(f.stop)
}

func (f *fakeClient) sentFrames() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]any, len(f.sent))
	copy(out, f.sent)
	return out
}

// loopHarness runs loop in a goroutine over injectable channels.
type loopHarness struct {
	events   chan transcript.Event
	errs     chan error
	client   *fakeClient
	out      *lockedBuffer
	errBuf   *lockedBuffer
	cancel   context.CancelFunc
	done     chan error    // buffered: loop's return value, read at most once
	finished chan struct{} // closed after loop returns
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startLoop(t *testing.T) *loopHarness { return startLoopWith(t, 10*time.Millisecond) }

func startLoopWith(t *testing.T, debounce time.Duration) *loopHarness {
	t.Helper()
	withDebounce(t, debounce)
	ctx, cancel := context.WithCancel(context.Background())
	h := &loopHarness{
		events:   make(chan transcript.Event, 16),
		errs:     make(chan error, 4),
		client:   newFakeClient(),
		out:      &lockedBuffer{},
		errBuf:   &lockedBuffer{},
		cancel:   cancel,
		done:     make(chan error, 1),
		finished: make(chan struct{}),
	}
	cfg := Config{Out: h.out, Err: h.errBuf}
	sl := statusline.New(h.out)
	go func() {
		h.done <- loop(ctx, cfg, h.events, h.errs, h.client, sl)
		close(h.finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.finished:
		case <-time.After(2 * time.Second):
			t.Error("loop did not return after cancel")
		}
	})
	return h
}

func (h *loopHarness) waitSent(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		got := frameNames(h.client.sentFrames())
		if strings.Join(got, ",") == strings.Join(want, ",") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("sent frames = %v, want %v", got, want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitUntil polls cond until true or a generous deadline passes.
func waitUntil(t *testing.T, what string, cond func() bool) {
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

func frameNames(frames []any) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		switch f.(type) {
		case proto.Hello:
			out = append(out, "hello")
		case proto.Ready:
			out = append(out, "ready")
		case proto.Busy:
			out = append(out, "busy")
		default:
			out = append(out, "?")
		}
	}
	return out
}

// containsInOrder reports whether want appears as an ordered subsequence of got.
func containsInOrder(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func TestTurnStartSendsReady(t *testing.T) {
	h := startLoop(t)
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")
}

func TestTurnEndSendsBusy(t *testing.T) {
	h := startLoop(t)
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")
	h.events <- transcript.Event{Kind: transcript.TurnEnd}
	h.waitSent(t, "ready", "busy")
}

func TestBurstCoalescesToSettledState(t *testing.T) {
	// A wide debounce (200ms) versus instant channel sends: all five events are
	// guaranteed to land in one window even on a slow CI runner, so the test is
	// about coalescing, not scheduler luck.
	h := startLoopWith(t, 200*time.Millisecond)
	// A seed replay: several transitions land inside one debounce window.
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.events <- transcript.Event{Kind: transcript.TurnEnd}
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.events <- transcript.Event{Kind: transcript.TurnEnd}
	h.events <- transcript.Event{Kind: transcript.TurnStart}

	// Exactly one frame, and it is the settled state (ready).
	h.waitSent(t, "ready")

	// And nothing more arrives after the window closes.
	time.Sleep(150 * time.Millisecond)
	if got := frameNames(h.client.sentFrames()); len(got) != 1 || got[0] != "ready" {
		t.Fatalf("burst produced %v, want exactly one settled [ready] frame", got)
	}
}

func TestConnectedRepushesCurrentState(t *testing.T) {
	h := startLoop(t)
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")

	// A reconnect: the client announces Connected and the loop must re-push the
	// current state with no operator action.
	h.client.events <- wsclient.Event{Kind: wsclient.Connected}
	h.waitSent(t, "ready", "ready")
}

// TestReconnectRepushesEvenWhenEventsDrainIsDelayed covers the combined flapping
// case: while loop is parked in a slow SendState (not draining Events()), a
// Reconnecting then a Connected queue up; once loop drains them the current
// desired state must still be re-announced — the client's blocking emit
// guarantees neither event is lost, and pushState runs on Connected.
func TestReconnectRepushesEvenWhenEventsDrainIsDelayed(t *testing.T) {
	h := startLoop(t)

	gate := make(chan struct{})
	entry := make(chan struct{}, 1)
	h.client.setGate(gate, entry)

	h.events <- transcript.Event{Kind: transcript.TurnStart} // desired = ready

	// Debounce fires, flush() calls SendState, which signals entry and blocks.
	select {
	case <-entry:
	case <-time.After(2 * time.Second):
		t.Fatal("loop never reached SendState")
	}

	// loop is not draining Events(): queue a whole reconnect cycle.
	h.client.events <- wsclient.Event{Kind: wsclient.Reconnecting}
	h.client.events <- wsclient.Event{Kind: wsclient.Connected}

	// Let subsequent sends through, then release the parked one.
	h.client.setGate(nil, nil)
	close(gate)

	// The parked flush records one "ready"; the drained Connected re-announces
	// → a second "ready". Nothing was dropped.
	h.waitSent(t, "ready", "ready")
	waitUntil(t, "reconnecting status", func() bool {
		return strings.Contains(h.out.String(), "picking it back up")
	})
}

// TestFailedSendDoesNotAdvanceSent: when a debounce flush's SendState fails,
// loop must not record the state as sent — otherwise a later flush for the same
// desired state (sent == desired) skips and the state is never delivered.
func TestFailedSendDoesNotAdvanceSent(t *testing.T) {
	h := startLoop(t)

	// First flush fails.
	h.client.setSendErr(wsclient.ErrNotConnected)
	h.events <- transcript.Event{Kind: transcript.TurnStart} // desired = ready
	time.Sleep(40 * time.Millisecond)
	if got := h.client.sentFrames(); len(got) != 0 {
		t.Fatalf("a failed send still recorded frames: %v", frameNames(got))
	}

	// Send path recovers, and another event keeps desired == ready. The next
	// flush must actually send "ready" (it would be skipped if the failed send
	// had advanced `sent`).
	h.client.setSendErr(nil)
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")
}

func TestReconnectingShowsPhase(t *testing.T) {
	h := startLoop(t)
	h.client.events <- wsclient.Event{Kind: wsclient.Reconnecting}

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(h.out.String(), "picking it back up") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("status never showed the reconnecting line:\n%s", h.out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestConnectedWithNoTurnsSendsBusyAndShowsWaiting is the I/O matrix "Normal
// startup" row: on the first Connected with no transcript turn seen yet, exactly
// one busy frame goes out and the status line reads "waiting".
func TestConnectedWithNoTurnsSendsBusyAndShowsWaiting(t *testing.T) {
	h := startLoop(t)

	h.client.events <- wsclient.Event{Kind: wsclient.Connected}

	h.waitSent(t, "busy")
	waitUntil(t, "the waiting status line", func() bool {
		return strings.Contains(h.out.String(), "waiting")
	})

	// No second frame appears.
	time.Sleep(40 * time.Millisecond)
	if got := frameNames(h.client.sentFrames()); len(got) != 1 || got[0] != "busy" {
		t.Fatalf("sent = %v, want exactly [busy]", got)
	}
}

func TestDebounceIntervalDefault(t *testing.T) {
	if debounceInterval != 250*time.Millisecond {
		t.Fatalf("shipped debounceInterval = %v, want 250ms", debounceInterval)
	}
}

func TestContextCancelReturnsNil(t *testing.T) {
	h := startLoop(t)
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil on context cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel")
	}
}

func TestSessionEndedReturnsNil(t *testing.T) {
	h := startLoop(t)
	h.client.endRunWith(wsclient.ResultSessionEnded)
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil on session_ended", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after session_ended")
	}
}

func TestPleaseUpdateStaysAliveThenExitsCleanOnCancel(t *testing.T) {
	h := startLoop(t)
	h.client.endRunWith(wsclient.ResultPleaseUpdate)

	// The process stays alive: loop must NOT return yet.
	select {
	case err := <-h.done:
		t.Fatalf("loop returned %v after please_update; it must stay alive", err)
	case <-time.After(200 * time.Millisecond):
	}

	// One stderr notice, carrying no key/content, and the update-needed status.
	if s := h.errBuf.String(); !strings.Contains(s, "update") {
		t.Fatalf("stderr = %q, want an update notice", s)
	}
	if s := h.out.String(); !strings.Contains(s, "out of date") {
		t.Fatalf("status = %q, want the update-needed line", s)
	}

	// SIGINT-equivalent: cancel → clean exit.
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel following please_update")
	}
}

func TestFatalWatchErrorReturnsError(t *testing.T) {
	h := startLoop(t)
	// The watcher's fatal contract: one error on Errors, then both channels
	// close.
	h.errs <- errors.New("transcript: gave up re-opening /tmp/x after 25 attempts")
	close(h.errs)
	close(h.events)

	select {
	case err := <-h.done:
		if err == nil {
			t.Fatal("loop returned nil, want a fatal error")
		}
		if !strings.Contains(err.Error(), "transcript watch") {
			t.Fatalf("error = %v, want it to mention the transcript watch", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after a fatal watch error")
	}
}

func TestWatcherStopNoErrorReturnsError(t *testing.T) {
	h := startLoop(t)
	close(h.errs)
	close(h.events)

	select {
	case err := <-h.done:
		if err == nil {
			t.Fatal("loop returned nil, want an error when the watch stops")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after the watch stopped")
	}
}

func TestTransientWatchErrorDoesNotStop(t *testing.T) {
	h := startLoop(t)

	const transient = "transcript: read /tmp/x: temporary glitch"
	h.errs <- errors.New(transient)

	// Wait until the loop has logged it, so its ordering with the next event is
	// defined.
	waitUntil(t, "the watch-error line", func() bool {
		return strings.Contains(h.errBuf.String(), transient)
	})

	// The line must not prejudge severity.
	if strings.Contains(h.errBuf.String(), "hiccup") {
		t.Fatalf("transient error line calls it a hiccup: %q", h.errBuf.String())
	}
	if !strings.Contains(h.errBuf.String(), "transcript watch error:") {
		t.Fatalf("stderr = %q, want a neutral 'transcript watch error:' line", h.errBuf.String())
	}

	// A normal event: the watcher recovered. This clears the stashed error.
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")

	// Now the watcher stops for a non-fatal reason (ctx NOT cancelled). The
	// earlier transient error must NOT resurface as the fatal cause.
	close(h.errs)
	close(h.events)
	select {
	case err := <-h.done:
		if err == nil {
			t.Fatal("expected an error when the watch stops")
		}
		if strings.Contains(err.Error(), "temporary glitch") {
			t.Fatalf("stale transient error resurfaced as the fatal cause: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after the watch stopped")
	}
}

// TestContextCancelBeatsWatcherClose drives the exact ordering transcript.Watch
// produces on a clean SIGINT/SIGTERM: ctx is cancelled, then the tailer closes
// both Events and Errors with nothing on Errors. All three of <-ctx.Done(), the
// closed-Events receive, and the closed-Errors receive can be ready in the same
// select. loop must return nil (exit 0) no matter which branch select picks —
// never errWatchStopped. Looped so the scheduler exercises every ordering.
func TestContextCancelBeatsWatcherClose(t *testing.T) {
	old := debounceInterval
	debounceInterval = 10 * time.Millisecond
	defer func() { debounceInterval = old }()

	for i := 0; i < 200; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan transcript.Event)
		errs := make(chan error)
		fc := newFakeClient()
		var out, errBuf lockedBuffer
		cfg := Config{Out: &out, Err: &errBuf}
		sl := statusline.New(&out)

		done := make(chan error, 1)
		go func() { done <- loop(ctx, cfg, events, errs, fc, sl) }()

		// The real teardown order: cancel triggers the watch to unwind, which
		// then closes its channels with nothing sent on Errors.
		cancel()
		close(errs)
		close(events)

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("iter %d: loop returned %v, want nil on a clean context cancel", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: loop did not return", i)
		}
	}
}

// --- integration: real transcript.Watch + real wsclient over httptest --------

// wsRecorder is a coder/websocket server that records decoded frames per
// connection. onConn scripts a connection; dropAfterHello, when it returns true
// for a connection index, closes that connection the instant its hello is seen
// (deterministic, no sleeps).
type wsRecorder struct {
	ts             *httptest.Server
	onConn         func(idx int, c *websocket.Conn)
	dropAfterHello func(idx int) bool

	mu       sync.Mutex
	conns    int
	received [][]any
}

func newWSRecorder(t *testing.T, onConn func(idx int, c *websocket.Conn)) *wsRecorder {
	return newWSRecorderCfg(t, &wsRecorder{onConn: onConn})
}

func newWSRecorderDropping(t *testing.T, dropAfterHello func(idx int) bool) *wsRecorder {
	return newWSRecorderCfg(t, &wsRecorder{dropAfterHello: dropAfterHello})
}

func newWSRecorderCfg(t *testing.T, r *wsRecorder) *wsRecorder {
	t.Helper()
	r.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()

		r.mu.Lock()
		idx := r.conns
		r.conns++
		r.received = append(r.received, nil)
		r.mu.Unlock()

		done := make(chan struct{})
		if r.onConn != nil {
			go func() { r.onConn(idx, c); close(done) }()
		} else {
			close(done)
		}

		for {
			_, data, err := c.Read(req.Context())
			if err != nil {
				<-done
				return
			}
			msg, derr := proto.Decode(data)
			if derr != nil {
				continue
			}
			r.mu.Lock()
			r.received[idx] = append(r.received[idx], msg)
			r.mu.Unlock()

			if _, isHello := msg.(proto.Hello); isHello && r.dropAfterHello != nil && r.dropAfterHello(idx) {
				_ = c.Close(websocket.StatusNormalClosure, "")
				<-done
				return
			}
		}
	}))
	t.Cleanup(r.ts.Close)
	return r
}

func (r *wsRecorder) wsURL() string { return "ws" + strings.TrimPrefix(r.ts.URL, "http") + "/ws" }

func (r *wsRecorder) frames(idx int) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idx >= len(r.received) {
		return nil
	}
	out := make([]any, len(r.received[idx]))
	copy(out, r.received[idx])
	return out
}

func (r *wsRecorder) connCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns
}

const (
	humanPrompt = `{"type":"user","isSidechain":false,"message":{"role":"user","content":"hi"},"uuid":"u1","timestamp":"2026-09-05T16:45:00.000Z"}`
	endTurn     = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","id":"m1","content":[{"type":"text"}],"stop_reason":"end_turn"},"timestamp":"2026-09-05T16:45:05.000Z"}`
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// waitFrames waits until want appears as an ordered subsequence of the frames
// get() returns. A leading idle "busy" (the state re-announced right after every
// hello) is legitimately interleaved, so this is a subsequence match, not exact.
func waitFrames(t *testing.T, what string, get func() []string, want ...string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		got := get()
		if containsInOrder(got, want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s: frames = %v, want %v in order", what, got, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunEndToEnd_HelloThenReadyThenBusy(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	rec := newWSRecorder(t, nil)
	const key = "acct-e2e-key"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     key,
			ConfigDir:      ackedConfigDir(t),
			Out:            &bytes.Buffer{},
			Err:            &bytes.Buffer{},
		})
	}()

	// hello lands on connect.
	waitFrames(t, "after connect", func() []string { return frameNames(rec.frames(0)) }, "hello")
	if h, ok := rec.frames(0)[0].(proto.Hello); !ok {
		t.Fatalf("first frame %T, want Hello", rec.frames(0)[0])
	} else if h.AccountKey != key || h.ProtocolVersion != proto.PROTOCOL_VERSION {
		t.Fatalf("hello = %+v, want key %q version %d", h, key, proto.PROTOCOL_VERSION)
	}

	// A human prompt → ready.
	appendLine(t, transcriptPath, humanPrompt)
	waitFrames(t, "after prompt", func() []string { return frameNames(rec.frames(0)) }, "hello", "ready")

	// The assistant finishing its turn → busy.
	appendLine(t, transcriptPath, endTurn)
	waitFrames(t, "after end_turn", func() []string { return frameNames(rec.frames(0)) }, "hello", "ready", "busy")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunEndToEnd_ReconnectReannounces(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	// First connection is dropped the instant its hello is seen; later
	// connections stay up. Deterministic — no sleeps.
	rec := newWSRecorderDropping(t, func(idx int) bool { return idx == 0 })
	const key = "acct-reconnect"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     key,
			ConfigDir:      ackedConfigDir(t),
			Out:            &bytes.Buffer{},
			Err:            &bytes.Buffer{},
		})
	}()

	// Establish, then move to the ready state.
	waitFrames(t, "conn0 hello", func() []string { return frameNames(rec.frames(0)) }, "hello")
	appendLine(t, transcriptPath, humanPrompt)

	// After the server drops conn0, the client reconnects, re-sends hello, then
	// re-announces the current transcript state (ready) — no operator action.
	deadline := time.After(6 * time.Second)
	for {
		if rec.connCount() >= 2 && containsInOrder(frameNames(rec.frames(1)), []string{"hello", "ready"}) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("reconnect did not re-announce ready; conn1 frames = %v", frameNames(rec.frames(1)))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRunTearsDownWatcherOnSessionEnded proves Run cancels the transcript
// watcher (its goroutine and fsnotify handle) on a non-context terminal return:
// after N session_ended cycles with an un-cancellable parent context, the
// goroutine count returns to baseline. Without the derived-context teardown,
// each cycle would leak the tailer plus fsnotify's internal goroutines.
func TestRunTearsDownWatcherOnSessionEnded(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	rec := newWSRecorder(t, func(idx int, c *websocket.Conn) {
		b, _ := proto.Encode(proto.SessionEnded{})
		_ = c.Write(context.Background(), websocket.MessageText, b)
	})

	// A pre-acknowledged config dir: the gate is a straight-through
	// "already accepted" on every cycle, spawning no goroutine of its own.
	ackDir := ackedConfigDir(t)

	// Let the httptest server's own goroutines settle, then baseline.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	base := runtime.NumGoroutine()

	const cycles = 6
	for i := 0; i < cycles; i++ {
		if err := Run(context.Background(), Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     "k",
			ConfigDir:      ackDir,
			Out:            io.Discard,
			Err:            io.Discard,
		}); err != nil {
			t.Fatalf("cycle %d: Run returned %v, want nil", i, err)
		}
	}

	deadline := time.After(3 * time.Second)
	for {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n <= base+2 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("goroutines did not settle after %d Run/session_ended cycles: base=%d now=%d", cycles, base, n)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// TestOutputScrub covers the frozen I/O matrix "Output scrub" row: across a
// representative session — connect, a prompt whose body carries a recognizable
// secret, a turn end, and the please_update stderr path — neither the account
// key nor any transcript line body may appear in the Out or Err buffers, and
// nothing is written anywhere else (wsclient is logging-free by construction).
func TestOutputScrub(t *testing.T) {
	shrinkDebounce(t)

	const (
		secretKey  = "acct-scrub-SECRET-9f3ab2c1"
		secretBody = "my street address is 42 hidden lane and my card is 4111 1111 1111 1111"
	)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	// First connection sends please_update after a beat, exercising that stderr
	// path too.
	rec := newWSRecorder(t, func(idx int, c *websocket.Conn) {
		if idx == 0 {
			time.Sleep(80 * time.Millisecond)
			b, _ := proto.Encode(proto.PleaseUpdate{})
			_ = c.Write(context.Background(), websocket.MessageText, b)
		}
	})

	var out, errBuf lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     secretKey,
			ConfigDir:      ackedConfigDir(t),
			Out:            &out,
			Err:            &errBuf,
		})
	}()

	waitFrames(t, "hello", func() []string { return frameNames(rec.frames(0)) }, "hello")

	prompt := `{"type":"user","isSidechain":false,"message":{"role":"user","content":"` +
		secretBody + `"},"uuid":"u1","timestamp":"2026-09-05T16:45:00.000Z"}`
	appendLine(t, transcriptPath, prompt)
	waitFrames(t, "ready", func() []string { return frameNames(rec.frames(0)) }, "hello", "ready")
	appendLine(t, transcriptPath, endTurn)

	// Wait for the please_update path to run (status shows the update-needed line).
	deadline := time.After(3 * time.Second)
	for !strings.Contains(out.String(), "out of date") {
		select {
		case <-deadline:
			t.Fatalf("please_update path never ran; out=%q err=%q", out.String(), errBuf.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	for _, b := range []struct{ name, s string }{{"Out", out.String()}, {"Err", errBuf.String()}} {
		if strings.Contains(b.s, secretKey) {
			t.Fatalf("%s contains the account key:\n%s", b.name, b.s)
		}
		if strings.Contains(b.s, secretBody) || strings.Contains(b.s, "42 hidden lane") ||
			strings.Contains(b.s, "4111 1111 1111 1111") {
			t.Fatalf("%s contains transcript content:\n%s", b.name, b.s)
		}
	}
	// Sanity: Out did receive the status copy it is supposed to.
	if !strings.Contains(out.String(), "connecting") {
		t.Fatalf("Out missing expected status copy:\n%s", out.String())
	}
}

// --- first-run gate at the top of Run ---------------------------------------

// TestRunFirstRunAcceptConnects: no prior ack, In delivers "yes\n" — the screen
// reaches Out, <configDir>/safety-ack is written 0600 with the version and an
// accepted-at line and no account key, and the server then observes hello.
func TestRunFirstRunAcceptConnects(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cfgDir := t.TempDir()

	rec := newWSRecorder(t, nil)
	const key = "acct-first-run-accept"
	var out lockedBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     key,
			ConfigDir:      cfgDir,
			In:             strings.NewReader("yes\n"),
			Out:            &out,
			Err:            &bytes.Buffer{},
		})
	}()

	waitFrames(t, "after accept", func() []string { return frameNames(rec.frames(0)) }, "hello")

	waitUntil(t, "the first-run screen on Out", func() bool {
		return strings.Contains(out.String(), "18 or older")
	})

	ackPath := filepath.Join(cfgDir, "safety-ack")
	fi, err := os.Stat(ackPath)
	if err != nil {
		t.Fatalf("safety-ack not written: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("safety-ack mode = %v, want 0600", fi.Mode().Perm())
	}
	body, err := os.ReadFile(ackPath)
	if err != nil {
		t.Fatalf("read safety-ack: %v", err)
	}
	if !strings.Contains(string(body), "version 1\n") || !strings.Contains(string(body), "accepted_at ") {
		t.Errorf("safety-ack body = %q, want a version and an accepted-at line", body)
	}
	if strings.Contains(string(body), key) {
		t.Errorf("safety-ack leaked the account key:\n%s", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestRunFirstRunDeclineStaysInert: no prior ack, In at EOF — no connection is
// dialed, Out shows the inert line, safety-ack is not created, and Run returns
// nil after the context is cancelled.
func TestRunFirstRunDeclineStaysInert(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cfgDir := t.TempDir()

	rec := newWSRecorder(t, nil)
	var out lockedBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     "acct-first-run-decline",
			ConfigDir:      cfgDir,
			In:             strings.NewReader(""), // immediate EOF
			Out:            &out,
			Err:            &bytes.Buffer{},
		})
	}()

	waitUntil(t, "the inert status line", func() bool {
		return strings.Contains(out.String(), "you can turn this on whenever you like")
	})

	if n := rec.connCount(); n != 0 {
		t.Fatalf("a declined gate still dialed the server: connCount = %d", n)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "safety-ack")); !os.IsNotExist(err) {
		t.Fatalf("safety-ack created on a decline: stat err = %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel following a decline")
	}
}

// TestRunPreAcceptedConnectsSilently: <configDir>/safety-ack already records the
// current screen version — no screen text is written to Out and hello is sent
// with no stdin input.
func TestRunPreAcceptedConnectsSilently(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cfgDir := ackedConfigDir(t)

	rec := newWSRecorder(t, nil)
	var out lockedBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     "acct-pre-accepted",
			ConfigDir:      cfgDir,
			In:             strings.NewReader(""), // must not be read
			Out:            &out,
			Err:            &bytes.Buffer{},
		})
	}()

	waitFrames(t, "pre-accepted connect", func() []string { return frameNames(rec.frames(0)) }, "hello")

	if strings.Contains(out.String(), "18 or older") {
		t.Fatalf("the first-run screen was printed for a pre-accepted user:\n%s", out.String())
	}
}

// TestRunGateErrorPropagates: a ConfigDir whose parent path component is a
// regular file makes safety.Gate's Accepted read fail; Run must return a
// wrapped "first-run gate" error carrying no key material, and must not dial.
func TestRunGateErrorPropagates(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := newWSRecorder(t, nil)
	var out lockedBuffer

	err := Run(context.Background(), Config{
		TranscriptPath: transcriptPath,
		ServerURL:      rec.wsURL(),
		AccountKey:     "acct-gate-error",
		ConfigDir:      filepath.Join(notADir, "cfg"),
		In:             strings.NewReader("yes\n"),
		Out:            &out,
		Err:            &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("Run returned nil, want a wrapped first-run gate error")
	}
	if !strings.Contains(err.Error(), "first-run gate") {
		t.Fatalf("error = %v, want it to mention the first-run gate", err)
	}
	if uuidLike.MatchString(err.Error()) {
		t.Fatalf("error carries key-shaped material: %v", err)
	}
	if n := rec.connCount(); n != 0 {
		t.Fatalf("server was dialed despite a gate error: connCount = %d", n)
	}
}

// TestRunGateAbortReturnsNil: ctx cancelled while the gate is parked on a stdin
// read that will never complete. Run returns nil, nothing is dialed, and Out
// carries neither the inert line nor any wire frame.
func TestRunGateAbortReturnsNil(t *testing.T) {
	shrinkDebounce(t)

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cfgDir := t.TempDir() // deliberately un-acked

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	rec := newWSRecorder(t, nil)
	var out lockedBuffer

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			TranscriptPath: transcriptPath,
			ServerURL:      rec.wsURL(),
			AccountKey:     "acct-gate-abort",
			ConfigDir:      cfgDir,
			In:             pr,
			Out:            &out,
			Err:            &bytes.Buffer{},
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on an aborted gate", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel during the gate")
	}

	if n := rec.connCount(); n != 0 {
		t.Fatalf("server was dialed on an aborted gate: connCount = %d", n)
	}
	if s := out.String(); strings.Contains(s, "you can turn this on whenever you like") {
		t.Fatalf("the inert line was shown on an aborted gate:\n%s", s)
	}
	if frames := frameNames(rec.frames(0)); len(frames) != 0 {
		t.Fatalf("wire frames observed on an aborted gate: %v", frames)
	}
}

// --- chat surface launch / teardown (Story 2.3) ----------------------------

// fakeChatProgram stands in for *tea.Program so the loop's launch/teardown is
// exercised with no PTY. Run blocks until Quit (or, when ignoreQuit is set, only
// until Kill). Println captures the content-free intent lines loop routes
// through the program.
type fakeChatProgram struct {
	notify     func(chatui.Intent)
	send       func(clientMsgID, text string)
	model      tea.Model // the real model newChatModel built, never Run by the fake
	quit       chan struct{}
	kill       chan struct{}
	ran        chan struct{}
	ignoreQuit bool

	mu       sync.Mutex
	killed   bool
	prints   []string
	received []tea.Msg
}

func (f *fakeChatProgram) Run() (tea.Model, error) {
	close(f.ran)
	if f.ignoreQuit {
		<-f.kill
	} else {
		<-f.quit
	}
	return nil, nil
}

func (f *fakeChatProgram) Println(args ...any) {
	f.mu.Lock()
	f.prints = append(f.prints, fmt.Sprint(args...))
	f.mu.Unlock()
}

func (f *fakeChatProgram) Send(msg tea.Msg) {
	f.mu.Lock()
	f.received = append(f.received, msg)
	f.mu.Unlock()
}

func (f *fakeChatProgram) receivedMsgs() []tea.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tea.Msg, len(f.received))
	copy(out, f.received)
	return out
}

func (f *fakeChatProgram) Quit() {
	select {
	case <-f.quit:
	default:
		close(f.quit)
	}
}

func (f *fakeChatProgram) Kill() {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	select {
	case <-f.kill:
	default:
		close(f.kill)
	}
	f.Quit()
}

func (f *fakeChatProgram) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

func (f *fakeChatProgram) printed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.prints, "\n")
}

// stubChatProgram swaps newChatProgram for a fake and hands back a channel that
// yields each constructed fake. It also shrinks the Kill grace so a test that
// wants the Kill path does not wait seconds. Any opts are applied to every fake
// it constructs (e.g. to set ignoreQuit).
//
// The fake still calls the real newChatModel with the loop's own notify/send
// closures — so the real chatui.New(m, WithNotify, WithSend) wiring is exercised
// on every run test — but never Runs the model, so no PTY and no goroutine
// touches it.
func stubChatProgram(t *testing.T, opts ...func(*fakeChatProgram)) <-chan *fakeChatProgram {
	t.Helper()
	oldNew, oldGrace := newChatProgram, chatQuitGrace
	chatQuitGrace = 100 * time.Millisecond
	made := make(chan *fakeChatProgram, 4)
	newChatProgram = func(_ context.Context, _ Config, m proto.Matched, notify func(chatui.Intent), send func(clientMsgID, text string)) chatProgram {
		f := &fakeChatProgram{
			notify: notify,
			send:   send,
			quit:   make(chan struct{}),
			kill:   make(chan struct{}),
			ran:    make(chan struct{}),
		}
		f.model = newChatModel(m, notify, send)
		for _, o := range opts {
			o(f)
		}
		made <- f
		return f
	}
	t.Cleanup(func() { newChatProgram, chatQuitGrace = oldNew, oldGrace })
	return made
}

func waitChat(t *testing.T, made <-chan *fakeChatProgram) *fakeChatProgram {
	t.Helper()
	select {
	case f := <-made:
		select {
		case <-f.ran:
		case <-time.After(2 * time.Second):
			t.Fatal("chat program was constructed but never Run")
		}
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no chat program constructed after matched")
		return nil
	}
}

// TestMatchedLaunchesChatSurface: the first matched constructs and runs the chat
// program, and no payload content leaks into any log sink.
func TestMatchedLaunchesChatSurface(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{
		SessionID: "sess-SECRET",
		Pseudonym: "pseud-SECRET",
		Blurb:     "blurb-SECRET",
		Opener:    "opener-SECRET",
	}

	f := waitChat(t, made)

	// Give any (content-free) launch logging a moment to land, then scan every
	// sink the loop can write to.
	time.Sleep(50 * time.Millisecond)
	for _, secret := range []string{"sess-SECRET", "pseud-SECRET", "blurb-SECRET", "opener-SECRET"} {
		for name, s := range map[string]string{"stderr": h.errBuf.String(), "status": h.out.String(), "program": f.printed()} {
			if strings.Contains(s, secret) {
				t.Fatalf("%s leaked matched payload %q:\n%s", name, secret, s)
			}
		}
	}
}

// TestLeaveIntentReturnsToStatusLine: the leave intent logs a content-free line
// through the program, tears the surface down with a clean Quit, and the loop
// keeps running (the process stays alive).
func TestLeaveIntentReturnsToStatusLine(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	f.notify(chatui.IntentLeave)

	waitUntil(t, "the content-free leave log line", func() bool {
		return strings.Contains(f.printed(), "leave requested")
	})
	if strings.Contains(f.printed(), "\x1b") {
		t.Fatalf("leave log went to a raw sink, not the program: %q", f.printed())
	}

	select {
	case <-f.quit:
	case <-time.After(2 * time.Second):
		t.Fatal("leave did not stop the chat program")
	}
	if f.wasKilled() {
		t.Fatal("leave Killed the program instead of a clean Quit")
	}

	// The loop is still running.
	select {
	case err := <-h.done:
		t.Fatalf("loop returned %v after a leave; the process must stay alive", err)
	case <-time.After(150 * time.Millisecond):
	}

	// And a fresh matched can open the surface again.
	h.client.matched <- proto.Matched{Opener: "o2"}
	waitChat(t, made)
}

// TestSecondMatchedWhileChatActiveIsIgnored: a stray second matched frame while
// the surface is already up does not construct a second program.
func TestSecondMatchedWhileChatActiveIsIgnored(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	waitChat(t, made)

	h.client.matched <- proto.Matched{Opener: "o-again"}
	select {
	case <-made:
		t.Fatal("a second matched constructed a second chat program")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestStatusLineSuppressedWhileChatActive: while the surface owns the pane, no
// status-line copy is written — but wire frames still flow.
func TestStatusLineSuppressedWhileChatActive(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	waitChat(t, made)

	snapshot := h.out.String()

	// A reconnect notice (whose status phase is gated) and a transcript turn
	// (whose debounce flush also calls showPhase) — neither may touch the pane.
	h.client.events <- wsclient.Event{Kind: wsclient.Reconnecting}
	h.events <- transcript.Event{Kind: transcript.TurnStart}

	// The frame still goes on the wire (only sl.Show is gated).
	h.waitSent(t, "ready")

	time.Sleep(60 * time.Millisecond)
	if got := h.out.String(); got != snapshot {
		t.Fatalf("status line wrote while the chat surface was active:\nbefore: %q\nafter:  %q", snapshot, got)
	}
}

// TestChatKilledWhenQuitIgnored: a wedged program that never returns on Quit is
// Kill()ed after the grace window and the loop still tears down and exits.
func TestChatKilledWhenQuitIgnored(t *testing.T) {
	made := stubChatProgram(t, func(f *fakeChatProgram) { f.ignoreQuit = true })
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	h.client.endRunWith(wsclient.ResultSessionEnded)

	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop hung on a Quit-ignoring chat program instead of Kill()ing it")
	}
	if !f.wasKilled() {
		t.Fatal("teardown did not Kill() a program that ignored Quit")
	}
}

// TestSessionEndedTearsDownChatSurface: session_ended stops the chat program and
// the loop returns nil (exit code 0).
func TestSessionEndedTearsDownChatSurface(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	h.client.endRunWith(wsclient.ResultSessionEnded)

	select {
	case <-f.quit:
	case <-time.After(2 * time.Second):
		t.Fatal("session_ended did not stop the chat program")
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v on session_ended, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after session_ended mid-chat")
	}
}

// TestContextCancelTearsDownChatSurface: a ctx cancel stops the chat program and
// the loop returns nil.
func TestContextCancelTearsDownChatSurface(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	h.cancel()

	select {
	case <-f.quit:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx cancel did not stop the chat program")
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("loop returned %v on ctx cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after ctx cancel mid-chat")
	}
}

// TestBlockReportIntentsLoggedContentFree: block/report log a content-free line
// through the program (not a raw sink) and leave the surface running.
func TestBlockReportIntentsLoggedContentFree(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	f.notify(chatui.IntentBlock)
	f.notify(chatui.IntentReport)

	waitUntil(t, "the block log line", func() bool {
		return strings.Contains(f.printed(), "block requested")
	})
	waitUntil(t, "the report log line", func() bool {
		return strings.Contains(f.printed(), "report requested")
	})
	// Routed through the program, not written raw to stderr mid-frame.
	if strings.Contains(h.errBuf.String(), "block requested") || strings.Contains(h.errBuf.String(), "report requested") {
		t.Fatalf("intent log went raw to stderr, not through the program:\n%s", h.errBuf.String())
	}

	// The surface is still up: block/report do not tear it down.
	select {
	case <-f.quit:
		t.Fatal("block/report stopped the chat program")
	case <-time.After(120 * time.Millisecond):
	}
}

// --- chat relay: inbound forward + outbound send (Story 2.4) --------------

// TestInboundChatMsgForwardedToProgram: after a match, a peer proto.ChatMsg on
// client.ChatMsgs() is handed to the live chat program as a chatui.PeerMsg with
// fields intact.
func TestInboundChatMsgForwardedToProgram(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	h.client.chatMsgs <- proto.ChatMsg{ClientMsgID: "peer-1", Text: "hi from them"}

	waitUntil(t, "the forwarded PeerMsg", func() bool {
		for _, m := range f.receivedMsgs() {
			if pm, ok := m.(chatui.PeerMsg); ok && pm.ClientMsgID == "peer-1" && pm.Text == "hi from them" {
				return true
			}
		}
		return false
	})
}

// TestInboundChatMsgWithNoChatIsDropped: a peer line arriving with no active
// chat surface is dropped — no panic, nothing to Out.
func TestInboundChatMsgWithNoChatIsDropped(t *testing.T) {
	h := startLoop(t)

	h.client.chatMsgs <- proto.ChatMsg{ClientMsgID: "peer-1", Text: "nobody listening"}

	// Give the loop a beat to process it, then confirm it is still healthy.
	time.Sleep(40 * time.Millisecond)
	h.events <- transcript.Event{Kind: transcript.TurnStart}
	h.waitSent(t, "ready")
	if strings.Contains(h.out.String(), "nobody listening") {
		t.Fatalf("dropped peer line leaked to Out:\n%s", h.out.String())
	}
}

// TestOutboundSendDrivesSendChat: the WithSend closure the loop passed into the
// chat surface pushes onto chatSends, and the loop calls client.SendChat with
// the right client_msg_id and text.
func TestOutboundSendDrivesSendChat(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	f.send("cmid-1", "hello peer")

	waitUntil(t, "the SendChat call", func() bool {
		for _, m := range h.client.chatSentFrames() {
			if m.ClientMsgID == "cmid-1" && m.Text == "hello peer" {
				return true
			}
		}
		return false
	})
}

// TestOutboundSendEndToEndThroughRealChatModel exercises the real wiring:
// newChatModel builds an actual chatui.Model with the loop's own WithSend
// closure, and a non-empty line + Enter driven straight through that model's
// Update (no PTY, no tea.Program) must reach client.SendChat with the echoed
// text. If chatui.WithSend(send) were dropped from newChatModel this fails.
func TestOutboundSendEndToEndThroughRealChatModel(t *testing.T) {
	oldModel := newChatModel
	var (
		realModel   tea.Model
		capturedSet = make(chan struct{})
	)
	newChatModel = func(m proto.Matched, notify func(chatui.Intent), send func(clientMsgID, text string)) tea.Model {
		realModel = chatui.New(m, chatui.WithNotify(notify), chatui.WithSend(send))
		close(capturedSet)
		return realModel
	}
	t.Cleanup(func() { newChatModel = oldModel })

	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	waitChat(t, made)
	<-capturedSet // realModel is now published (happens-before via the channel)

	// Drive a line + Enter straight through the real model. Only this goroutine
	// touches it — the fake program never Runs it.
	for _, r := range "hello peer" {
		next, _ := realModel.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		realModel = next
	}
	realModel, _ = realModel.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	waitUntil(t, "SendChat with the echoed line", func() bool {
		for _, m := range h.client.chatSentFrames() {
			if m.Text == "hello peer" && m.ClientMsgID != "" {
				return true
			}
		}
		return false
	})
}

// TestOutboundSendFailureLogsContentFree: when SendChat fails, the loop prints
// exactly one content-free line through the program and does not tear down.
func TestOutboundSendFailureLogsContentFree(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.setSendChatErr(wsclient.ErrNotConnected)
	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	f.send("cmid-1", "secret chat text")

	waitUntil(t, "the content-free send-failure line", func() bool {
		return strings.Contains(f.printed(), "a chat line could not be sent")
	})
	if strings.Contains(f.printed(), "secret chat text") || strings.Contains(f.printed(), "cmid-1") {
		t.Fatalf("send-failure log leaked chat content:\n%s", f.printed())
	}
	if strings.Contains(h.errBuf.String(), "a chat line could not be sent") {
		t.Fatalf("send-failure log went raw to stderr, not through the program:\n%s", h.errBuf.String())
	}
	// Not torn down.
	select {
	case <-f.quit:
		t.Fatal("a send failure tore the chat surface down")
	case <-time.After(120 * time.Millisecond):
	}
}

// TestTeardownStopsInboundForwarding: after the surface is torn down (leave), an
// inbound peer line is no longer forwarded anywhere.
func TestTeardownStopsInboundForwarding(t *testing.T) {
	made := stubChatProgram(t)
	h := startLoop(t)

	h.client.matched <- proto.Matched{Opener: "o"}
	f := waitChat(t, made)

	f.notify(chatui.IntentLeave)
	waitUntil(t, "the leave teardown", func() bool {
		select {
		case <-f.quit:
			return true
		default:
			return false
		}
	})
	before := len(f.receivedMsgs())

	h.client.chatMsgs <- proto.ChatMsg{ClientMsgID: "peer-late", Text: "too late"}
	time.Sleep(60 * time.Millisecond)

	for _, m := range f.receivedMsgs() {
		if pm, ok := m.(chatui.PeerMsg); ok && pm.ClientMsgID == "peer-late" {
			t.Fatal("a peer line was forwarded to a torn-down chat program")
		}
	}
	if len(f.receivedMsgs()) != before {
		t.Fatalf("received message count changed after teardown: %d -> %d", before, len(f.receivedMsgs()))
	}
}
