package hub

import (
	"encoding/base64"
	"regexp"
	"testing"
)

var urlSafeNoPad = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func TestNewSessionIDIsURLSafeAndUnpadded(t *testing.T) {
	for i := 0; i < 1000; i++ {
		id := newSessionID()
		if !urlSafeNoPad.MatchString(id) {
			t.Fatalf("session id %q is not URL-safe base64 without padding", id)
		}
		if got, err := base64.RawURLEncoding.DecodeString(id); err != nil {
			t.Fatalf("session id %q does not decode as RawURLEncoding: %v", id, err)
		} else if len(got) != sessionIDBytes {
			t.Fatalf("session id %q decodes to %d bytes, want %d", id, len(got), sessionIDBytes)
		}
	}
}

func TestNewSessionIDEntropyFloor(t *testing.T) {
	// 16 bytes = 128 bits, the spine's minimum for an opaque, unguessable id.
	if sessionIDBytes < 16 {
		t.Fatalf("sessionIDBytes = %d, want >= 16 (128 bits)", sessionIDBytes)
	}
	// RawURLEncoding of 16 bytes is 22 chars.
	if got := len(newSessionID()); got != 22 {
		t.Fatalf("session id length = %d, want 22", got)
	}
}

func TestNewSessionIDUnique(t *testing.T) {
	const n = 100_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := newSessionID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate session id after %d mints: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}
