package main

import "os"

// Story 1.7 fills this in: resolve the platform companion binary under
// ${CLAUDE_PLUGIN_ROOT}/plugin/bin/<os>-<arch>/ and spawn it detached with
// (transcript path, config-dir path, server URL), returning in ~50ms and
// swallowing every failure with nothing on stdout or stderr — the launcher must
// be silent and fail-open so it never disturbs the Claude Code session.
//
// For now this is a compile-only stub: it accepts exactly the three positional
// arguments and exits, printing nothing. It imports no sibling module — the real
// launcher execs the companion binary by path, it does not link it.
func main() {
	if len(os.Args[1:]) != 3 {
		os.Exit(2)
	}
}
