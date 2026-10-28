package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
// so its production route wiring and default /v1 config are exercised.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h, logs := runHub(t)
	return startHarness(t, h, logs, New(h, loggerTo(logs)))
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

// --- relay latency -----------------------------------------------------------

func TestRelayRoundTripP90UnderCeiling(t *testing.T) {
	h := newHarness(t)
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")

	const n = 50
	lat := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		a.mustSend(proto.ChatMsg{ClientMsgID: "c", Text: "ping"})
		if got := b.chatTexts(1); got[0] != "ping" {
			t.Fatalf("B: chat %d = %q, want ping", i, got[0])
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
		v1Hello(t, h, key, "")
	}
	// A second hello on k1 supersedes the first handle and must not add to the count.
	v1Hello(t, h, "k1", "")

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

// The /ws websocket transport was removed in Story 7.6; an old client's upgrade
// handshake now just hits another unknown path.
func TestWSIs404(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequest(http.MethodGet, h.ts.URL+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ws: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status code = %d, want 404", resp.StatusCode)
	}
}

func TestNew_NilLoggerDoesNotPanic(t *testing.T) {
	h, _ := runHub(t)
	handler := New(h, nil)
	if handler == nil {
		t.Fatal("New(h, nil) returned nil")
	}
}
