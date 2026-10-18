package proto

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// update regenerates testdata/fixtures from the current encoder:
//
//	(cd proto && go test . -run TestGoldenFixtures -update)
var update = flag.Bool("update", false, "rewrite the golden JSON fixtures in testdata/fixtures")

const fixturesDir = "testdata/fixtures"

// eventFixtureSeq is the seq stamped into the event.json fixture: one entry of a
// GET /v1/events response list.
const eventFixtureSeq = 7

// goldenFixtures is the committed wire contract the mod's TypeScript types are
// checked against (AD-3): one <type>.json per registered message, encoded from
// roundTripSamples, plus event.json for the seq'd long-poll event form. Keys are
// sorted (encoding/json map order), so the bytes are deterministic.
func goldenFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, sample := range roundTripSamples {
		raw, err := Encode(sample)
		if err != nil {
			t.Fatalf("Encode(%T): %v", sample, err)
		}
		out[discriminatorByType[reflect.TypeOf(sample)]+".json"] = append(raw, '\n')
	}
	raw, err := EncodeEvent(eventFixtureSeq, ChatMsg{ClientMsgID: "cmid-42", Text: "hi there"})
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	out["event.json"] = append(raw, '\n')
	return out
}

func TestGoldenFixtures(t *testing.T) {
	want := goldenFixtures(t)
	if *update {
		if err := os.RemoveAll(fixturesDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(fixturesDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, b := range want {
			if err := os.WriteFile(filepath.Join(fixturesDir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("read %s (run with -update to create it): %v", fixturesDir, err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		onDisk[e.Name()] = true
		if _, ok := want[e.Name()]; !ok {
			t.Errorf("stale fixture %s has no registered message type (run with -update)", e.Name())
		}
	}
	for name, b := range want {
		if !onDisk[name] {
			t.Errorf("missing fixture %s (run with -update)", name)
			continue
		}
		got, err := os.ReadFile(filepath.Join(fixturesDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, b) {
			t.Errorf("fixture %s is stale (run with -update):\n got  %s want %s", name, got, b)
		}
	}
}

func TestEncodeEventIsFlatWithSeq(t *testing.T) {
	raw, err := EncodeEvent(42, Matched{SessionID: "s-1", Opener: "hey"})
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["seq"] != float64(42) || m["type"] != TypeMatched || m["v"] != float64(PROTOCOL_VERSION) || m["session_id"] != "s-1" {
		t.Errorf("event = %s, want flat {seq:42,type:matched,v,...payload}", raw)
	}
	// The event still decodes as its message; the extra seq key is ignored.
	got, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != (Matched{SessionID: "s-1", Opener: "hey"}) {
		t.Errorf("Decode = %#v", got)
	}
	if _, err := EncodeEvent(1, struct{}{}); err == nil {
		t.Error("EncodeEvent(unregistered) = nil error, want error")
	}
}
