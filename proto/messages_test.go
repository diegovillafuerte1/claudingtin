package proto

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// roundTripSamples holds one populated (or deliberately empty) value for every
// v1 message type. Keep it in lockstep with registry — TestRegistryFullyCovered
// fails if a registered type has no sample here.
var roundTripSamples = []any{
	// client -> server
	Hello{AccountKey: "acct-1a2b3c", ProtocolVersion: 1},
	Ready{},
	Busy{},
	ChatMsg{ClientMsgID: "cmid-42", Text: "héllo 😀 مرحبا"},
	Leave{},
	Block{},
	Report{LastN: 5},
	ProfilePut{},
	ForgetMe{},
	NotePut{},
	Heartbeat{},
	// server -> client
	Queued{},
	Matched{SessionID: "s-9f8e7d6c5b4a", Pseudonym: "quiet-otter", Blurb: "likes long compiles", Opener: "what are you avoiding right now?"},
	SessionEnded{},
	Blocklist{},
	ProfileAck{},
	Error{Code: "rate_limited", Msg: "slow down a moment"},
	PleaseUpdate{},
}

// wireNames maps each discriminator constant to a zero value of its struct, so
// the test can assert the constant, the registry, and the struct all agree.
var wireNames = map[string]any{
	TypeHello:        Hello{},
	TypeReady:        Ready{},
	TypeBusy:         Busy{},
	TypeChatMsg:      ChatMsg{},
	TypeLeave:        Leave{},
	TypeBlock:        Block{},
	TypeReport:       Report{},
	TypeProfilePut:   ProfilePut{},
	TypeForgetMe:     ForgetMe{},
	TypeNotePut:      NotePut{},
	TypeHeartbeat:    Heartbeat{},
	TypeQueued:       Queued{},
	TypeMatched:      Matched{},
	TypeSessionEnded: SessionEnded{},
	TypeBlocklist:    Blocklist{},
	TypeProfileAck:   ProfileAck{},
	TypeError:        Error{},
	TypePleaseUpdate: PleaseUpdate{},
}

var snakeCase = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)

func TestRoundTripEveryMessageType(t *testing.T) {
	for _, sample := range roundTripSamples {
		name := reflect.TypeOf(sample).Name()
		t.Run(name, func(t *testing.T) {
			raw, err := Encode(sample)
			if err != nil {
				t.Fatalf("Encode(%s) error: %v", name, err)
			}

			// Envelope is present and correct.
			var env Envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("envelope unmarshal: %v", err)
			}
			if env.V != PROTOCOL_VERSION {
				t.Errorf("envelope v = %d, want %d", env.V, PROTOCOL_VERSION)
			}
			wantType := discriminatorByType[reflect.TypeOf(sample)]
			if env.Type != wantType {
				t.Errorf("envelope type = %q, want %q", env.Type, wantType)
			}

			// Wire form is flat: type, v, and payload keys share one level.
			var flat map[string]any
			if err := json.Unmarshal(raw, &flat); err != nil {
				t.Fatalf("flat unmarshal: %v", err)
			}
			if _, nested := flat["payload"]; nested {
				t.Errorf("wire form nests payload under a key: %s", raw)
			}

			// Decode yields a value deep-equal to the original.
			got, err := Decode(raw)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if !reflect.DeepEqual(got, sample) {
				t.Errorf("round-trip mismatch:\n got  %#v\n want %#v", got, sample)
			}
		})
	}
}

func TestKnownMessageFlatWire(t *testing.T) {
	raw, err := Encode(Hello{AccountKey: "k-123", ProtocolVersion: 1})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["type"] != "hello" {
		t.Errorf(`type = %v, want "hello"`, m["type"])
	}
	if m["v"] != float64(PROTOCOL_VERSION) {
		t.Errorf("v = %v, want %d", m["v"], PROTOCOL_VERSION)
	}
	if m["account_key"] != "k-123" {
		t.Errorf(`account_key = %v, want "k-123"`, m["account_key"])
	}
}

func TestRegistryFullyCovered(t *testing.T) {
	sampled := map[string]bool{}
	for _, sample := range roundTripSamples {
		name, ok := discriminatorByType[reflect.TypeOf(sample)]
		if !ok {
			t.Errorf("sample %T is not a registered message type", sample)
			continue
		}
		if sampled[name] {
			t.Errorf("duplicate round-trip sample for %q", name)
		}
		sampled[name] = true
	}
	for name := range registry {
		if !sampled[name] {
			t.Errorf("registry has %q but roundTripSamples does not cover it", name)
		}
	}
	if len(sampled) != len(registry) {
		t.Errorf("covered %d types, registry has %d", len(sampled), len(registry))
	}
}

