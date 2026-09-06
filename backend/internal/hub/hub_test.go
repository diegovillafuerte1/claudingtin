package hub

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newSession builds a Session with just the fields the hub touches. Conn stays
// nil — the hub goroutine never dereferences it.
func newSession(key string) *Session {
	return &Session{Key: key, Evict: make(chan struct{})}
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
