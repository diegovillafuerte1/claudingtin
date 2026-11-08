package hub

import (
	"fmt"
	"testing"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/store"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

func (w *countingWriter) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

// pairUp registers and readies a then b and returns their session_id.
func pairUp(t *testing.T, h *Hub, a, b *Session) string {
	t.Helper()
	readyInOrder(h, a, b)
	ma, mb := matchedFrame(t, a), matchedFrame(t, b)
	if ma.SessionID == "" || ma.SessionID != mb.SessionID {
		t.Fatalf("%s/%s should pair", a.Key, b.Key)
	}
	return ma.SessionID
}

// neverMatchedWithin drains s's frames for d and fails on any matched.
func neverMatchedWithin(t *testing.T, s *Session, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-s.Outbound:
			if _, ok := m.(proto.Matched); ok {
				t.Fatalf("%s was matched", s.Key)
			}
		case <-deadline:
			return
		}
	}
}

// expectWrites checks the write-throughs after h has handled every command sent
// so far (Count is a round trip through the hub goroutine).
func expectWrites(t *testing.T, h *Hub, w *countingWriter, want ...string) {
	t.Helper()
	h.Count()
	got := w.snapshot()
	if len(got) != len(want) {
		t.Fatalf("writer = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("writer = %v, want %v", got, want)
		}
	}
}

func blockHub(t *testing.T) (*Hub, *countingWriter, func()) {
	t.Helper()
	w := &countingWriter{}
	h, stop := startHubWith(t, WithPolicy(store.Policy{}, w))
	return h, w, stop
}

func TestBlockEndsTheLivePairLikeALeaveAndReenqueuesTheBlocker(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Block(a, sid)

	// The peer's frame is the ordinary session_ended (TestLeaveMatchesBusyFrameForPeer
	// pins leave's to busy's); the blocker gets nothing at all.
	if got := recvOutbound(t, b); got != any(proto.SessionEnded{}) {
		t.Fatalf("peer frame = %#v, want proto.SessionEnded{}", got)
	}
	expectNoOutbound(t, a)
	expectWrites(t, h, w, "block a b")

	// Both are queued again, never with each other: each pairs with a newcomer.
	c, d := newSession("c"), newSession("d")
	readyInOrder(h, c)
	mc := matchedFrame(t, c)
	readyInOrder(h, d)
	md := matchedFrame(t, d)
	ma, mb := matchedFrame(t, a), matchedFrame(t, b)
	if ma.SessionID == mb.SessionID {
		t.Fatal("a and b rematched after a block")
	}
	pairs := map[string]bool{mc.SessionID: true, md.SessionID: true}
	if !pairs[ma.SessionID] || !pairs[mb.SessionID] {
		t.Fatal("a and b should each pair with a newcomer")
	}
}

func TestBlockedPairNeverRematchesOverManyCycles(t *testing.T) {
	h, _, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Block(b, sid) // b blocks a: the reverse direction holds too
	recvOutbound(t, a)

	for i := 0; i < 5; i++ {
		h.Busy(a)
		h.Busy(b)
		first, second := a, b
		if i%2 == 1 {
			first, second = b, a
		}
		readyInOrder(h, first, second)
		neverMatchedWithin(t, a, 100*time.Millisecond)
		neverMatchedWithin(t, b, 50*time.Millisecond)
	}
}

// The peer's own end crossed the block on the wire: the block still lands on
// the chat it names, and nobody gets a frame for it.
func TestBlockAfterThePeerEndedRecordsOnly(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Busy(b)
	if _, ok := recvOutbound(t, a).(proto.SessionEnded); !ok {
		t.Fatal("a should see b's end")
	}
	h.Block(a, sid)
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
	expectWrites(t, h, w, "block a b")
	// a is still queued (Story 3.5); b coming back never pairs with it.
	h.Ready(b)
	neverMatchedWithin(t, a, 150*time.Millisecond)
}

