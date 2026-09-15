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

// shrinkGraceWindow makes the reconnect grace window tiny so the timing-
// sensitive cases run fast (mirrors wsclient_test.go's shrinkBackoff /
// shrinkSessionEndedGrace). Tests here never run in parallel, so mutating the
// package var is safe. It stays large enough (well over expectNoOutbound's
// 100ms window) that a resume issued immediately after Unregister — with no
// deliberate delay in between — reliably lands before the timer fires.
func shrinkGraceWindow(t *testing.T) {
	t.Helper()
	orig := ReconnectGraceWindow
	ReconnectGraceWindow = 300 * time.Millisecond
	t.Cleanup(func() { ReconnectGraceWindow = orig })
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

func TestLeaveWhilePairedTearsDownAndNotifiesPeer(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Leave(a)

	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A left")
	}
	expectNoOutbound(t, a) // the leaver hears nothing
	expectNoOutbound(t, b) // and is not re-enqueued

	// Both p.pairings directions are gone: a chat line from B now finds no
	// pairing and is dropped, and B is freely re-matchable with a fresh peer.
	h.Relay(b, proto.ChatMsg{ClientMsgID: "x", Text: "still there?"})
	expectNoOutbound(t, b)

	c := newSession("c")
	h.Register(c)
	h.Ready(c)
	h.Ready(b)
	if mb, mc := matchedFrame(t, b), matchedFrame(t, c); mb.SessionID == "" || mb.SessionID != mc.SessionID {
		t.Fatalf("B not cleanly unpaired after leave: %q vs %q", mb.SessionID, mc.SessionID)
	}
}

// TestLeaveMatchesBusyFrameForPeer proves the peer-facing frame is the exact
// same value for leave and for busy — both are proto.SessionEnded{} out of the
// one teardownPair emission point.
func TestLeaveMatchesBusyFrameForPeer(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	pairAndEnd := func(end func(h *Hub, s *Session)) any {
		a, b := newSession("a"), newSession("b")
		h.Register(a)
		h.Register(b)
		h.Ready(a)
		h.Ready(b)
		_ = matchedFrame(t, a)
		_ = matchedFrame(t, b)
		end(h, a)
		return recvOutbound(t, b)
	}

	viaBusy := pairAndEnd((*Hub).Busy)
	viaLeave := pairAndEnd((*Hub).Leave)

	if _, ok := viaBusy.(proto.SessionEnded); !ok {
		t.Fatalf("busy: peer frame = %#v, want proto.SessionEnded{}", viaBusy)
	}
	if viaLeave != viaBusy {
		t.Fatalf("leave peer frame %#v differs from busy peer frame %#v", viaLeave, viaBusy)
	}
}

func TestLeaveWhileUnpairedIsSilentNoOp(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	// Idle (never readied): silent no-op, no panic.
	a := newSession("a")
	h.Register(a)
	h.Leave(a)
	expectNoOutbound(t, a)

	// Queued: leaving the queue is silent, and it really leaves.
	b, c := newSession("b"), newSession("c")
	h.Register(b)
	h.Ready(b)
	if _, ok := recvOutbound(t, b).(proto.Queued); !ok {
		t.Fatal("B: expected queued")
	}
	h.Leave(b)
	expectNoOutbound(t, b)

	h.Register(c)
	h.Ready(c)
	if _, ok := recvOutbound(t, c).(proto.Queued); !ok {
		t.Fatal("C: expected queued (B must have left the queue)")
	}
	expectNoOutbound(t, b)
}

