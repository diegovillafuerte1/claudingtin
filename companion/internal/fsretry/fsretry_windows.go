//go:build windows

// Package fsretry wraps the filesystem calls in the companion that race badly on
// Windows: opening or publishing a path while another process renames, links, or
// replaces it. See fsretry.go for the rationale. This build adds a bounded retry
// — retryAttempts attempts spaced retryInterval apart, ~200ms worst case — on
// exactly the two transient errors a concurrent MoveFileEx replace or
// CreateHardLink on the target produces. Any other error (including a genuine,
// persistent ERROR_ACCESS_DENIED, and ERROR_ALREADY_EXISTS from a lost link
// race) is returned on the first try after the window, so the wrappers never
// mask a real failure, only paper over the rename/link race.
package fsretry

import (
	"errors"
	"os"
	"syscall"
	"time"
)

const (
	retryAttempts = 20
	retryInterval = 10 * time.Millisecond

	// errSharingViolation is Win32 ERROR_SHARING_VIOLATION. The standard
	// syscall package defines ERROR_ACCESS_DENIED but not this one, and it is a
	// fixed, documented Win32 error code — safe to name directly.
	errSharingViolation = syscall.Errno(32)
)

// racyReplace reports whether err is one Windows returns transiently while a
// concurrent process renames or deletes the path being opened.
func racyReplace(err error) bool {
	return errors.Is(err, errSharingViolation) ||
		errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}

// Open is os.Open with a bounded retry while a peer's rename/replace of name is
// in flight.
func Open(name string) (*os.File, error) {
	for attempt := 0; ; attempt++ {
		f, err := os.Open(name)
		if err == nil || attempt == retryAttempts-1 || !racyReplace(err) {
			return f, err
		}
		time.Sleep(retryInterval)
	}
}

// Rename is os.Rename with the same bounded retry: the replace target may be
// momentarily open in a peer process.
func Rename(oldpath, newpath string) error {
	for attempt := 0; ; attempt++ {
		err := os.Rename(oldpath, newpath)
		if err == nil || attempt == retryAttempts-1 || !racyReplace(err) {
			return err
		}
		time.Sleep(retryInterval)
	}
}

// Link is os.Link with the same bounded retry: a peer may be renaming or
// linking over newname at the instant we publish. A lost link race surfaces as
// ERROR_ALREADY_EXISTS, which racyReplace excludes, so it returns immediately
// for the caller to handle as os.ErrExist.
func Link(oldname, newname string) error {
	for attempt := 0; ; attempt++ {
		err := os.Link(oldname, newname)
		if err == nil || attempt == retryAttempts-1 || !racyReplace(err) {
			return err
		}
		time.Sleep(retryInterval)
	}
}
