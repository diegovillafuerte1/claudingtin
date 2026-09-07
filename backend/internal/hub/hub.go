// Package hub is the single-writer core: exactly one goroutine owns the
// key -> session map, the strict-FIFO wait queue, the active pairings, the
// session_id mint, and the opener rotation cursor, and every other goroutine
// touches that state only by sending a command on a channel (architecture
// decision AD-8). The hub goroutine never
// performs network I/O and writes no logs — a takeover only closes the displaced
// session's evict channel; a match, teardown, or chat relay only does a
// non-blocking send of a ready-to-write proto frame onto a Session.Outbound
// channel that the connection handler drains. A relay sends the sender's
// proto.ChatMsg to its paired peer only — never back to the sender, and a no-op
// when the sender is not paired. Sending frames and closing sockets happens on
// the connection handler's own goroutine.
package hub

import (
	"context"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/backend"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// Session is one connected account key: its websocket connection, an evict
// channel the hub closes to signal that a newer connection for the same key has
// taken over, and a buffered Outbound channel the hub uses to hand the
// connection handler a proto frame to write (queued / matched / session_ended).
// The hub goroutine only ever closes Evict and does a non-blocking send on
// Outbound; it never reads or writes Conn and never blocks on Outbound.
type Session struct {
	Key      string
	Conn     *websocket.Conn
	Evict    chan struct{}
	Outbound chan any
}

type command interface{ isCommand() }

type registerCmd struct{ s *Session }

type unregisterCmd struct{ s *Session }

type countCmd struct{ reply chan int }

type readyCmd struct{ s *Session }

type busyCmd struct{ s *Session }

type chatMsgCmd struct {
	s   *Session
	msg proto.ChatMsg
}

func (registerCmd) isCommand()   {}
func (unregisterCmd) isCommand() {}
func (countCmd) isCommand()      {}
func (readyCmd) isCommand()      {}
func (busyCmd) isCommand()       {}
func (chatMsgCmd) isCommand()    {}

// maxScan bounds how far past the queue head the pairing loop looks for an
// eligible successor before the head simply waits — the bounded scan AD-8 calls
// for.
const maxScan = 64

// Hub is the single-writer connection registry. Construct it with New, start its
// owning goroutine with Run, and interact with it only through Register,
// Unregister and Count.
type Hub struct {
	commands chan command
	stopped  chan struct{}
}

// New returns a Hub whose goroutine is not yet running; call Run.
func New() *Hub {
	return &Hub{
		commands: make(chan command),
		stopped:  make(chan struct{}),
	}
}

// Run owns the registry map, the FIFO wait queue, the active pairings, the
// session_id mint, and the opener rotation cursor, and consumes the command
// channel until ctx is cancelled. It
// performs no network I/O and writes no logs: on a match, teardown, or chat
// relay it does a non-blocking send of a ready-to-write proto value onto a
// Session.Outbound, and the connection handler — the sole writer for that
// socket — drains it. Run must be called exactly once.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)

	sessions := make(map[string]*Session)
	p := &pairingState{
		pairings: make(map[*Session]*Session),
		openers:  backend.Openers(),
	}

	// The one eligibility seam. Story 2.1: any non-self successor is eligible.
	// Epic 4 ANDs block / cooldown / ban predicates into this closure without
	// touching the scan or the loop.
	eligible := func(a, b *Session) bool { return a.Key != b.Key }

	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.commands:
			switch cmd := c.(type) {
			case registerCmd:
				// A second hello for a live key takes over: the prior session is
				// displaced and told to end. Closing evict is the only thing the
				// hub goroutine does here — the old handler goroutine sends
				// session_ended and closes the socket.
				if prev, ok := sessions[cmd.s.Key]; ok && prev != cmd.s {
					close(prev.Evict)
					// Evict the displaced session from the queue / pairing now,
					// not when its handler eventually fires unregisterCmd — until
					// then it could still be matched with a third session. The
					// later unregisterCmd{prev} cleanup then just no-ops.
					p.removeFromQueue(prev)
					p.teardownPair(prev)
					// Re-drain: a pair that only becomes formable once prev is
					// gone should form now. This is the Epic-4 eligibility seam —
					// with a narrower predicate a departing head can unblock a
					// waiting pair.
					p.drainQueue(eligible)
				}
				sessions[cmd.s.Key] = cmd.s
			case unregisterCmd:
				// Remove only if the map still points at this exact session, so a
				// stale Unregister from a connection that was already taken over
				// cannot evict its replacement.
				if cur, ok := sessions[cmd.s.Key]; ok && cur == cmd.s {
					delete(sessions, cmd.s.Key)
				}
				// A disconnect or takeover also drops the session from the queue
				// (silently) and tears down any pairing (the surviving peer gets
				// a bare session_ended). Both act on the session pointer, so a
				// stale Unregister still cleans up its own queue / pairing entry.
				p.removeFromQueue(cmd.s)
				p.teardownPair(cmd.s)
				// Re-drain in case the departure unblocks a waiting pair. This is
				// the Epic-4 eligibility seam — under a narrower predicate a
				// blocking head leaving can free the sessions behind it.
				p.drainQueue(eligible)
			case readyCmd:
				// Enqueue the longest-waiting-first queue, then try to pair. Only
				// a session that is newly enqueued (not already queued, not
				// already paired) gets a queued frame when it is left waiting.
				if p.enqueue(cmd.s) {
					p.drainQueue(eligible)
					if p.inQueue(cmd.s) {
						deliver(cmd.s, proto.Queued{})
					}
				}
			case busyCmd:
				// Leaving while queued is silent; leaving while paired tears the
				// pairing down and notifies the peer. A session is never both.
				p.removeFromQueue(cmd.s)
				p.teardownPair(cmd.s)
				// Re-drain in case the departure unblocks a waiting pair. This is
				// the Epic-4 eligibility seam — under a narrower predicate a
				// blocking head leaving can free the sessions behind it.
				p.drainQueue(eligible)
			case chatMsgCmd:
				// Route a chat line to the sender's paired peer only. This is a
				// pure read of p.pairings plus the same non-blocking deliver the
				// match/teardown paths use — no network I/O, no logging. If the
				// sender is not paired (never matched, or the pairing already
				// ended) the frame is dropped silently: no delivery, no error.
				p.relay(cmd.s, cmd.msg)
			case countCmd:
				cmd.reply <- len(sessions)
			}
		}
	}
}

