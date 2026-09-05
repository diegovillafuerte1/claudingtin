package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Tuning knobs for the tailer. Package-level vars, not consts, only so the tests
// can shrink the timings; nothing outside this package touches them.
var (
	// pollInterval is a backstop stat/read done regardless of fsnotify, so a
	// missed or coalesced filesystem event still gets picked up (and so the
	// tailer behaves the same on platforms where fsnotify is less precise).
	pollInterval = time.Second
	// reopenInterval and reopenAttempts bound the wait for a rotated-away
	// transcript to reappear: ~5s total before giving up.
	reopenInterval = 200 * time.Millisecond
	reopenAttempts = 25
)

// eventBuffer is the depth of the outbound Events channel.
const eventBuffer = 64

// errGone is the internal signal that the transcript path does not currently
// exist (truncation's cousin: rotation). It triggers the bounded reopen loop.
var errGone = errors.New("transcript: path does not exist")

// Watcher tails a transcript file and publishes turn boundaries.
//
// Events is closed when the watcher stops (context cancelled, or the file was
// rotated away and did not come back within the bounded retry window). A fatal
// stop always delivers its one error on Errors before both channels close, so a
// consumer can tell a failure from a clean context-cancel stop (which delivers
// nothing). Transient read errors are delivered on Errors on a best-effort
// basis — never blocking the tail loop — and do not stop the watcher.
type Watcher struct {
	Events <-chan Event
	Errors <-chan error
}

// Watch starts tailing the transcript at path. The file itself need not exist
// yet, but its parent directory must. The returned Watcher's goroutine runs
// until ctx is cancelled or the file is lost past recovery.
func Watch(ctx context.Context, path string) (*Watcher, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("transcript: resolve path: %w", err)
	}
	dir := filepath.Dir(abs)
	// Canonicalise the directory so the path we compare fsnotify events against
	// matches what fsnotify reports (macOS temp dirs, for one, are symlinks).
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	abs = filepath.Join(dir, filepath.Base(abs))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("transcript: watch dir %s: not an accessible directory", dir)
	}
	// The file need not exist yet, but if something is already there it must be a
	// regular file — os.Open on a directory succeeds and then every read errors.
	if fi, err := os.Stat(abs); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("transcript: %s is not a regular file", abs)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("transcript: new fsnotify watcher: %w", err)
	}
	// Watch the directory, not the file: a watch on the file goes deaf the
	// moment it is renamed or removed, but the directory keeps reporting the
	// REMOVE / RENAME / CREATE that a rotation is made of.
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("transcript: watch %s: %w", dir, err)
	}

	events := make(chan Event, eventBuffer)
	errs := make(chan error, 1)
	t := &tailer{
		ctx:            ctx,
		path:           abs,
		fsw:            fsw,
		parser:         NewParser(),
		events:         events,
		errs:           errs,
		pollInterval:   pollInterval,
		reopenInterval: reopenInterval,
		reopenAttempts: reopenAttempts,
	}
	go t.run()

	return &Watcher{Events: events, Errors: errs}, nil
}

// tailer is the running state of one Watch call.
type tailer struct {
	ctx    context.Context
	path   string
	fsw    *fsnotify.Watcher
	parser *Parser

	// Timings snapshotted at Watch time so the run goroutine never reads the
	// package-level vars the tests mutate.
	pollInterval   time.Duration
	reopenInterval time.Duration
	reopenAttempts int

	offset  int64  // bytes of the file already consumed
	partial []byte // bytes read past the last newline, awaiting the rest of the line

	events chan<- Event
	errs   chan<- error
}

func (t *tailer) run() {
	defer close(t.events)
	defer close(t.errs)
	defer t.fsw.Close()

	// Seed from whatever is already in the file.
	if err := t.readAppend(); err != nil && !errors.Is(err, errGone) {
		t.emitErr(err)
	}

	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.ctx.Done():
			return

		case ev, ok := <-t.fsw.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != t.path {
				continue // some other file in the directory
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				if !t.reopen() {
					return // retries exhausted; error already emitted
				}
				continue
			}
			// Create / Write / Chmod: read whatever is new. readAppend also
			// detects and recovers from truncation on its own.
			if err := t.readAppend(); err != nil {
				if errors.Is(err, errGone) {
					if !t.reopen() {
						return
					}
					continue
				}
				t.emitErr(err)
			}

		case err, ok := <-t.fsw.Errors:
			if !ok {
				return
			}
			if err != nil {
				t.emitErr(fmt.Errorf("transcript: fsnotify: %w", err))
			}

		case <-ticker.C:
			// Backstop: catch anything fsnotify did not deliver.
			if err := t.readAppend(); err != nil {
				if errors.Is(err, errGone) {
					if !t.reopen() {
						return
					}
					continue
				}
				t.emitErr(err)
			}
		}
	}
}

