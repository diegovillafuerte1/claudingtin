//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedProcess is Windows' DETACHED_PROCESS creation flag. It is a local
// const so the plugin module needs no golang.org/x/sys dependency.
const detachedProcess = 0x00000008

// detachedSpawn starts bin with args as a detached, non-blocking child: its
// stdio goes to NUL and the CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS
// creation flags give it its own process group with no inherited console, so it
// outlives this launcher and the SessionStart hook. It calls Start and returns
// immediately — never Wait.
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

	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}

	return cmd.Start()
}
