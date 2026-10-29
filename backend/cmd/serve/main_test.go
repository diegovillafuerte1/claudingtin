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
