package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// archTriple mirrors the <os>-<arch> directory segment used by both the Go
// launcher (companionPath) and plugin/hooks/session-start.sh.
func archTriple() string { return runtime.GOOS + "-" + runtime.GOARCH }

func companionName() string {
	if runtime.GOOS == "windows" {
		return "companion.exe"
	}
	return "companion"
}

// writeExecutable creates path (and parents) as a non-empty file with mode
// 0o755 so isExecutable and [ -x ] both accept it.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakePluginRoot builds <dir>/bin/<os>-<arch>/companion as an executable stub
// and points osExecutable at a sibling launcher path so pluginRoot resolves to
// <dir>. The override is undone on cleanup.
func fakePluginRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "bin", archTriple(), companionName()), "#!/bin/sh\n")
	fakeExe := filepath.Join(root, "bin", archTriple(), "session-start")
	prev := osExecutable
	osExecutable = func() (string, error) { return fakeExe, nil }
	t.Cleanup(func() { osExecutable = prev })
	return root
}

// stubNoWindow points openWindow at a func that always declines, so a test that
// exercises the detached-spawn fallback does not depend on the host's terminal
// emulators (and never opens a real window on macOS). The override is undone on
// cleanup, mirroring the placePane restore idiom.
func stubNoWindow(t *testing.T) {
	t.Helper()
	prev := openWindow
	openWindow = func(func(string) string, string, string, string, []string) bool { return false }
	t.Cleanup(func() { openWindow = prev })
}

func TestParse(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantErr        bool
		wantSessionID  string
		wantTranscript string
	}{
		{
			name:           "full object",
			in:             `{"session_id":"s1","transcript_path":"/p/t.jsonl","hook_event_name":"SessionStart"}`,
			wantSessionID:  "s1",
			wantTranscript: "/p/t.jsonl",
		},
		{name: "unknown fields ignored", in: `{"foo":1,"bar":"x"}`},
		{name: "missing transcript_path", in: `{"session_id":"s1"}`, wantSessionID: "s1"},
		{name: "empty string", in: ``, wantErr: true},
		{name: "not json", in: `not json`, wantErr: true},
		{name: "truncated", in: `{"session_id":`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(strings.NewReader(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parse(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse(%q) unexpected error: %v", tc.in, err)
			}
			if got.SessionID != tc.wantSessionID || got.TranscriptPath != tc.wantTranscript {
				t.Fatalf("parse(%q) = %+v, want {%q %q}", tc.in, got, tc.wantSessionID, tc.wantTranscript)
			}
		})
	}
}

func TestOptedOut(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "Yes", "on", "  on  ", "oN"}
	falsey := []string{"", "0", "false", "no", "off", "enable", "2", "   "}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == disableEnvVar {
				return v
			}
			return ""
		}
	}
	for _, v := range truthy {
		if !optedOut(env(v)) {
			t.Errorf("optedOut(%q) = false, want true", v)
		}
	}
	for _, v := range falsey {
		if optedOut(env(v)) {
			t.Errorf("optedOut(%q) = true, want false", v)
		}
	}
	// Unset entirely.
	if optedOut(func(string) string { return "" }) {
		t.Errorf("optedOut(unset) = true, want false")
	}
}

func TestAcquire(t *testing.T) {
	dir := t.TempDir()

	if err := acquire(dir, "s1"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	lock := filepath.Join(dir, "claudingtin-s1.lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	if err := acquire(dir, "s1"); err == nil {
		t.Fatalf("second acquire for the same session id succeeded, want O_EXCL failure")
	}
	// The lock is never removed by acquire.
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("lock file disappeared: %v", err)
	}

	// A distinct session id gets its own lock.
	if err := acquire(dir, "s2"); err != nil {
		t.Fatalf("acquire s2: %v", err)
	}

	// Path separators in the session id are sanitised, so the lock stays a
	// single flat file inside tempDir.
	if err := acquire(dir, "../../etc/passwd"); err != nil {
		t.Fatalf("acquire with unsafe id: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claudingtin-.._.._etc_passwd.lock")); err != nil {
		t.Fatalf("sanitised lock file not found: %v", err)
	}
}

