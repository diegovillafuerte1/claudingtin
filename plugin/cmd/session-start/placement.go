package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// This file is the pane-placement strategy registry (Epic 2 retrospective
// finding F10). Story 1.8 shipped one placement — a tmux split-window — and a
// printed hint for everything else. tmux is not special: any terminal
// multiplexer with a scriptable "split a pane and run this" CLI can place the
// companion beside the Claude session with a real PTY, which also means its
// first-run safety gate works instead of reading EOF and going inert.
//
// launch() still tries tmux first (that path is unchanged). When it is not
// inside tmux — or the tmux split failed — it calls placeInPane, which walks
// this registry: the first strategy whose environment variable is present gets
// one attempt, and any failure falls through to the detached-spawn + hint
// fallback, exactly as before. Each strategy talks to an already-running mux
// server over that env var, so the call returns in milliseconds; the timeout
// only guards a wedged server.
//
// Not covered here and left for a follow-up: iTerm2 (needs AppleScript / the
// Python API and an automation-permission prompt) and the plain-terminal
// windowed fallback (open the companion in a new OS window so a bare terminal /
// editor-integrated terminal also gets a PTY — the remaining half of Epic 1
// finding F7).

// muxPlacementTimeout bounds one placement child. Like tmuxSplitTimeout it is a
// backstop for a hung mux server, not the expected latency (which is a local
// socket round-trip).
const muxPlacementTimeout = 3 * time.Second

// muxStrategy is one scriptable multiplexer. env is the variable whose presence
// means "the launcher is running inside this multiplexer and can drive it";
// argv builds the full command line (tool + its args) that splits a pane and
// runs the companion. companionArgs is [transcriptPath, "", ""].
type muxStrategy struct {
	name string
	env  string
	argv func(getenv func(string) string, bin string, companionArgs []string) []string
}

// muxStrategies is the ordered registry placeInPane walks. It is a package var
// so a test can swap it. tmux is deliberately absent — launch() keeps its own
// first-class tmux branch ahead of this. Order is by rough popularity; in
// practice at most one env var is set.
var muxStrategies = []muxStrategy{
	{
		name: "wezterm",
		env:  "WEZTERM_PANE",
		argv: func(_ func(string) string, bin string, a []string) []string {
			return append([]string{"wezterm", "cli", "split-pane", "--right", "--percent", "40", "--", bin}, a...)
		},
	},
	{
		name: "zellij",
		env:  "ZELLIJ",
		argv: func(_ func(string) string, bin string, a []string) []string {
			return append([]string{"zellij", "action", "new-pane", "--direction", "right", "--", bin}, a...)
		},
	},
	{
		// KITTY_LISTEN_ON (not KITTY_WINDOW_ID) is the right gate: it is set only
		// when remote control is enabled and listening, which `kitty @` needs.
		name: "kitty",
		env:  "KITTY_LISTEN_ON",
		argv: func(_ func(string) string, bin string, a []string) []string {
			return append([]string{"kitty", "@", "launch", "--type=window", "--location=vsplit", "--dont-take-focus", "--cwd=current", "--", bin}, a...)
		},
	},
	{
		name: "wt", // Windows Terminal
		env:  "WT_SESSION",
		argv: func(_ func(string) string, bin string, a []string) []string {
			return append([]string{"wt", "-w", "0", "split-pane", bin}, a...)
		},
	},
}

// placePane is the seam launch() calls. Production points it at placeInPane; a
// test swaps it to assert the launcher wires placement ahead of the detached
// spawn without driving a real multiplexer.
var placePane = placeInPane

// placeInPane tries the first registered strategy whose env var is set. It
// returns true only when that strategy's command exits 0 (the companion is now
// in an adjacent pane); a missing tool, a non-zero exit, or the timeout returns
// false and the caller falls back to the detached spawn. A strategy that is
// detected but fails is not retried against the others — you are in one
// multiplexer, not several.
func placeInPane(getenv func(string) string, bin string, companionArgs []string) bool {
	for _, s := range muxStrategies {
		if strings.TrimSpace(getenv(s.env)) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), muxPlacementTimeout)
		err := runPlacement(ctx, s.argv(getenv, bin, companionArgs))
		cancel()
		return err == nil
	}
	return false
}

// runPlacement is the seam placeInPane uses to run a strategy's command. A test
// swaps it to record the argv and choose the outcome without a real binary.
var runPlacement = runPlacementTool

// runPlacementTool runs argv[0] with argv[1:] as a child with every stdio stream
// pointed at os.DevNull, waiting no longer than the context deadline. A missing
// binary (exec.LookPath fails), a non-zero exit, or the deadline all come back
// as an error. No SysProcAttr: the new pane belongs to the mux server, never to
// this launcher. (Parallel to tmux.go's tmuxSplit, kept separate so that
// battle-tested path is untouched.)
func runPlacementTool(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return exec.ErrNotFound
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, argv[1:]...)

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
