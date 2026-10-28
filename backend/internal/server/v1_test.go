package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// --- /v1 harness ------------------------------------------------------------

func testV1Config() v1Config {
	return v1Config{
		pollHold:   200 * time.Millisecond,
		leaseAfter: 2 * time.Second,
		handleTTL:  5 * time.Second,
		eventCap:   256,
	}
}

// newV1Harness serves the /v1 routes (and /status) with cfg, so timing-sensitive
// cases can shrink the hold, lease, TTL and cap.
func newV1Harness(t *testing.T, cfg v1Config) *harness {
	t.Helper()
	h, logs := runHub(t)
	srv := &server{hub: h, logger: loggerTo(logs)}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", srv.handleStatus)
	newV1API(h, srv.logger, cfg).register(mux)
	return startHarness(t, h, logs, mux)
}

func shrinkGrace(t *testing.T, d time.Duration) {
	t.Helper()
	orig := hub.ReconnectGraceWindow
	hub.ReconnectGraceWindow = d
	t.Cleanup(func() { hub.ReconnectGraceWindow = orig })
}

// v1Client is a minimal mod stand-in: it holds a handle and the last acked seq.
type v1Client struct {
	t      *testing.T
	base   string
	handle string
	after  int64
}

type v1Event struct {
	Seq  int64  `json:"seq"`
	Type string `json:"type"`
	V    int    `json:"v"`
	raw  json.RawMessage
}

