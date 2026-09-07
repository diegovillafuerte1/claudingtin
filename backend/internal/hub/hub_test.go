package hub

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// newSession builds a Session with just the fields the hub touches. Conn stays
// nil — the hub goroutine never dereferences it. Outbound is buffered like the
// real one so the hub's non-blocking delivery lands instead of being dropped.
func newSession(key string) *Session {
	return &Session{Key: key, Evict: make(chan struct{}), Outbound: make(chan any, 4)}
}

// recvOutbound returns the next frame on s.Outbound, or fails if none arrives.
func recvOutbound(t *testing.T, s *Session) any {
	t.Helper()
	select {
	case msg := <-s.Outbound:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("no outbound frame for session %q", s.Key)
		return nil
	}
}

// expectNoOutbound fails if any frame lands on s.Outbound within a short window.
func expectNoOutbound(t *testing.T, s *Session) {
	t.Helper()
	select {
	case msg := <-s.Outbound:
		t.Fatalf("unexpected outbound frame for session %q: %#v", s.Key, msg)
	case <-time.After(100 * time.Millisecond):
	}
}

// startHub runs a Hub on a goroutine and returns it plus a stop func.
func startHub(t *testing.T) (*Hub, func()) {
	t.Helper()
	h := New()
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

func waitCount(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if got := h.Count(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("Count never reached %d (last %d)", want, h.Count())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestRegisterThenCount(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	h.Register(newSession("a"))
	h.Register(newSession("b"))
	h.Register(newSession("c"))

	if got := h.Count(); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
}

func TestRegisterSameKeyDoesNotDoubleCount(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	h.Register(newSession("k"))
	h.Register(newSession("k"))

	if got := h.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
}

func TestTakeoverClosesPriorEvictAndHoldsCount(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	first := newSession("k")
	h.Register(first)
	waitCount(t, h, 1)

	second := newSession("k")
	h.Register(second)

	// The displaced session must be signalled.
	select {
	case <-first.Evict:
	case <-time.After(time.Second):
		t.Fatal("prior session's Evict was not closed on takeover")
	}

	// The replacement must not have been signalled.
	if isClosed(second.Evict) {
		t.Fatal("replacement session's Evict was closed")
	}

	// One distinct key, still.
	if got := h.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1 after takeover", got)
	}
}

func TestStaleUnregisterAfterTakeoverIsNoOp(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	first := newSession("k")
	h.Register(first)
	second := newSession("k")
	h.Register(second)
	waitCount(t, h, 1)

	// The old handler goroutine unwinds and unregisters itself; the key is now
	// owned by second, so this must not remove it.
	h.Unregister(first)

	if got := h.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1 (stale Unregister removed the replacement)", got)
	}

	// second unregistering itself does drop the key.
	h.Unregister(second)
	if got := h.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0 after the live session unregisters", got)
	}
}

func TestUnregisterUnknownSessionIsNoOp(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	h.Register(newSession("a"))
	h.Unregister(newSession("never-registered"))

	if got := h.Count(); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}
}

func TestCountOnStoppedHubReturnsZero(t *testing.T) {
	h, stop := startHub(t)
	h.Register(newSession("a"))
	stop()

	if got := h.Count(); got != 0 {
		t.Fatalf("Count on stopped hub = %d, want 0", got)
	}
	// Register / Unregister on a stopped hub must not block.
	h.Register(newSession("b"))
	h.Unregister(newSession("b"))
}

// --- FIFO queue and pairing -------------------------------------------------

func TestLoneReadyGetsQueued(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a := newSession("a")
	h.Register(a)
	h.Ready(a)

	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("lone Ready: expected a queued frame")
	}
	expectNoOutbound(t, a)
}

func TestTwoReadyMatchWithEqualSessionID(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)

	ma := matchedFrame(t, a)
	mb := matchedFrame(t, b)

	if ma.SessionID == "" {
		t.Fatal("matched session_id is empty")
	}
	if ma.SessionID != mb.SessionID {
		t.Fatalf("session_id differs between peers: %q vs %q", ma.SessionID, mb.SessionID)
	}
	if ma.Opener == "" {
		t.Fatal("matched opener is empty")
	}
	if ma.Opener != mb.Opener {
		t.Fatalf("opener differs between peers: %q vs %q", ma.Opener, mb.Opener)
	}
	if !slices.Contains(backend.Openers(), ma.Opener) {
		t.Fatalf("opener %q is not in the curated set", ma.Opener)
	}
	if ma.Pseudonym != "" || ma.Blurb != "" {
		t.Fatalf("pseudonym/blurb must be empty in Epic 2, got %#v", ma)
	}
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
}

