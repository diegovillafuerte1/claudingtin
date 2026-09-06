//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDetachedSpawn exercises the real detachedSpawn (every other test injects a
// fake spawn or a shell stub, so a regression to Run/Wait or a dropped detach
// would otherwise ship green and hang the hook until its timeout). It checks
// that the call returns immediately (Start, not Wait), returns no error for a
// child that starts fine, and — on unix — puts the child in its own session /
// process group.
func TestDetachedSpawn(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	pgidFile := filepath.Join(dir, "pgid")
	// Record the child's own pid and process-group id, then linger a moment so
	// it is still observable (not yet reaped) while the assertions run.
	script := "echo $$ > " + pidFile + "; ps -o pgid= -p $$ > " + pgidFile + "; sleep 2"

	start := time.Now()
	if err := detachedSpawn(sh, []string{"-c", script}); err != nil {
		t.Fatalf("detachedSpawn returned an error for a child that starts fine: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("detachedSpawn blocked for %v — it must Start the child, never Wait on it", elapsed)
	}

	read := func(p string) string {
		t.Helper()
		for i := 0; i < 200; i++ {
			if b, err := os.ReadFile(p); err == nil {
				if s := strings.TrimSpace(string(b)); s != "" {
					return s
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("child never wrote %s", filepath.Base(p))
		return ""
	}

	childPID, err := strconv.Atoi(read(pidFile))
	if err != nil {
		t.Fatalf("child pid unparseable: %v", err)
	}
	childPGID, err := strconv.Atoi(read(pgidFile))
	if err != nil {
		t.Skipf("`ps -o pgid=` output unparseable (%v) — cannot assert session detachment", err)
	}

	if childPGID != childPID {
		t.Errorf("child pgid %d != child pid %d — Setsid did not make the child a new session/group leader", childPGID, childPID)
	}
	selfPGID, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid(self): %v", err)
	}
	if childPGID == selfPGID {
		t.Errorf("child shares the test's process group %d — it was not detached", selfPGID)
	}
}
