#!/bin/sh
# SessionStart hook — arch-dispatch wrapper.
#
# Whole job: map the host to Go's <os>-<arch>, then run the compiled launcher
# at bin/<os>-<arch>/session-start with stdin inherited. Every launch decision —
# the CLAUDINGTIN_DISABLE opt-out, the once-per-session dedup, the detached
# spawn, and always exiting 0 — lives in that Go binary, never here. A missing
# launcher, or one that cannot be executed, is a silent exit 0, so a Claude Code
# session is never blocked (a native shell without POSIX sh is the accepted
# degradation: the hook no-ops and the session is untouched).

os=$(uname -s 2>/dev/null)
arch=$(uname -m 2>/dev/null)

case "$os" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN*) os=windows ;;
	*) exit 0 ;;
esac

case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) exit 0 ;;
esac

root=${CLAUDE_PLUGIN_ROOT:-"$(dirname "$0")/.."}
launcher=$root/bin/$os-$arch/session-start
if [ "$os" = windows ]; then
	launcher=$launcher.exe
fi

# Deliberately NOT `exec`: run the launcher as a child, then exit 0
# unconditionally. Under a non-interactive POSIX sh a failed `exec` (wrong-arch
# or corrupt committed binary, exec-format error, noexec mount, a TOCTOU race
# after the -x test) exits 126/127 and would make this hook fail non-zero — the
# one thing it must never do. The launcher returns well within the hook budget
# and detaches the companion into its own session, so the companion is not a
# child of this wrapper and outlives it.
[ -x "$launcher" ] || exit 0
"$launcher"
exit 0
