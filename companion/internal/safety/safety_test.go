package safety

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// uuidLike catches any account-key-shaped string leaking into the screen or the
// acknowledgement file. No key is ever in scope in this package; these assert it
// stays that way.
var uuidLike = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func TestScreenContent(t *testing.T) {
	s := Screen()
	low := strings.ToLower(s)

	// Required elements: what it is / strangers, the identifying-info and
	// screenshot warning, block, report, and the affirmative 18+ prompt.
	for _, want := range []string{"stranger", "18", "block", "report", "screenshot", "yes"} {
		if !strings.Contains(low, want) {
			t.Errorf("Screen() is missing the %q element:\n%s", want, s)
		}
	}

	// Never names the machinery.
	for _, forbidden := range []string{"queue", "disconnect", "websocket", "fifo", "relay"} {
		if strings.Contains(low, forbidden) {
			t.Errorf("Screen() names the machinery (%q):\n%s", forbidden, s)
		}
	}

	if uuidLike.MatchString(s) {
		t.Errorf("Screen() contains key-shaped material:\n%s", s)
	}
}

func TestScreenVersionIsOne(t *testing.T) {
	if ScreenVersion != 1 {
		t.Fatalf("ScreenVersion = %d, want 1", ScreenVersion)
	}
}

func writeAck(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ackFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write ack: %v", err)
	}
}

