// Package store is the backend's durable policy mirror (AD-8, AD-18): one
// SQLite file, opened WAL with a busy timeout so `serve` and the maintainer
// CLI can share it, and migrated forward-only on open from the embedded
// backend/migrations set. The hub loads policy from here once at startup and
// writes through to it; no matching decision ever reads it.
//
// No account key, and no message content, is ever put in an error or a log.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diegovillafuerte1/claudingtin/backend/migrations"

	// The pure-Go SQLite driver ("sqlite"): no cgo, so every target cross-builds.
	"modernc.org/sqlite"
)

// BusyTimeoutMs is how long a connection waits on a lock another connection or
// process holds before failing with "database is locked".
const BusyTimeoutMs = 5000

// Cooldown is one pair kept apart until ExpiresMs (Unix epoch ms). KeyA < KeyB.
type Cooldown struct {
	KeyA, KeyB string
	ExpiresMs  int64
}

// Policy is everything a matching decision needs, as loaded at startup.
// Blocks are [blocker, blocked]; Cooldowns holds only the still-live ones.
type Policy struct {
	Blocks    [][2]string
	Cooldowns []Cooldown
	Bans      []string
}

// Store is an open, migrated policy database. Safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path, applies every pending
// migration, and returns it. It fails if the file cannot be opened, a
// migration fails (the whole pending batch is rolled back), or the database is
// already at a schema version newer than this binary knows.
func Open(ctx context.Context, path string) (*Store, error) {
	return open(ctx, path, migrations.FS)
}

func open(ctx context.Context, path string, migs fs.FS) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	// The pragmas ride on the DSN's query string: a path that has one of its
	// own would drop or garble them.
	if strings.ContainsAny(path, "?#") {
		return nil, errors.New("store: database path must not contain '?' or '#'")
	}
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)", path, BusyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := pingThroughBusy(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// WAL is what lets serve and the CLI share the file; a filesystem that
	// cannot do it (some network mounts) silently keeps the rollback journal.
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		_ = db.Close()
		return nil, fmt.Errorf("store: journal_mode is %q, want wal", mode)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx, migs); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// pingThroughBusy opens the first connection. Switching a fresh file to WAL
// can fail SQLITE_BUSY at once, without the busy handler, while another
// process is doing the same; that is retried for up to BusyTimeoutMs.
func pingThroughBusy(ctx context.Context, db *sql.DB) error {
	deadline := time.Now().Add(BusyTimeoutMs * time.Millisecond)
	for {
		err := db.PingContext(ctx)
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// isBusy reports SQLITE_BUSY (primary code 5, any extended code).
func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == 5
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

type migration struct {
	version int
	name    string
	sql     string
}

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// loadMigrations reads every NNNN_name.sql in migs, ordered by version. The
// versions must run 1..N with no gap or duplicate.
func loadMigrations(migs fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migs, ".")
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			// Never skipped silently: a misnamed file would never run.
			return nil, fmt.Errorf("store: migration %q is not named NNNN_lower_snake.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(migs, e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations must run 0001, 0002, … with no gap or duplicate; %s is out of place", m.name)
		}
	}
	return out, nil
}

// migrate applies every migration above schema_version in one BEGIN IMMEDIATE
// transaction: a second process starting at the same moment waits on the
// write lock, then finds nothing left to do.
func (s *Store) migrate(ctx context.Context, migs fs.FS) (err error) {
	all, err := loadMigrations(migs)
	if err != nil {
		return err
	}
	latest := len(all)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("store: migrate: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Rollback on a fresh context: a cancelled ctx must not leave the
			// write lock held on a pooled connection.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)"); err != nil {
		return fmt.Errorf("store: migrate: schema_version: %w", err)
	}
	current := 0
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("store: migrate: read schema_version: %w", err)
	}
	if current > latest {
		return fmt.Errorf("store: database schema_version %d is newer than this binary's latest %d", current, latest)
	}
	if current == latest {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("store: migrate: commit: %w", err)
		}
		committed = true
		return nil
	}
	for _, m := range all[current:] {
		if _, err := conn.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM schema_version"); err != nil {
		return fmt.Errorf("store: migrate: schema_version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", latest); err != nil {
		return fmt.Errorf("store: migrate: schema_version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("store: migrate: commit: %w", err)
	}
	committed = true
	return nil
}

// SchemaVersion is the database's applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("store: schema_version: %w", err)
	}
	return v, nil
}

