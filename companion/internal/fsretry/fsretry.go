//go:build !windows

// Package fsretry wraps the two filesystem calls in the companion that race
// badly on Windows: opening a path while another process renames or replaces it.
//
// Unix lets a file be opened, renamed, and unlinked concurrently without error.
// Windows, for the brief window a MoveFileEx replace is in flight, fails a
// concurrent open of the target with ERROR_SHARING_VIOLATION (and, when a delete
// is pending, ERROR_ACCESS_DENIED). Both the account-key regeneration path and
// the transcript tailer legitimately open a file that a peer process may be
// rotating at that instant, so a bare os.Open there is flaky on Windows.
//
// On every non-Windows OS these are straight passthroughs to the os package;
// the Windows build (fsretry_windows.go) adds a short bounded retry.
package fsretry

import "os"

// Open is os.Open.
func Open(name string) (*os.File, error) { return os.Open(name) }

// Rename is os.Rename.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
