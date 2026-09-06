package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// buildLauncherFixture compiles the real ./cmd/session-start into a fresh
// fixture plugin tree and returns the module dir, the plugin root, and its
// bin/<os>-<arch> dir. It applies the same platform guards as the other
// end-to-end tests. Mirrors TestLauncherEndToEndExitsZero's setup.
func buildLauncherFixture(t *testing.T) (moduleDir, root, binDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh wrapper is not exercised on Windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	moduleDir, err = filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	binDir = filepath.Join(root, "bin", archTriple())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(goBin, "build", "-o", filepath.Join(binDir, "session-start"), "./cmd/session-start")
	build.Dir = moduleDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/session-start: %v\n%s", err, out)
	}
	return moduleDir, root, binDir
}

// writeTmuxStub drops <dir>/tmux as an executable shell script that writes each
// argv element on its own line to recordFile (so empty args survive as blank
// lines) and exits with exitCode. dir is meant to be prepended to $PATH so the
// real tmuxSplit's exec.LookPath("tmux") resolves to it.
func writeTmuxStub(t *testing.T, dir, recordFile string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + recordFile + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	writeExecutable(t, filepath.Join(dir, "tmux"), script)
}

// readTmuxArgv reads back the argv the stub recorded. printf '%s\n' appends one
// trailing newline past the final arg, so exactly one trailing "" is dropped;
// the two empty companion args are preserved.
func readTmuxArgv(t *testing.T, recordFile string) []string {
	t.Helper()
	data, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatalf("tmux stub recorded nothing: %v", err)
	}
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestLauncherEndToEndTmuxSplit drives the real compiled launcher through
// hooks/session-start.sh with a recording `tmux` stub on $PATH and $TMUX set,
// exercising the production tmuxSplit seam end to end.
func TestLauncherEndToEndTmuxSplit(t *testing.T) {
	moduleDir, root, binDir := buildLauncherFixture(t)
	wrapper := filepath.Join(moduleDir, "hooks", "session-start.sh")

	// A companion stub that starts and exits at once, so the manual fallback
	// (when it fires) reaches the success-only hint line.
	companion := filepath.Join(binDir, companionName())
	writeExecutable(t, companion, "#!/bin/sh\nexit 0\n")
	companionSuffix := filepath.Join("bin", archTriple(), companionName())

	const transcript = "/tmp/t.jsonl"

	run := func(t *testing.T, stubExit int, extraEnv ...string) (stdout string, argvFile string) {
		t.Helper()
		stubDir := t.TempDir()
		record := filepath.Join(t.TempDir(), "argv")
		writeTmuxStub(t, stubDir, record, stubExit)

		cmd := exec.Command("sh", wrapper)
		cmd.Env = append(os.Environ(),
			"CLAUDE_PLUGIN_ROOT="+root,
			"TMPDIR="+t.TempDir(),
			"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		cmd.Env = append(cmd.Env, extraEnv...)
		cmd.Stdin = strings.NewReader(`{"session_id":"e2etmux","transcript_path":"` + transcript + `"}`)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("wrapper exited non-zero: %v (stderr %q)", err, errb.String())
		}
		return out.String(), record
	}

	t.Run("tmux present: adjacent side-by-side no-focus split, empty stdout", func(t *testing.T) {
		stdout, argvFile := run(t, 0,
			"TMUX=/tmp/tmux-1000/default,900,0",
			"TMUX_PANE=%3",
		)
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty on the tmux path", stdout)
		}
		argv := readTmuxArgv(t, argvFile)
		if len(argv) < 5 || argv[0] != "split-window" {
			t.Fatalf("tmux argv = %#v, want it to start with split-window", argv)
		}
		if !slices.Contains(argv, "-h") || !slices.Contains(argv, "-d") {
			t.Fatalf("tmux argv %#v missing -h/-d (side-by-side, no focus change)", argv)
		}
		if i := slices.Index(argv, "-t"); i < 0 || i+1 >= len(argv) || argv[i+1] != "%3" {
			t.Fatalf("tmux argv %#v does not target $TMUX_PANE via -t %%3", argv)
		}
		tail := argv[len(argv)-5:]
		if tail[0] != "--" || !strings.HasSuffix(tail[1], companionSuffix) ||
			tail[2] != transcript || tail[3] != "" || tail[4] != "" {
			t.Fatalf("tmux argv tail = %#v, want [-- <companion> %q \"\" \"\"]", tail, transcript)
		}
	})

	t.Run("tmux set, TMUX_PANE unset: -t omitted entirely", func(t *testing.T) {
		stdout, argvFile := run(t, 0, "TMUX=/tmp/tmux-1000/default,900,0")
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty on the tmux path", stdout)
		}
		argv := readTmuxArgv(t, argvFile)
		if slices.Contains(argv, "-t") {
			t.Fatalf("tmux argv %#v carries -t with no $TMUX_PANE set", argv)
		}
		if argv[0] != "split-window" || !slices.Contains(argv, "-h") || !slices.Contains(argv, "-d") {
			t.Fatalf("tmux argv %#v is not a well-formed split-window", argv)
		}
		tail := argv[len(argv)-4:]
		if !strings.HasSuffix(tail[0], companionSuffix) || tail[1] != transcript || tail[2] != "" || tail[3] != "" {
			t.Fatalf("tmux argv tail = %#v, want [<companion> %q \"\" \"\"]", tail, transcript)
		}
	})

	t.Run("tmux exits non-zero: manual fallback prints exactly one hint line", func(t *testing.T) {
		stdout, argvFile := run(t, 1,
			"TMUX=/tmp/tmux-1000/default,900,0",
			"TMUX_PANE=%3",
		)
		if _, err := os.Stat(argvFile); err != nil {
			t.Fatalf("tmux stub was not invoked before the fallback: %v", err)
		}
		line := strings.TrimRight(stdout, "\n")
		if line == "" || strings.Contains(line, "\n") {
			t.Fatalf("stdout = %q, want exactly one non-empty line", stdout)
		}
		if !strings.Contains(line, companion) || !strings.Contains(line, transcript+` "" ""`) {
			t.Fatalf("fallback hint %q does not name the companion invocation", line)
		}
		if strings.Contains(stdout, "e2etmux") {
			t.Fatalf("fallback hint %q leaks the session id", stdout)
		}
	})
}
