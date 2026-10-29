package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/diegovillafuerte1/claudingtin/backend/migrations"
)

var ctx = context.Background()

func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "claudingtin.db")
}

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func latestVersion(t *testing.T) int {
	t.Helper()
	all, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}

func tables(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

func TestFreshDBAppliesAllMigrationsAndPragmas(t *testing.T) {
	s := mustOpen(t, dbPath(t))

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := latestVersion(t); v != want || v < 1 {
		t.Fatalf("schema_version = %d, want %d", v, want)
	}
	got := tables(t, s)
	for _, name := range []string{"account", "profile", "block", "cooldown", "ban", "report", "schema_version"} {
		if !got[name] {
			t.Errorf("table %q missing (have %v)", name, got)
		}
	}

	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var busy int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != BusyTimeoutMs {
		t.Errorf("busy_timeout = %d, want %d", busy, BusyTimeoutMs)
	}
}

func TestReopenUpToDateRunsNothing(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	if err := s.PutBan(ctx, "k1", 1, "n"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// 0001 creates its tables without IF NOT EXISTS: re-running it would fail
	// the reopen, so a clean reopen proves nothing ran.
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if v, _ := s2.SchemaVersion(ctx); v != latestVersion(t) {
		t.Fatalf("schema_version = %d", v)
	}
	p, err := s2.LoadPolicy(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Bans) != 1 {
		t.Fatalf("bans after reopen = %v", p.Bans)
	}
}

// withExtra is the embedded set plus extra files.
func withExtra(t *testing.T, extra map[string]string) fstest.MapFS {
	t.Helper()
	all, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	m := fstest.MapFS{}
	for _, mg := range all {
		m[mg.name] = &fstest.MapFile{Data: []byte(mg.sql)}
	}
	for k, v := range extra {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func TestPartlyMigratedRunsOnlyPending(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	_ = s.Close()

	next := fmt.Sprintf("%04d_add_probe.sql", latestVersion(t)+1)
	after := fmt.Sprintf("%04d_add_probe2.sql", latestVersion(t)+2)
	migs := withExtra(t, map[string]string{
		next:  "CREATE TABLE probe (x INTEGER);",
		after: "INSERT INTO probe (x) VALUES (7);",
	})
	s2, err := open(ctx, path, migs)
	if err != nil {
		t.Fatalf("migrate pending: %v", err)
	}
	defer s2.Close()
	v, _ := s2.SchemaVersion(ctx)
	if v != latestVersion(t)+2 {
		t.Fatalf("schema_version = %d", v)
	}
	var n int
	if err := s2.db.QueryRowContext(ctx, "SELECT count(*) FROM probe").Scan(&n); err != nil || n != 1 {
		t.Fatalf("probe rows = %d, err %v", n, err)
	}
}

func TestFailingMigrationRollsBackTheWholeBatch(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	_ = s.Close()
	base := latestVersion(t)

	migs := withExtra(t, map[string]string{
		fmt.Sprintf("%04d_ok.sql", base+1):  "CREATE TABLE probe (x INTEGER);",
		fmt.Sprintf("%04d_bad.sql", base+2): "THIS IS NOT SQL;",
	})
	if _, err := open(ctx, path, migs); err == nil {
		t.Fatal("expected the bad migration to fail Open")
	}

	s2 := mustOpen(t, path)
	v, _ := s2.SchemaVersion(ctx)
	if v != base {
		t.Fatalf("schema_version = %d after a failed batch, want %d", v, base)
	}
	if tables(t, s2)["probe"] {
		t.Fatal("probe table survived a rolled-back batch")
	}
}

func TestFailingFirstMigrationLeavesFreshDBEmpty(t *testing.T) {
	path := dbPath(t)
	migs := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE probe (x INTEGER);")},
		"0002_bad.sql": {Data: []byte("NOPE;")},
	}
	if _, err := open(ctx, path, migs); err == nil {
		t.Fatal("expected failure")
	}
	s := mustOpen(t, path)
	if tables(t, s)["probe"] {
		t.Fatal("probe table survived")
	}
}

func TestNewerDatabaseRefusesToOpen(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	if _, err := s.db.ExecContext(ctx, "UPDATE schema_version SET version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err := Open(ctx, path)
	if err == nil {
		t.Fatal("expected a newer schema to refuse")
	}
	if !strings.Contains(err.Error(), "999") || !strings.Contains(err.Error(), fmt.Sprint(latestVersion(t))) {
		t.Fatalf("error should name both versions: %v", err)
	}
}

func TestMigrationSetMustBeContiguous(t *testing.T) {
	for name, migs := range map[string]fstest.MapFS{
		"gap":       {"0001_a.sql": {Data: []byte("SELECT 1;")}, "0003_c.sql": {Data: []byte("SELECT 1;")}},
		"duplicate": {"0001_a.sql": {Data: []byte("SELECT 1;")}, "0001_b.sql": {Data: []byte("SELECT 1;")}},
		"no start":  {"0002_b.sql": {Data: []byte("SELECT 1;")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMigrations(migs); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBadPathFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "x.db")
	if _, err := Open(ctx, missing); err == nil {
		t.Fatal("expected a missing directory to fail")
	}
	if _, err := Open(ctx, ""); err == nil {
		t.Fatal("expected an empty path to fail")
	}
}

// Two handles on one file stand in for serve plus a CLI process: SQLite's
// locking is file-level, so concurrent writers from both must all land with no
// "database is locked".
func TestTwoOpenersWriteConcurrently(t *testing.T) {
	path := dbPath(t)
	a := mustOpen(t, path)
	b := mustOpen(t, path)

	const each = 100
	var wg sync.WaitGroup
	errs := make(chan error, 4*each)
	for w, s := range []*Store{a, b, a, b} {
		wg.Add(1)
		go func(w int, s *Store) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := s.PutBan(ctx, fmt.Sprintf("w%d-%d", w, i), int64(i), ""); err != nil {
					errs <- err
				}
				if _, err := s.LoadPolicy(ctx, 0); err != nil {
					errs <- err
				}
			}
		}(w, s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access: %v", err)
	}
	p, err := b.LoadPolicy(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Bans) != 4*each {
		t.Fatalf("bans = %d, want %d", len(p.Bans), 4*each)
	}
}

func TestConcurrentFreshStartMigratesOnce(t *testing.T) {
	path := dbPath(t)
	var wg sync.WaitGroup
	stores := make([]*Store, 4)
	errs := make([]error, 4)
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stores[i], errs[i] = Open(ctx, path)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", i, err)
		}
		defer stores[i].Close()
	}
	var n int
	if err := stores[0].db.QueryRowContext(ctx, "SELECT count(*) FROM schema_version").Scan(&n); err != nil || n != 1 {
		t.Fatalf("schema_version rows = %d, err %v", n, err)
	}
	for i, s := range stores {
		if v, _ := s.SchemaVersion(ctx); v != latestVersion(t) {
			t.Fatalf("opener %d sees version %d", i, v)
		}
	}
}

func TestLoadPolicyRoundTripAndSkipsExpiredCooldowns(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutBlock(ctx, "a", "b", 1))
	must(s.PutBlock(ctx, "a", "b", 2)) // repeat: no-op
	must(s.PutBlock(ctx, "c", "a", 3))
	must(s.PutCooldown(ctx, "z", "y", 2000)) // stored canonical: y, z
	must(s.PutCooldown(ctx, "p", "q", 500))  // expired at now=1000
	must(s.PutCooldown(ctx, "q", "p", 1500)) // same pair, either order: extends it
	must(s.PutCooldown(ctx, "m", "n", 1000)) // expires exactly now: not live
	must(s.PutBan(ctx, "x", 1, "first"))
	must(s.PutBan(ctx, "x", 2, "second"))
	_ = s.Close()

	s2 := mustOpen(t, path)
	p, err := s2.LoadPolicy(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(p.Blocks) != "[[a b] [c a]]" {
		t.Errorf("blocks = %v", p.Blocks)
	}
	if fmt.Sprint(p.Cooldowns) != "[{p q 1500} {y z 2000}]" {
		t.Errorf("cooldowns = %v", p.Cooldowns)
	}
	if fmt.Sprint(p.Bans) != "[x]" {
		t.Errorf("bans = %v", p.Bans)
	}
	var note string
	if err := s2.db.QueryRowContext(ctx, "SELECT note FROM ban WHERE account_key = 'x'").Scan(&note); err != nil || note != "second" {
		t.Errorf("ban note = %q, err %v", note, err)
	}
}

func TestCanonicalPair(t *testing.T) {
	if a, b := CanonicalPair("b", "a"); a != "a" || b != "b" {
		t.Fatal(a, b)
	}
	if a, b := CanonicalPair("a", "b"); a != "a" || b != "b" {
		t.Fatal(a, b)
	}
}

func TestPathWithQueryCharactersIsRefused(t *testing.T) {
	for _, p := range []string{"x.db?mode=ro", "x#y.db"} {
		if _, err := Open(ctx, filepath.Join(t.TempDir(), p)); err == nil {
			t.Errorf("%q should be refused", p)
		}
	}
}

func TestMisnamedMigrationIsAnErrorNotSkipped(t *testing.T) {
	for _, name := range []string{"0002_AddX.sql", "2_x.sql", "0002-x.sql"} {
		migs := fstest.MapFS{
			"0001_a.sql": {Data: []byte("SELECT 1;")},
			name:         {Data: []byte("SELECT 1;")},
		}
		if _, err := loadMigrations(migs); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	// Non-.sql files (docs, the embed's Go file) are ignored.
	migs := fstest.MapFS{"0001_a.sql": {Data: []byte("SELECT 1;")}, "README.md": {Data: []byte("x")}}
	if all, err := loadMigrations(migs); err != nil || len(all) != 1 {
		t.Fatalf("non-sql files should be ignored: %v %v", all, err)
	}
}

func TestPutCooldownNeverShortens(t *testing.T) {
	s := mustOpen(t, dbPath(t))
	if err := s.PutCooldown(ctx, "a", "b", 5000); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCooldown(ctx, "b", "a", 1000); err != nil {
		t.Fatal(err)
	}
	p, err := s.LoadPolicy(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Cooldowns) != 1 || p.Cooldowns[0].ExpiresMs != 5000 {
		t.Fatalf("cooldowns = %v", p.Cooldowns)
	}
}

func TestSchemaVersionReadsTheHighestRow(t *testing.T) {
	path := dbPath(t)
	s := mustOpen(t, path)
	// A stray extra row (hand edit) must not make migrations re-run.
	if _, err := s.db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (0)"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2 := mustOpen(t, path)
	if v, _ := s2.SchemaVersion(ctx); v != latestVersion(t) {
		t.Fatalf("schema_version = %d", v)
	}
}