// LoadPolicy reads every block, every cooldown still live at nowMs, and every
// ban, as one consistent snapshot (a single read transaction).
func (s *Store) LoadPolicy(ctx context.Context, nowMs int64) (Policy, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Policy{}, fmt.Errorf("store: load: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var p Policy
	rows, err := tx.QueryContext(ctx, "SELECT blocker_key, blocked_key FROM block ORDER BY blocker_key, blocked_key")
	if err != nil {
		return Policy{}, fmt.Errorf("store: load blocks: %w", err)
	}
	for rows.Next() {
		var b [2]string
		if err := rows.Scan(&b[0], &b[1]); err != nil {
			rows.Close()
			return Policy{}, fmt.Errorf("store: load blocks: %w", err)
		}
		p.Blocks = append(p.Blocks, b)
	}
	if err := closeRows(rows); err != nil {
		return Policy{}, fmt.Errorf("store: load blocks: %w", err)
	}

	rows, err = tx.QueryContext(ctx, "SELECT key_a, key_b, expires_ms FROM cooldown WHERE expires_ms > ? ORDER BY key_a, key_b", nowMs)
	if err != nil {
		return Policy{}, fmt.Errorf("store: load cooldowns: %w", err)
	}
	for rows.Next() {
		var c Cooldown
		if err := rows.Scan(&c.KeyA, &c.KeyB, &c.ExpiresMs); err != nil {
			rows.Close()
			return Policy{}, fmt.Errorf("store: load cooldowns: %w", err)
		}
		p.Cooldowns = append(p.Cooldowns, c)
	}
	if err := closeRows(rows); err != nil {
		return Policy{}, fmt.Errorf("store: load cooldowns: %w", err)
	}

	rows, err = tx.QueryContext(ctx, "SELECT account_key FROM ban ORDER BY account_key")
	if err != nil {
		return Policy{}, fmt.Errorf("store: load bans: %w", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return Policy{}, fmt.Errorf("store: load bans: %w", err)
		}
		p.Bans = append(p.Bans, k)
	}
	if err := closeRows(rows); err != nil {
		return Policy{}, fmt.Errorf("store: load bans: %w", err)
	}
	return p, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// CanonicalPair orders two keys so a pair has one cooldown row whichever side
// names it first.
func CanonicalPair(a, b string) (string, string) {
	if b < a {
		return b, a
	}
	return a, b
}

// PutBlock records that blocker blocked blocked; a repeat is a no-op.
func (s *Store) PutBlock(ctx context.Context, blocker, blocked string, createdMs int64) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO block (blocker_key, blocked_key, created_ms) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
		blocker, blocked, createdMs)
	if err != nil {
		return fmt.Errorf("store: put block: %w", err)
	}
	return nil
}

// PutCooldown keeps a and b apart until expiresMs. It only ever extends: an
// earlier expiry never shortens a stored one.
func (s *Store) PutCooldown(ctx context.Context, a, b string, expiresMs int64) error {
	ka, kb := CanonicalPair(a, b)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO cooldown (key_a, key_b, expires_ms) VALUES (?, ?, ?) ON CONFLICT (key_a, key_b) DO UPDATE SET expires_ms = max(expires_ms, excluded.expires_ms)",
		ka, kb, expiresMs)
	if err != nil {
		return fmt.Errorf("store: put cooldown: %w", err)
	}
	return nil
}

// PutBan bans key, replacing the note of an existing ban.
func (s *Store) PutBan(ctx context.Context, key string, createdMs int64, note string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO ban (account_key, created_ms, note) VALUES (?, ?, ?) ON CONFLICT (account_key) DO UPDATE SET note = excluded.note",
		key, createdMs, note)
	if err != nil {
		return fmt.Errorf("store: put ban: %w", err)
	}
	return nil
}