// The blocker was already re-matched with c when its block for the chat with
// b lands: b is blocked, and the chat with c is untouched.
func TestLateBlockNeverTouchesTheNewPartner(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sidAB := pairUp(t, h, a, b)
	h.Busy(b) // b's end; a silently re-enqueued
	recvOutbound(t, a)
	c := newSession("c")
	readyInOrder(h, c)
	sidAC := matchedFrame(t, a).SessionID
	if matchedFrame(t, c).SessionID != sidAC {
		t.Fatal("a and c should pair")
	}

	h.Block(a, sidAB)
	expectWrites(t, h, w, "block a b")
	expectNoOutbound(t, c)
	expectNoOutbound(t, a)
	// The a–c chat still relays.
	h.Relay(c, proto.ChatMsg{ClientMsgID: "x", Text: "still here"})
	if m, ok := recvOutbound(t, a).(proto.ChatMsg); !ok || m.Text != "still here" {
		t.Fatalf("a–c relay broken: %#v", m)
	}
}

func TestBlockWithAnUnknownStaleOrEmptySessionIDIsANoOp(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	pairUp(t, h, a, b)
	for _, sid := range []string{"", "s-unknown"} {
		h.Block(a, sid)
	}
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
	expectWrites(t, h, w)
	h.Relay(a, proto.ChatMsg{ClientMsgID: "x", Text: "hi"})
	if _, ok := recvOutbound(t, b).(proto.ChatMsg); !ok {
		t.Fatal("the pairing should be intact")
	}

	// A chat older than the last few is beyond reach too.
	solo := newSession("solo")
	readyInOrder(h, solo)
	expectQueuedOnly(t, solo)
	h.Block(solo, "s-never")
	expectWrites(t, h, w)
}

func TestRecentPairsKeepsTheCurrentAndThePreviousChat(t *testing.T) {
	r := newRecentPairs()
	r.remember("a", "s1", "b")
	r.remember("a", "s2", "c")
	if r.peer("a", "s1") != "b" || r.peer("a", "s2") != "c" {
		t.Fatal("the current and the previous pairing should both be reachable")
	}
	r.remember("a", "s3", "d")
	if r.peer("a", "s1") != "" {
		t.Fatal("the pairing before the previous one should have aged out")
	}
	if r.peer("a", "s2") != "c" || r.peer("a", "s3") != "d" {
		t.Fatal("the last two pairings should be reachable")
	}
	if r.peer("x", "s3") != "" {
		t.Fatal("another key's session_id names nothing")
	}
}

func TestRecentPairsForgetsTheLongestUnusedKeyPastTheCap(t *testing.T) {
	r := newRecentPairs()
	for i := 0; i <= maxRecentKeys; i++ {
		r.remember(fmt.Sprintf("k%d", i), "s", "p")
	}
	if len(r.byKey) != maxRecentKeys || r.peer("k0", "s") != "" {
		t.Fatalf("keys = %d; the oldest key should be forgotten", len(r.byKey))
	}
	if r.peer(fmt.Sprintf("k%d", maxRecentKeys), "s") != "p" {
		t.Fatal("the newest key should be kept")
	}
}

// End to end: after two newer chats, the first is beyond reach.
func TestABlockForAnAgedOutChatIsANoOp(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()
	a, b := newSession("a"), newSession("b")
	sidAB := pairUp(t, h, a, b)
	h.Leave(a)
	recvOutbound(t, b)
	h.Busy(b)
	for _, k := range []string{"c", "d"} {
		x := newSession(k)
		h.Ready(a)
		readyInOrder(h, x)
		matchedFrame(t, a)
		matchedFrame(t, x)
		h.Leave(a)
		recvOutbound(t, x)
		h.Busy(x)
	}
	h.Block(a, sidAB)
	expectWrites(t, h, w)
}