func do(t *testing.T, method, url, handle string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if handle != "" {
		req.Header.Set(handleHeader, handle)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func encode(t *testing.T, msg any) []byte {
	t.Helper()
	b, err := proto.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeBody(t *testing.T, b []byte) any {
	t.Helper()
	m, err := proto.Decode(b)
	if err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

// hello posts a hello and returns a client on the minted handle.
func v1Hello(t *testing.T, h *harness, key, sessionID string) *v1Client {
	t.Helper()
	code, b := do(t, http.MethodPost, h.ts.URL+"/v1/hello", "",
		encode(t, proto.Hello{AccountKey: key, ProtocolVersion: proto.PROTOCOL_VERSION, SessionID: sessionID}))
	if code != http.StatusOK {
		t.Fatalf("hello: status %d body %s", code, b)
	}
	w, ok := decodeBody(t, b).(proto.Welcome)
	if !ok || w.Handle == "" {
		t.Fatalf("hello: want welcome with a handle, got %s", b)
	}
	return &v1Client{t: t, base: h.ts.URL, handle: w.Handle}
}

func (c *v1Client) send(msg any) int {
	c.t.Helper()
	code, _ := do(c.t, http.MethodPost, c.base+"/v1/send", c.handle, encode(c.t, msg))
	return code
}

func (c *v1Client) mustSend(msg any) {
	c.t.Helper()
	if code := c.send(msg); code != http.StatusNoContent {
		c.t.Fatalf("send %T: status %d, want 204", msg, code)
	}
}

// tryPoll does one GET /v1/events?after=after without acking anything. It
// never calls t.Fatal, so it is safe from a helper goroutine.
func (c *v1Client) tryPoll(after int64) (int, []v1Event, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/events?after=%d", c.base, after), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set(handleHeader, c.handle)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(b, &raws); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("events body %s: %w", b, err)
	}
	evs := make([]v1Event, 0, len(raws))
	for _, r := range raws {
		var e v1Event
		if err := json.Unmarshal(r, &e); err != nil {
			return resp.StatusCode, nil, err
		}
		e.raw = r
		evs = append(evs, e)
	}
	return resp.StatusCode, evs, nil
}

// pollRaw does one GET /v1/events?after=c.after without acking anything.
func (c *v1Client) pollRaw() (int, []v1Event) {
	c.t.Helper()
	code, evs, err := c.tryPoll(c.after)
	if err != nil {
		c.t.Fatalf("poll: %v", err)
	}
	return code, evs
}

// chatTexts polls until n chat lines arrive (failing on any other event type
// or after 5s) and returns their texts in order.
func (c *v1Client) chatTexts(n int) []string {
	c.t.Helper()
	var got []string
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < n {
		if time.Now().After(deadline) {
			c.t.Fatalf("got %d chat lines %v, want %d", len(got), got, n)
		}
		for _, e := range c.poll() {
			m, ok := eventMsg(c.t, e).(proto.ChatMsg)
			if !ok {
				c.t.Fatalf("got %s, want only chat_msg", e.Type)
			}
			got = append(got, m.Text)
		}
	}
	return got
}

// poll does one poll, requires 200, checks seq order, and acks what it got.
func (c *v1Client) poll() []v1Event {
	c.t.Helper()
	code, evs := c.pollRaw()
	if code != http.StatusOK {
		c.t.Fatalf("poll: status %d, want 200", code)
	}
	for _, e := range evs {
		if e.Seq <= c.after {
			c.t.Fatalf("poll: seq %d not after %d", e.Seq, c.after)
		}
		c.after = e.Seq
	}
	return evs
}

// until polls until an event of type want arrives, returning every event seen.
func (c *v1Client) until(want string) []v1Event {
	c.t.Helper()
	var seen []v1Event
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range c.poll() {
			seen = append(seen, e)
			if e.Type == want {
				return seen
			}
		}
	}
	c.t.Fatalf("never got %q; saw %v", want, types(seen))
	return nil
}

// quiet asserts that polls over d deliver nothing.
func (c *v1Client) quiet(d time.Duration) {
	c.t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if evs := c.poll(); len(evs) > 0 {
			c.t.Fatalf("want no events, got %v", types(evs))
		}
	}
}

func types(evs []v1Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func eventMsg(t *testing.T, e v1Event) any {
	t.Helper()
	return decodeBody(t, e.raw)
}

// matchPair hellos two keys, readies both, and returns them matched.
func matchPair(t *testing.T, h *harness, keyA, keyB string) (*v1Client, *v1Client, string) {
	t.Helper()
	a := v1Hello(t, h, keyA, "")
	b := v1Hello(t, h, keyB, "")
	a.mustSend(proto.Ready{})
	b.mustSend(proto.Ready{})
	ea := a.until(proto.TypeMatched)
	eb := b.until(proto.TypeMatched)
	ma := eventMsg(t, ea[len(ea)-1]).(proto.Matched)
	mb := eventMsg(t, eb[len(eb)-1]).(proto.Matched)
	if ma.SessionID == "" || ma.SessionID != mb.SessionID {
		t.Fatalf("session ids %q / %q, want equal and non-empty", ma.SessionID, mb.SessionID)
	}
	return a, b, ma.SessionID
}

// --- hello ------------------------------------------------------------------

func TestV1HelloWelcomesWithHandle(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a := v1Hello(t, h, "acct-a", "")
	b := v1Hello(t, h, "acct-b", "")
	if a.handle == b.handle {
		t.Fatal("two hellos minted the same handle")
	}
	if len(a.handle) < 40 {
		t.Errorf("handle %q looks guessable", a.handle)
	}
	waitCount(t, h.hub, 2)
}

func TestV1HelloTooOldGetsPleaseUpdateAndNoHandle(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	code, b := do(t, http.MethodPost, h.ts.URL+"/v1/hello", "",
		encode(t, proto.Hello{AccountKey: "acct-old", ProtocolVersion: proto.PROTOCOL_VERSION - 2}))
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if _, ok := decodeBody(t, b).(proto.PleaseUpdate); !ok {
		t.Fatalf("body %s, want please_update", b)
	}
	if n := h.hub.Count(); n != 0 {
		t.Fatalf("hub.Count = %d, want 0", n)
	}
}

func TestV1HelloEdgeVersionsWelcomed(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	for i, pv := range []int{proto.PROTOCOL_VERSION - 1, proto.PROTOCOL_VERSION + 5} {
		code, b := do(t, http.MethodPost, h.ts.URL+"/v1/hello", "",
			encode(t, proto.Hello{AccountKey: fmt.Sprintf("acct-edge-%d", i), ProtocolVersion: pv}))
		if code != http.StatusOK {
			t.Fatalf("pv %d: status %d, want 200", pv, code)
		}
		if w, ok := decodeBody(t, b).(proto.Welcome); !ok || w.Handle == "" {
			t.Fatalf("pv %d: body %s, want welcome with a handle", pv, b)
		}
	}
}

func TestV1BadHelloIs400WithError(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	cases := []struct {
		name, body, code string
	}{
		{"undecodable", `{"type":`, "bad_frame"},
		{"empty", ``, "bad_frame"},
		{"not hello", `{"type":"ready","v":1}`, "expected_hello"},
		{"empty key", `{"type":"hello","v":1,"protocol_version":1,"account_key":""}`, "bad_frame"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, b := do(t, http.MethodPost, h.ts.URL+"/v1/hello", "", []byte(tc.body))
			if code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", code)
			}
			e, ok := decodeBody(t, b).(proto.Error)
			if !ok || e.Code != tc.code {
				t.Fatalf("body %s, want error %q", b, tc.code)
			}
		})
	}
	if n := h.hub.Count(); n != 0 {
		t.Fatalf("hub.Count = %d, want 0", n)
	}
}