func TestDiscriminatorConstantsMatchWireNames(t *testing.T) {
	if len(wireNames) != len(registry) {
		t.Fatalf("wireNames has %d entries, registry has %d", len(wireNames), len(registry))
	}
	for wire, zero := range wireNames {
		if !snakeCase.MatchString(wire) {
			t.Errorf("discriminator %q is not snake_case", wire)
		}
		ctor, ok := registry[wire]
		if !ok {
			t.Errorf("discriminator %q is absent from the registry", wire)
			continue
		}
		gotType := reflect.TypeOf(ctor()).Elem()
		wantType := reflect.TypeOf(zero)
		if gotType != wantType {
			t.Errorf("discriminator %q maps to %s, want %s", wire, gotType, wantType)
		}
		raw, err := Encode(zero)
		if err != nil {
			t.Fatalf("Encode(%s): %v", wantType, err)
		}
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("envelope unmarshal: %v", err)
		}
		if env.Type != wire {
			t.Errorf("Encode(%s) wrote type %q, want %q", wantType, env.Type, wire)
		}
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"unknown discriminator", `{"type":"bogus","v":1}`},
		{"missing type", `{"v":1}`},
		{"empty type", `{"type":"","v":1}`},
		{"malformed json", `{"type":`},
		{"not an object", `["type","hello"]`},
		{"empty input", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode panicked: %v", r)
				}
			}()
			got, err := Decode([]byte(tc.in))
			if err == nil {
				t.Fatalf("Decode(%q) = %#v, want error", tc.in, got)
			}
		})
	}

	t.Run("nil slice", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode panicked: %v", r)
			}
		}()
		if got, err := Decode(nil); err == nil {
			t.Fatalf("Decode(nil) = %#v, want error", got)
		}
	})
}

// TestPayloadFieldTagsAreSnakeCaseAndUnreserved walks every registered message
// struct and asserts its exported fields carry a snake_case json tag that is not
// one of the envelope-reserved names ("type", "v"), which Encode would silently
// overwrite. This guards the flat-wire contract as later stories add fields.
func TestPayloadFieldTagsAreSnakeCaseAndUnreserved(t *testing.T) {
	tag := regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
	for wire, ctor := range registry {
		st := reflect.TypeOf(ctor()).Elem()
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			if !f.IsExported() {
				continue
			}
			raw, ok := f.Tag.Lookup("json")
			if !ok {
				t.Errorf("%s.%s (wire %q) has no json tag", st.Name(), f.Name, wire)
				continue
			}
			name := strings.Split(raw, ",")[0]
			if name == "" || name == "-" {
				t.Errorf("%s.%s (wire %q) json tag %q has no field name", st.Name(), f.Name, wire, raw)
				continue
			}
			if !tag.MatchString(name) {
				t.Errorf("%s.%s json tag %q is not snake_case", st.Name(), f.Name, name)
			}
			if name == "type" || name == "v" {
				t.Errorf("%s.%s json tag %q collides with an envelope-reserved key", st.Name(), f.Name, name)
			}
		}
	}
}

func TestDecodeUnknownErrorNamesTheType(t *testing.T) {
	_, err := Decode([]byte(`{"type":"bogus","v":1}`))
	if err == nil {
		t.Fatal("want error")
	}
	if !regexp.MustCompile(`bogus`).MatchString(err.Error()) {
		t.Errorf("error %q does not name the unknown type", err)
	}
}

// TestChatMsgInvalidUTF8NormalizesToReplacementChar locks the wire contract for
// ChatMsg.Text: proto.Encode/Decode go through encoding/json, which replaces an
// invalid UTF-8 byte with U+FFFD on both marshal and unmarshal. A relayed chat
// line therefore reaches the peer as valid UTF-8 with the bad bytes swapped for
// the replacement char — never rejected, never a bytes-preserving passthrough.
// (Closes the spec-1-1 deferred item.)
func TestChatMsgInvalidUTF8NormalizesToReplacementChar(t *testing.T) {
	raw, err := Encode(ChatMsg{ClientMsgID: "u1", Text: "ab\x80cd"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	msg, ok := got.(ChatMsg)
	if !ok {
		t.Fatalf("Decode returned %T, want ChatMsg", got)
	}
	if !utf8.ValidString(msg.Text) {
		t.Fatalf("decoded text is not valid UTF-8: %q", msg.Text)
	}
	if strings.ContainsRune(msg.Text, 0x80) {
		t.Fatalf("decoded text still carries the invalid 0x80 byte: %q", msg.Text)
	}
	if !strings.ContainsRune(msg.Text, '�') {
		t.Fatalf("decoded text has no U+FFFD replacement char: %q", msg.Text)
	}
	if msg.ClientMsgID != "u1" {
		t.Fatalf("client_msg_id = %q, want %q", msg.ClientMsgID, "u1")
	}
}

func TestEncodeRejectsUnregistered(t *testing.T) {
	type NotAMessage struct{ X int }
	if _, err := Encode(NotAMessage{X: 1}); err == nil {
		t.Error("Encode(NotAMessage) = nil error, want error")
	}
	if _, err := Encode(nil); err == nil {
		t.Error("Encode(nil) = nil error, want error")
	}
}
