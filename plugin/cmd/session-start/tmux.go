package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// tmuxSplitTimeout bounds the tmux child. split-window normally returns in
// milliseconds by talking to the already-running server over $TMUX; this cap
// keeps a hung or overloaded server (stuck socket, slow update-environment)
// from stalling the SessionStart hook toward its 10s budget. When it elapses
// the call returns a non-nil error and the caller falls back to the detached
// spawn — the intended degradation.
const tmuxSplitTimeout = 3 * time.Second

// tmuxSplit runs `tmux <args...>` as a child, with every stdio stream pointed at
// os.DevNull, and waits for it to return — but no longer than tmuxSplitTimeout.
//
// A missing tmux binary (exec.LookPath fails — the case on Windows and in any
// shell with no tmux), a non-zero tmux exit (dead server, bad target), or the
// timeout elapsing all come back as an error, and the caller falls back to the
// detached spawn. No SysProcAttr: the new pane is owned by the tmux server,
// never a child of this launcher.
func tmuxSplit(args []string) error {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxSplitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tmux, args...)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	return cmd.Run()
}