func TestV1WrongMethodIs405(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/hello"},
		{http.MethodPost, "/v1/events"},
		{http.MethodGet, "/v1/send"},
	} {
		if code, _ := do(t, tc.method, h.ts.URL+tc.path, "", nil); code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status %d, want 405", tc.method, tc.path, code)
		}
	}
}

// --- supersede (AD-16) --------------------------------------------------------

func TestV1SecondHelloSupersedesIdleHandle(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	old := v1Hello(t, h, "acct-twice", "")
	old.mustSend(proto.Ready{})
	old.until(proto.TypeQueued)

	fresh := v1Hello(t, h, "acct-twice", "")

	evs := old.poll()
	if len(evs) != 1 || evs[0].Type != proto.TypeSessionEnded {
		t.Fatalf("superseded handle got %v, want exactly [session_ended]", types(evs))
	}
	if code, _ := old.pollRaw(); code != http.StatusGone {
		t.Fatalf("superseded poll after drain: status %d, want 410", code)
	}
	if code := old.send(proto.Ready{}); code != http.StatusGone {
		t.Fatalf("superseded send: status %d, want 410", code)
	}
	fresh.mustSend(proto.Ready{})
	fresh.until(proto.TypeQueued)
	waitCount(t, h.hub, 1)
}

func TestV1SupersedeWhilePairedEndsPeerAndOldHandleOnce(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")

	v1Hello(t, h, "acct-a", "")

	b.until(proto.TypeSessionEnded)
	evs := a.poll()
	if len(evs) != 1 || evs[0].Type != proto.TypeSessionEnded {
		t.Fatalf("superseded paired handle got %v, want exactly [session_ended]", types(evs))
	}
	if code, _ := a.pollRaw(); code != http.StatusGone {
		t.Fatalf("status %d, want 410", code)
	}
}

func TestV1SupersedeDoesNotDuplicateTrailingSessionEnded(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")
	b.mustSend(proto.Leave{})
	// Wait until a's session_ended is queued, but do not ack it.
	saved := a.after
	a.until(proto.TypeSessionEnded)
	a.after = saved

	v1Hello(t, h, "acct-a", "")

	evs := a.poll()
	if got := types(evs); len(got) != 1 || got[0] != proto.TypeSessionEnded {
		t.Fatalf("got %v, want one session_ended (never two in a row)", got)
	}
}

// --- events -------------------------------------------------------------------

func TestV1EventsReturnInSeqOrderAndAckDrops(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")

	for i := 0; i < 5; i++ {
		b.mustSend(proto.ChatMsg{ClientMsgID: fmt.Sprint("m", i), Text: fmt.Sprint("line ", i)})
	}
	got := a.chatTexts(5)
	for i, txt := range got {
		if txt != fmt.Sprint("line ", i) {
			t.Fatalf("chat order %v", got)
		}
	}
	// Everything ≤ after is acked: a re-poll at the same after returns nothing.
	if evs := a.poll(); len(evs) != 0 {
		t.Fatalf("acked events came back: %v", types(evs))
	}
	// Re-polling at an older after is at-least-once only for events not yet
	// dropped; acked ones are gone.
	a.after = 0
	if evs := a.poll(); len(evs) != 0 {
		t.Fatalf("dropped events came back: %v", types(evs))
	}
}