// TestTakeoverBranchTearsDownPairingBeforeEvict covers the reordered takeover
// branch: the displaced session's peer still gets exactly one bare session_ended,
// and the pairing is already gone by the time anything downstream of the branch
// (its Evict close, the later stale Unregister) runs.
func TestTakeoverBranchTearsDownPairingBeforeEvict(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	// prev (key "k") is paired with peer.
	prev := newSession("k")
	peer := newSession("a")
	h.Register(prev)
	h.Register(peer)
	h.Ready(prev)
	h.Ready(peer)
	_ = matchedFrame(t, prev)
	_ = matchedFrame(t, peer)

	// A newer connection for "k" takes over.
	next := newSession("k")
	h.Register(next)

	// prev is signalled to end.
	select {
	case <-prev.Evict:
	case <-time.After(2 * time.Second):
		t.Fatal("prev.Evict was not closed on takeover")
	}
	// The peer of the torn-down pairing gets exactly one bare session_ended.
	if _, ok := recvOutbound(t, peer).(proto.SessionEnded); !ok {
		t.Fatal("peer: expected a bare session_ended when prev was taken over")
	}
	expectNoOutbound(t, peer)

	// The pairing is gone: the stale Unregister prev's handler eventually fires
	// is a no-op that notifies nobody, and the peer is freely re-matchable.
	h.Unregister(prev)
	expectNoOutbound(t, peer)

	c := newSession("c")
	h.Register(c)
	h.Ready(c)
	h.Ready(peer)
	if mp, mc := matchedFrame(t, peer), matchedFrame(t, c); mp.SessionID == "" || mp.SessionID != mc.SessionID {
		t.Fatalf("peer not cleanly unpaired after prev's takeover: %q vs %q", mp.SessionID, mc.SessionID)
	}
	expectNoOutbound(t, next) // the replacement was never made ready
}

// TestStaleLeaveAfterTakeoverIsNoOp covers the I/O-matrix row for a late leave
// frame from a connection that was already taken over: leaveCmd never touches the
// sessions map, so it cannot evict the replacement, and its teardownPair /
// removeFromQueue both no-op on the already-displaced pointer, so nobody is
// notified.
func TestStaleLeaveAfterTakeoverIsNoOp(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	prev := newSession("k")
	peer := newSession("a")
	h.Register(prev)
	h.Register(peer)
	h.Ready(prev)
	h.Ready(peer)
	_ = matchedFrame(t, prev)
	_ = matchedFrame(t, peer)

	// A newer connection for "k" takes over; drain the peer's takeover frame.
	next := newSession("k")
	h.Register(next)
	select {
	case <-prev.Evict:
	case <-time.After(2 * time.Second):
		t.Fatal("prev.Evict was not closed on takeover")
	}
	if _, ok := recvOutbound(t, peer).(proto.SessionEnded); !ok {
		t.Fatal("peer: expected a bare session_ended on takeover")
	}

	// The stale leave prev's read loop may still emit before it unwinds.
	h.Leave(prev)

	if got := h.Count(); got != 2 { // "k" (now next) + "a"
		t.Fatalf("Count = %d, want 2 (stale leave evicted the replacement)", got)
	}
	expectNoOutbound(t, next) // replacement untouched
	expectNoOutbound(t, peer) // nobody re-notified

	// next is still the live owner of "k" and can be matched.
	h.Ready(next)
	h.Ready(peer)
	if mn, mp := matchedFrame(t, next), matchedFrame(t, peer); mn.SessionID == "" || mn.SessionID != mp.SessionID {
		t.Fatalf("next not usable after stale leave: %q vs %q", mn.SessionID, mp.SessionID)
	}
}