// Another account's session_id names nothing of the sender's.
func TestBlockWithAnotherAccountsSessionIDIsANoOp(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()
	a, b := newSession("a"), newSession("b")
	sidAB := pairUp(t, h, a, b)
	c := newSession("c")
	readyInOrder(h, c)
	expectQueuedOnly(t, c)
	h.Block(c, sidAB)
	expectWrites(t, h, w)
	expectNoOutbound(t, a)
	expectNoOutbound(t, b)
}

func TestARepeatedBlockWritesOnce(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()
	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Block(a, sid)
	h.Block(a, sid)
	recvOutbound(t, b)
	expectWrites(t, h, w, "block a b")
}

// A block naming an older chat with the person a is chatting with again now
// also ends the live chat: the blocked person never stays in it.
func TestBlockingAnOlderChatEndsALiveOneWithTheSamePerson(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()
	a, b := newSession("a"), newSession("b")
	sid1 := pairUp(t, h, a, b)
	h.Leave(a)
	recvOutbound(t, b)
	h.Busy(b)
	h.Ready(a)
	h.Ready(b)
	if matchedFrame(t, a).SessionID != matchedFrame(t, b).SessionID {
		t.Fatal("a and b should pair again")
	}
	h.Block(a, sid1)
	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("the live chat with the blocked person should end")
	}
	expectWrites(t, h, w, "block a b")
}

// A takeover (a fresh hello, no resume) keeps the account's recent pairings:
// a block that crosses the peer's end still lands.
func TestTakeoverKeepsRecentPairings(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()
	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Busy(b)
	recvOutbound(t, a)
	a2 := newSession("a") // a fresh hello for the same key
	h.Register(a2)
	h.Block(a2, sid)
	expectWrites(t, h, w, "block a b")
}

// Claude back on a held chat: the mod sends busy, then block. The busy ends the
// chat (a no longer wants a match); the block records it; a is never re-queued.
func TestHeldChatBusyThenBlockNeverRequeuesTheBlocker(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Busy(a)
	recvOutbound(t, b) // b's session_ended; b silently re-enqueued
	h.Block(a, sid)
	expectWrites(t, h, w, "block a b")

	c, d := newSession("c"), newSession("d")
	readyInOrder(h, c)
	if matchedFrame(t, b).SessionID != matchedFrame(t, c).SessionID {
		t.Fatal("b and c should pair")
	}
	// Were a wrongly queued, d would pair with it.
	readyInOrder(h, d)
	expectQueuedOnly(t, d)
	neverMatchedWithin(t, a, 100*time.Millisecond)
}

// A grace-held blocker (disconnected mid-chat) ends the chat but is not
// re-enqueued while its grace window runs.
func TestGraceHeldBlockerIsNotReenqueued(t *testing.T) {
	shrinkGraceWindow(t)
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Unregister(a) // grace window starts; the pairing is held
	h.Block(a, sid)
	if _, ok := recvOutbound(t, b).(proto.SessionEnded); !ok {
		t.Fatal("b should see the end")
	}
	expectWrites(t, h, w, "block a b")

	c, d := newSession("c"), newSession("d")
	readyInOrder(h, c)
	if matchedFrame(t, b).SessionID != matchedFrame(t, c).SessionID {
		t.Fatal("b and c should pair")
	}
	readyInOrder(h, d)
	expectQueuedOnly(t, d)
}

// A resumed session keeps its recent pairings: a block that crosses the
// peer's end after a resume still lands.
func TestResumeCarriesRecentPairings(t *testing.T) {
	h, w, stop := blockHub(t)
	defer stop()

	a, b := newSession("a"), newSession("b")
	sid := pairUp(t, h, a, b)
	h.Unregister(a)
	a2 := newSession("a")
	a2.ResumeSessionID = sid
	h.Register(a2)
	time.Sleep(20 * time.Millisecond)
	h.Busy(b)
	if _, ok := recvOutbound(t, a2).(proto.SessionEnded); !ok {
		t.Fatal("the resumed a should see b's end")
	}
	h.Block(a2, sid)
	expectWrites(t, h, w, "block a b")
}
