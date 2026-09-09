package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// ---------------------------------------------------------------------------
// v1 message set.
//
// Each message is a PascalCase struct paired with a snake_case discriminator
// constant that matches its wire "type". Story 1.1 defines the shapes and their
// (de)serialisation only — no handling. Payload fields that later stories flesh
// out are called out inline; the structs still round-trip today.
// ---------------------------------------------------------------------------

// --- client -> server ---

// TypeHello is the discriminator for Hello.
const TypeHello = "hello"

// Hello is the first frame the companion sends after the socket opens. It
// carries the client's protocol version and the account key that is the user's
// whole identity to the backend.
type Hello struct {
	AccountKey      string `json:"account_key"`
	ProtocolVersion int    `json:"protocol_version"`
}

// TypeReady is the discriminator for Ready.
const TypeReady = "ready"

// Ready tells the backend the model is thinking and the user is available to be
// matched.
type Ready struct{}

// TypeBusy is the discriminator for Busy.
const TypeBusy = "busy"

// Busy tells the backend the user's turn is over and they are no longer
// available.
type Busy struct{}

// TypeChatMsg is the discriminator for ChatMsg. The same shape travels in both
// directions; it is never echoed back to its sender.
const TypeChatMsg = "chat_msg"

// ChatMsg is a single chat line. ClientMsgID is a client-generated id used to
// de-duplicate and correlate.
type ChatMsg struct {
	ClientMsgID string `json:"client_msg_id"`
	Text        string `json:"text"`
}

// TypeLeave is the discriminator for Leave.
const TypeLeave = "leave"

// Leave tells the backend the user is ending the current matched session.
type Leave struct{}

// TypeBlock is the discriminator for Block.
const TypeBlock = "block"

// Block asks the backend to block the current counterpart. Payload fields land
// in a later epic.
type Block struct{}

// TypeReport is the discriminator for Report.
const TypeReport = "report"

// Report flags the current counterpart to moderation. LastN is how many recent
// messages to attach.
type Report struct {
	LastN int `json:"last_n"`
}

// TypeProfilePut is the discriminator for ProfilePut.
const TypeProfilePut = "profile_put"

// ProfilePut upserts the user's profile. Payload fields land in a later epic.
type ProfilePut struct{}

// TypeForgetMe is the discriminator for ForgetMe.
const TypeForgetMe = "forget_me"

// ForgetMe asks the backend to erase everything tied to the account key.
type ForgetMe struct{}

// TypeNotePut is the discriminator for NotePut.
const TypeNotePut = "note_put"

// NotePut stores a private note about a counterpart. Payload fields land in a
// later epic.
type NotePut struct{}

// TypeHeartbeat is the discriminator for Heartbeat.
const TypeHeartbeat = "heartbeat"

// Heartbeat keeps the connection alive.
type Heartbeat struct{}

// --- server -> client ---

// TypeQueued is the discriminator for Queued.
const TypeQueued = "queued"

// Queued acknowledges the user is waiting for a match.
type Queued struct{}

// TypeMatched is the discriminator for Matched.
const TypeMatched = "matched"

// Matched announces a counterpart has been found. SessionID is the backend-minted
// opaque, unguessable identifier for the pairing — one per match, byte-identical
// for both peers. Pseudonym and Blurb come from the backend's canonical profile
// (empty until Epic 5). Opener is the pre-written conversation opener the backend
// selects per match from its curated set, rotating through the set so the same
// opener is never used for two consecutive matches; both peers get the identical
// string.
type Matched struct {
	SessionID string `json:"session_id"`
	Pseudonym string `json:"pseudonym"`
	Blurb     string `json:"blurb"`
	Opener    string `json:"opener"`
}

// TypeSessionEnded is the discriminator for SessionEnded.
const TypeSessionEnded = "session_ended"

// SessionEnded announces the matched session is over. It is cause-agnostic and
// payload-free by contract: it carries no reason, no session_id, no timestamp,
// and no field of any kind, so its wire form — {"type":"session_ended","v":1} —
// is byte-identical for every way a session can end (peer model-return / busy,
// peer leave, peer disconnect, connection takeover, and — in later epics — ban
// and reconnect-grace expiry). Every backend-side end routes its peer
// notification through the one hub teardownPair emission point; the taken-over
// connection itself is told by exactly one path in the connection handler. This
// type stays struct{} — there is no versioned variant that adds a cause.
type SessionEnded struct{}

// TypeBlocklist is the discriminator for Blocklist.
const TypeBlocklist = "blocklist"

// Blocklist carries the user's current blocklist. Payload fields land in a later
// epic.
type Blocklist struct{}

// TypeProfileAck is the discriminator for ProfileAck.
const TypeProfileAck = "profile_ack"

