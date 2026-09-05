package transcript

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Structural one-liners for the watcher tests. No conversation content.
const (
	wMode    = `{"type":"mode","mode":"","sessionId":"S"}`
	wPrompt  = `{"type":"user","isSidechain":false,"message":{"role":"user","content":""},"uuid":"u1","timestamp":"2026-09-05T16:45:00.000Z","origin":{"kind":"human"}}`
	wToolUse = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","id":"m1","content":[{"type":"tool_use"}],"stop_reason":"tool_use"}}`
	wEndTurn = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","id":"m2","content":[{"type":"text"}],"stop_reason":"end_turn"},"timestamp":"2026-09-05T16:45:05.000Z"}`
)

// shrinkTimings makes the poll backstop and the reopen retry fast so the
// watcher tests run in well under a second. Tests here never run in parallel,
// so mutating the package vars is safe.
func shrinkTimings(t *testing.T) {
	t.Helper()
	op, or, oa := pollInterval, reopenInterval, reopenAttempts
	pollInterval = 15 * time.Millisecond
	reopenInterval = 15 * time.Millisecond
	reopenAttempts = 40
	t.Cleanup(func() {
		pollInterval, reopenInterval, reopenAttempts = op, or, oa
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		appendRaw(t, path, l+"\n")
	}
}

func startWatch(t *testing.T, path string) *Watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w, err := Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return w
}

func wantEvent(t *testing.T, w *Watcher, kind EventKind) {
	t.Helper()
	select {
	case e, ok := <-w.Events:
		if !ok {
			t.Fatalf("Events closed while waiting for %v", kind)
		}
		if e.Kind != kind {
			t.Fatalf("event = %v, want %v", e.Kind, kind)
		}
	case err := <-w.Errors:
		t.Fatalf("unexpected error while waiting for %v: %v", kind, err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %v", kind)
	}
}

func wantNoEvent(t *testing.T, w *Watcher, within time.Duration) {
	t.Helper()
	select {
	case e, ok := <-w.Events:
		if !ok {
			t.Fatal("Events closed while expecting quiet (watcher died)")
		}
		t.Fatalf("unexpected event %v", e.Kind)
	case err := <-w.Errors:
		t.Fatalf("unexpected error while expecting quiet: %v", err)
	case <-time.After(within):
	}
}

func TestWatcherTailsAppends(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n")

	w := startWatch(t, path)

	appendLines(t, path, wPrompt)
	wantEvent(t, w, TurnStart)

	appendLines(t, path, wToolUse) // a tool call must not produce an event
	wantNoEvent(t, w, 100*time.Millisecond)

	appendLines(t, path, wEndTurn)
	wantEvent(t, w, TurnEnd)
}

// The fsnotify path must deliver on its own, without the poll backstop: push
// pollInterval out of reach and still expect the event promptly.
func TestWatcherFsnotifyWithoutPoll(t *testing.T) {
	shrinkTimings(t)
	pollInterval = 30 * time.Second // effectively disabled for this test
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n")

	w := startWatch(t, path)

	appendLines(t, path, wPrompt)
	select {
	case e, ok := <-w.Events:
		if !ok {
			t.Fatal("Events closed")
		}
		if e.Kind != TurnStart {
			t.Fatalf("event = %v, want TurnStart", e.Kind)
		}
	case err := <-w.Errors:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("fsnotify did not deliver TurnStart within 1s (poll backstop disabled)")
	}
}

// Watch must accept a path whose file does not exist yet, then pick it up once
// it appears.
func TestWatcherFileAppearsLater(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	w := startWatch(t, path) // no file on disk yet

	writeFile(t, path, wMode+"\n")
	appendLines(t, path, wPrompt)
	wantEvent(t, w, TurnStart)
}

func TestWatcherBuffersPartialLine(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, "")

	w := startWatch(t, path)

	// Write the prompt line with no terminating newline: incomplete, must buffer.
	appendRaw(t, path, wPrompt)
	wantNoEvent(t, w, 150*time.Millisecond)

	// The newline arrives; the line completes and fires.
	appendRaw(t, path, "\n")
	wantEvent(t, w, TurnStart)
}

func TestWatcherRecoversFromTruncation(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n")

	w := startWatch(t, path)

	appendLines(t, path, wPrompt, wEndTurn)
	wantEvent(t, w, TurnStart)
	wantEvent(t, w, TurnEnd)

	// Shrink the file below the read offset: a rewrite/truncation.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	appendLines(t, path, wPrompt, wEndTurn)
	wantEvent(t, w, TurnStart)
	wantEvent(t, w, TurnEnd)
}

func TestWatcherRecoversFromRotation(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n")

	w := startWatch(t, path)
	appendLines(t, path, wPrompt, wEndTurn)
	wantEvent(t, w, TurnStart)
	wantEvent(t, w, TurnEnd)

	// Atomic-rename style rotation: move the file aside, drop a fresh one in.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	writeFile(t, path, wMode+"\n"+wPrompt+"\n"+wEndTurn+"\n")

	wantEvent(t, w, TurnStart)
	wantEvent(t, w, TurnEnd)
}

func TestWatcherRotationExhaustedReportsError(t *testing.T) {
	shrinkTimings(t)
	reopenAttempts = 8 // ~120ms before giving up
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n"+wPrompt+"\n")

	w := startWatch(t, path)
	wantEvent(t, w, TurnStart)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	select {
	case err, ok := <-w.Errors:
		if !ok {
			t.Fatal("Errors closed without delivering the give-up error")
		}
		if err == nil {
			t.Fatal("nil error on Errors")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the give-up error")
	}

	// The watcher stops: Events must close.
	select {
	case _, ok := <-w.Events:
		if ok {
			t.Fatal("Events still open after the watcher gave up")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Events not closed after the watcher gave up")
	}
}

func TestWatcherContextCancelClosesChannels(t *testing.T) {
	shrinkTimings(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeFile(t, path, wMode+"\n")

	ctx, cancel := context.WithCancel(context.Background())
	w, err := Watch(ctx, path)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	cancel()

	select {
	case _, ok := <-w.Events:
		if ok {
			t.Fatal("Events delivered a value after cancel; want closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Events not closed after context cancel")
	}
}

func TestWatchMissingDirectoryFails(t *testing.T) {
	_, err := Watch(context.Background(), filepath.Join(t.TempDir(), "nope", "session.jsonl"))
	if err == nil {
		t.Fatal("Watch on a missing parent directory should fail")
	}
}