func TestV1EventsHeldThenEmpty(t *testing.T) {
	cfg := testV1Config()
	cfg.pollHold = 300 * time.Millisecond
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-idle", "")
	start := time.Now()
	evs := a.poll()
	if len(evs) != 0 {
		t.Fatalf("got %v, want []", types(evs))
	}
	if el := time.Since(start); el < 250*time.Millisecond {
		t.Fatalf("poll returned after %v, want it held ~300ms", el)
	}
}

func TestV1EventsHeldPollWakesOnEvent(t *testing.T) {
	cfg := testV1Config()
	cfg.pollHold = 5 * time.Second
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	done := make(chan []v1Event, 1)
	go func() {
		_, evs, err := a.tryPoll(0)
		if err != nil {
			t.Errorf("held poll: %v", err)
		}
		done <- evs
	}()
	time.Sleep(100 * time.Millisecond)
	a.mustSend(proto.Ready{})
	select {
	case evs := <-done:
		if len(evs) != 1 || evs[0].Type != proto.TypeQueued {
			t.Fatalf("got %v, want [queued]", types(evs))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held poll did not wake on a new event")
	}
}

func TestV1EventsBadAfterIs400(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a := v1Hello(t, h, "acct-a", "")
	for _, q := range []string{"x", "-1", "1.5"} {
		code, b := do(t, http.MethodGet, h.ts.URL+"/v1/events?after="+q, a.handle, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("after=%s: status %d, want 400", q, code)
		}
		if _, ok := decodeBody(t, b).(proto.Error); !ok {
			t.Fatalf("after=%s: body %s, want error", q, b)
		}
	}
}

func TestV1OverflowEndsSessionNotSilentDrop(t *testing.T) {
	cfg := testV1Config()
	cfg.eventCap = 3
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	b := v1Hello(t, h, "acct-b", "")
	a.mustSend(proto.Ready{})
	b.mustSend(proto.Ready{})
	b.until(proto.TypeMatched)

	// a never polls, so its queue fills: queued?, matched, chat… then overflow.
	for i := 0; i < 10; i++ {
		b.mustSend(proto.ChatMsg{ClientMsgID: fmt.Sprint("m", i), Text: "x"})
	}
	b.until(proto.TypeSessionEnded) // the overflow leave ended b's side

	evs := a.poll()
	got := types(evs)
	if len(got) != cfg.eventCap+1 || got[len(got)-1] != proto.TypeSessionEnded {
		t.Fatalf("got %v, want %d queued events then one session_ended", got, cfg.eventCap)
	}
	for _, typ := range got[:len(got)-1] {
		if typ == proto.TypeSessionEnded {
			t.Fatalf("session_ended before the end: %v", got)
		}
	}
	// The pairing is gone: later chat from a reaches no one, and a is quiet.
	a.mustSend(proto.ChatMsg{ClientMsgID: "late", Text: "x"})
	b.quiet(300 * time.Millisecond)
	a.quiet(300 * time.Millisecond)
}

// --- send -------------------------------------------------------------------

func TestV1ChatRelayKeepsOrderAndMultiByteText(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")
	want := []string{"first", "héllo — 你好 👋🏽", "third\nline"}
	for i, text := range want {
		a.mustSend(proto.ChatMsg{ClientMsgID: fmt.Sprintf("c%d", i), Text: text})
	}
	got := b.chatTexts(len(want))
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The peer's session_ended is the same bare frame whatever ended the pairing.
func TestV1PeerSessionEndedBytesIdenticalAcrossCauses(t *testing.T) {
	canonical := string(encode(t, proto.SessionEnded{}))
	causes := map[string]func(t *testing.T, h *harness, a *v1Client, key string){
		"busy":      func(t *testing.T, h *harness, a *v1Client, key string) { a.mustSend(proto.Busy{}) },
		"leave":     func(t *testing.T, h *harness, a *v1Client, key string) { a.mustSend(proto.Leave{}) },
		"supersede": func(t *testing.T, h *harness, a *v1Client, key string) { v1Hello(t, h, key, "") },
	}
	for name, end := range causes {
		t.Run(name, func(t *testing.T) {
			h := newV1Harness(t, testV1Config())
			a, b, _ := matchPair(t, h, "acct-a-"+name, "acct-b-"+name)
			end(t, h, a, "acct-a-"+name)
			evs := b.until(proto.TypeSessionEnded)
			var m map[string]any
			if err := json.Unmarshal(evs[len(evs)-1].raw, &m); err != nil {
				t.Fatal(err)
			}
			delete(m, "seq")
			got, _ := json.Marshal(m)
			if string(got) != canonical {
				t.Fatalf("session_ended = %s, want %s", got, canonical)
			}
		})
	}
}

func TestV1SendRoutesAndValidates(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a := v1Hello(t, h, "acct-a", "")

	// Client messages with no hub meaning yet are accepted and ignored.
	for _, m := range []any{proto.Block{}, proto.Report{LastN: 3}, proto.ProfilePut{}, proto.ForgetMe{},
		proto.NotePut{}, proto.Hello{AccountKey: "acct-a", ProtocolVersion: 1}, proto.Leave{}, proto.Busy{},
		proto.ChatMsg{ClientMsgID: "u", Text: "unpaired"}} {
		a.mustSend(m)
	}
	a.quiet(300 * time.Millisecond)

	// Server→client types and junk are protocol errors: 400 + error body.
	for _, body := range []string{
		string(encode(t, proto.Queued{})),
		string(encode(t, proto.Welcome{Handle: "x"})),
		`{"type":"heartbeat","v":1}`,
		`{"type":`,
		``,
	} {
		code, b := do(t, http.MethodPost, h.ts.URL+"/v1/send", a.handle, []byte(body))
		if code != http.StatusBadRequest {
			t.Fatalf("send %q: status %d, want 400", body, code)
		}
		if _, ok := decodeBody(t, b).(proto.Error); !ok {
			t.Fatalf("send %q: body %s, want error", body, b)
		}
	}

	a.mustSend(proto.Ready{})
	a.until(proto.TypeQueued)
}

func TestV1OversizedBodyIs400(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a := v1Hello(t, h, "acct-a", "")
	big := `{"type":"chat_msg","v":1,"client_msg_id":"x","text":"` + strings.Repeat("a", maxV1Body) + `"}`
	if code, _ := do(t, http.MethodPost, h.ts.URL+"/v1/send", a.handle, []byte(big)); code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
}

func TestV1UnknownHandleIs410(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	for _, hd := range []string{"", "not-a-handle"} {
		code, b := do(t, http.MethodGet, h.ts.URL+"/v1/events", hd, nil)
		if code != http.StatusGone {
			t.Fatalf("events handle %q: status %d, want 410", hd, code)
		}
		if e, ok := decodeBody(t, b).(proto.Error); !ok || e.Code != "handle_gone" {
			t.Fatalf("events body %s, want error handle_gone", b)
		}
		if code, _ := do(t, http.MethodPost, h.ts.URL+"/v1/send", hd, encode(t, proto.Ready{})); code != http.StatusGone {
			t.Fatalf("send handle %q: status %d, want 410", hd, code)
		}
	}
}

// --- lease / grace (AD-17) ------------------------------------------------------

func TestV1LeaseLapseUnregistersIdleHandle(t *testing.T) {
	cfg := testV1Config()
	cfg.leaseAfter = 100 * time.Millisecond
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	a.mustSend(proto.Ready{})
	waitCount(t, h.hub, 1)
	waitCount(t, h.hub, 0) // no poll: the lease lapses and the queued session leaves

	// A later poll inside the handle TTL re-registers and replays ready.
	evs := a.until(proto.TypeQueued)
	if got := types(evs); got[len(got)-1] != proto.TypeQueued {
		t.Fatalf("got %v", got)
	}
}

func TestV1LeaseLapsedHandleExpiresAfterTTL(t *testing.T) {
	cfg := testV1Config()
	cfg.leaseAfter = 50 * time.Millisecond
	cfg.handleTTL = 100 * time.Millisecond
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	time.Sleep(400 * time.Millisecond)
	if code, _ := a.pollRaw(); code != http.StatusGone {
		t.Fatalf("status %d, want 410 after the handle TTL", code)
	}
}

func TestV1PollResumesInsideGraceAndKeepsGraceTimeChat(t *testing.T) {
	shrinkGrace(t, 3*time.Second)
	cfg := testV1Config()
	cfg.leaseAfter = 100 * time.Millisecond
	h := newV1Harness(t, cfg)
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")

	// b keeps polling (its lease holds); a goes silent and lapses.
	stop := make(chan struct{})
	var bSeen []v1Event
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, evs, err := b.tryPoll(b.after)
			if err != nil {
				t.Errorf("b poll: %v", err)
				return
			}
			for _, e := range evs {
				b.after = e.Seq
				bSeen = append(bSeen, e)
			}
		}
	}()
	time.Sleep(400 * time.Millisecond) // a's lease (100ms) lapses; grace runs

	sendRaw := func(c *v1Client, text string) {
		code, _ := do(t, http.MethodPost, c.base+"/v1/send", c.handle,
			encode(t, proto.ChatMsg{ClientMsgID: text, Text: text}))
		if code != http.StatusNoContent {
			t.Errorf("send: %d", code)
		}
	}
	sendRaw(b, "during-grace-1")
	sendRaw(b, "during-grace-2")

	// a polls inside grace: it resumes and gets both lines, in order.
	got := a.chatTexts(2)
	if got[0] != "during-grace-1" || got[1] != "during-grace-2" {
		t.Fatalf("grace-time chat %v", got)
	}

	// The pairing is live on the resumed handle: a → b still relays.
	a.mustSend(proto.ChatMsg{ClientMsgID: "after", Text: "after-resume"})
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-bDone
	sawAfter := false
	for _, e := range bSeen {
		if e.Type == proto.TypeSessionEnded {
			t.Fatal("peer saw session_ended across a resume inside grace")
		}
		if e.Type == proto.TypeChatMsg && eventMsg(t, e).(proto.ChatMsg).Text == "after-resume" {
			sawAfter = true
		}
	}
	if !sawAfter {
		t.Fatalf("b never got the post-resume line; saw %v", types(bSeen))
	}
}