// TestUnregisterWhilePairedNotifiesSurvivor covers the reconnect-grace-window
// timeout path (Story 3.4): A's disconnect while paired no longer tears the
// pairing down immediately — it is held for ReconnectGraceWindow — so B's
// session_ended only arrives once that window actually expires with no
// resume.
func TestUnregisterWhilePairedNotifiesSurvivor(t *testing.T) {
	shrinkGraceWindow(t)
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
		t.Fatal("B: expected a bare session_ended once the grace window expired")
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

// --- reconnect grace window (Story 3.4) -------------------------------------

// TestQuickResumeWithinGraceWindowSendsNoSessionEnded covers the matrix row
// "Paired, brief drop, quick resume": A drops, and a new connection for A's
// key presenting A's own session_id arrives before the grace window expires.
// B never sees a session_ended, the pairing is repointed onto the new
// connection with the SAME session_id (no mint), and the relay works both
// ways again — all with no deliberate delay between the drop and the resume,
// proving the hold survives at least that long.
func TestQuickResumeWithinGraceWindowSendsNoSessionEnded(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	ma := matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Unregister(a) // A's socket drops; the pairing is held, not torn down

	// A reconnects immediately, naming its own session_id.
	a2 := newSession("a")
	a2.ResumeSessionID = ma.SessionID
	h.Register(a2)

	// Silent resume: nobody gets anything, even after the (now-cancelled)
	// grace window would have expired.
	expectNoOutbound(t, b)
	expectNoOutbound(t, a2)

	// h.Count() round-trips through the hub goroutine, so it happens-after the
	// registerCmd body above finished — including its write to a2.SessionID —
	// making the read below race-free.
	_ = h.Count()
	if a2.SessionID != ma.SessionID {
		t.Fatalf("resumed session_id = %q, want the original %q (no re-mint)", a2.SessionID, ma.SessionID)
	}

	// The relay works both ways on the resumed pairing.
	h.Relay(a2, proto.ChatMsg{ClientMsgID: "c1", Text: "back"})
	if got, ok := recvOutbound(t, b).(proto.ChatMsg); !ok || got.Text != "back" {
		t.Fatalf("B: expected the relayed chat_msg, got %#v", got)
	}
	h.Relay(b, proto.ChatMsg{ClientMsgID: "c2", Text: "hi"})
	if got, ok := recvOutbound(t, a2).(proto.ChatMsg); !ok || got.Text != "hi" {
		t.Fatalf("a2: expected the relayed chat_msg, got %#v", got)
	}
}

// TestReconnectWithWrongSessionIDTearsDownHeldPairing covers the mismatched-id
// half of the matrix row "Paired, drop, reconnect with wrong/absent/expired
// session_id": a still-held pairing (grace not yet expired) whose reconnect
// names a session_id that is not the one being held tears down now, exactly
// like an immediate teardown — B gets its session_ended right away, no wait
// for the window — and the new connection is ALSO told its named session_id
// ended (the resume it asked for did not happen), resetting any stale local
// pane via the same cause-agnostic frame.
func TestReconnectWithWrongSessionIDTearsDownHeldPairing(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Unregister(a) // A's socket drops; grace-held

	a2 := newSession("a")
	a2.ResumeSessionID = "not-the-right-id"
	h.Register(a2)

	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended after A's mismatched reconnect")
	}
	expectNoOutbound(t, b)
	if _, ok := recvOutbound(t, a2).(proto.SessionEnded); !ok {
		t.Fatal("a2: expected a session_ended for its failed resume attempt")
	}
	expectNoOutbound(t, a2)

	// The new connection is otherwise an ordinary fresh session: it can be
	// readied and matched like any other.
	c := newSession("c")
	h.Register(c)
	h.Ready(a2)
	h.Ready(c)
	ma2, mc := matchedFrame(t, a2), matchedFrame(t, c)
	if ma2.SessionID == "" || ma2.SessionID != mc.SessionID {
		t.Fatalf("a2/c did not cleanly match: %q vs %q", ma2.SessionID, mc.SessionID)
	}
}

// TestReconnectAfterGraceExpiryGetsSessionEndedAndCanRematch covers the
// expired half of the same matrix row: once the grace window has actually
// timed out (B already has its session_ended and the key is fully
// unregistered), a late reconnect naming that now-stale session_id gets
// exactly one session_ended on its OWN new connection — resetting a stale
// local pane — and can then be readied and matched normally.
func TestReconnectAfterGraceExpiryGetsSessionEndedAndCanRematch(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	ma := matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Unregister(a) // grace-held

	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended once the grace window expired")
	}
	expectNoOutbound(t, b)

	// A reconnects late, naming the now-expired session_id.
	a2 := newSession("a")
	a2.ResumeSessionID = ma.SessionID
	h.Register(a2)

	if _, ok := recvOutbound(t, a2).(proto.SessionEnded); !ok {
		t.Fatal("a2: expected a session_ended for the stale resume attempt")
	}
	expectNoOutbound(t, a2)

	c := newSession("c")
	h.Register(c)
	h.Ready(a2)
	h.Ready(c)
	ma2, mc := matchedFrame(t, a2), matchedFrame(t, c)
	if ma2.SessionID == "" || ma2.SessionID != mc.SessionID {
		t.Fatalf("a2/c did not cleanly match: %q vs %q", ma2.SessionID, mc.SessionID)
	}
}