// pairingState is the hub goroutine's private FIFO queue, pairing table, and
// opener rotation cursor. It is created inside Run and never escapes that
// goroutine, so every method here runs single-writer.
type pairingState struct {
	queue        []*Session
	pairings     map[*Session]*Session
	openers      []string
	openerCursor int
}

// enqueue appends s to the tail of the wait queue unless it is already queued or
// already in a pairing. It reports whether s was actually added.
func (p *pairingState) enqueue(s *Session) bool {
	if _, paired := p.pairings[s]; paired {
		return false
	}
	if p.inQueue(s) {
		return false
	}
	p.queue = append(p.queue, s)
	return true
}

func (p *pairingState) inQueue(s *Session) bool {
	for _, q := range p.queue {
		if q == s {
			return true
		}
	}
	return false
}

// removeFromQueue drops s from the wait queue if present; a no-op otherwise. No
// frame is sent — leaving the queue unmatched is always silent.
func (p *pairingState) removeFromQueue(s *Session) {
	for i, q := range p.queue {
		if q == s {
			p.queue = append(p.queue[:i], p.queue[i+1:]...)
			return
		}
	}
}

// teardownPair ends s's pairing if it has one: both directions are deleted from
// the table and the surviving peer receives a bare session_ended. Neither peer
// is re-enqueued (Epic 3 owns re-enqueue). A no-op if s is not paired.
func (p *pairingState) teardownPair(s *Session) {
	peer, ok := p.pairings[s]
	if !ok {
		return
	}
	delete(p.pairings, s)
	delete(p.pairings, peer)
	deliver(peer, proto.SessionEnded{})
}

// relay hands msg to from's paired peer and to no one else — the sender is never
// echoed its own line (the companion already showed it optimistically, keyed by
// client_msg_id). It is a no-op if from is not paired: a chat line from a
// session that was never matched, or whose pairing has already been torn down,
// is dropped silently with no error frame. The frame reaches the peer only by
// the same best-effort non-blocking deliver the match/teardown paths use; v1
// does not ack or retry a chat_msg.
func (p *pairingState) relay(from *Session, msg proto.ChatMsg) {
	if peer, ok := p.pairings[from]; ok {
		deliver(peer, msg)
	}
}