// ProfileAck acknowledges a ProfilePut. Payload fields land in a later epic.
type ProfileAck struct{}

// TypeError is the discriminator for Error. Client-facing errors are always an
// Error message, never a transport close.
const TypeError = "error"

// Error is a client-facing error. Code is a stable machine string; Msg is human
// text.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// TypePleaseUpdate is the discriminator for PleaseUpdate. It is its own type,
// sent once before the socket closes on an unsupported client version — never an
// Error code.
const TypePleaseUpdate = "please_update"

// PleaseUpdate tells the client its version is too old to be served.
type PleaseUpdate struct{}

// ---------------------------------------------------------------------------
// Registry + (de)serialisation.
// ---------------------------------------------------------------------------

// registry maps every v1 discriminator to a constructor returning a pointer to a
// fresh zero value of the matching struct. It is the one list Decode switches
// on; keep it in lockstep with the message set above and with the round-trip
// test.
var registry = map[string]func() any{
	TypeHello:        func() any { return new(Hello) },
	TypeReady:        func() any { return new(Ready) },
	TypeBusy:         func() any { return new(Busy) },
	TypeChatMsg:      func() any { return new(ChatMsg) },
	TypeLeave:        func() any { return new(Leave) },
	TypeBlock:        func() any { return new(Block) },
	TypeReport:       func() any { return new(Report) },
	TypeProfilePut:   func() any { return new(ProfilePut) },
	TypeForgetMe:     func() any { return new(ForgetMe) },
	TypeNotePut:      func() any { return new(NotePut) },
	TypeHeartbeat:    func() any { return new(Heartbeat) },
	TypeQueued:       func() any { return new(Queued) },
	TypeMatched:      func() any { return new(Matched) },
	TypeSessionEnded: func() any { return new(SessionEnded) },
	TypeBlocklist:    func() any { return new(Blocklist) },
	TypeProfileAck:   func() any { return new(ProfileAck) },
	TypeError:        func() any { return new(Error) },
	TypePleaseUpdate: func() any { return new(PleaseUpdate) },
}

// discriminatorByType is the reverse of registry, built once from it so the two
// can never drift. Encode looks a value's concrete struct type up here.
var discriminatorByType map[reflect.Type]string

func init() {
	discriminatorByType = make(map[reflect.Type]string, len(registry))
	for name, ctor := range registry {
		discriminatorByType[reflect.TypeOf(ctor()).Elem()] = name
	}
}

// Encode marshals a v1 message value into its flat wire form:
//
//	{ "type": <discriminator>, "v": PROTOCOL_VERSION, ...payload }
//
// msg must be one of the registered v1 message structs, passed by value or by
// pointer. "v" is always stamped to PROTOCOL_VERSION regardless of the input.
func Encode(msg any) ([]byte, error) {
	t := reflect.TypeOf(msg)
	if t == nil {
		return nil, errors.New("proto: cannot encode a nil message")
	}
	if t.Kind() == reflect.Pointer {
		if reflect.ValueOf(msg).IsNil() {
			return nil, errors.New("proto: cannot encode a nil message pointer")
		}
		t = t.Elem()
	}
	name, ok := discriminatorByType[t]
	if !ok {
		return nil, fmt.Errorf("proto: %s is not a registered v1 message type", t)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("proto: message payload is not a JSON object: %w", err)
	}

	typeRaw, _ := json.Marshal(name)
	vRaw, _ := json.Marshal(PROTOCOL_VERSION)
	fields["type"] = typeRaw
	fields["v"] = vRaw

	return json.Marshal(fields)
}

// Decode reads the envelope, resolves the concrete struct via the registry, and
// unmarshals the whole frame into it. The returned value is the message struct
// itself (e.g. Hello, not *Hello); callers type-assert on the discriminator.
//
// It returns a non-nil error and never panics for malformed JSON, a missing or
// empty "type", or an unknown discriminator.
//
// The wire contract is forward-compatible: unknown payload fields in an incoming
// frame are ignored, not rejected, so a newer peer may add fields without
// breaking an older one.
func Decode(data []byte) (any, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("proto: malformed frame: %w", err)
	}
	if env.Type == "" {
		return nil, errors.New("proto: frame has no \"type\" discriminator")
	}
	ctor, ok := registry[env.Type]
	if !ok {
		return nil, fmt.Errorf("proto: unknown message type %q", env.Type)
	}

	msg := ctor()
	if err := json.Unmarshal(data, msg); err != nil {
		return nil, fmt.Errorf("proto: decoding %q payload: %w", env.Type, err)
	}
	return reflect.ValueOf(msg).Elem().Interface(), nil
}
