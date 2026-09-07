package chatui

import (
	"strings"
	"testing"
)

func TestInert(t *testing.T) {
	const fffd = "�"
	c1 := string(rune(0x9b)) // U+009B, a C1 control, as valid 2-byte UTF-8

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "hello world", "hello world"},
		{"newline preserved", "line one\nline two", "line one\nline two"},
		{"multi-byte runes kept", "cafe ☕ 日本語 \U0001F642", "cafe ☕ 日本語 \U0001F642"},
		{"CSI / SGR colour", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"bare ESC", "a\x1bb", "ab"},
		{"BEL", "ding\x07 dong", "ding dong"},
		{"DEL", "x\x7fy", "xy"},
		{"C1 code point U+009B (valid UTF-8)", "x" + c1 + "y", "xy"},
		{"tab is a C0 byte", "a\tb", "ab"},
		{"carriage return dropped", "a\r\nb", "a\nb"},
		{"OSC 8 hyperlink, ST-terminated", "\x1b]8;;http://x\x1b\\click\x1b]8;;\x1b\\", "]8;;http://x\\click]8;;\\"},
		{"OSC 8 hyperlink, BEL-terminated", "\x1b]8;;http://x\x07link", "]8;;http://xlink"},
		{"OSC 52 clipboard", "\x1b]52;c;Zm9v\x07", "]52;c;Zm9v"},
		{"HTML passes through literally", "<b>bold</b> & <script>x</script>", "<b>bold</b> & <script>x</script>"},
		{"markdown passes through literally", "[link](http://x) **bold** `code`", "[link](http://x) **bold** `code`"},
		{"invalid UTF-8 byte becomes U+FFFD", "a\xffb", "a" + fffd + "b"},
		{"lone continuation byte becomes U+FFFD", "a\x80b", "a" + fffd + "b"},
		{"ZWJ emoji sequence survives intact", "\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"variation selector VS16 survives", "\u2764\uFE0F", "\u2764\uFE0F"},
		{"skin-tone modifier survives", "\U0001F44B\U0001F3FF", "\U0001F44B\U0001F3FF"},
		{"non-breaking space survives", "a\u00A0b", "a\u00A0b"},
		{"thin space survives", "a\u2009b", "a\u2009b"},
		{"zero-width space is dropped", "a\u200Bb", "ab"},
		{"word joiner is dropped", "a\u2060b", "ab"},
		{"bidi override RLO/PDF is dropped", "a\u202Eb\u202Cc", "abc"},
		{"ZWJ next to a control byte keeps only the ZWJ", "a\u200d\x1bb", "a\u200db"},
		{"empty stays empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inert(tc.in)
			if got != tc.want {
				t.Fatalf("inert(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsRune(got, 0x1b) {
				t.Fatalf("inert(%q) = %q still contains a raw ESC byte", tc.in, got)
			}
			for _, r := range got {
				if r == '\n' {
					continue
				}
				if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
					t.Fatalf("inert(%q) = %q contains control rune %#U", tc.in, got, r)
				}
			}
		})
	}
}

// TestInertNeutralizesEveryC0ByteExceptNewline is the exhaustive guarantee: for
// every byte 0x00-0x1F other than '\n', inert removes it entirely.
func TestInertNeutralizesEveryC0ByteExceptNewline(t *testing.T) {
	for b := 0; b < 0x20; b++ {
		if b == '\n' {
			continue
		}
		in := "a" + string(rune(b)) + "b"
		if got := inert(in); got != "ab" {
			t.Fatalf("inert(%q) = %q, want %q (C0 byte %#x not neutralized)", in, got, "ab", b)
		}
	}
}
