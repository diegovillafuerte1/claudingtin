// Package safety is the companion's local, network-free first-run gate. Before
// the transcript is watched or any websocket is dialed, one plain screen is
// printed to the companion's own stdout: what this is, an honest warning that
// you are put in a room with a stranger and should share nothing identifying,
// how block and report work, and an affirmative "18 or older" prompt. The
// companion then reads one line from its own stdin.
//
// An affirmative line is recorded at <configDir>/safety-ack (mode 0600, written
// via a sibling temp file + rename, exactly like identity.regenerate) and the
// companion connects as usual. Anything else — a different line, a blank line,
// or EOF — is a decline: nothing is recorded, so the screen returns next run.
//
// The acknowledgement file holds only the screen version and an accepted-at
// Unix-millis line. No account key, no other identity, is in scope anywhere in
// this package, and nothing about the acknowledgement is ever sent to the
// backend. Accepted is defensive like identity.Load: a missing, empty,
// malformed, or older-version file is simply "not accepted, no error"; only a
// real read failure is a wrapped error, and it carries no key material.
//
// stdlib only, plus internal/fsretry for the Windows-safe open/rename.
package safety

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/fsretry"
)

// ScreenVersion is the version of the copy in Screen(). It is written into
// safety-ack on accept; Accepted returns true only when the stored version
// equals this. Bump it in the same change as any material copy change so
// already-accepted users see the new screen once.
const ScreenVersion = 1

const (
	// ackFileName is the fixed basename of the acknowledgement file inside
	// configDir.
	ackFileName = "safety-ack"
	// ackFileMode is the permission every write of the acknowledgement file
	// uses.
	ackFileMode = 0o600
	// ackDirMode is the permission a freshly created configDir gets — matches
	// identity's configDirMode.
	ackDirMode = 0o700
	// maxAckFileSize caps how many bytes Accepted reads. The canonical form is
	// well under 64 bytes; anything materially larger is not a file this
	// package wrote, so it is treated as malformed (re-prompt) without a large
	// read.
	maxAckFileSize = 512
)

// Screen returns the full first-run screen. All safety copy lives here. It is
// addressed to a person, warm but never softening the risk, and never names the
// machinery (per _bmad-output/specs/spec-claudingtin/voice.md).
func Screen() string {
	return `here's the deal, before anything connects.

while claude is off thinking, this puts you in a room with one other person
who's also waiting around — a stranger, picked at random. no names, no
profiles, no history. you won't know who they are and they won't know you.

so keep the private stuff private: no real name, no address or city, nothing
about where you work or your money, and no photos of yourself. anything you
type can be screenshotted or quoted somewhere else — write like it might not
stay in the room.

if the other person makes it unpleasant, you can leave whenever you want.
block keeps you from being paired with them again. report hands the last few
messages to a human to look at. neither one sends the other person a notice.

you also have to be 18 or older.

if that's you and you want to go ahead, type  yes  and press enter.
anything else — another answer, a blank line, nothing at all — keeps this
quiet: no one is connected, and you'll see this screen again next time.
`
}

// Outcome is the result of Gate.
type Outcome int

const (
	// OutcomeAccepted: the user affirmed just now and the acknowledgement was
	// recorded.
	OutcomeAccepted Outcome = iota
	// OutcomeAlreadyAccepted: a valid acknowledgement was already on disk; no
	// screen was printed and no input was read.
	OutcomeAlreadyAccepted
	// OutcomeDeclined: no affirmative line (a different line, a blank line, or
	// EOF). Nothing was recorded.
	OutcomeDeclined
	// OutcomeAborted: the context was cancelled while waiting for the line.
	OutcomeAborted
)

