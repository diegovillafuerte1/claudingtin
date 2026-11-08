package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/internal/hub"
	"github.com/diegovillafuerte1/claudingtin/backend/internal/store"
	"github.com/diegovillafuerte1/claudingtin/proto"

	_ "modernc.org/sqlite"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOpenPolicyHubRefusesAnUnusableStore(t *testing.T) {
	ctx := context.Background()
	if _, _, err := openPolicyHub(ctx, filepath.Join(t.TempDir(), "no", "dir", "x.db"), time.Now(), quiet); err == nil {
		t.Fatal("a missing directory must refuse to start")
	}

	newer := filepath.Join(t.TempDir(), "newer.db")
	s, err := store.Open(ctx, newer)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// Bump the version from outside, as a newer backend would have.
	raw, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "UPDATE schema_version SET version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, _, err := openPolicyHub(ctx, newer, time.Now(), quiet); err == nil {
		t.Fatal("a newer schema must refuse to start")
	}
}

func TestOpenPolicyHubDefaultsDBPath(t *testing.T) {
	t.Chdir(t.TempDir())
	db, _, err := openPolicyHub(context.Background(), "", time.Now(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(defaultDBPath); err != nil {
		t.Fatalf("empty DB_PATH should create %s: %v", defaultDBPath, err)
	}
}

// The policy on disk is the policy the hub enforces: a persisted ban and block
// keep those keys out of the match serve's hub makes.
func TestOpenPolicyHubEnforcesPersistedPolicy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutBan(ctx, "banned", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlock(ctx, "a", "b", 1); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	db, h, err := openPolicyHub(ctx, path, time.Now(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { h.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	sess := func(k string) *hub.Session {
		return &hub.Session{Key: k, Evict: make(chan struct{}), Outbound: make(chan any, 4)}
	}
	banned, a, b, c := sess("banned"), sess("a"), sess("b"), sess("c")
	for _, x := range []*hub.Session{banned, a, b, c} {
		h.Register(x)
		h.Ready(x)
		time.Sleep(10 * time.Millisecond)
	}
	matched := func(x *hub.Session) bool {
		deadline := time.After(time.Second)
		for {
			select {
			case m := <-x.Outbound:
				if _, ok := m.(proto.Matched); ok {
					return true
				}
			case <-deadline:
				return false
			}
		}
	}
	// a skips b (blocked) and pairs with c; banned never even queues.
	if !matched(a) || !matched(c) {
		t.Fatal("a and c should pair")
	}
	if matched(b) {
		t.Fatal("b is blocked by a and alone: no match")
	}
	if matched(banned) {
		t.Fatal("a banned key must never be matched")
	}
}

// Story 4.2: a block made through serve's hub is written through, survives a
// restart, and touches no profile row.
func TestBlockSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.db")
	sess := func(k string) *hub.Session {
		return &hub.Session{Key: k, Evict: make(chan struct{}), Outbound: make(chan any, 8)}
	}
	matchedSID := func(x *hub.Session, within time.Duration) string {
		deadline := time.After(within)
		for {
			select {
			case m := <-x.Outbound:
				if mm, ok := m.(proto.Matched); ok {
					return mm.SessionID
				}
			case <-deadline:
				return ""
			}
		}
	}
	run := func() (*store.Store, *hub.Hub, func()) {
		db, h, err := openPolicyHub(ctx, path, time.Now(), quiet)
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { h.Run(runCtx); close(done) }()
		return db, h, func() { cancel(); <-done; _ = db.Close() }
	}

	_, h, stop := run()
	a, b := sess("a"), sess("b")
	for _, x := range []*hub.Session{a, b} {
		h.Register(x)
		h.Ready(x)
		time.Sleep(10 * time.Millisecond)
	}
	sid := matchedSID(a, time.Second)
	if sid == "" || matchedSID(b, time.Second) != sid {
		t.Fatal("a and b should pair first")
	}
	h.Block(a, sid)
	h.Count() // a round trip: the block (and its write-through) is done
	stop()

	db, h2, stop2 := run()
	defer stop2()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var profiles int
	if err := raw.QueryRowContext(ctx, "SELECT count(*) FROM profile").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatalf("profile rows = %d, err %v; a block must touch none", profiles, err)
	}
	p, err := db.LoadPolicy(ctx, time.Now().UnixMilli())
	if err != nil || len(p.Blocks) != 1 || p.Blocks[0] != [2]string{"a", "b"} {
		t.Fatalf("blocks after restart = %v, err %v", p.Blocks, err)
	}
	a2, b2 := sess("a"), sess("b")
	for _, x := range []*hub.Session{a2, b2} {
		h2.Register(x)
		h2.Ready(x)
		time.Sleep(10 * time.Millisecond)
	}
	if matchedSID(a2, 300*time.Millisecond) != "" {
		t.Fatal("a and b matched again after a restart")
	}
}