func TestV1PollAfterGraceExpiryEndsThenRequeues(t *testing.T) {
	shrinkGrace(t, 100*time.Millisecond)
	cfg := testV1Config()
	cfg.leaseAfter = 100 * time.Millisecond
	h := newV1Harness(t, cfg)
	a, b, _ := matchPair(t, h, "acct-a", "acct-b")

	// b keeps polling; a lapses and its grace expires.
	b.until(proto.TypeSessionEnded)

	// a comes back: session_ended for the lost chat, then (ready replayed, b
	// silently re-enqueued by the hub) a fresh match.
	evs := a.until(proto.TypeMatched)
	got := types(evs)
	if got[0] != proto.TypeSessionEnded {
		t.Fatalf("a got %v, want session_ended first", got)
	}
	b.until(proto.TypeMatched)
}

func TestV1HelloWithSessionIDResumesInsideGrace(t *testing.T) {
	shrinkGrace(t, 3*time.Second)
	cfg := testV1Config()
	cfg.leaseAfter = 100 * time.Millisecond
	h := newV1Harness(t, cfg)
	a, b, sid := matchPair(t, h, "acct-a", "acct-b")

	time.Sleep(400 * time.Millisecond) // a lapses (b too, but b's poll below resumes it)

	a2 := v1Hello(t, h, "acct-a", sid)
	b.mustSend(proto.ChatMsg{ClientMsgID: "x", Text: "to-the-new-handle"})
	evs := a2.until(proto.TypeChatMsg)
	if got := types(evs); len(got) != 1 {
		t.Fatalf("resumed handle got %v, want just the chat line", got)
	}
	b.quiet(300 * time.Millisecond) // no session_ended to the peer

	// The old handle is retired: a terminal session_ended, then 410.
	if evs := a.poll(); len(evs) != 1 || evs[0].Type != proto.TypeSessionEnded {
		t.Fatalf("old handle got %v, want [session_ended]", types(evs))
	}
	if code, _ := a.pollRaw(); code != http.StatusGone {
		t.Fatalf("old handle: status %d, want 410", code)
	}
}