func TestAccepted(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		ok, err := Accepted(t.TempDir())
		if ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("current version", func(t *testing.T) {
		dir := t.TempDir()
		writeAck(t, dir, "version 1\naccepted_at 1730000000000\n")
		ok, err := Accepted(dir)
		if !ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("older version re-prompts", func(t *testing.T) {
		dir := t.TempDir()
		writeAck(t, dir, "version 0\naccepted_at 1730000000000\n")
		ok, err := Accepted(dir)
		if ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		writeAck(t, dir, "")
		ok, err := Accepted(dir)
		if ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("corrupt body", func(t *testing.T) {
		dir := t.TempDir()
		writeAck(t, dir, "this is not the file you are looking for\nversion banana\n")
		ok, err := Accepted(dir)
		if ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("oversized file", func(t *testing.T) {
		dir := t.TempDir()
		writeAck(t, dir, "version 1\n"+strings.Repeat("x", maxAckFileSize+10)+"\n")
		ok, err := Accepted(dir)
		if ok || err != nil {
			t.Fatalf("Accepted = (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("unreadable path is a wrapped error", func(t *testing.T) {
		// A configDir whose parent path component is a regular file: the open
		// fails with ENOTDIR — not os.ErrNotExist — so Accepted returns a
		// wrapped error. Works regardless of euid (no chmod needed).
		tmp := t.TempDir()
		notADir := filepath.Join(tmp, "file")
		if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		ok, err := Accepted(filepath.Join(notADir, "cfg"))
		if err == nil {
			t.Fatalf("Accepted = (%v, nil), want a wrapped error", ok)
		}
		if ok {
			t.Errorf("Accepted returned true alongside an error")
		}
		if !strings.Contains(err.Error(), "safety:") {
			t.Errorf("error %q is not wrapped", err)
		}
		if uuidLike.MatchString(err.Error()) {
			t.Errorf("error carries key-shaped material: %v", err)
		}
	})
}

func TestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()

	before := time.Now().UnixMilli()
	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	after := time.Now().UnixMilli()

	ok, err := Accepted(dir)
	if !ok || err != nil {
		t.Fatalf("Accepted after Record = (%v, %v), want (true, nil)", ok, err)
	}

	path := filepath.Join(dir, ackFileName)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat ack: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("ack mode = %v, want 0600", fi.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "version 1\n") {
		t.Errorf("ack body missing the version line:\n%s", body)
	}
	v, ok := parseAckVersion(body)
	if !ok || v != ScreenVersion {
		t.Errorf("parseAckVersion(%q) = (%d, %v), want (%d, true)", body, v, ok, ScreenVersion)
	}
	if uuidLike.MatchString(body) {
		t.Errorf("ack body contains key-shaped material:\n%s", body)
	}

	// The accepted-at line is present and plausibly now.
	var acceptedAt int64
	for _, ln := range strings.Split(body, "\n") {
		fields := strings.Fields(ln)
		if len(fields) == 2 && fields[0] == "accepted_at" {
			acceptedAt, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if acceptedAt < before || acceptedAt > after {
		t.Errorf("accepted_at = %d, want within [%d, %d]", acceptedAt, before, after)
	}
}

func TestGateAccept(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"yes newline", "yes\n"},
		{"y newline", "y\n"},
		{"phrase", "i am 18 or older\n"},
		{"padded mixed case", "   YES \n"},
		{"phrase mixed case no newline", "I Am 18 Or Older"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var out strings.Builder
			outcome, err := Gate(context.Background(), strings.NewReader(tc.in), &out, dir)
			if err != nil {
				t.Fatalf("Gate: %v", err)
			}
			if outcome != OutcomeAccepted {
				t.Fatalf("outcome = %v, want OutcomeAccepted", outcome)
			}
			if !strings.Contains(out.String(), "18 or older") {
				t.Errorf("screen was not printed:\n%s", out.String())
			}
			if ok, _ := Accepted(dir); !ok {
				t.Errorf("acceptance was not recorded")
			}
		})
	}
}

func TestGateDecline(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"a different line", "no\n"},
		{"blank line", "\n"},
		{"immediate EOF", ""},
		{"yeah is not yes", "yeah\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var out strings.Builder
			outcome, err := Gate(context.Background(), strings.NewReader(tc.in), &out, dir)
			if err != nil {
				t.Fatalf("Gate: %v", err)
			}
			if outcome != OutcomeDeclined {
				t.Fatalf("outcome = %v, want OutcomeDeclined", outcome)
			}
			if !strings.Contains(out.String(), "18 or older") {
				t.Errorf("screen should still be printed on a decline:\n%s", out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, ackFileName)); !os.IsNotExist(err) {
				t.Errorf("safety-ack was created on a decline: stat err = %v", err)
			}
		})
	}
}

func TestGateAlreadyAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var out strings.Builder
	// An input that would be a decline if it were read — it must not be.
	outcome, err := Gate(context.Background(), strings.NewReader("no\n"), &out, dir)
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if outcome != OutcomeAlreadyAccepted {
		t.Fatalf("outcome = %v, want OutcomeAlreadyAccepted", outcome)
	}
	if out.String() != "" {
		t.Errorf("nothing should be written to out when already accepted, got:\n%s", out.String())
	}
}

func TestGateAbortOnContextCancel(t *testing.T) {
	dir := t.TempDir()

	// A pipe whose write end is never written: the read goroutine blocks, so
	// only ctx cancellation can end Gate.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Outcome, 1)
	var out strings.Builder
	go func() {
		o, err := Gate(ctx, pr, &out, dir)
		if err != nil {
			t.Errorf("Gate returned error on abort: %v", err)
		}
		done <- o
	}()

	// Give Gate time to print the screen and park on the read, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case o := <-done:
		if o != OutcomeAborted {
			t.Fatalf("outcome = %v, want OutcomeAborted", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Gate did not return after context cancel")
	}
	if _, err := os.Stat(filepath.Join(dir, ackFileName)); !os.IsNotExist(err) {
		t.Errorf("safety-ack created on an aborted gate")
	}
}

func TestGateAbortWhenContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out strings.Builder
	outcome, err := Gate(ctx, strings.NewReader("yes\n"), &out, t.TempDir())
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if outcome != OutcomeAborted {
		t.Fatalf("outcome = %v, want OutcomeAborted", outcome)
	}
	if out.String() != "" {
		t.Errorf("the screen was printed to a shutting-down process:\n%s", out.String())
	}
}

func TestGateUnreadableConfigDirIsAnError(t *testing.T) {
	// A configDir whose parent path component is a regular file: the Accepted
	// read fails with ENOTDIR (not os.ErrNotExist) and Gate surfaces a wrapped
	// error.
	tmp := t.TempDir()
	notADir := filepath.Join(tmp, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out strings.Builder
	_, err := Gate(context.Background(), strings.NewReader("yes\n"), &out, filepath.Join(notADir, "cfg"))
	if err == nil {
		t.Fatal("Gate returned a nil error for an unreadable configDir")
	}
	if !strings.Contains(err.Error(), "safety:") {
		t.Errorf("error %q is not safety:-wrapped", err)
	}
	if uuidLike.MatchString(err.Error()) {
		t.Errorf("error carries key-shaped material: %v", err)
	}
}

func TestGateRecordFailureIsDeclinedWithError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0500 does not block writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission")
	}
	// A readable but non-writable configDir: Accepted sees no file (false, nil),
	// the screen prints, the affirmative line is read — and Record then fails to
	// create its temp file.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var out strings.Builder
	outcome, err := Gate(context.Background(), strings.NewReader("yes\n"), &out, dir)
	if err == nil {
		t.Fatalf("Gate = (%v, nil), want a non-nil error when Record cannot write", outcome)
	}
	if outcome != OutcomeDeclined {
		t.Errorf("outcome = %v, want OutcomeDeclined on a Record failure", outcome)
	}
	if !strings.Contains(err.Error(), "safety:") {
		t.Errorf("error %q is not safety:-wrapped", err)
	}
	if uuidLike.MatchString(err.Error()) {
		t.Errorf("error carries key-shaped material: %v", err)
	}
}