// matchedFrame drains frames from s until it sees a Matched (a Queued may
// legitimately precede it) and returns it.
func matchedFrame(t *testing.T, s *Session) proto.Matched {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-s.Outbound:
			switch m := msg.(type) {
			case proto.Matched:
				return m
			case proto.Queued:
				continue
			default:
				t.Fatalf("session %q: unexpected frame %#v", s.Key, msg)
			}
		case <-deadline:
			t.Fatalf("session %q: no matched frame", s.Key)
		}
	}
}

func TestFIFOOrderWithThreeWaiters(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b, c := newSession("a"), newSession("b"), newSession("c")
	for _, s := range []*Session{a, b, c} {
		h.Register(s)
		h.Ready(s)
		// Serialise the enqueues so queue order is deterministic: each Ready is a
		// blocking send consumed by the hub before the next, but the drain that
		// follows is async, so give it a beat.
		time.Sleep(10 * time.Millisecond)
	}

	// A (longest waiter) pairs with B (first eligible successor).
	ma := matchedFrame(t, a)
	mb := matchedFrame(t, b)
	if ma.SessionID != mb.SessionID || ma.SessionID == "" {
		t.Fatalf("A/B session_id mismatch: %q vs %q", ma.SessionID, mb.SessionID)
	}

	// C is left waiting with only a queued frame.
	if _, ok := recvOutbound(t, c).(proto.Queued); !ok {
		t.Fatal("C: expected a queued frame")
	}
	expectNoOutbound(t, c)
}

// TestOpenerRotationNeverRepeatsBackToBack forms len(Openers())+2 pairings in
// sequence and checks the opener each pair receives: every one is a verbatim
// entry of the curated set, no two consecutive pairs share an opener, and the
// sequence walks the set in file order and wraps.
func TestOpenerRotationNeverRepeatsBackToBack(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	set := backend.Openers()
	if len(set) < 2 {
		t.Fatalf("curated set has %d entries, want >= 2", len(set))
	}
	n := len(set) + 2

	got := make([]string, 0, n)
	for i := 0; i < n; i++ {
		a := newSession(fmt.Sprintf("rot-a-%d", i))
		b := newSession(fmt.Sprintf("rot-b-%d", i))
		h.Register(a)
		h.Register(b)
		h.Ready(a)
		h.Ready(b)

		ma := matchedFrame(t, a)
		mb := matchedFrame(t, b)
		if ma.Opener != mb.Opener {
			t.Fatalf("pair %d: peers got different openers %q vs %q", i, ma.Opener, mb.Opener)
		}
		got = append(got, ma.Opener)
	}

	for i, o := range got {
		if !slices.Contains(set, o) {
			t.Errorf("pair %d opener %q is not in the curated set", i, o)
		}
		if i > 0 && o == got[i-1] {
			t.Errorf("pair %d opener %q repeats the previous match's opener", i, o)
		}
		if want := set[i%len(set)]; o != want {
			t.Errorf("pair %d opener = %q, want %q (round-robin, file order, wrapping)", i, o, want)
		}
	}
}