// --- contract -------------------------------------------------------------------

// TestV1ContractMatchChatLeaveRequeue drives two HTTP clients through the whole
// loop the mod relies on.
func TestV1ContractMatchChatLeaveRequeue(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a, b, sid := matchPair(t, h, "acct-a", "acct-b")

	a.mustSend(proto.ChatMsg{ClientMsgID: "a1", Text: "hi from a"})
	eb := b.until(proto.TypeChatMsg)
	if m := eventMsg(t, eb[len(eb)-1]).(proto.ChatMsg); m.Text != "hi from a" || m.ClientMsgID != "a1" {
		t.Fatalf("b got %+v", m)
	}
	b.mustSend(proto.ChatMsg{ClientMsgID: "b1", Text: "hi from b"})
	ea := a.until(proto.TypeChatMsg)
	if m := eventMsg(t, ea[len(ea)-1]).(proto.ChatMsg); m.Text != "hi from b" {
		t.Fatalf("a got %+v", m)
	}
	// Never echoed: neither side sees its own line.
	noOwnLine := func(evs []v1Event, own string) {
		t.Helper()
		for _, e := range evs {
			if e.Type == proto.TypeChatMsg && eventMsg(t, e).(proto.ChatMsg).Text == own {
				t.Fatalf("own line echoed: %s", e.raw)
			}
		}
	}
	noOwnLine(ea, "hi from a")
	noOwnLine(eb, "hi from b")

	// a leaves: b gets session_ended and is silently re-enqueued; a gets nothing.
	a.mustSend(proto.Leave{})
	b.until(proto.TypeSessionEnded)
	a.quiet(300 * time.Millisecond)

	// a readies again and is rematched with b under a fresh session_id.
	a.mustSend(proto.Ready{})
	ea = a.until(proto.TypeMatched)
	eb = b.until(proto.TypeMatched)
	sa := eventMsg(t, ea[len(ea)-1]).(proto.Matched).SessionID
	sb := eventMsg(t, eb[len(eb)-1]).(proto.Matched).SessionID
	if sa != sb || sa == sid {
		t.Fatalf("rematch session ids %q/%q (first %q), want equal and fresh", sa, sb, sid)
	}
	for _, e := range eb {
		if e.Type == proto.TypeQueued {
			t.Fatal("b's silent re-enqueue surfaced a queued event")
		}
	}
}

