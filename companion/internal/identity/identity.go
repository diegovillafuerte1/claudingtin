// Package identity gives the companion a stable, anonymous identity to present
// to the backend: a single random UUIDv4, generated on first run and then
// reused on every start (and regenerated if the file goes missing or corrupt).
//
// The key lives at <configDir>/account-key, where configDir is an OS-config
// path *outside* the plugin's own directory (see DefaultConfigDir). Keeping it
// there is the whole point — reinstalling or updating the plugin wipes the
// plugin directory but leaves the key untouched, so blocks and bans keyed to a
// user survive a reinstall (ARCHITECTURE-SPINE AD-11, PRD NFR3/NFR4).
//
// Load is defensive: a key file that is missing, empty, whitespace-only, or not
// a canonical UUIDv4 string is treated as absent and (re)generated. A valid key
// is returned verbatim (after trimming surrounding whitespace) with the file
// otherwise left alone.
//
// The key is the user's identity to the backend and must never leak. This
// package writes no logs, and the key never appears in a returned error or a
// panic message. stdlib only — no third-party dependency.
package identity

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// keyFileName is the fixed basename of the key file inside configDir.
	keyFileName = "account-key"
	// configSubdir is appended to os.UserConfigDir() by DefaultConfigDir.
	configSubdir = "claudingtin"

	// keyFileMode is the permission every write of the key file uses.
	keyFileMode = 0o600
	// configDirMode is the permission a freshly created configDir gets.
	configDirMode = 0o700
)

// convergeAttempts / convergeInterval bound how long a process that lost the
// O_EXCL first-create race waits for the winner to write its key before
// concluding the file is genuinely empty/corrupt and replacing it. ~100ms worst
// case, only ever hit under a real first-run race. Package-level vars, not
// consts, only so a test can widen the window; nothing outside this package
// touches them.
var (
	convergeAttempts = 20
	convergeInterval = 5 * time.Millisecond
)

// DefaultConfigDir returns the directory the key file lives in by default:
// os.UserConfigDir() joined with "claudingtin", and nothing else — no env var,
// no config file, no override. The real directory is passed to the companion as
// a launch argument in a later story; this helper is the fallback and the
// documentation of the canonical location.
func DefaultConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("identity: resolve user config dir: %w", err)
	}
	return filepath.Join(base, configSubdir), nil
}

// Load returns the account key stored at filepath.Join(configDir, "account-key"),
// creating it on first run and regenerating it when the on-disk file is empty or
// corrupt.
//
// configDir is created with mode 0700 if it does not exist. The key file is
// always written with mode 0600; an existing valid key file with broader
// permissions is tightened on a best-effort basis. First creation uses
// O_CREATE|O_EXCL so that racing first-run processes converge on one key;
// regeneration writes a sibling temp file and renames it into place. After any
// create or regenerate the file is re-read and the on-disk value returned.
//
// Load only ever touches configDir and the account-key file (plus a sibling
// temp file during regeneration). It resolves no paths of its own and never
// reaches into the plugin directory.
//
// On failure Load returns a wrapped error naming the operation that failed and
// no key; the error never contains key material.
func Load(configDir string) (string, error) {
	if err := os.MkdirAll(configDir, configDirMode); err != nil {
		return "", fmt.Errorf("identity: create config dir: %w", err)
	}
	path := filepath.Join(configDir, keyFileName)

	key, state, err := inspect(path)
	if err != nil {
		return "", err
	}
	switch state {
	case stateValid:
		tightenPerms(path)
		return key, nil
	case stateMissing:
		return firstCreate(path)
	default: // stateCorrupt
		return regenerate(path)
	}
}

// keyState is the classification inspect assigns to the key file.
type keyState int

const (
	stateValid keyState = iota
	stateMissing
	stateCorrupt
)

// maxKeyFileSize caps how many bytes inspect reads from the key file. The
// canonical form is 37 bytes including its trailing newline; anything materially
// larger is not a key this package wrote, so it is classified stateCorrupt (and
// regenerated) without reading the file whole — this also stops a pathologically
// large or endless-device file from causing a huge read or a hang.
const maxKeyFileSize = 512

// inspect opens the key file and classifies it. A missing file is stateMissing;
// a file that is zero-length, whitespace-only, larger than maxKeyFileSize, or
// not a canonical UUIDv4 string is stateCorrupt; otherwise stateValid with the
// trimmed key. Any other open/read error (e.g. permission denied) is returned
// wrapped.
func inspect(path string) (string, keyState, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", stateMissing, nil
		}
		return "", stateCorrupt, fmt.Errorf("identity: open key file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxKeyFileSize+1))
	if err != nil {
		return "", stateCorrupt, fmt.Errorf("identity: read key file: %w", err)
	}
	if len(data) > maxKeyFileSize {
		return "", stateCorrupt, nil
	}
	key := strings.TrimSpace(string(data))
	if !looksLikeUUIDv4(key) {
		return "", stateCorrupt, nil
	}
	return key, stateValid, nil
}

