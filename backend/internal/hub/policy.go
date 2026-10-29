package hub

import (
	"context"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/store"
)

// Writer is the durable mirror policy writes go through to (Story 4.1, AD-8):
// *store.Store satisfies it. Matching never reads it.
type Writer interface {
	PutBlock(ctx context.Context, blocker, blocked string, createdMs int64) error
	PutCooldown(ctx context.Context, a, b string, expiresMs int64) error
	PutBan(ctx context.Context, key string, createdMs int64, note string) error
}

var _ Writer = (*store.Store)(nil)

// writeBudget bounds one write-through. It outlives the hub's own context (a
// change made as the hub stops still lands) but never stalls the hub for the
// store's whole busy timeout.
const writeBudget = 2 * time.Second

// Option configures a Hub at New.
type Option func(*config)

type config struct {
	policy     store.Policy
	writer     Writer
	now        func() time.Time
	onWriteErr func(error)
}

// WithPolicy seeds the hub's in-memory policy (as loaded at startup) and the
// writer every later policy change writes through to.
func WithPolicy(p store.Policy, w Writer) Option {
	return func(c *config) {
		c.policy = p
		c.writer = w
	}
}

// WithClock replaces time.Now for cooldown expiry (tests).
func WithClock(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}

// WithWriteError is called, on the hub goroutine, when a write-through fails.
// The in-memory change stands either way. The error carries no account key.
func WithWriteError(f func(error)) Option {
	return func(c *config) { c.onWriteErr = f }
}

// policyState is the hub goroutine's in-memory policy: the only thing a
// matching decision reads. Changes land here first, then write through.
type policyState struct {
	blocks    map[[2]string]struct{} // [blocker, blocked]
	cooldowns map[[2]string]int64    // canonical pair -> expires_ms
	bans      map[string]struct{}

	writer     Writer
	now        func() time.Time
	onWriteErr func(error)
}

func newPolicyState(c config) *policyState {
	ps := &policyState{
		blocks:     make(map[[2]string]struct{}, len(c.policy.Blocks)),
		cooldowns:  make(map[[2]string]int64, len(c.policy.Cooldowns)),
		bans:       make(map[string]struct{}, len(c.policy.Bans)),
		writer:     c.writer,
		now:        c.now,
		onWriteErr: c.onWriteErr,
	}
	if ps.now == nil {
		ps.now = time.Now
	}
	for _, b := range c.policy.Blocks {
		ps.blocks[b] = struct{}{}
	}
	for _, cd := range c.policy.Cooldowns {
		ps.cooldowns[pairKey(cd.KeyA, cd.KeyB)] = cd.ExpiresMs
	}
	for _, k := range c.policy.Bans {
		ps.bans[k] = struct{}{}
	}
	return ps
}

func pairKey(a, b string) [2]string {
	ka, kb := store.CanonicalPair(a, b)
	return [2]string{ka, kb}
}

// admits reports whether key may join the queue at all: not banned.
func (ps *policyState) admits(key string) bool {
	_, banned := ps.bans[key]
	return !banned
}

// allows reports whether a and b may be paired: not the same key, no block
// either way, no live cooldown, and neither banned. Memory only.
func (ps *policyState) allows(a, b string) bool {
	if a == b || !ps.admits(a) || !ps.admits(b) {
		return false
	}
	if _, ok := ps.blocks[[2]string{a, b}]; ok {
		return false
	}
	if _, ok := ps.blocks[[2]string{b, a}]; ok {
		return false
	}
	k := pairKey(a, b)
	if exp, ok := ps.cooldowns[k]; ok {
		if exp > ps.now().UnixMilli() {
			return false
		}
		delete(ps.cooldowns, k) // expired: drop it from memory
	}
	return true
}

// write runs one write-through on a context detached from the hub's (so a
// shutdown in progress does not cancel it) and bounded by writeBudget.
func (ps *policyState) write(ctx context.Context, put func(context.Context) error) {
	if ps.writer == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeBudget)
	defer cancel()
	ps.wrote(put(wctx))
}

func (ps *policyState) wrote(err error) {
	if err != nil && ps.onWriteErr != nil {
		ps.onWriteErr(err)
	}
}

// An empty key or a self-pair is never a policy row.
func validPair(a, b string) bool { return a != "" && b != "" && a != b }

// addBlock records that blocker blocked blocked (Story 4.2 calls it).
func (ps *policyState) addBlock(ctx context.Context, blocker, blocked string) {
	if !validPair(blocker, blocked) {
		return
	}
	ps.blocks[[2]string{blocker, blocked}] = struct{}{}
	ps.write(ctx, func(c context.Context) error {
		return ps.writer.PutBlock(c, blocker, blocked, ps.now().UnixMilli())
	})
}

// addCooldown keeps a and b apart for d (Story 4.4 calls it). It only ever
// extends: a shorter or non-positive d leaves a longer live cooldown as it is.
func (ps *policyState) addCooldown(ctx context.Context, a, b string, d time.Duration) {
	if !validPair(a, b) || d <= 0 {
		return
	}
	k := pairKey(a, b)
	exp := ps.now().Add(d).UnixMilli()
	if old, ok := ps.cooldowns[k]; ok && old >= exp {
		return
	}
	ps.cooldowns[k] = exp
	ps.write(ctx, func(c context.Context) error { return ps.writer.PutCooldown(c, a, b, exp) })
}

// addBan bans key (Story 4.9 calls it).
func (ps *policyState) addBan(ctx context.Context, key, note string) {
	if key == "" {
		return
	}
	ps.bans[key] = struct{}{}
	ps.write(ctx, func(c context.Context) error {
		return ps.writer.PutBan(c, key, ps.now().UnixMilli(), note)
	})
}