// TestMultiplePairsInOneDrainGetConsecutiveOpeners covers the matrix row
// "Multiple pairs in one drain": a single drainQueue invocation forms two pairs,
// so its `for len(p.queue) >= 2` loop calls nextOpener() twice — the two pairs
// must get consecutive, distinct entries from the rotation (set[0] then set[1]).
//
// Reaching a >=4-deep queue of pairable sessions before a drain needs the
// bounded scan (maxScan) to hide the pairable waiters from a blocked head:
// the head plus maxScan same-key fillers sit ahead of two other-key waiters, so
// nothing pairs while the queue is built (cursor stays at 0). Removing the
// blocked head re-drains the now-66-deep queue and forms both pairs at once.
//
// These sessions are never Register()ed: readyCmd/busyCmd act on the session
// pointer alone, and Register()ing many sessions under one key would trigger
// takeover eviction that dismantles the blocked queue this test builds.
func TestMultiplePairsInOneDrainGetConsecutiveOpeners(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	set := backend.Openers()
	if len(set) < 2 {
		t.Fatalf("curated set has %d entries, want >= 2", len(set))
	}

	const blockedKey, waiterKey = "blocked", "waiter"

	// Blocked head: its bounded scan will only ever reach the fillers.
	head := newSession(blockedKey)
	h.Ready(head)
	if _, ok := recvOutbound(t, head).(proto.Queued); !ok {
		t.Fatal("head: expected queued")
	}

	// Exactly maxScan fillers, same key as the head. With the head at index 0 the
	// scan (i = 1..maxScan) covers indices 1..maxScan, i.e. every filler and
	// nothing past them.
	fillers := make([]*Session, maxScan)
	for i := range fillers {
		f := newSession(blockedKey)
		fillers[i] = f
		h.Ready(f)
		if _, ok := recvOutbound(t, f).(proto.Queued); !ok {
			t.Fatalf("filler %d: expected queued (head must stay blocked)", i)
		}
	}

	// Two pairable waiters, past the scan window, so the head stays blocked and
	// no pair forms while they enqueue.
	b, c := newSession(waiterKey), newSession(waiterKey)
	for _, s := range []*Session{b, c} {
		h.Ready(s)
		if _, ok := recvOutbound(t, s).(proto.Queued); !ok {
			t.Fatal("waiter: expected queued (head must still be blocking the drain)")
		}
	}

	// Drop the blocked head. Its removeFromQueue + re-drain now walks the queue
	// and forms (filler0, b) then (filler1, c) in one drainQueue call.
	h.Busy(head)

	ob := matchedFrame(t, b)
	oc := matchedFrame(t, c)
	of0 := matchedFrame(t, fillers[0])
	of1 := matchedFrame(t, fillers[1])

	if ob.Opener != of0.Opener {
		t.Fatalf("first pair peers disagree on opener: %q vs %q", ob.Opener, of0.Opener)
	}
	if oc.Opener != of1.Opener {
		t.Fatalf("second pair peers disagree on opener: %q vs %q", oc.Opener, of1.Opener)
	}
	if ob.Opener != set[0] {
		t.Fatalf("first pair opener = %q, want %q (set[0])", ob.Opener, set[0])
	}
	if oc.Opener != set[1] {
		t.Fatalf("second pair opener = %q, want %q (set[1], the next rotation entry)", oc.Opener, set[1])
	}
	if ob.Opener == oc.Opener {
		t.Fatalf("two pairs from one drain share opener %q", ob.Opener)
	}
}

// TestTeardownDoesNotRewindOpenerCursor covers the matrix row "Teardown then
// re-pair": after a pairing is torn down and both peers re-ready, they get the
// NEXT opener in rotation — the cursor is not rewound by the teardown.
func TestTeardownDoesNotRewindOpenerCursor(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	set := backend.Openers()
	if len(set) < 2 {
		t.Fatalf("curated set has %d entries, want >= 2", len(set))
	}

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)

	ma := matchedFrame(t, a)
	mb := matchedFrame(t, b)
	if ma.Opener != set[0] || mb.Opener != set[0] {
		t.Fatalf("first pairing opener = %q / %q, want %q (set[0])", ma.Opener, mb.Opener, set[0])
	}

	// Tear the pairing down: A goes busy, B gets a bare session_ended.
	h.Busy(a)
	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A went busy")
	}

	// Both re-ready and re-pair.
	h.Ready(a)
	h.Ready(b)

	ma2 := matchedFrame(t, a)
	mb2 := matchedFrame(t, b)
	if ma2.Opener != mb2.Opener {
		t.Fatalf("re-pair peers disagree on opener: %q vs %q", ma2.Opener, mb2.Opener)
	}
	if ma2.Opener != set[1] {
		t.Fatalf("re-pair opener = %q, want %q (set[1]) — teardown must not rewind the cursor", ma2.Opener, set[1])
	}
}

func TestBusyWhileQueuedIsSilent(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}

	h.Busy(a) // leaves the queue silently
	expectNoOutbound(t, a)

	// The queue is empty again: B readying is a lone waiter, not a match.
	h.Ready(b)
	if _, ok := recvOutbound(t, b).(proto.Queued); !ok {
		t.Fatal("B: expected queued (A must have left the queue)")
	}
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
}