func TestCompanionPath(t *testing.T) {
	root := t.TempDir()
	fakeExe := filepath.Join(root, "bin", archTriple(), "session-start")
	prev := osExecutable
	osExecutable = func() (string, error) { return fakeExe, nil }
	defer func() { osExecutable = prev }()

	got := companionPath(func(string) string { return "" })
	want := filepath.Join(root, "bin", archTriple(), companionName())
	if got != want {
		t.Fatalf("companionPath = %q, want %q", got, want)
	}
	if !strings.HasSuffix(got, filepath.Join("bin", archTriple(), companionName())) {
		t.Fatalf("companionPath = %q, missing bin/<os>-<arch>/companion suffix", got)
	}
}

func TestCompanionPathUsesEnvRootAndRejectsRelative(t *testing.T) {
	// An absolute CLAUDE_PLUGIN_ROOT is the fallback when os.Executable fails.
	prev := osExecutable
	osExecutable = func() (string, error) { return "", os.ErrNotExist }
	defer func() { osExecutable = prev }()

	envRoot := t.TempDir() // always an absolute path on the host platform
	got := companionPath(func(k string) string {
		if k == "CLAUDE_PLUGIN_ROOT" {
			return envRoot
		}
		return ""
	})
	want := filepath.Join(envRoot, "bin", archTriple(), companionName())
	if got != want {
		t.Fatalf("companionPath (abs env root) = %q, want %q", got, want)
	}

	// When os.Executable resolves to an absolute path, it wins over the env var.
	exeRoot := t.TempDir()
	osExecutable = func() (string, error) {
		return filepath.Join(exeRoot, "bin", archTriple(), "session-start"), nil
	}
	got = companionPath(func(k string) string {
		if k == "CLAUDE_PLUGIN_ROOT" {
			return envRoot
		}
		return ""
	})
	if want := filepath.Join(exeRoot, "bin", archTriple(), companionName()); got != want {
		t.Fatalf("companionPath (exe resolves, env also set) = %q, want %q", got, want)
	}

	// A relative CLAUDE_PLUGIN_ROOT is ignored (never resolved against CWD);
	// with os.Executable also failing, there is no root at all -> "".
	osExecutable = func() (string, error) { return "", os.ErrNotExist }
	if got := companionPath(func(k string) string {
		if k == "CLAUDE_PLUGIN_ROOT" {
			return "relative/plugin/root"
		}
		return ""
	}); got != "" {
		t.Fatalf("companionPath (relative env root, no exe) = %q, want \"\"", got)
	}

	// os.Executable landing on a relative path is likewise refused.
	osExecutable = func() (string, error) { return filepath.Join("rel", "bin", "x", "session-start"), nil }
	if got := companionPath(func(string) string { return "" }); got != "" {
		t.Fatalf("companionPath (relative exe) = %q, want \"\"", got)
	}
}

// recordingSpawn captures the arguments of each spawn call and returns errRet.
type recordingSpawn struct {
	calls  [][]string
	bins   []string
	errRet error
}

func (r *recordingSpawn) fn(bin string, args []string) error {
	r.bins = append(r.bins, bin)
	r.calls = append(r.calls, args)
	return r.errRet
}

// recordingTmux is the tmuxRun twin of recordingSpawn: it captures the argv of
// each call and returns errRet.
type recordingTmux struct {
	calls  [][]string
	errRet error
}

func (r *recordingTmux) fn(args []string) error {
	r.calls = append(r.calls, args)
	return r.errRet
}

func TestInsideTmux(t *testing.T) {
	cases := []struct {
		name string
		tmux string
		want bool
	}{
		{"set", "/tmp/tmux-1000/default,12345,0", true},
		{"unset", "", false},
		{"whitespace only", "  \t ", false},
		{"padded value still counts", "  x ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := insideTmux(func(k string) string {
				if k == "TMUX" {
					return tc.tmux
				}
				return ""
			})
			if got != tc.want {
				t.Fatalf("insideTmux(TMUX=%q) = %v, want %v", tc.tmux, got, tc.want)
			}
		})
	}
}

