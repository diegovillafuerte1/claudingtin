package backend

import (
	"fmt"
	"strings"
	"testing"
)

// TestEmbeddedOpenersParse: the real embedded file parses without error and
// yields >= 2 entries. Calling parseOpeners directly (rather than Openers())
// means a contributor's bad line surfaces as the descriptive, entry-naming
// error instead of an opaque panic.
func TestEmbeddedOpenersParse(t *testing.T) {
	got, err := parseOpeners(openersRaw)
	if err != nil {
		t.Fatalf("embedded openers.txt does not parse: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("embedded openers.txt yields %d entries, want >= 2", len(got))
	}
}

// TestOpenersReturnsCopy: a caller mutating the returned slice must not affect
// the shared set handed to the next caller.
func TestOpenersReturnsCopy(t *testing.T) {
	a := Openers()
	if len(a) == 0 {
		t.Fatal("Openers() returned nothing")
	}
	a[0] = "MUTATED"
	if b := Openers(); b[0] == "MUTATED" {
		t.Fatal("Openers() shares its backing array between calls")
	}
}

func TestParseOpeners(t *testing.T) {
	const valid = "what made you laugh today?"

	t.Run("ignores blank and comment lines and trims entries", func(t *testing.T) {
		raw := "# header line\n\n  what made you laugh today?  \n\n   # indented comment\nwhat are you avoiding right now?\n"
		got, err := parseOpeners(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"what made you laugh today?", "what are you avoiding right now?"}
		if len(got) != len(want) {
			t.Fatalf("got %d entries %q, want %q", len(got), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("strips a leading UTF-8 BOM", func(t *testing.T) {
		raw := "\uFEFF" + valid + "\nwhat are you avoiding right now?\n"
		got, err := parseOpeners(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0] != valid {
			t.Fatalf("first entry = %q, want %q (BOM not stripped)", got[0], valid)
		}
	})

	t.Run("accepts an entry of exactly openerMinLen runes", func(t *testing.T) {
		min := strings.Repeat("a", openerMinLen)
		got, err := parseOpeners(valid + "\n" + min + "\n")
		if err != nil {
			t.Fatalf("unexpected error for a %d-rune entry: %v", openerMinLen, err)
		}
		if len(got) != 2 || got[1] != min {
			t.Fatalf("got %q, want the %d-rune entry accepted", got, openerMinLen)
		}
	})

	t.Run("rejects an entry of openerMinLen-1 runes", func(t *testing.T) {
		short := strings.Repeat("a", openerMinLen-1)
		_, err := parseOpeners(valid + "\n" + short + "\n")
		if err == nil || !strings.Contains(err.Error(), short) {
			t.Fatalf("want an error naming %q, got %v", short, err)
		}
	})

	t.Run("accepts an entry of exactly openerMaxLen runes", func(t *testing.T) {
		max := strings.Repeat("a", openerMaxLen)
		got, err := parseOpeners(valid + "\n" + max + "\n")
		if err != nil {
			t.Fatalf("unexpected error for a %d-rune entry: %v", openerMaxLen, err)
		}
		if len(got) != 2 || got[1] != max {
			t.Fatalf("got %d entries, want the %d-rune entry accepted", len(got), openerMaxLen)
		}
	})

	t.Run("rejects a too-short entry and names it", func(t *testing.T) {
		_, err := parseOpeners(valid + "\nhey\n")
		if err == nil || !strings.Contains(err.Error(), "hey") {
			t.Fatalf("want an error naming \"hey\", got %v", err)
		}
	})

	t.Run("rejects a too-long entry", func(t *testing.T) {
		long := strings.Repeat("x", openerMaxLen+1)
		_, err := parseOpeners(valid + "\n" + long + "\n")
		if err == nil || !strings.Contains(err.Error(), long) {
			t.Fatalf("want an error naming the over-long entry, got %v", err)
		}
	})

	t.Run("rejects an entry with a control byte and names it", func(t *testing.T) {
		bad := "what\tabout\ttab characters?"
		_, err := parseOpeners(valid + "\n" + bad + "\n")
		// The error quotes the entry with %q, so control bytes appear escaped.
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", bad)) {
			t.Fatalf("want an error naming %q, got %v", bad, err)
		}
	})

	t.Run("rejects DEL and C1 control characters", func(t *testing.T) {
		// \x7f is DEL; \u0085 (NEL) is a C1 control -- both valid UTF-8 and
		// neither a C0 byte, so they exercise the widened check specifically.
		for name, bad := range map[string]string{
			"DEL": "a question with \x7f in it?",
			"C1":  "a question with \u0085 in it?",
		} {
			if _, err := parseOpeners(valid + "\n" + bad + "\n"); err == nil {
				t.Fatalf("want an error for the %s control character, got nil", name)
			}
		}
	})

	t.Run("rejects a duplicate entry", func(t *testing.T) {
		_, err := parseOpeners(valid + "\n" + valid + "\n")
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("want a duplicate error, got %v", err)
		}
	})

	t.Run("rejects a zero-entry input", func(t *testing.T) {
		if _, err := parseOpeners("# only a comment\n\n\n"); err == nil {
			t.Fatal("want an error for 0 entries, got nil")
		}
	})

	t.Run("rejects a one-entry input", func(t *testing.T) {
		if _, err := parseOpeners(valid + "\n"); err == nil {
			t.Fatal("want an error for 1 entry, got nil")
		}
	})
}
