package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// sessionStart is the subset of the Claude Code SessionStart hook stdin JSON the
// launcher acts on. Every other field is ignored.
type sessionStart struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// osExecutable is a seam over os.Executable so the plugin-root derivation is
// testable. Production code never reassigns it.
var osExecutable = os.Executable

// maxStdinBytes caps how much of stdin the launcher will read. The SessionStart
// hook JSON is tiny; this only guards against a runaway pipe.
const maxStdinBytes = 1 << 20

// parse reads the SessionStart JSON from r. A read error or malformed / empty
// body is returned as an error for the caller to swallow.
func parse(r io.Reader) (sessionStart, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxStdinBytes))
	if err != nil {
		return sessionStart{}, err
	}
	var in sessionStart
	if err := json.Unmarshal(data, &in); err != nil {
		return sessionStart{}, err
	}
	return in, nil
}

// optedOut reports whether CLAUDINGTIN_DISABLE is set to a truthy value
// (1/true/yes/on, case-insensitive, surrounding whitespace ignored). Anything
// else — including unset, empty, "0", "false" — means proceed.
func optedOut(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(disableEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// pluginRoot resolves the plugin's root directory: three levels up from the
// launcher binary (which lives at <root>/bin/<os>-<arch>/session-start), with
// ${CLAUDE_PLUGIN_ROOT} — set by Claude Code and by the shell wrapper — as a
// fallback used only when os.Executable fails. It returns "" when neither yields
// an absolute path — never a relative path, so the launcher can never resolve
// the companion against Claude Code's working directory (the user's project).
func pluginRoot(getenv func(string) string) string {
	if exe, err := osExecutable(); err == nil {
		if root := filepath.Dir(filepath.Dir(filepath.Dir(exe))); filepath.IsAbs(root) {
			return root
		}
	}
	if root := getenv("CLAUDE_PLUGIN_ROOT"); filepath.IsAbs(root) {
		return root
	}
	return ""
}

// companionPath builds the absolute path to the committed companion binary for
// the current platform: <plugin-root>/bin/<GOOS>-<GOARCH>/companion (with a
// .exe suffix on Windows). It returns "" when the plugin root cannot be
// resolved to an absolute path. It does not check that the file exists.
func companionPath(getenv func(string) string) string {
	root := pluginRoot(getenv)
	if !filepath.IsAbs(root) {
		return ""
	}
	name := "companion"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(root, "bin", runtime.GOOS+"-"+runtime.GOARCH, name)
}

// isExecutable reports whether path is a regular file that can be executed. On
// Windows the executable bit is not meaningful, so a regular file is enough.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// lockPath is <tempDir>/claudingtin-<sanitized session id>.lock. Any rune
// outside [A-Za-z0-9._-] is replaced with '_' so the name is filesystem-safe.
func lockPath(tempDir, sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, sessionID)
	return filepath.Join(tempDir, lockPrefix+safe+".lock")
}

// acquire creates the per-session lock file with O_CREATE|O_EXCL. It returns an
// error if the file already exists (the session was already launched) or cannot
// be created for any other reason. The lock is never removed.
func acquire(tempDir, sessionID string) error {
	f, err := os.OpenFile(lockPath(tempDir, sessionID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// placementHint is the single line the launcher writes to stdout on the no-tmux
// fallback, once the companion is already running. SessionStart stdout is folded
// into the model's context rather than shown in the user's terminal, so this is
// phrased as a declarative, plugin-namespaced status note — a leading
// "(claudingtin)" tag, a statement of fact, then how to watch it — that a model
// reads as informational, never as an instruction to act on. One line, no
// session id, no account key, no transcript content; the two %s are the
// companion binary path and the transcript path, both already on the user's own
// disk.
const placementHint = "(claudingtin) companion is running; to watch it live, run:  %s %s \"\" \"\""

// insideTmux reports whether the launcher is running inside a tmux client — a
// non-blank $TMUX. It is the entire input to the pane-placement choice, kept
// pure and table-testable.
func insideTmux(getenv func(string) string) bool {
	return strings.TrimSpace(getenv("TMUX")) != ""
}

// launch is the launcher's whole policy, kept pure so it is unit-testable: it
// reads the SessionStart JSON from stdin, honours the opt-out and the
// once-per-session lock, resolves the companion binary, and — when everything
// lines up — places the companion beside the Claude session. Inside tmux
// ($TMUX set) it asks tmuxRun for an adjacent, unfocused split-window pane
// running the companion with args [transcriptPath, "", ""]; with no tmux, or if
// that split fails, it asks spawn to start the companion detached with the same
// args and, only when the spawn succeeds, writes the one-line placementHint to
// stdout.
//
// It never calls os.Exit. It returns nil whenever it deliberately does nothing
// (opted out, unparseable input, no transcript path, no session id to dedup on,
// unresolvable plugin root, missing or non-executable binary, session already
// launched) or once placement has succeeded, and returns spawn's error only
// when a detached spawn was actually attempted and failed — the thin main
// swallows that too and exits 0.
func launch(stdin io.Reader, getenv func(string) string, tempDir string, stdout io.Writer, spawn func(bin string, args []string) error, tmuxRun func(args []string) error) error {
	if optedOut(getenv) {
		return nil
	}

	in, err := parse(stdin)
	if err != nil {
		return nil
	}
	if in.TranscriptPath == "" {
		return nil
	}
	if in.SessionID == "" {
		// No id to build the once-per-session lock on: fail open rather than
		// take a shared "claudingtin-.lock" that would wedge every later
		// empty-id session for this temp dir's lifetime.
		return nil
	}

	bin := companionPath(getenv)
	if bin == "" || !isExecutable(bin) {
		return nil
	}

	if err := acquire(tempDir, in.SessionID); err != nil {
		return nil
	}

	args := []string{in.TranscriptPath, "", ""}

	// Inside tmux: bring the companion up in an adjacent pane that does not
	// steal focus from the Claude session. split-window talks to the running
	// server over $TMUX and returns at once, so this stays well inside the hook
	// budget. Any tmux failure — no binary (Windows, non-tmux shells), dead
	// server, non-zero exit — drops through to the detached spawn below.
	if insideTmux(getenv) {
		split := []string{"split-window", "-h", "-d"}
		if pane := strings.TrimSpace(getenv("TMUX_PANE")); pane != "" {
			split = append(split, "-t", pane)
		}
		split = append(split, "--", bin)
		split = append(split, args...)
		if tmuxRun(split) == nil {
			return nil
		}
	}

	// No tmux, or the split failed: Story 1.7's detached spawn, unchanged. The
	// one-line hint is written only once the spawn has actually started — a
	// spawn failure stays exactly as silent as 1.7 (the thin main still writes
	// its single non-identifying stderr line).
	if err := spawn(bin, args); err != nil {
		return err
	}
	hint := fmt.Sprintf(placementHint, bin, in.TranscriptPath)
	fmt.Fprintln(stdout, hint)
	return nil
}