func TestLaunch(t *testing.T) {
	const stdinJSON = `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	// tmuxEnv is a getenv with $TMUX set (and optionally $TMUX_PANE), so launch
	// takes the tmux branch.
	tmuxEnv := func(pane string) func(string) string {
		return func(k string) string {
			switch k {
			case "TMUX":
				return "/tmp/tmux-1000/default,900,0"
			case "TMUX_PANE":
				return pane
			default:
				return ""
			}
		}
	}

	t.Run("normal launch (no tmux) spawns detached and prints one hint line", func(t *testing.T) {
		fakePluginRoot(t)
		stubNoWindow(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()

		if err := launch(strings.NewReader(stdinJSON), env(nil), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch returned error: %v", err)
		}
		if len(tmx.calls) != 0 {
			t.Fatalf("tmux calls = %d, want 0 (no $TMUX)", len(tmx.calls))
		}
		if len(rec.calls) != 1 {
			t.Fatalf("spawn calls = %d, want 1", len(rec.calls))
		}
		wantSuffix := filepath.Join("bin", archTriple(), companionName())
		if !strings.HasSuffix(rec.bins[0], wantSuffix) {
			t.Fatalf("spawned %q, want suffix %q", rec.bins[0], wantSuffix)
		}
		if got := rec.calls[0]; len(got) != 3 || got[0] != "/tmp/t.jsonl" || got[1] != "" || got[2] != "" {
			t.Fatalf("spawn args = %#v, want [/tmp/t.jsonl \"\" \"\"]", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "claudingtin-s1.lock")); err != nil {
			t.Fatalf("lock not created: %v", err)
		}
		// Exactly one stdout line, naming the companion binary and its
		// [transcript "" ""] invocation, and no session id.
		line := strings.TrimRight(out.String(), "\n")
		if line == "" || strings.Contains(line, "\n") {
			t.Fatalf("stdout = %q, want exactly one non-empty line", out.String())
		}
		if !strings.Contains(line, rec.bins[0]) || !strings.Contains(line, `/tmp/t.jsonl "" ""`) {
			t.Fatalf("hint line %q does not name the companion invocation", line)
		}
		if strings.Contains(out.String(), "s1") {
			t.Fatalf("hint line %q leaks the session id", out.String())
		}
	})

	t.Run("inside tmux places an adjacent unfocused split and does not spawn", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()

		if err := launch(strings.NewReader(stdinJSON), tmuxEnv("%7"), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch returned error: %v", err)
		}
		if len(rec.calls) != 0 {
			t.Fatalf("spawn calls = %d, want 0 (tmux split handled it)", len(rec.calls))
		}
		if out.Len() != 0 {
			t.Fatalf("stdout = %q, want empty on the tmux path", out.String())
		}
		if len(tmx.calls) != 1 {
			t.Fatalf("tmux calls = %d, want 1", len(tmx.calls))
		}
		argv := tmx.calls[0]
		if len(argv) == 0 || argv[0] != "split-window" {
			t.Fatalf("tmux argv = %#v, want it to start with split-window", argv)
		}
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, " -h") || !strings.Contains(joined, " -d") {
			t.Fatalf("tmux argv %#v missing -h/-d (side-by-side split, no focus change)", argv)
		}
		if !strings.Contains(joined, "-t %7") {
			t.Fatalf("tmux argv %#v does not target $TMUX_PANE", argv)
		}
		// The pane command is the companion binary with the unchanged three
		// args as trailing argv.
		last4 := argv[len(argv)-4:]
		wantSuffix := filepath.Join("bin", archTriple(), companionName())
		if !strings.HasSuffix(last4[0], wantSuffix) || last4[1] != "/tmp/t.jsonl" || last4[2] != "" || last4[3] != "" {
			t.Fatalf("tmux pane command = %#v, want <companion> /tmp/t.jsonl \"\" \"\"", last4)
		}
		if _, err := os.Stat(filepath.Join(dir, "claudingtin-s1.lock")); err != nil {
			t.Fatalf("lock not created on the tmux path: %v", err)
		}
	})

	t.Run("tmux split failure falls back to the detached spawn + hint", func(t *testing.T) {
		fakePluginRoot(t)
		stubNoWindow(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{errRet: exec.ErrNotFound}
		var out bytes.Buffer

		if err := launch(strings.NewReader(stdinJSON), tmuxEnv(""), t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch returned error: %v", err)
		}
		if len(tmx.calls) != 1 {
			t.Fatalf("tmux calls = %d, want 1 (attempted then failed)", len(tmx.calls))
		}
		if len(rec.calls) != 1 {
			t.Fatalf("spawn calls = %d, want 1 (fell back)", len(rec.calls))
		}
		line := strings.TrimRight(out.String(), "\n")
		if line == "" || strings.Contains(line, "\n") {
			t.Fatalf("stdout = %q, want exactly one non-empty line", out.String())
		}
		if !strings.Contains(line, rec.bins[0]) || !strings.Contains(line, `/tmp/t.jsonl "" ""`) {
			t.Fatalf("fallback hint %q does not name the companion invocation", line)
		}
		if strings.Contains(out.String(), "s1") {
			t.Fatalf("fallback hint %q leaks the session id", out.String())
		}
	})

	t.Run("opt-out truthy", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()

		if err := launch(strings.NewReader(stdinJSON), env(map[string]string{"CLAUDINGTIN_DISABLE": "1"}), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("opt-out did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("opt-out created files: %v", entries)
		}
	})

	t.Run("opt-out falsey proceeds", func(t *testing.T) {
		fakePluginRoot(t)
		stubNoWindow(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		if err := launch(strings.NewReader(stdinJSON), env(map[string]string{"CLAUDINGTIN_DISABLE": "0"}), t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 1 {
			t.Fatalf("spawn calls = %d, want 1", len(rec.calls))
		}
	})

	t.Run("already launched", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		if err := acquire(dir, "s1"); err != nil {
			t.Fatal(err)
		}
		if err := launch(strings.NewReader(stdinJSON), tmuxEnv(""), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("resume did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
	})

	t.Run("malformed stdin", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		if err := launch(strings.NewReader("not json"), env(nil), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("malformed input did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("malformed input created files: %v", entries)
		}
	})

	t.Run("empty stdin", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		if err := launch(strings.NewReader(""), env(nil), t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("empty stdin did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
	})

	t.Run("missing transcript_path", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		for _, in := range []string{`{"session_id":"s1"}`, `{"session_id":"s1","transcript_path":""}`} {
			if err := launch(strings.NewReader(in), env(nil), dir, &out, rec.fn, tmx.fn); err != nil {
				t.Fatalf("launch(%s): %v", in, err)
			}
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("missing transcript_path did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
	})

	t.Run("missing session_id", func(t *testing.T) {
		fakePluginRoot(t)
		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		for _, in := range []string{
			`{"transcript_path":"/tmp/t.jsonl"}`,
			`{"session_id":"","transcript_path":"/tmp/t.jsonl"}`,
		} {
			if err := launch(strings.NewReader(in), env(nil), dir, &out, rec.fn, tmx.fn); err != nil {
				t.Fatalf("launch(%s): %v", in, err)
			}
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("missing session_id did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("empty-session_id path created files: %v", entries)
		}
	})

	t.Run("missing companion binary", func(t *testing.T) {
		// osExecutable points three dirs below an empty temp dir: no companion.
		root := t.TempDir()
		fakeExe := filepath.Join(root, "bin", archTriple(), "session-start")
		prev := osExecutable
		osExecutable = func() (string, error) { return fakeExe, nil }
		defer func() { osExecutable = prev }()

		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		if err := launch(strings.NewReader(stdinJSON), tmuxEnv(""), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("missing binary did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("missing-binary path created files: %v", entries)
		}
	})

	t.Run("non-executable companion binary", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("isExecutable treats any regular file as executable on Windows")
		}
		root := t.TempDir()
		comp := filepath.Join(root, "bin", archTriple(), companionName())
		if err := os.MkdirAll(filepath.Dir(comp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(comp, []byte("#!/bin/sh\n"), 0o644); err != nil { // present, no +x
			t.Fatal(err)
		}
		fakeExe := filepath.Join(root, "bin", archTriple(), "session-start")
		prev := osExecutable
		osExecutable = func() (string, error) { return fakeExe, nil }
		defer func() { osExecutable = prev }()

		rec := &recordingSpawn{}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		if err := launch(strings.NewReader(stdinJSON), env(nil), dir, &out, rec.fn, tmx.fn); err != nil {
			t.Fatalf("launch: %v", err)
		}
		if len(rec.calls) != 0 || len(tmx.calls) != 0 || out.Len() != 0 {
			t.Fatalf("non-executable companion did something: spawn=%d tmux=%d stdout=%q", len(rec.calls), len(tmx.calls), out.String())
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("non-executable-companion path created files: %v", entries)
		}
	})

	t.Run("spawn error is returned, no stdout, lock kept", func(t *testing.T) {
		fakePluginRoot(t)
		stubNoWindow(t)
		rec := &recordingSpawn{errRet: exec.ErrNotFound}
		tmx := &recordingTmux{}
		var out bytes.Buffer
		dir := t.TempDir()
		if err := launch(strings.NewReader(stdinJSON), env(nil), dir, &out, rec.fn, tmx.fn); err == nil {
			t.Fatalf("launch returned nil, want the spawn error surfaced")
		}
		if len(rec.calls) != 1 {
			t.Fatalf("spawn calls = %d, want 1", len(rec.calls))
		}
		if out.Len() != 0 {
			t.Fatalf("stdout = %q, want empty (hint is success-only)", out.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "claudingtin-s1.lock")); err != nil {
			t.Fatalf("lock removed after spawn error: %v", err)
		}
	})
}

// TestSessionStartWrapper drives the real plugin/hooks/session-start.sh through
// `sh` against a fixture plugin tree.
func TestSessionStartWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh wrapper is not exercised on Windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	wrapper, err := filepath.Abs(filepath.Join("..", "..", "hooks", "session-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wrapper); err != nil {
		t.Fatalf("wrapper not found: %v", err)
	}

	const stdinJSON = `{"session_id":"s1","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	t.Run("launcher present receives stdin", func(t *testing.T) {
		root := t.TempDir()
		record := filepath.Join(root, "stdin.txt")
		stub := "#!/bin/sh\ncat > \"" + record + "\"\n"
		writeExecutable(t, filepath.Join(root, "bin", archTriple(), "session-start"), stub)

		cmd := exec.Command("sh", wrapper)
		cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root, "TMUX=")
		cmd.Stdin = strings.NewReader(stdinJSON)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("wrapper exited non-zero: %v (output %q)", err, out)
		}
		if len(out) != 0 {
			t.Fatalf("wrapper wrote output: %q", out)
		}
		got, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("stub did not record stdin: %v", err)
		}
		if string(got) != stdinJSON {
			t.Fatalf("stub stdin = %q, want %q", got, stdinJSON)
		}
	})

	t.Run("launcher absent is a silent exit 0", func(t *testing.T) {
		root := t.TempDir() // no bin/<os>-<arch>/session-start

		cmd := exec.Command("sh", wrapper)
		cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root, "TMUX=")
		cmd.Stdin = strings.NewReader(stdinJSON)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("wrapper exited non-zero with no launcher: %v (output %q)", err, out)
		}
		if len(out) != 0 {
			t.Fatalf("wrapper wrote output with no launcher: %q", out)
		}
	})
}

