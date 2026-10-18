// Package proto is the single source of truth for the claudingtin wire
// contract: the message envelope, the protocol version, and every v1 message
// shape plus its encode/decode helpers. It is transport-neutral: the same
// messages travel as /ws frames and as /v1 HTTPS request bodies, response
// bodies and long-poll events.
//
// It imports no sibling module and no third-party package — only the Go standard
// library. The backend imports it; the mod's TypeScript types are checked
// against the golden fixtures its tests write to testdata/fixtures. Nothing else
// defines a message shape.
//
// Wire format: every message is a single flat JSON object
//
//	{ "type": <snake_case string>, "v": <int>, ...payload }
//
// The payload fields sit at the same level as "type" and "v" (never nested under
// a key). On encode, "v" is always stamped to PROTOCOL_VERSION; message structs
// never carry a "v" field of their own.
package proto

// PROTOCOL_VERSION is the wire-protocol version stamped into every envelope by
// Encode. Any breaking wire change bumps it.
const PROTOCOL_VERSION = 1

// Envelope is the routing frame shared by every message. Decode reads it to find
// the "type" discriminator; Encode writes it alongside the payload fields.
// Message payload structs do not embed it — Encode adds "type" and "v" itself.
type Envelope struct {
	Type string `json:"type"`
	V    int    `json:"v"`
}
