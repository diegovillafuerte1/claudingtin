package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestWindowSupported covers the knob (truthy / falsey / unset) against the
// display env (set / unset), interpreted for the host GOOS: darwin and windows
// always support a window unless the knob forbids it; every other GOOS needs a
// display server.
func TestWindowSupported(t *testing.T) {
	graphical := runtime.GOOS == "darwin" || runtime.GOOS == "windows"

	// Knob truthy always wins, display or not.
	for _, v := range []string{"1", "true", "YES", " on ", "On"} {
		if windowSupported(envFrom(map[string]string{noWindowEnvVar: v})) {
			t.Errorf("windowSupported(%s=%q) = true, want false", noWindowEnvVar, v)
		}
		if windowSupported(envFrom(map[string]string{noWindowEnvVar: v, "DISPLAY": ":0"})) {
			t.Errorf("windowSupported(%s=%q, DISPLAY set) = true, want false", noWindowEnvVar, v)
		}
	}

	// Knob unset / falsey, no display: graphical hosts yes, others no.
	for _, m := range []map[string]string{nil, {noWindowEnvVar: "0"}, {noWindowEnvVar: "false"}, {noWindowEnvVar: ""}} {
		if got := windowSupported(envFrom(m)); got != graphical {
			t.Errorf("windowSupported(%v, no display) on %s = %v, want %v", m, runtime.GOOS, got, graphical)
		}
	}

	// A display server makes it true on every GOOS (knob still unset).
	if !windowSupported(envFrom(map[string]string{"DISPLAY": ":0"})) {
		t.Errorf("windowSupported($DISPLAY set) = false, want true")
	}
	if !windowSupported(envFrom(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})) {
		t.Errorf("windowSupported($WAYLAND_DISPLAY set) = false, want true")
	}
	if windowSupported(envFrom(map[string]string{"DISPLAY": "  ", "WAYLAND_DISPLAY": " "})) != graphical {
		t.Errorf("windowSupported(blank display vars) on %s should match the no-display case", runtime.GOOS)
	}
}

// TestLinuxTermArgv pins the exact command line each probe-list emulator builds.
func TestLinuxTermArgv(t *testing.T) {
	const bin = "/plugin/bin/linux-amd64/companion"
	args := []string{"/t.jsonl", "", ""}

	want := map[string][]string{
		"gnome-terminal":      {"gnome-terminal", "--", bin, "/t.jsonl", "", ""},
		"konsole":             {"konsole", "-e", bin, "/t.jsonl", "", ""},
		"xfce4-terminal":      {"xfce4-terminal", "-x", bin, "/t.jsonl", "", ""},
		"alacritty":           {"alacritty", "-e", bin, "/t.jsonl", "", ""},
		"kitty":               {"kitty", bin, "/t.jsonl", "", ""},
		"xterm":               {"xterm", "-e", bin, "/t.jsonl", "", ""},
		"x-terminal-emulator": {"x-terminal-emulator", "-e", bin, "/t.jsonl", "", ""},
	}

	seen := map[string]bool{}
	for _, term := range linuxTerms {
		seen[term.name] = true
		got := term.argv(bin, args)
		if !reflect.DeepEqual(got, want[term.name]) {
			t.Errorf("%s argv =\n  %#v\nwant\n  %#v", term.name, got, want[term.name])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("probe list is missing %q", name)
		}
	}
}