// drainQueue pairs the queue head with its first eligible successor for as long
// as two or more sessions are waiting and a pair can be formed. Each pairing
// mints exactly one session_id, delivered byte-identical to both peers. If the
// head has no eligible successor within the bounded scan, the head waits and
// drainQueue stops.
func (p *pairingState) drainQueue(eligible func(a, b *Session) bool) {
	for len(p.queue) >= 2 {
		idx, ok := firstEligibleSuccessor(p.queue, eligible)
		if !ok {
			return
		}
		head := p.queue[0]
		succ := p.queue[idx]

		next := make([]*Session, 0, len(p.queue)-2)
		for i, s := range p.queue {
			if i == 0 || i == idx {
				continue
			}
			next = append(next, s)
		}
		p.queue = next

		m := proto.Matched{SessionID: newSessionID(), Opener: p.nextOpener()}
		deliver(head, m)
		deliver(succ, m)
		p.pairings[head] = succ
		p.pairings[succ] = head
	}
}

// nextOpener returns the opener at the rotation cursor and advances the cursor
// by one, wrapping at the end. With two or more openers loaded this guarantees
// the same opener is never used for two consecutive matches. The empty-set guard
// is defensive: Run always populates p.openers from backend.Openers(), which
// panics rather than returning fewer than two entries.
func (p *pairingState) nextOpener() string {
	if len(p.openers) == 0 {
		return ""
	}
	o := p.openers[p.openerCursor]
	p.openerCursor = (p.openerCursor + 1) % len(p.openers)
	return o
}

// firstEligibleSuccessor scans from just past the queue head, up to maxScan
// entries, for the first successor the predicate accepts. It returns that
// successor's index and true, or 0 and false if none qualifies within the bound.
// The scan shape is frozen: Epic 4 only widens the predicate.
func firstEligibleSuccessor(queue []*Session, eligible func(a, b *Session) bool) (idx int, ok bool) {
	head := queue[0]
	for i := 1; i < len(queue) && i <= maxScan; i++ {
		if eligible(head, queue[i]) {
			return i, true
		}
	}
	return 0, false
}

// deliver hands msg to a session's connection handler without ever blocking the
// hub goroutine. A full or nil Outbound channel means the handler is already
// gone, and dropping the frame is correct.
func deliver(s *Session, msg any) {
	select {
	case s.Outbound <- msg:
	default:
	}
}

// send delivers a command to the hub goroutine, or reports false if Run has
// already returned (so callers never block forever on a stopped hub).
func (h *Hub) send(c command) bool {
	select {
	case h.commands <- c:
		return true
	case <-h.stopped:
		return false
	}
}

// Register adds s to the registry. If a different session is already mapped to
// s.Key, that session's Evict channel is closed (takeover) before s replaces it.
func (h *Hub) Register(s *Session) {
	h.send(registerCmd{s: s})
}

// Unregister removes s from the registry, but only if the key still maps to this
// exact session (a no-op after a takeover replaced it). It always removes s from
// the wait queue and tears down any pairing s was in.
func (h *Hub) Unregister(s *Session) {
	h.send(unregisterCmd{s: s})
}

// Ready puts s into the FIFO wait queue and attempts a pairing. If s ends up
// waiting it receives a queued frame on its Outbound channel; if it is paired
// immediately both peers receive a matched frame. A second Ready for a session
// that is already queued or already paired does nothing.
func (h *Hub) Ready(s *Session) {
	h.send(readyCmd{s: s})
}

// Busy removes s from the wait queue if it is waiting (silently), or tears down
// its pairing and sends the surviving peer a bare session_ended if it is paired.
func (h *Hub) Busy(s *Session) {
	h.send(busyCmd{s: s})
}

// Relay delivers msg to s's paired peer only, never back to s; it is a no-op if
// s is not paired (never matched, or the pairing already ended). Delivery is
// best-effort non-blocking on the peer's Outbound channel — v1 does not ack or
// retry a chat_msg — and the hub goroutine does no network I/O and no logging
// for it.
func (h *Hub) Relay(s *Session, msg proto.ChatMsg) {
	h.send(chatMsgCmd{s: s, msg: msg})
}

// Count returns the number of distinct connected account keys. It returns 0 if
// the hub goroutine has stopped.
func (h *Hub) Count() int {
	reply := make(chan int, 1)
	if !h.send(countCmd{reply: reply}) {
		return 0
	}
	return <-reply
}
