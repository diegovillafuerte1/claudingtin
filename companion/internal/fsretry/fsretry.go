//go:build !windows

// Package fsretry wraps the filesystem calls in the companion that race badly on
// Windows: opening or publishing a path while another process renames, links, or
// replaces it.
//
// Unix lets a file be opened, renamed, linked, and unlinked concurrently without
// error. Windows, for the brief window a MoveFileEx replace or a CreateHardLink
// is in flight, fails a concurrent open of the target with
// ERROR_SHARING_VIOLATION (and, when a delete is pending, ERROR_ACCESS_DENIED).
// Both the account-key create/regenerate paths and the transcript tailer
// legitimately touch a file that a peer process may be rotating at that instant,
// so a bare os call there is flaky on Windows.
//
// On every non-Windows OS these are straight passthroughs to the os package;
// the Windows build (fsretry_windows.go) adds a short bounded retry.
package fsretry

import "os"

// Open is os.Open.
func Open(name string) (*os.File, error) { return os.Open(name) }

// Rename is os.Rename.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Link is os.Link.
func Link(oldname, newname string) error { return os.Link(oldname, newname) }
