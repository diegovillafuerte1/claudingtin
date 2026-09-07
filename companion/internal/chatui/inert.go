package chatui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// zeroWidthJoiner (U+200D) is the one Unicode format code point inert keeps: it
// is load-bearing inside emoji sequences (family / profession / flag glyphs).
// Every other Cf code point — zero-width space, word joiner, and the bidi
// overrides that enable Trojan-Source-style display spoofing — stays dropped.
const zeroWidthJoiner = '\u200d'

// inert renders a peer- or opener-supplied string as terminal-safe literal text.
//
// The threat model is a terminal pane, not a browser: the danger in remote text
// is escape/control bytes that drive the emulator — colour and cursor moves via
// ESC/CSI, the clipboard via OSC 52, clickable hyperlinks via OSC 8, an audible
// bell via BEL. inert is a whitelist, mirroring the control-byte rule of
// backend/openers.go:
//
//   - graphic runes (letters, marks, numbers, punctuation, symbols, and every
//     Unicode space — NBSP, thin space, …) and '\n' are kept as-is, plus the
//     zero-width joiner so emoji sequences survive intact;
//   - every C0 byte except '\n' (0x00–0x1F, ESC included), DEL (0x7F), and the
//     C1 range (0x80–0x9F) are dropped, so no ANSI/CSI/OSC sequence — every one
//     of which begins with ESC or a C1 introducer — can reach the terminal;
//   - any other non-graphic rune (other Unicode control/format code points,
//     including bidi overrides) is dropped;
//   - an invalid UTF-8 byte becomes U+FFFD.
//
// It does no markdown, HTML, or link interpretation and emits no OSC 8
// hyperlink: markup passes through as the literal characters that were typed.
func inert(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// A byte that is not valid UTF-8.
			b.WriteRune(utf8.RuneError) // U+FFFD
			i++
			continue
		}
		i += size

		switch {
		case r == '\n', r == zeroWidthJoiner:
			b.WriteRune(r)
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// C0 (bar '\n', handled above), DEL, C1 — dropped.
		case !unicode.IsGraphic(r):
			// Any other control/format code point — dropped.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