// readAppend opens the file, recovers from truncation (on-disk size below the
// read offset → re-read from 0 with a fresh parser), reads every byte past the
// offset, and feeds each complete line to the parser. A missing file returns
// errGone. Any other open/read error is returned as-is and is treated as
// transient by the caller.
func (t *tailer) readAppend() error {
	f, err := os.Open(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errGone
		}
		return fmt.Errorf("transcript: open %s: %w", t.path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("transcript: stat %s: %w", t.path, err)
	}
	size := fi.Size()

	if size < t.offset {
		// Truncation / rewrite: start over. A fresh parser avoids carrying a
		// half-finished turn from content that is no longer on disk.
		t.reset()
	}
	if size == t.offset {
		return nil // nothing new
	}

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return fmt.Errorf("transcript: seek %s: %w", t.path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("transcript: read %s: %w", t.path, err)
	}
	t.offset += int64(len(data))
	t.consume(data)
	return nil
}

// consume appends data to the partial-line buffer and feeds every complete line
// to the parser, leaving any trailing partial line buffered for next time. It
// scans with a cursor and re-slices the buffer once, so seeding from a large
// transcript is linear, not quadratic.
func (t *tailer) consume(data []byte) {
	t.partial = append(t.partial, data...)
	start := 0
	for {
		i := bytes.IndexByte(t.partial[start:], '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSuffix(t.partial[start:start+i], []byte("\r"))
		for _, e := range t.parser.Feed(line) {
			t.emit(e)
		}
		start += i + 1
	}
	if start > 0 {
		// Drop the consumed prefix in one shot; keep the trailing partial line
		// detached from the buffer we keep appending to.
		rest := make([]byte, len(t.partial)-start)
		copy(rest, t.partial[start:])
		t.partial = rest
	}
}

// reopen waits, with a bounded retry, for a rotated-away transcript to reappear.
// On success it resets and re-reads the new file and returns true. If the file
// does not come back in time it emits one error and returns false, telling run
// to stop.
func (t *tailer) reopen() bool {
	for attempt := 0; attempt < t.reopenAttempts; attempt++ {
		select {
		case <-t.ctx.Done():
			return false
		case <-time.After(t.reopenInterval):
		}
		if _, err := os.Stat(t.path); err == nil {
			t.reset()
			if err := t.readAppend(); err != nil && !errors.Is(err, errGone) {
				t.emitErr(err)
			}
			return true
		}
	}
	// Fatal: the file never came back. This error must reach the consumer
	// before run() closes the channels, so send it blocking (until ctx is
	// done), never best-effort.
	t.emitFatal(fmt.Errorf("transcript: gave up re-opening %s after %d attempts (~%s)",
		t.path, t.reopenAttempts, time.Duration(t.reopenAttempts)*t.reopenInterval))
	return false
}

// reset drops all read state and starts the parser over.
func (t *tailer) reset() {
	t.offset = 0
	t.partial = t.partial[:0]
	*t.parser = Parser{}
}

func (t *tailer) emit(e Event) {
	select {
	case t.events <- e:
	case <-t.ctx.Done():
	}
}

// emitErr delivers a transient error best-effort: if nobody is draining the
// depth-1 Errors channel it is dropped rather than stalling the tail loop.
func (t *tailer) emitErr(err error) {
	select {
	case t.errs <- err:
	case <-t.ctx.Done():
	default:
	}
}

// emitFatal delivers the error that is about to stop the watcher. Unlike
// emitErr it never drops: it blocks until the consumer takes the error (past
// any undrained transient error) or the context is cancelled.
func (t *tailer) emitFatal(err error) {
	select {
	case t.errs <- err:
	case <-t.ctx.Done():
	}
}