// TestResumeAfterPeerLeftDuringGraceGetsSessionEnded covers a gap review
// caught: B's pairing entry can disappear out from under a grace-held A
// before A ever reconnects — here because B explicitly leaves, but Busy hits
// the identical path. teardownPair(B) deletes both p.pairings directions but
// has no reason to touch A's graceTimer (it doesn't know A is grace-held), so
// A's timer keeps running and A.SessionID stays valid. A must not come back
// as a silent "resume" with nothing to show for it: it gets the
// session_ended its actually-ended session earned.
func TestResumeAfterPeerLeftDuringGraceGetsSessionEnded(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	ma := matchedFrame(t, a)
	_ = matchedFrame(t, b)

	h.Unregister(a) // A's socket drops; grace-held, timer running

	h.Leave(b) // B leaves while A is still grace-held — B gets nothing back

	// A reconnects promptly, naming its own still-valid session_id — the
	// timer has not expired and the id genuinely matches, but there is no
	// pairing left to repoint onto.
	a2 := newSession("a")
	a2.ResumeSessionID = ma.SessionID
	h.Register(a2)

	if _, ok := recvOutbound(t, a2).(proto.SessionEnded); !ok {
		t.Fatal("a2: expected a session_ended — the resume target's pairing is already gone")
	}
	expectNoOutbound(t, a2)
	if a2.SessionID != "" {
		t.Fatalf("a2.SessionID = %q, want empty (never repointed onto a dead pairing)", a2.SessionID)
	}

	// a2 is otherwise an ordinary fresh session: it can be readied and
	// matched like any other, proving it was not left in some orphaned
	// registered-but-unpaired-and-unqueued limbo.
	c := newSession("c")
	h.Register(c)
	h.Ready(a2)
	h.Ready(c)
	ma2, mc := matchedFrame(t, a2), matchedFrame(t, c)
	if ma2.SessionID == "" || ma2.SessionID != mc.SessionID {
		t.Fatalf("a2/c did not cleanly match: %q vs %q", ma2.SessionID, mc.SessionID)
	}
}

// TestResumeAttemptAgainstStillLiveSessionIsOrdinaryTakeover covers a
// verification gap the review flagged: every other resume/mismatch test
// disconnects `a` (arming graceTimer) before the resume attempt, so the
// registerCmd condition `prev.graceTimer != nil && ... ResumeSessionID ==
// prev.SessionID` never actually exercises its first conjunct — the ID match
// alone would make every one of those tests pass too. Here `a` is never
// unregistered: it is still fully live and paired when a2 reconnects naming
// a's own (still current) session_id — the exact race between a client
// deciding to reconnect and the backend's own read loop noticing the old
// socket died. This must be treated as an ordinary takeover (a's Evict
// closed, B gets one session_ended) and never as a silent resume — a still-
// live connection's pairing must not be stolen out from under it with no
// eviction and no notification to its peer. a2 also gets its own
// session_ended: the uniform rule is that any Hello naming a session_id that
// did not resolve into an actual resume gets told so on its own connection,
// and "took over a still-live session instead" counts as not resolving.
func TestResumeAttemptAgainstStillLiveSessionIsOrdinaryTakeover(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	ma := matchedFrame(t, a)
	_ = matchedFrame(t, b)

	// a is never Unregistered: graceTimer stays nil throughout.
	a2 := newSession("a")
	a2.ResumeSessionID = ma.SessionID // names a's own still-current session_id
	h.Register(a2)

	select {
	case <-a.Evict:
	case <-time.After(2 * time.Second):
		t.Fatal("a: expected Evict closed — a still-live session must be evicted, not silently repointed")
	}
	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("B: expected a bare session_ended — the takeover path, not a silent resume")
	}
	expectNoOutbound(t, b)
	if _, ok := recvOutbound(t, a2).(proto.SessionEnded); !ok {
		t.Fatal("a2: expected a session_ended — its named resume did not resolve, it caused a takeover instead")
	}
	expectNoOutbound(t, a2)
	if a2.SessionID != "" {
		t.Fatalf("a2.SessionID = %q, want empty (a fresh takeover mints nothing)", a2.SessionID)
	}
}

