package hub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/store"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// startHubWith is startHub with options.
func startHubWith(t *testing.T, opts ...Option) (*Hub, func()) {
	t.Helper()
	h := New(opts...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	return h, func() {
		cancel()
		<-done
	}
}

// readyInOrder registers and readies each session, a beat apart so the queue
// order is deterministic (as TestFIFOOrderWithThreeWaiters does).
func readyInOrder(h *Hub, ss ...*Session) {
	for _, s := range ss {
		h.Register(s)
		h.Ready(s)
		time.Sleep(10 * time.Millisecond)
	}
}

func expectQueuedOnly(t *testing.T, s *Session) {
	t.Helper()
	if _, ok := recvOutbound(t, s).(proto.Queued); !ok {
		t.Fatalf("%s: expected a queued frame", s.Key)
	}
	expectNoOutbound(t, s)
}

// The head skips a successor policy forbids and pairs with the next one.
func TestPolicyKeepsPairApartAndHeadTakesNextEligible(t *testing.T) {
	for name, p := range map[string]store.Policy{
		"a blocked b":   {Blocks: [][2]string{{"a", "b"}}},
		"b blocked a":   {Blocks: [][2]string{{"b", "a"}}},
		"live cooldown": {Cooldowns: []store.Cooldown{{KeyA: "a", KeyB: "b", ExpiresMs: time.Now().Add(time.Hour).UnixMilli()}}},
	} {
		t.Run(name, func(t *testing.T) {
			h, stop := startHubWith(t, WithPolicy(p, nil))
			defer stop()
			a, b, c := newSession("a"), newSession("b"), newSession("c")
			readyInOrder(h, a, b, c)

			ma, mc := matchedFrame(t, a), matchedFrame(t, c)
			if ma.SessionID == "" || ma.SessionID != mc.SessionID {
				t.Fatalf("a/c should pair: %q vs %q", ma.SessionID, mc.SessionID)
			}
			expectQueuedOnly(t, b)
		})
	}
}

// A banned key never joins the queue, so it cannot sit at the head and stall
// everyone behind it; it gets no frame at all (Story 4.9 owns what it sees).
func TestBannedKeyNeverQueuesOrStallsTheQueue(t *testing.T) {
	h, stop := startHubWith(t, WithPolicy(store.Policy{Bans: []string{"a"}}, nil))
	defer stop()
	a, b, c := newSession("a"), newSession("b"), newSession("c")
	readyInOrder(h, a, b, c)
	mb, mc := matchedFrame(t, b), matchedFrame(t, c)
	if mb.SessionID == "" || mb.SessionID != mc.SessionID {
		t.Fatal("b/c should pair with a banned a ahead of them")
	}
	expectNoOutbound(t, a)
}

// A banned successor is never in the queue either: the head pairs past it.
func TestBannedSuccessorIsSkipped(t *testing.T) {
	h, stop := startHubWith(t, WithPolicy(store.Policy{Bans: []string{"b"}}, nil))
	defer stop()
	a, b, c := newSession("a"), newSession("b"), newSession("c")
	readyInOrder(h, a, b, c)
	ma, mc := matchedFrame(t, a), matchedFrame(t, c)
	if ma.SessionID != mc.SessionID {
		t.Fatal("a/c should pair")
	}
	expectNoOutbound(t, b)
}

// fakeClock is a settable now for cooldown expiry.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestCooldownExpiresAndThePairBecomesEligible(t *testing.T) {
	clk := &fakeClock{t: time.UnixMilli(1_000_000)}
	p := store.Policy{Cooldowns: []store.Cooldown{{KeyA: "a", KeyB: "b", ExpiresMs: 1_000_500}}}
	h, stop := startHubWith(t, WithPolicy(p, nil), WithClock(clk.now))
	defer stop()

	a, b := newSession("a"), newSession("b")
	readyInOrder(h, a, b)
	expectQueuedOnly(t, a)
	expectQueuedOnly(t, b)

	// Past the expiry, the next drain (c arriving) pairs the head with b.
	clk.advance(time.Second)
	c := newSession("c")
	readyInOrder(h, c)
	ma, mb := matchedFrame(t, a), matchedFrame(t, b)
	if ma.SessionID != mb.SessionID {
		t.Fatal("a/b should pair once the cooldown expired")
	}
	expectQueuedOnly(t, c)
}

// countingWriter records every write-through and can fail them.
type countingWriter struct {
	mu    sync.Mutex
	calls []string
	fail  error
}

func (w *countingWriter) record(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, s)
	return w.fail
}

func (w *countingWriter) PutBlock(_ context.Context, blocker, blocked string, _ int64) error {
	return w.record("block " + blocker + " " + blocked)
}

func (w *countingWriter) PutCooldown(_ context.Context, a, b string, _ int64) error {
	return w.record("cooldown " + a + " " + b)
}

func (w *countingWriter) PutBan(_ context.Context, key string, _ int64, note string) error {
	return w.record("ban " + key + " " + note)
}

func (w *countingWriter) n() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

// Matching and teardown never touch the writer: policy is read from memory.
func TestMatchingNeverTouchesTheWriter(t *testing.T) {
	w := &countingWriter{}
	h, stop := startHubWith(t, WithPolicy(store.Policy{Blocks: [][2]string{{"a", "b"}}}, w))
	defer stop()
	a, b, c := newSession("a"), newSession("b"), newSession("c")
	readyInOrder(h, a, b, c)
	matchedFrame(t, a)
	matchedFrame(t, c)
	h.Leave(a)
	time.Sleep(20 * time.Millisecond)
	if w.n() != 0 {
		t.Fatalf("writer called on the match path: %v", w.calls)
	}
}

