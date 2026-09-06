//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedSpawn starts bin with args as a detached, non-blocking child: its
// stdio goes to /dev/null and Setsid puts it in a new session so it outlives
// this launcher and the SessionStart hook. It calls Start and returns
// immediately — never Wait — so the launcher stays well under its time budget.
func detachedSpawn(bin string, args []string) error {
	cmd := exec.Command(bin, args...)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	return cmd.Start()
}
