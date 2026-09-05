// Package hub is the connection registry: exactly one goroutine owns the
// key -> session map and every other goroutine touches it only by sending a
// command on a channel (architecture decision AD-8). The registry goroutine
// never performs network I/O — a takeover only closes the displaced session's
// evict channel; sending session_ended and closing the socket happens on the
// connection handler's own goroutine.
package hub

import (
	"context"

	"github.com/coder/websocket"
)

// Session is one connected account key: its websocket connection plus an evict
// channel the hub closes to signal that a newer connection for the same key has
// taken over. The hub goroutine only ever closes Evict; it never reads or writes
// Conn.
type Session struct {
	Key   string
	Conn  *websocket.Conn
	Evict chan struct{}
}

type command interface{ isCommand() }

type registerCmd struct{ s *Session }

type unregisterCmd struct{ s *Session }

type countCmd struct{ reply chan int }

func (registerCmd) isCommand()   {}
func (unregisterCmd) isCommand() {}
func (countCmd) isCommand()      {}

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

// Run owns the registry map and consumes the command channel until ctx is
// cancelled. It performs no network I/O. Run must be called exactly once.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)

	sessions := make(map[string]*Session)
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
				}
				sessions[cmd.s.Key] = cmd.s
			case unregisterCmd:
				// Remove only if the map still points at this exact session, so a
				// stale Unregister from a connection that was already taken over
				// cannot evict its replacement.
				if cur, ok := sessions[cmd.s.Key]; ok && cur == cmd.s {
					delete(sessions, cmd.s.Key)
				}
			case countCmd:
				cmd.reply <- len(sessions)
			}
		}
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
// exact session (a no-op after a takeover replaced it).
func (h *Hub) Unregister(s *Session) {
	h.send(unregisterCmd{s: s})
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