// TestLinuxWindowArgv exercises the first-present selection over an injected
// PATH probe, independent of the host's installed terminals.
func TestLinuxWindowArgv(t *testing.T) {
	const bin = "/plugin/bin/linux-amd64/companion"
	args := []string{"/t.jsonl", "", ""}

	// First entry (gnome-terminal) absent, second (konsole) present: konsole wins.
	present := map[string]bool{"konsole": true, "xterm": true}
	lookPath := func(name string) (string, error) {
		if present[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	got, err := linuxWindowArgv(lookPath, bin, args)
	if err != nil {
		t.Fatalf("linuxWindowArgv: unexpected error %v", err)
	}
	want := []string{"konsole", "-e", bin, "/t.jsonl", "", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("linuxWindowArgv picked %#v, want the first present entry %#v", got, want)
	}

	// None present: exec.ErrNotFound, nil argv.
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	if argv, err := linuxWindowArgv(none, bin, args); argv != nil || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("linuxWindowArgv(no emulator) = (%#v, %v), want (nil, exec.ErrNotFound)", argv, err)
	}
}

// TestWindowsWindowArgv pins the `cmd /c start "" <bin> <args>` command line
// from the frozen I/O matrix.
func TestWindowsWindowArgv(t *testing.T) {
	const bin = `C:\plugin\bin\windows-amd64\companion.exe`
	got := windowsWindowArgv(bin, []string{"/t.jsonl", "", ""})
	want := []string{"cmd", "/c", "start", "", bin, "/t.jsonl", "", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windowsWindowArgv =\n  %#v\nwant\n  %#v", got, want)
	}
}

// TestDarwinScript pins the self-deleting .command body and proves shell
// metacharacters in the paths stay inert (single-quoted).
func TestDarwinScript(t *testing.T) {
	const bin = "/plugin/bin/darwin-arm64/companion"
	got := darwinScript(bin, []string{"/tmp/t.jsonl", "", ""})
	want := "#!/bin/sh\nrm -f \"$0\"\nexec '" + bin + "' '/tmp/t.jsonl' '' ''\n"
	if got != want {
		t.Fatalf("darwinScript =\n%q\nwant\n%q", got, want)
	}

	// A transcript path full of shell metacharacters: every one must land
	// inside single quotes, with embedded ' rendered as '\''.
	nasty := "/tmp/$(touch pwned)`id`\"; rm -rf ~ #'x"
	got = darwinScript("/bin/companion", []string{nasty, "", ""})
	wantArg := "'/tmp/$(touch pwned)`id`\"; rm -rf ~ #'\\''x'"
	if wantArg != "'"+strings.ReplaceAll(nasty, "'", `'\''`)+"'" {
		t.Fatalf("test's own escape expectation is wrong: %q", wantArg)
	}
	wantLine := "#!/bin/sh\nrm -f \"$0\"\nexec '/bin/companion' " + wantArg + " '' ''\n"
	if got != wantLine {
		t.Fatalf("darwinScript(metachar path) =\n%q\nwant\n%q", got, wantLine)
	}
}

// TestOpenInWindowDarwin: on darwin, openInWindow writes the 0700 .command
// script into tempDir (named with the sanitised session id) and asks runWindow
// to run `open -a Terminal <script>`.
func TestOpenInWindowDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin .command script path")
	}
	prev := runWindow
	var gotArgv []string
	runWindow = func(argv []string) error { gotArgv = argv; return nil }
	t.Cleanup(func() { runWindow = prev })

	dir := t.TempDir()
	const bin = "/plugin/bin/darwin-arm64/companion"
	args := []string{"/tmp/t.jsonl", "", ""}

	if !openInWindow(envFrom(nil), dir, "sess/1", bin, args) {
		t.Fatal("openInWindow = false, want true (runWindow stubbed to succeed)")
	}

	wantScript := filepath.Join(dir, "claudingtin-sess_1.command")
	want := []string{"open", "-a", "Terminal", wantScript}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Fatalf("runWindow argv = %#v, want %#v", gotArgv, want)
	}

	body, err := os.ReadFile(wantScript)
	if err != nil {
		t.Fatalf("script not written: %v", err)
	}
	if string(body) != darwinScript(bin, args) {
		t.Fatalf("script body =\n%q\nwant\n%q", body, darwinScript(bin, args))
	}
	info, err := os.Stat(wantScript)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("script mode = %v, want -rwx------", info.Mode().Perm())
	}
}

