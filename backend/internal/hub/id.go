package hub

import (
	"crypto/rand"
	"encoding/base64"
)

// sessionIDBytes is the entropy of a minted session_id, in bytes: 16 bytes =
// 128 bits, the floor the spine requires for an "opaque, unguessable" id.
const sessionIDBytes = 16

// newSessionID mints the opaque identifier carried in a matched frame: 16 bytes
// from crypto/rand, base64 URL-safe with no padding (~22 chars). It is not
// UUID-shaped on purpose — the wire contract only asks for opaque and
// unguessable, and the backend cannot reach the companion's UUID helper across
// the dependency-direction guard. A crypto/rand read failure is unrecoverable
// here, so it panics rather than mint a low-entropy id.
func newSessionID() string {
	var b [sessionIDBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("hub: crypto/rand read failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
