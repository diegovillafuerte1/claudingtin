// Command session-start is the claudingtin SessionStart launcher. Claude Code
// runs it (via plugin/hooks/session-start.sh) once per session start or resume.
// It reads the SessionStart hook JSON on stdin, honours the
// CLAUDINGTIN_DISABLE opt-out and a per-session_id lock, resolves the committed
// companion binary under <plugin-root>/bin/<os>-<arch>/, and then places the
// companion one of these ways:
//
//   - inside tmux ($TMUX set): an adjacent tmux split-window pane beside the
//     Claude session that does not take focus;
//   - inside another scriptable multiplexer (WezTerm, Zellij, Kitty with remote
//     control, Windows Terminal): the equivalent adjacent pane via that tool's
//     own CLI — see placement.go;
//   - none of those, or every split attempt failed: the companion spawned
//     detached with (transcript-path, "", "") without waiting, plus exactly one
//     stdout line telling the user how to open a live view themselves;
//   - any precondition unmet (opt-out, malformed input, missing binary,
//     session already launched, …): a silent no-op.
//
// It is fail-open: every path — including a multiplexer split that falls back to
// the detached spawn, a spawn failure, and a panic in the core — ends at the
// single deferred os.Exit(0). It never exits non-zero (a non-zero SessionStart
// hook, in particular exit 2, blocks the session) and never blocks on the
// child. The only thing ever written to stdout is that single fallback hint
// line, and only once the detached spawn has succeeded. All policy lives in
// launch.go's pure core; this file only wires the real stdin / env / temp-dir /
// stdout / spawn / tmux seams and maps the result to the exit.
package main

import (
	"io"
	"os"
)

const (
	// disableEnvVar, when truthy, makes the launcher a no-op.
	disableEnvVar = "CLAUDINGTIN_DISABLE"
	// lockPrefix names the per-session lock file: <lockPrefix><session id>.lock.
	lockPrefix = "claudingtin-"
)

func main() {
	// The deferred exit always runs — and swallows a panic anywhere in the
	// core — so the SessionStart hook can never return non-zero.
	defer func() {
		_ = recover()
		os.Exit(0)
	}()

	if err := launch(os.Stdin, os.Getenv, os.TempDir(), os.Stdout, detachedSpawn, tmuxSplit); err != nil {
		// Non-identifying: no session id, no transcript content. Best-effort;
		// the process exits 0 regardless.
		io.WriteString(os.Stderr, "claudingtin: could not start companion\n")
	}
}
