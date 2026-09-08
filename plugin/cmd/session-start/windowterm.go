package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// This file is the new-OS-window placement fallback (Epic 2 retrospective
// finding F10, action item 17 — the second half; the first half is the
// muxStrategies registry in placement.go). Story 1.8 shipped a tmux split and a
// printed hint for everything else; placement.go added the other scriptable
// multiplexers. A user in a bare terminal or a VS Code / JetBrains integrated
// terminal still fell through to the Story 1.7 detached spawn, which wires the
// companion's stdin to /dev/null — so safety.Gate reads EOF, declines, and the
// companion is permanently inert (Epic 1 finding F7).
//
// launch() calls openWindow between placePane and the detached spawn. It opens
// the companion in a brand-new OS terminal window — macOS Terminal via a
// self-deleting .command script, the first probed Linux terminal emulator, or
// `cmd /c start` on Windows. A window gives the companion its own PTY, so the
// first-run 18+/safety screen is reachable. No emulator, CLAUDINGTIN_NO_WINDOW
// set, or a failed attempt falls through to the unchanged detached spawn + hint.
//
// Grace model. Emulators split into "spawn the window then exit" (open,
// gnome-terminal, cmd start, konsole) and "stay attached until the window
// closes" (xterm -e, alacritty -e, kitty). runWindowTool handles both blindly:
// Start(), then select a Wait() goroutine against time.After(windowSpawnGrace).
// Exited within the grace => return Wait's error (a non-zero exit means bad
// flags / no server, so the caller falls through). Still running after the
// grace => the window is up => return nil and leave it; main calls os.Exit(0)
// moments later, so the orphaned Wait goroutine and unreaped child are harmless.
// Unlike the tmux / mux rungs — which bound the child with a 3s deadline that
// kills it and only count exit 0 as success — this rung's success is a liveness
// heuristic (still alive after windowSpawnGrace == "window is up") and the child
// is never killed.
//
// No SysProcAttr. detachedSpawn needs Setsid because it *is* the long-lived
// companion; here the child is the terminal emulator, which owns its own
// session/window. Adding Setsid would mean per-OS build-tagged files for no
// established need.

// windowSpawnGrace bounds a window child: a non-zero exit within it is a
// failure (fall through); still running after it is success (the window is up).
// A package var so tests can shrink it.
var windowSpawnGrace = 700 * time.Millisecond

// openWindow is the seam launch() calls, between placePane and the detached
// spawn. Production points it at openInWindow; a test swaps it to assert the
// launcher wires the window rung ahead of the spawn without opening a real
// terminal window.
var openWindow = openInWindow

// windowSupported reports whether opening a new OS terminal window is worth
// attempting. It is false when CLAUDINGTIN_NO_WINDOW is truthy (same parse as
// the CLAUDINGTIN_DISABLE opt-out); otherwise true on darwin and windows, and
// on linux / other only when $DISPLAY or $WAYLAND_DISPLAY is non-blank (no
// display server means no window to open).
//
// This switches on runtime.GOOS; keep the cases in sync with windowArgv.
func windowSupported(getenv func(string) string) bool {
	if truthy(getenv(noWindowEnvVar)) {
		return false
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return strings.TrimSpace(getenv("DISPLAY")) != "" ||
			strings.TrimSpace(getenv("WAYLAND_DISPLAY")) != ""
	}
}

// linuxTerm is one Linux terminal emulator: the binary name (also the PATH
// probe key) and the flag sequence that means "run this command line in the
// window". kitty takes the command with no flag.
type linuxTerm struct {
	name string
	flag []string
}

// linuxTerms is the ordered probe list. The first whose binary is on PATH gets
// the one attempt; there is no retry across the rest (F10 boundary).
var linuxTerms = []linuxTerm{
	{"gnome-terminal", []string{"--"}},
	{"konsole", []string{"-e"}},
	{"xfce4-terminal", []string{"-x"}},
	{"alacritty", []string{"-e"}},
	{"kitty", nil},
	{"xterm", []string{"-e"}},
	{"x-terminal-emulator", []string{"-e"}},
}

// argv builds the full command line: <name> <flag...> <bin> <companionArgs...>.
func (t linuxTerm) argv(bin string, companionArgs []string) []string {
	out := append([]string{t.name}, t.flag...)
	out = append(out, bin)
	return append(out, companionArgs...)
}