// TestLauncherEndToEndExitsZero compiles the real ./cmd/session-start, drops it
// into a fixture plugin tree, and drives it through the real
// hooks/session-start.sh via `sh`. It asserts the contract the shell-stub tests
// cannot: the actual compiled launcher exits 0 with an empty stdout on both the
// spawn-error path and the malformed-input path.
func TestLauncherEndToEndExitsZero(t *testing.T) {
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

	moduleDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(moduleDir, "hooks", "session-start.sh")

	root := t.TempDir()
	binDir := filepath.Join(root, "bin", archTriple())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	build := exec.Command(goBin, "build", "-o", filepath.Join(binDir, "session-start"), "./cmd/session-start")
	build.Dir = moduleDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/session-start: %v\n%s", err, out)
	}

	// A "companion" that is a regular +x file but not a runnable image, so the
	// real detachedSpawn's cmd.Start() fails and launch() surfaces the error —
	// exercising main's spawn-error branch (which still exits 0).
	writeExecutable(t, filepath.Join(binDir, companionName()), "this is not an executable image\n")

	cases := []struct{ name, stdin string }{
		{"valid stdin, companion cannot be started", `{"session_id":"e2e1","transcript_path":"/tmp/t.jsonl"}`},
		{"malformed stdin", `not json at all`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", wrapper)
			cmd.Env = append(os.Environ(),
				"CLAUDE_PLUGIN_ROOT="+root,
				"TMPDIR="+t.TempDir(),     // isolate the per-session lock file
				"TMUX=",                   // deterministically the no-tmux (manual) path
				"CLAUDINGTIN_NO_WINDOW=1", // no real terminal window during tests
			)
			cmd.Stdin = strings.NewReader(tc.stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("real launcher via wrapper exited non-zero: %v (stderr %q)", err, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("real launcher wrote to stdout: %q", stdout.String())
			}
		})
	}
}