// Accepted reports whether <configDir>/safety-ack records acceptance of the
// current screen version. It is deliberately lenient: a missing, empty,
// oversized, malformed, or older-version file is (false, nil). Only a real
// open/read failure (e.g. permission denied, a non-directory in the path) is a
// wrapped error — and no key material is ever in scope to leak into it.
func Accepted(configDir string) (bool, error) {
	path := filepath.Join(configDir, ackFileName)

	// fsretry.Open, not os.Open: on Windows a peer process renaming a freshly
	// written safety-ack over this path makes a bare open fail transiently with
	// ERROR_SHARING_VIOLATION. Mirrors identity.inspect.
	f, err := fsretry.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("safety: open acknowledgement file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxAckFileSize+1))
	if err != nil {
		return false, fmt.Errorf("safety: read acknowledgement file: %w", err)
	}
	if len(data) > maxAckFileSize {
		return false, nil
	}

	v, ok := parseAckVersion(string(data))
	if !ok {
		return false, nil
	}
	return v == ScreenVersion, nil
}

// parseAckVersion scans the two-line acknowledgement body for a `version <n>`
// line and returns n. Any deviation — no such line, an unparseable number —
// yields ok=false, which the caller treats as "not accepted", never an error.
func parseAckVersion(body string) (int, bool) {
	for _, ln := range strings.Split(body, "\n") {
		fields := strings.Fields(ln)
		if len(fields) == 2 && fields[0] == "version" {
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

// Record writes the acknowledgement for the current screen version to
// <configDir>/safety-ack, mode 0600, via a sibling temp file + fsretry.Rename
// so a reader never sees a half-written file and no partial file is left behind
// on failure. The body is two plain lines — the screen version and an
// accepted-at Unix-millis line — and nothing else. Modelled on
// identity.regenerate (companion/internal/identity/identity.go).
func Record(configDir string) error {
	if err := os.MkdirAll(configDir, ackDirMode); err != nil {
		return fmt.Errorf("safety: create config dir: %w", err)
	}
	path := filepath.Join(configDir, ackFileName)
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+ackFileName+"-*")
	if err != nil {
		return fmt.Errorf("safety: create temp acknowledgement file: %w", err)
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	// os.CreateTemp already restricts to 0600 on Unix; this is belt-and-braces
	// and best-effort (Windows permissions are advisory).
	_ = tmp.Chmod(ackFileMode)

	body := fmt.Sprintf("version %d\naccepted_at %d\n", ScreenVersion, time.Now().UnixMilli())
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("safety: write acknowledgement file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("safety: sync acknowledgement file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("safety: close acknowledgement file: %w", err)
	}
	if err := fsretry.Rename(tmpName, path); err != nil {
		return fmt.Errorf("safety: replace acknowledgement file: %w", err)
	}
	removeTmp = false
	return nil
}

// affirmativeLines are the only accepted answers, after TrimSpace + ToLower.
var affirmativeLines = map[string]struct{}{
	"y":                {},
	"yes":              {},
	"i am 18 or older": {},
}

func affirmative(line string) bool {
	_, ok := affirmativeLines[strings.ToLower(strings.TrimSpace(line))]
	return ok
}

// Gate runs the first-run precondition. If the screen is already acknowledged
// it returns OutcomeAlreadyAccepted immediately, having printed nothing and
// read nothing. Otherwise it prints Screen() to out and reads one line from in
// on a goroutine, raced against ctx:
//
//   - an affirmative line ⇒ Record, then OutcomeAccepted (a Record failure is a
//     wrapped error and OutcomeDeclined);
//   - any other line, a blank line, or EOF ⇒ OutcomeDeclined, nothing recorded;
//   - ctx cancelled first ⇒ OutcomeAborted (os.Stdin.Read is not
//     context-cancellable, so the read goroutine leaks until stdin closes —
//     acceptable for a one-shot at a startup that is exiting).
//
// No logging happens here and the account key is never in scope.
func Gate(ctx context.Context, in io.Reader, out io.Writer, configDir string) (Outcome, error) {
	// Already shutting down (e.g. Ctrl-C during identity load): do not dump the
	// consent screen to a process on its way out.
	if ctx.Err() != nil {
		return OutcomeAborted, nil
	}

	ok, err := Accepted(configDir)
	if err != nil {
		return OutcomeDeclined, err
	}
	if ok {
		return OutcomeAlreadyAccepted, nil
	}

	fmt.Fprint(out, Screen())

	lineCh := make(chan string, 1)
	go func() {
		// bufio over the raw reader: one line, ignore the error — a line
		// delivered with io.EOF (no trailing newline) is still the user's
		// answer; an empty read with io.EOF is a decline.
		s, _ := bufio.NewReader(in).ReadString('\n')
		lineCh <- s
	}()

	select {
	case <-ctx.Done():
		return OutcomeAborted, nil
	case line := <-lineCh:
		if !affirmative(line) {
			return OutcomeDeclined, nil
		}
		if err := Record(configDir); err != nil {
			return OutcomeDeclined, err
		}
		return OutcomeAccepted, nil
	}
}