// shellSingleQuote wraps s in a /bin/sh single-quoted string, rewriting each
// embedded single quote as close-quote, escaped-quote, reopen-quote. The result
// is inert: no dollar sign, backtick, backslash, or newline inside is
// interpreted. Used to build the darwin .command script so a transcript path
// containing shell metacharacters cannot break or inject it.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// darwinScript is the body of the self-deleting .command file Terminal.app runs
// on open: a #!/bin/sh script that removes itself first, then execs the
// companion with each value single-quoted. A .command file is opened by
// Terminal.app with a real PTY, and `open -a Terminal x.command` raises no
// Automation (TCC) prompt because no Apple Event leaves this process.
func darwinScript(bin string, companionArgs []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("rm -f \"$0\"\n")
	b.WriteString("exec")
	for _, a := range append([]string{bin}, companionArgs...) {
		b.WriteByte(' ')
		b.WriteString(shellSingleQuote(a))
	}
	b.WriteString("\n")
	return b.String()
}

// windowsWindowArgv is `cmd /c start "" "<bin>" <companionArgs...>` per the
// frozen I/O matrix — the empty string is start's window-title argument. (See
// windowterm.go's Design Notes in the spec for the review-flagged, unverified
// risk that cmd.exe collapses the trailing empty args.)
func windowsWindowArgv(bin string, companionArgs []string) []string {
	return append([]string{"cmd", "/c", "start", "", bin}, companionArgs...)
}

// linuxWindowArgv returns the argv for the first emulator in linuxTerms that
// lookPath resolves, or exec.ErrNotFound when none is present. lookPath is
// injected (production passes exec.LookPath) so the first-present selection is
// testable on any host.
func linuxWindowArgv(lookPath func(string) (string, error), bin string, companionArgs []string) ([]string, error) {
	for _, t := range linuxTerms {
		if _, err := lookPath(t.name); err == nil {
			return t.argv(bin, companionArgs), nil
		}
	}
	return nil, exec.ErrNotFound
}

// windowArgv builds the per-OS command line that opens the companion in a new
// window and, on darwin, an optional cleanup that removes the .command script
// (called by openInWindow when the open attempt fails, since the script's own
// `rm -f "$0"` only runs if Terminal actually executes it).
//
// This switches on runtime.GOOS; keep the cases in sync with windowSupported.
func windowArgv(tempDir, sessionID, bin string, companionArgs []string) (argv []string, cleanup func(), err error) {
	switch runtime.GOOS {
	case "darwin":
		script := filepath.Join(tempDir, lockPrefix+sanitizeID(sessionID)+".command")
		// O_EXCL like the sibling acquire(): never follow a symlink, never
		// clobber a predictable path in a shared temp dir. Already-exists (or
		// any other error) => fall through to the detached spawn.
		f, oerr := os.OpenFile(script, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if oerr != nil {
			return nil, nil, oerr
		}
		_, werr := f.WriteString(darwinScript(bin, companionArgs))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(script)
			if werr != nil {
				return nil, nil, werr
			}
			return nil, nil, cerr
		}
		return []string{"open", "-a", "Terminal", script}, func() { _ = os.Remove(script) }, nil
	case "windows":
		return windowsWindowArgv(bin, companionArgs), nil, nil
	default:
		a, lerr := linuxWindowArgv(exec.LookPath, bin, companionArgs)
		return a, nil, lerr
	}
}

// openInWindow is the production openWindow. It declines fast when
// windowSupported is false, builds the per-OS argv, and runs it through
// runWindow. A true return means the window is up (launch() returns nil and
// writes nothing); false means the caller falls through to the detached spawn —
// and on darwin the just-written .command script is removed first.
func openInWindow(getenv func(string) string, tempDir, sessionID, bin string, companionArgs []string) bool {
	if !windowSupported(getenv) {
		return false
	}
	argv, cleanup, err := windowArgv(tempDir, sessionID, bin, companionArgs)
	if err != nil {
		return false
	}
	if runWindow(argv) != nil {
		if cleanup != nil {
			cleanup()
		}
		return false
	}
	return true
}

// runWindow is the seam openInWindow uses to run the window command. A test
// swaps it to record the argv and choose the outcome without a real emulator.
var runWindow = runWindowTool

// runWindowTool runs argv[0] with argv[1:] as a child, every stdio stream
// pointed at os.DevNull, and applies the grace model: a nil/empty argv or a
// missing binary is an immediate error; otherwise Start(), then race a Wait()
// goroutine against windowSpawnGrace — the child exiting first returns its
// error (nil on a clean exit 0), the grace elapsing first returns nil (the
// window is up, and the child is left running). No SysProcAttr: the window
// belongs to the emulator, not this launcher. Parallel to placement.go's
// runPlacementTool and tmux.go's tmuxSplit, kept separate so those paths are
// untouched.
func runWindowTool(argv []string) error {
	if len(argv) == 0 {
		return exec.ErrNotFound
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, argv[1:]...)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(windowSpawnGrace):
		return nil
	}
}
