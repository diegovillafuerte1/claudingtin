// Command session-start is the claudingtin SessionStart launcher. Claude Code
// runs it (via plugin/hooks/session-start.sh) once per session start or resume.
// It reads the SessionStart hook JSON on stdin, honours the
// CLAUDINGTIN_DISABLE opt-out and a per-session_id lock, resolves the committed
// companion binary under <plugin-root>/bin/<os>-<arch>/, and spawns it detached
// with (transcript-path, "", "") without waiting.
//
// It is fail-open and silent: every path — success, opt-out, malformed input,
// missing binary, spawn failure, even a panic in the core — ends at the single
// deferred os.Exit(0) with nothing written to stdout. It never exits non-zero
// (a non-zero SessionStart hook, in particular exit 2, blocks the session) and
// never blocks on the child. All policy lives in launch.go's pure core; this
// file only wires the real stdin / env / temp-dir / spawn and maps the result
// to the exit.
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

	if err := launch(os.Stdin, os.Getenv, os.TempDir(), detachedSpawn); err != nil {
		// Non-identifying: no session id, no transcript content. Best-effort;
		// the process exits 0 regardless.
		io.WriteString(os.Stderr, "claudingtin: could not start companion\n")
	}
}