func TestV1LogsCarryNoHandleKeyOrText(t *testing.T) {
	shrinkGrace(t, 100*time.Millisecond)
	cfg := testV1Config()
	cfg.leaseAfter = 150 * time.Millisecond
	cfg.handleTTL = 200 * time.Millisecond
	cfg.eventCap = 4
	h := newV1Harness(t, cfg)

	const keyA, keyB = "acct-secret-v1-aaa", "acct-secret-v1-bbb"
	const text = "please-do-not-log-this-v1-text"

	a, b, _ := matchPair(t, h, keyA, keyB)
	a.mustSend(proto.ChatMsg{ClientMsgID: "c1", Text: text})
	b.until(proto.TypeChatMsg)
	for i := 0; i < 6; i++ { // overflow b's queue
		a.mustSend(proto.ChatMsg{ClientMsgID: "c", Text: text})
	}
	a.until(proto.TypeSessionEnded)
	a2 := v1Hello(t, h, keyA, "") // supersede
	_, _ = do(t, http.MethodPost, h.ts.URL+"/v1/hello", "",
		encode(t, proto.Hello{AccountKey: keyB, ProtocolVersion: proto.PROTOCOL_VERSION - 5}))
	_, _ = do(t, http.MethodPost, h.ts.URL+"/v1/hello", "", []byte(`{"type":"ready","v":1}`))
	time.Sleep(600 * time.Millisecond) // lease lapse + handle expiry

	logs := h.logs.String()
	for _, secret := range []string{keyA, keyB, text, a.handle, b.handle, a2.handle} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain %q:\n%s", secret, logs)
		}
	}
	for _, want := range []string{"v1 hello", "v1 superseded", "v1 lease lapsed", "v1 please_update", "v1 bad hello", "v1 event queue overflow"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q", want)
		}
	}
}

// --- review follow-ups ------------------------------------------------------------