// TestOpenInWindowDarwinCleansUpOnFailure: when runWindow reports an error the
// launcher falls through, and the just-written .command script (which never got
// a chance to rm -f "$0" itself) is removed.
func TestOpenInWindowDarwinCleansUpOnFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin .command script path")
	}
	prev := runWindow
	runWindow = func([]string) error { return errors.New("open failed") }
	t.Cleanup(func() { runWindow = prev })

	dir := t.TempDir()
	if openInWindow(envFrom(nil), dir, "s1", "/bin/companion", []string{"/t.jsonl", "", ""}) {
		t.Fatal("openInWindow = true despite runWindow error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("script left behind after a failed open: %v", entries)
	}
}

// TestOpenInWindowDarwinExclusive: a pre-existing script at the predictable path
// makes the O_EXCL create fail, so openInWindow declines (fall through) rather
// than clobbering it.
func TestOpenInWindowDarwinExclusive(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin .command script path")
	}
	prev := runWindow
	runWindow = func([]string) error { t.Fatal("runWindow called despite the O_EXCL clash"); return nil }
	t.Cleanup(func() { runWindow = prev })

	dir := t.TempDir()
	script := filepath.Join(dir, "claudingtin-s1.command")
	if err := os.WriteFile(script, []byte("pre-existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if openInWindow(envFrom(nil), dir, "s1", "/bin/companion", []string{"/t.jsonl", "", ""}) {
		t.Fatal("openInWindow = true over a pre-existing script path")
	}
	if b, _ := os.ReadFile(script); string(b) != "pre-existing\n" {
		t.Fatalf("pre-existing script was clobbered: %q", b)
	}
}

// TestOpenInWindowWindows: on windows, openInWindow flows the `cmd /c start`
// argv through runWindow, and a false runWindow result makes openInWindow false.
func TestOpenInWindowWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows cmd /c start path")
	}
	prev := runWindow
	var gotArgv []string
	runWindow = func(argv []string) error { gotArgv = argv; return nil }
	t.Cleanup(func() { runWindow = prev })

	const bin = `C:\plugin\bin\windows-amd64\companion.exe`
	args := []string{"/t.jsonl", "", ""}
	if !openInWindow(envFrom(nil), t.TempDir(), "s1", bin, args) {
		t.Fatal("openInWindow = false, want true (runWindow stubbed to succeed)")
	}
	if want := windowsWindowArgv(bin, args); !reflect.DeepEqual(gotArgv, want) {
		t.Fatalf("runWindow argv = %#v, want %#v", gotArgv, want)
	}

	runWindow = func([]string) error { return errors.New("start failed") }
	if openInWindow(envFrom(nil), t.TempDir(), "s1", bin, args) {
		t.Fatal("openInWindow = true despite runWindow error")
	}
}

// TestOpenInWindowDeclinesWhenUnsupported: the knob short-circuits before any
// argv is built or script written.
func TestOpenInWindowDeclinesWhenUnsupported(t *testing.T) {
	prev := runWindow
	runWindow = func([]string) error { t.Fatal("runWindow called despite CLAUDINGTIN_NO_WINDOW"); return nil }
	t.Cleanup(func() { runWindow = prev })

	dir := t.TempDir()
	if openInWindow(envFrom(map[string]string{noWindowEnvVar: "1"}), dir, "s1", "/bin/companion", []string{"/t.jsonl", "", ""}) {
		t.Fatal("openInWindow = true with the opt-out knob set")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("openInWindow wrote files while opted out: %v", entries)
	}
}