// TestLauncherEndToEndNoTmuxPrintsHint compiles the real ./cmd/session-start
// against a runnable companion stub and drives it through hooks/session-start.sh
// with $TMUX unset. It asserts the no-tmux acceptance contract: exit 0 and
// exactly one stdout line naming the companion invocation.
func TestLauncherEndToEndNoTmuxPrintsHint(t *testing.T) {
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

	moduleDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(moduleDir, "hooks", "session-start.sh")

	root := t.TempDir()
	binDir := filepath.Join(root, "bin", archTriple())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	build := exec.Command(goBin, "build", "-o", filepath.Join(binDir, "session-start"), "./cmd/session-start")
	build.Dir = moduleDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/session-start: %v\n%s", err, out)
	}

	// A companion stub that actually starts (and exits at once) so detachedSpawn
	// succeeds and the launcher reaches the success-only hint line.
	companion := filepath.Join(binDir, companionName())
	writeExecutable(t, companion, "#!/bin/sh\nexit 0\n")

	cmd := exec.Command("sh", wrapper)
	cmd.Env = append(os.Environ(),
		"CLAUDE_PLUGIN_ROOT="+root,
		"TMPDIR="+t.TempDir(),
		"TMUX=",                   // no tmux: the manual path with the one-line hint
		"CLAUDINGTIN_NO_WINDOW=1", // no real terminal window during tests
	)
	cmd.Stdin = strings.NewReader(`{"session_id":"e2ehint","transcript_path":"/tmp/t.jsonl"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("real launcher via wrapper exited non-zero: %v (stderr %q)", err, stderr.String())
	}

	line := strings.TrimRight(stdout.String(), "\n")
	if line == "" || strings.Contains(line, "\n") {
		t.Fatalf("stdout = %q, want exactly one non-empty line", stdout.String())
	}
	if !strings.Contains(line, companion) || !strings.Contains(line, `/tmp/t.jsonl "" ""`) {
		t.Fatalf("hint line %q does not name the companion invocation", line)
	}
	if strings.Contains(stdout.String(), "e2ehint") {
		t.Fatalf("hint line %q leaks the session id", stdout.String())
	}
}
