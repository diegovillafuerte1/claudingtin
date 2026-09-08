// Package backend is the module root. It exists so that backend/openers.txt —
// the curated conversation-opener set contributors can extend — can be embedded
// with //go:embed, which cannot reach a parent directory. Openers parses and
// validates the file once behind a sync.Once; the hub goroutine holds the
// in-memory rotation cursor that stamps one opener into every matched frame
// (Story 2.2).
package backend

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed openers.txt
var openersRaw string

// openerMinLen and openerMaxLen bound every entry's length after trimming.
const (
	openerMinLen = 8
	openerMaxLen = 120
)

// parseOpeners strips one leading UTF-8 BOM, splits raw on '\n', drops blank
// lines and lines whose first non-whitespace character is '#', trims each
// remaining line, and validates it: valid UTF-8, no C0/DEL/C1 control
// characters, length (in runes) in [openerMinLen, openerMaxLen], and no
// duplicate of an earlier entry. It requires at least two entries. On any
// violation it returns a descriptive error naming the offending entry; the
// embedded file is a build-time artifact, so a caller treats that error as fatal.
func parseOpeners(raw string) ([]string, error) {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	var openers []string
	seen := make(map[string]int)
	for i, line := range strings.Split(raw, "\n") {
		lineNo := i + 1
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		if !utf8.ValidString(entry) {
			return nil, fmt.Errorf("openers: line %d is not valid UTF-8: %q", lineNo, entry)
		}
		for _, r := range entry {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				return nil, fmt.Errorf("openers: line %d %q contains a control byte %#U", lineNo, entry, r)
			}
		}
		if n := utf8.RuneCountInString(entry); n < openerMinLen || n > openerMaxLen {
			return nil, fmt.Errorf("openers: line %d %q is %d characters, want %d-%d", lineNo, entry, n, openerMinLen, openerMaxLen)
		}
		if prev, dup := seen[entry]; dup {
			return nil, fmt.Errorf("openers: line %d %q duplicates the entry on line %d", lineNo, entry, prev)
		}
		seen[entry] = lineNo
		openers = append(openers, entry)
	}
	if len(openers) < 2 {
		return nil, fmt.Errorf("openers: need at least 2 entries, parsed %d", len(openers))
	}
	return openers, nil
}

// LoadOpeners parses and validates the embedded opener set and returns the
// entries or a descriptive error. It is the non-panicking form of Openers, for
// backend serve's main to call at startup: a malformed set then fails with one
// structured log line before anything is listening, instead of surfacing later
// as an unrecovered panic from inside the hub goroutine (Epic 2 retro F8).
func LoadOpeners() ([]string, error) {
	return parseOpeners(openersRaw)
}

var (
	openersOnce sync.Once
	openersSet  []string
)

// Openers returns the curated opener set embedded from backend/openers.txt. It
// parses and validates the file exactly once. A malformed file is a repository
// bug the format test catches in CI; if one somehow reaches production, Openers
// panics rather than returning a partial or empty set, so `backend serve` fails
// at startup instead of pairing people with a blank or garbage opener. The
// returned slice is a fresh copy the caller may mutate freely.
func Openers() []string {
	openersOnce.Do(func() {
		set, err := parseOpeners(openersRaw)
		if err != nil {
			panic(err)
		}
		openersSet = set
	})
	out := make([]string, len(openersSet))
	copy(out, openersSet)
	return out
}