// TestRunWindowTool exercises the real grace model against stub children.
func TestRunWindowTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("#!/bin/sh stub children are not run on Windows")
	}

	if err := runWindowTool(nil); err == nil {
		t.Fatal("runWindowTool(nil) = nil, want error")
	}
	if err := runWindowTool([]string{}); err == nil {
		t.Fatal("runWindowTool([]) = nil, want error")
	}
	if err := runWindowTool([]string{"claudingtin-no-such-terminal", "x"}); err == nil {
		t.Fatal("runWindowTool(missing binary) = nil, want error")
	}

	dir := t.TempDir()

	okStub := filepath.Join(dir, "ok.sh")
	writeExecutable(t, okStub, "#!/bin/sh\nexit 0\n")
	if err := runWindowTool([]string{okStub}); err != nil {
		t.Fatalf("runWindowTool(child exits 0) = %v, want nil", err)
	}

	badStub := filepath.Join(dir, "bad.sh")
	writeExecutable(t, badStub, "#!/bin/sh\nexit 3\n")
	if err := runWindowTool([]string{badStub}); err == nil {
		t.Fatal("runWindowTool(child exits 3 within the grace) = nil, want the non-zero exit")
	}

	// A child that outlives a shrunk grace: still running == the window is up.
	prev := windowSpawnGrace
	windowSpawnGrace = 20 * time.Millisecond
	t.Cleanup(func() { windowSpawnGrace = prev })

	slowStub := filepath.Join(dir, "slow.sh")
	writeExecutable(t, slowStub, "#!/bin/sh\nsleep 5\n")
	start := time.Now()
	if err := runWindowTool([]string{slowStub}); err != nil {
		t.Fatalf("runWindowTool(child outlives the grace) = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runWindowTool waited %v, want to return near the %v grace", elapsed, windowSpawnGrace)
	}
}

// TestLaunchTriesWindowBeforeSpawn: with placePane declining and openWindow
// reporting success, launch must not fall through to the detached spawn or
// print the hint, and the per-session lock is still taken.
func TestLaunchTriesWindowBeforeSpawn(t *testing.T) {
	const stdinJSON = `{"session_id":"win1","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	fakePluginRoot(t)

	restorePlace := placePane
	placePane = func(func(string) string, string, []string) bool { return false }
	t.Cleanup(func() { placePane = restorePlace })

	restoreWin := openWindow
	var opened [][]string
	openWindow = func(_ func(string) string, tempDir, sessionID, bin string, a []string) bool {
		opened = append(opened, append([]string{tempDir, sessionID, bin}, a...))
		return true
	}
	t.Cleanup(func() { openWindow = restoreWin })

	rec := &recordingSpawn{}
	tmx := &recordingTmux{}
	var out bytes.Buffer
	dir := t.TempDir()

	if err := launch(strings.NewReader(stdinJSON), envFrom(nil), dir, &out, rec.fn, tmx.fn); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(opened) != 1 {
		t.Fatalf("openWindow calls = %d, want 1", len(opened))
	}
	got := opened[0]
	if len(got) != 6 || got[0] != dir || got[1] != "win1" || got[3] != "/tmp/t.jsonl" || got[4] != "" || got[5] != "" {
		t.Fatalf("openWindow args = %#v, want [%q win1 <bin> /tmp/t.jsonl \"\" \"\"]", got, dir)
	}
	wantSuffix := filepath.Join("bin", archTriple(), companionName())
	if !strings.HasSuffix(got[2], wantSuffix) {
		t.Fatalf("openWindow bin = %q, want suffix %q", got[2], wantSuffix)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("launch fell through to the detached spawn: %#v", rec.calls)
	}
	if out.Len() != 0 {
		t.Fatalf("launch printed the fallback hint despite a window launch: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claudingtin-win1.lock")); err != nil {
		t.Fatalf("lock not created: %v", err)
	}
}

// TestLaunchWindowFailureFallsBackToSpawn: openWindow reporting false leaves the
// Story 1.7 detached-spawn + one-hint-line path exactly as it was.
func TestLaunchWindowFailureFallsBackToSpawn(t *testing.T) {
	const stdinJSON = `{"session_id":"win2","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	fakePluginRoot(t)

	restorePlace := placePane
	placePane = func(func(string) string, string, []string) bool { return false }
	t.Cleanup(func() { placePane = restorePlace })

	restoreWin := openWindow
	openWindow = func(func(string) string, string, string, string, []string) bool { return false }
	t.Cleanup(func() { openWindow = restoreWin })

	rec := &recordingSpawn{}
	tmx := &recordingTmux{}
	var out bytes.Buffer

	if err := launch(strings.NewReader(stdinJSON), envFrom(nil), t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 (window declined)", len(rec.calls))
	}
	line := strings.TrimRight(out.String(), "\n")
	if line == "" || strings.Contains(line, "\n") {
		t.Fatalf("stdout = %q, want exactly one non-empty hint line", out.String())
	}
}