func TestWriteThroughUpdatesMemoryThenWriter(t *testing.T) {
	clk := &fakeClock{t: time.UnixMilli(5_000)}
	w := &countingWriter{}
	ps := newPolicyState(config{writer: w, now: clk.now})
	ctx := context.Background()

	if !ps.allows("a", "b") {
		t.Fatal("empty policy should allow a/b")
	}
	ps.addBlock(ctx, "a", "b")
	if ps.allows("b", "a") {
		t.Fatal("a block keeps the pair apart in both directions")
	}
	ps.addCooldown(ctx, "d", "c", time.Second)
	if ps.allows("c", "d") {
		t.Fatal("live cooldown should keep c/d apart")
	}
	clk.advance(2 * time.Second)
	if !ps.allows("c", "d") {
		t.Fatal("expired cooldown should allow c/d")
	}
	ps.addBan(ctx, "e", "spam")
	if ps.allows("e", "f") || ps.allows("f", "e") {
		t.Fatal("a ban keeps e out of every pair")
	}
	want := []string{"block a b", "cooldown d c", "ban e spam"}
	if len(w.calls) != len(want) {
		t.Fatalf("writer calls = %v, want %v", w.calls, want)
	}
	for i := range want {
		if w.calls[i] != want[i] {
			t.Fatalf("writer calls = %v, want %v", w.calls, want)
		}
	}
}

func TestWriteErrorKeepsMemoryAndFiresCallback(t *testing.T) {
	w := &countingWriter{fail: errors.New("disk full")}
	var fired atomic.Int32
	ps := newPolicyState(config{writer: w, onWriteErr: func(error) { fired.Add(1) }})
	ctx := context.Background()
	ps.addBlock(ctx, "a", "b")
	ps.addBan(ctx, "c", "")
	if ps.allows("a", "b") || ps.allows("c", "x") {
		t.Fatal("memory should hold the change despite the write error")
	}
	if fired.Load() != 2 {
		t.Fatalf("callback fired %d times, want 2", fired.Load())
	}
}

// The real store round-trips through the hub's writer interface.
func TestStoreSatisfiesWriterAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/p.db"
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	ps := newPolicyState(config{writer: s})
	ps.addBlock(ctx, "a", "b")
	ps.addCooldown(ctx, "c", "d", time.Hour)
	ps.addBan(ctx, "e", "n")
	_ = s.Close()

	s2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	p, err := s2.LoadPolicy(ctx, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	reloaded := newPolicyState(config{policy: p})
	if reloaded.allows("a", "b") || reloaded.allows("c", "d") || reloaded.allows("e", "z") {
		t.Fatalf("reloaded policy lost a row: %+v", p)
	}
	if !reloaded.allows("a", "c") {
		t.Fatal("reloaded policy over-blocks")
	}
}

func TestPolicyGuardsBadKeysAndNeverShortensACooldown(t *testing.T) {
	clk := &fakeClock{t: time.UnixMilli(10_000)}
	w := &countingWriter{}
	ps := newPolicyState(config{writer: w, now: clk.now})
	ctx := context.Background()

	ps.addBlock(ctx, "a", "a")
	ps.addBlock(ctx, "", "b")
	ps.addCooldown(ctx, "a", "a", time.Hour)
	ps.addCooldown(ctx, "a", "", time.Hour)
	ps.addCooldown(ctx, "a", "b", 0)
	ps.addCooldown(ctx, "a", "b", -time.Second)
	ps.addBan(ctx, "", "")
	if w.n() != 0 || len(ps.blocks) != 0 || len(ps.cooldowns) != 0 || len(ps.bans) != 0 {
		t.Fatalf("bad keys must change nothing: writes %v, blocks %v, cooldowns %v, bans %v", w.calls, ps.blocks, ps.cooldowns, ps.bans)
	}

	ps.addCooldown(ctx, "a", "b", time.Hour)
	ps.addCooldown(ctx, "b", "a", time.Minute) // shorter: ignored
	if got := ps.cooldowns[pairKey("a", "b")]; got != clk.now().Add(time.Hour).UnixMilli() {
		t.Fatalf("cooldown shortened to %d", got)
	}
	if w.n() != 1 {
		t.Fatalf("a shorter cooldown must not write: %v", w.calls)
	}
	ps.addCooldown(ctx, "a", "b", 2*time.Hour) // longer: extends
	if w.n() != 2 {
		t.Fatalf("a longer cooldown should write: %v", w.calls)
	}
}

// A write made as the hub's context is cancelled (shutdown) still lands.
func TestWriteThroughOutlivesACancelledHubContext(t *testing.T) {
	w := &ctxWriter{}
	ps := newPolicyState(config{writer: w})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ps.addBan(ctx, "k", "")
	if w.cancelled {
		t.Fatal("write-through ran on a cancelled context")
	}
	if !w.hasDeadline {
		t.Fatal("write-through must be bounded")
	}
}

type ctxWriter struct{ cancelled, hasDeadline bool }

func (w *ctxWriter) see(ctx context.Context) error {
	w.cancelled = ctx.Err() != nil
	_, w.hasDeadline = ctx.Deadline()
	return nil
}
func (w *ctxWriter) PutBlock(ctx context.Context, _, _ string, _ int64) error { return w.see(ctx) }
func (w *ctxWriter) PutCooldown(ctx context.Context, _, _ string, _ int64) error {
	return w.see(ctx)
}
func (w *ctxWriter) PutBan(ctx context.Context, _ string, _ int64, _ string) error {
	return w.see(ctx)
}