// TestQueuedDropIsUnaffectedByGraceWindow covers the matrix row "Queued,
// drop": a session that was only queued (never paired) when it disconnects is
// removed silently and immediately, with no grace timer at all — a fresh
// connection reusing its key can be readied and matched right away, proving
// nothing was left waiting on a timer or blocking the queue.
func TestQueuedDropIsUnaffectedByGraceWindow(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a := newSession("a")
	h.Register(a)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}

	h.Unregister(a) // disconnect while queued: immediate silent removal
	expectNoOutbound(t, a)

	a2 := newSession("a")
	h.Register(a2)
	b := newSession("b")
	h.Register(b)
	h.Ready(a2)
	h.Ready(b)
	ma, mb := matchedFrame(t, a2), matchedFrame(t, b)
	if ma.SessionID == "" || ma.SessionID != mb.SessionID {
		t.Fatalf("a2/b did not cleanly match: %q vs %q", ma.SessionID, mb.SessionID)
	}
}

// --- chat relay ----------------------------------------------------------

// TestRelayDeliversToPeerOnlyNeverSender: A and B paired, Relay(A, msg) lands
// the identical ChatMsg on B's Outbound and nothing on A's.
func TestRelayDeliversToPeerOnlyNeverSender(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	msg := proto.ChatMsg{ClientMsgID: "c1", Text: "hey"}
	h.Relay(a, msg)

	got, ok := recvOutbound(t, b).(proto.ChatMsg)
	if !ok {
		t.Fatalf("B: expected a proto.ChatMsg, got %#v", got)
	}
	if got != msg {
		t.Fatalf("B received %#v, want %#v", got, msg)
	}
	expectNoOutbound(t, a) // the sender is never echoed its own line
	expectNoOutbound(t, b)

	// The reverse direction works too, and still never echoes.
	msg2 := proto.ChatMsg{ClientMsgID: "c2", Text: "hi back"}
	h.Relay(b, msg2)
	if got := recvOutbound(t, a).(proto.ChatMsg); got != msg2 {
		t.Fatalf("A received %#v, want %#v", got, msg2)
	}
	expectNoOutbound(t, b)
}

// TestRelayFromUnpairedSessionIsSilentNoOp: a Relay from a session that was
// never matched delivers nothing and does not panic.
func TestRelayFromUnpairedSessionIsSilentNoOp(t *testing.T) {
	h, stop := startHub(t)
	defer stop()

	a := newSession("a")
	h.Register(a)
	h.Ready(a)
	if _, ok := recvOutbound(t, a).(proto.Queued); !ok {
		t.Fatal("A: expected queued")
	}

	h.Relay(a, proto.ChatMsg{ClientMsgID: "c1", Text: "into the void"})
	expectNoOutbound(t, a)

	// An entirely unknown session is fine too.
	h.Relay(newSession("ghost"), proto.ChatMsg{ClientMsgID: "c2", Text: "nobody"})

	// Clear A out of the queue so the next pair forms cleanly.
	h.Busy(a)
	expectNoOutbound(t, a)

	// The hub is still healthy: a clean pair still matches.
	b, c := newSession("b"), newSession("c")
	h.Register(b)
	h.Register(c)
	h.Ready(b)
	h.Ready(c)
	if mb, mc := matchedFrame(t, b), matchedFrame(t, c); mb.SessionID == "" || mb.SessionID != mc.SessionID {
		t.Fatalf("post-noop match broken: %q vs %q", mb.SessionID, mc.SessionID)
	}
}

// TestRelayAfterTeardownDrops: once the pairing has ended, a chat line from the
// survivor finds no pairing entry and is dropped — no delivery, no error.
func TestRelayAfterTeardownDrops(t *testing.T) {
	shrinkGraceWindow(t)
	h, stop := startHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	h.Register(a)
	h.Register(b)
	h.Ready(a)
	h.Ready(b)
	_ = matchedFrame(t, a)
	_ = matchedFrame(t, b)

	// B disconnects; once the grace window expires A gets the bare
	// session_ended and the pairing is gone.
	h.Unregister(b)
	if _, ok := recvOutbound(t, a).(proto.SessionEnded); !ok {
		t.Fatal("A: expected session_ended after B disconnected")
	}

	h.Relay(a, proto.ChatMsg{ClientMsgID: "c1", Text: "still there?"})
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
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
				h.Relay(s, proto.ChatMsg{ClientMsgID: "x", Text: "race"})
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