func TestStaleUnregisterRemovesFromQueueSilently(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}

	h.Unregister(a) // disconnect while queued
	expectNoOutbound(t, a)

	h.Ready(b)
	if _, ok := recvOutbound(t, b).(proto.Queued); !ok {
		t.Fatal("B: expected queued (A must have been dropped from the queue)")
	}
}

func TestTakeoverWhileQueuedRemovesQueueEntryAndDoesNotAutoQueueReplacement(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	// A is queued under key "k".
	a := newSession("k")
	h.Register(a)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}

	// A newer connection for "k" takes over. A's handler would see Evict close
	// and unwind into Unregister; replay that here.
	b := newSession("k")
	h.Register(b)
	select {
	case <-a.Evict:
	case <-time.After(2 * time.Second):
		t.Fatal("A: Evict should be closed by the takeover")
	}
	h.Unregister(a)

	// B is a fresh connection: it was never made ready, so it must not be
	// sitting in the queue, and it gets no frame.
	expectNoOutbound(t, b)

	// Prove the queue is empty: a fresh waiter C only gets queued. If B (or a
	// stale A) were still queued, C would match instead.
	c := newSession("c")
	h.Register(c)
	h.Ready(c)
	if _, ok := recvOutbound(t, c).(proto.Queued); !ok {
		t.Fatal("C: expected queued (takeover must have emptied the queue)")
	}
	expectNoOutbound(t, b)
}

func TestBusyWhilePairedTearsDownAndNotifiesPeer(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Busy(a)

	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A went busy")
	}
	expectNoOutbound(t, a) // the leaver hears nothing
	expectNoOutbound(t, b) // and is not re-enqueued
}

func TestUnregisterWhilePairedNotifiesSurvivor(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Unregister(a) // A's socket dropped

	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A disconnected")
	}
	expectNoOutbound(t, b) // not re-enqueued

	// B is genuinely unpaired now: a fresh waiter C only gets queued.
	c := newSession("c")
	h.Register(c)
	h.Ready(c)
	if _, ok := recvOutbound(t, c).(proto.Queued); !ok {
		t.Fatal("C: expected queued (B must not still be matchable via a stale pairing)")
	}
	_ = b
}

func TestReadyTwiceQueuesOnceNoExtraFrame(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a := newSession("a")
	h.Register(a)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}
	h.Ready(a) // already queued: no-op, no second frame
	expectNoOutbound(t, a)
}

func TestConcurrentReadyBusyRaceClean(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "race-" + string(rune('a'+i%8))
			for j := 0; j < 200; j++ {
				s := newSession(key)
				h.Register(s)
				h.Ready(s)
				_ = h.Count()
				if j%2 == 0 {
					h.Busy(s)
				}
				h.Unregister(s)
				// Drain whatever the hub sent so the buffered channel never
				// wedges a delivery (the hub's send is non-blocking anyway).
				for {
					select {
					case <-s.Outbound:
						continue
					default:
					}
					break
				}
			}
		}(i)
	}
	wg.Wait()

	// Post-condition: a clean pair still matches with one shared session_id.
	a, b := newSession("z1"), newSession("z2")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	if ma, mb := matchedFrame(t, a), matchedFrame(t, b); ma.SessionID == "" || ma.SessionID != mb.SessionID {
		t.Fatalf("post-stress match broken: %q vs %q", ma.SessionID, mb.SessionID)
	}
}

func TestConcurrentRegisterAndCountRaceClean(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + i%8))
			for j := 0; j < 200; j++ {
				s := newSession(key)
				h.Register(s)
				_ = h.Count()
				h.Unregister(s)
			}
		}(i)
	}
	wg.Wait()

	// Deterministic post-condition: after the stress phase, a known set of
	// distinct keys registered and then all unregistered must leave Count at 0.
	const k = 12
	sessions := make([]*Session, k)
	for i := range sessions {
		sessions[i] = newSession("post-" + string(rune('A'+i)))
		h.Register(sessions[i])
	}
	if got := h.Count(); got != k {
		t.Fatalf("Count = %d, want %d after registering the known set", got, k)
	}
	for _, s := range sessions {
		h.Unregister(s)
	}
	if got := h.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0 after unregistering the known set", got)
	}
}