// TestV1RoutesServedByNew drives the real constructor, so the production /v1
// wiring and default config are exercised end to end.
func TestV1RoutesServedByNew(t *testing.T) {
	h := newHarness(t)
	if code, _ := do(t, http.MethodGet, h.ts.URL+"/v1/hello", "", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/hello via New: status %d, want 405", code)
	}
	a := v1Hello(t, h, "acct-new", "")
	a.mustSend(proto.Ready{})
	a.until(proto.TypeQueued)
}

func TestV1EventsAfterBeyondLastSeqIs400(t *testing.T) {
	h := newV1Harness(t, testV1Config())
	a := v1Hello(t, h, "acct-a", "")
	a.mustSend(proto.Ready{})
	a.until(proto.TypeQueued)
	a.after = 99
	code, b := do(t, http.MethodGet, h.ts.URL+"/v1/events?after=99", a.handle, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
	if _, ok := decodeBody(t, b).(proto.Error); !ok {
		t.Fatalf("body %s, want error", b)
	}
}

func TestV1OverflowedHandleChatsAgainAfterRematch(t *testing.T) {
	cfg := testV1Config()
	cfg.eventCap = 3
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	b := v1Hello(t, h, "acct-b", "")
	a.mustSend(proto.Ready{})
	b.mustSend(proto.Ready{})
	b.until(proto.TypeMatched)
	for i := 0; i < 6; i++ {
		b.mustSend(proto.ChatMsg{ClientMsgID: fmt.Sprint("m", i), Text: "x"})
	}
	b.until(proto.TypeSessionEnded)
	a.until(proto.TypeSessionEnded)

	// b was silently re-enqueued; a readies again and they rematch.
	a.mustSend(proto.Ready{})
	a.until(proto.TypeMatched)
	b.until(proto.TypeMatched)
	b.mustSend(proto.ChatMsg{ClientMsgID: "again", Text: "after-rematch"})
	if got := a.chatTexts(1); got[0] != "after-rematch" {
		t.Fatalf("got %v", got)
	}
}

func TestV1OverflowBlocksNoStateEvents(t *testing.T) {
	cfg := testV1Config()
	cfg.eventCap = 2
	h := newV1Harness(t, cfg)
	a := v1Hello(t, h, "acct-a", "")
	b := v1Hello(t, h, "acct-b", "")
	a.mustSend(proto.Ready{}) // a: queued
	b.mustSend(proto.Ready{}) // a: matched — at the cap, still appended
	b.until(proto.TypeMatched)
	for i := 0; i < 3; i++ {
		b.mustSend(proto.ChatMsg{ClientMsgID: fmt.Sprint("m", i), Text: "x"})
	}
	b.until(proto.TypeSessionEnded)
	// With a's queue still unacked and over the cap, a fresh match must not be lost.
	a.mustSend(proto.Ready{})
	b.until(proto.TypeMatched)
	got := types(a.poll())
	want := []string{proto.TypeQueued, proto.TypeMatched, proto.TypeSessionEnded, proto.TypeMatched}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// After busy or leave, a lease lapse must not replay ready.
func TestV1LapseAfterBusyOrLeaveDoesNotRequeue(t *testing.T) {
	for _, last := range []any{proto.Busy{}, proto.Leave{}} {
		t.Run(fmt.Sprintf("%T", last), func(t *testing.T) {
			cfg := testV1Config()
			cfg.leaseAfter = 100 * time.Millisecond
			h := newV1Harness(t, cfg)
			a, b, _ := matchPair(t, h, "acct-a", "acct-b")
			a.mustSend(last)
			b.until(proto.TypeSessionEnded)
			time.Sleep(300 * time.Millisecond) // both leases lapse; neither is paired
			a.quiet(400 * time.Millisecond)    // a re-registers: no replayed ready, no queued
			b.until(proto.TypeQueued)          // b re-registers and its ready is replayed
			c := v1Hello(t, h, "acct-c", "")
			c.mustSend(proto.Ready{})
			c.until(proto.TypeMatched) // c pairs with b; a, had it been requeued, was ahead
			b.until(proto.TypeMatched)
			a.quiet(300 * time.Millisecond)
		})
	}
}