// firstCreate generates a key and writes it with O_CREATE|O_EXCL|O_WRONLY. If
// another process won the race (os.ErrExist) it falls through to resolveExisting
// so both callers converge on the winner's key.
func firstCreate(path string) (string, error) {
	uuid, err := newUUIDv4()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, keyFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return resolveExisting(path)
		}
		return "", fmt.Errorf("identity: create key file: %w", err)
	}
	if err := finalizeKeyFile(f, uuid, "key file"); err != nil {
		// A write/sync/close failure after the O_EXCL open must not leave a
		// partial or zero-length key file behind.
		_ = os.Remove(path)
		return "", err
	}
	return reread(path, uuid), nil
}

// finalizeKeyFile writes uuid and a trailing newline to f, flushes the contents
// to stable storage (so a freshly written key survives a crash or power loss —
// the whole promise of a reinstall-stable path), and closes f. label names the
// file in any wrapped error. On any failure f is closed and the error returned
// wrapped; the caller removes any partial file. Only the file contents are
// synced — no parent-directory fsync, for portability.
func finalizeKeyFile(f *os.File, uuid, label string) error {
	if _, err := f.WriteString(uuid + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("identity: write %s: %w", label, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("identity: sync %s: %w", label, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("identity: close %s: %w", label, err)
	}
	return nil
}

// resolveExisting handles the O_EXCL loser: the file is there but the winner may
// not have written its key yet. Re-read across the convergence window, returning
// the winner's key as soon as it appears; only if the window expires with the
// file still empty/corrupt (no live writer) is it replaced.
func resolveExisting(path string) (string, error) {
	for attempt := 0; attempt < convergeAttempts; attempt++ {
		key, state, err := inspect(path)
		if err != nil {
			return "", err
		}
		if state == stateValid {
			return key, nil
		}
		time.Sleep(convergeInterval)
	}
	return regenerate(path)
}

// regenerate writes a fresh key to a sibling temp file (mode 0600) and renames
// it over path, then re-reads and returns the on-disk value. The rename is
// atomic, so a reader never sees a half-written key and no partial file is left
// behind on failure.
func regenerate(path string) (string, error) {
	uuid, err := newUUIDv4()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+keyFileName+"-*")
	if err != nil {
		return "", fmt.Errorf("identity: create temp key file: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	// os.CreateTemp already restricts to 0600 on Unix; this is belt-and-braces
	// and best-effort (Windows permissions are advisory).
	_ = tmp.Chmod(keyFileMode)
	if err := finalizeKeyFile(tmp, uuid, "temp key file"); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("identity: replace key file: %w", err)
	}
	removeTmp = false
	return reread(path, uuid), nil
}

// reread returns the trimmed on-disk key after a create/regenerate so racing
// processes converge on one value. If the file cannot be read back or does not
// parse (pathological — it was just written), the freshly generated value is
// returned instead, still without an error.
func reread(path, generated string) string {
	if key, state, err := inspect(path); err == nil && state == stateValid {
		return key
	}
	return generated
}

// tightenPerms best-effort narrows an existing valid key file to 0600 if it is
// currently broader. A chmod failure is non-fatal: the key is still usable.
func tightenPerms(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if fi.Mode().Perm()&^keyFileMode != 0 {
		_ = os.Chmod(path, keyFileMode)
	}
}

// newUUIDv4 builds a random RFC 4122 version-4 UUID from crypto/rand without a
// third-party dependency: 16 random bytes, the version nibble forced to 0x40 and
// the variant bits to 0x80, formatted as canonical lowercase 8-4-4-4-12 hex.
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("identity: read random bytes: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// looksLikeUUIDv4 reports whether s is a canonical version-4 UUID string:
// length 36, '-' at indices 8/13/18/23, lowercase hex everywhere else, the
// version character (index 14) '4', and the variant character (index 19) one of
// 8/9/a/b. Anything else — garbage, truncated, wrong case, wrong version or
// variant — is rejected, and the caller regenerates rather than repairs.
func looksLikeUUIDv4(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '4' {
				return false
			}
		case 19:
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				return false
			}
		default:
			if !isLowerHex(c) {
				return false
			}
		}
	}
	return true
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
