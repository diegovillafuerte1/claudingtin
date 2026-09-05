#!/usr/bin/env bash
# Dependency-direction check for the claudingtin monorepo.
#
# Allowed edges only:
#   companion -> proto
#   backend   -> proto
#
# Invariants enforced:
#   - proto imports no sibling module and no third-party package
#   - nothing (proto, backend, companion) imports plugin
#   - plugin imports no sibling module (it execs the companion binary by path)
#   - backend and companion each really do import proto (the edge is not dead)
#
# Test files are included (`go list -deps -test`), so a forbidden import added
# only in a _test.go file is still caught.
#
# Exit 0 and print the verified edges when the tree is clean; non-zero otherwise.
# An unresolvable import or a `go list` failure is a loud, explained failure —
# never a vacuous "OK" and never a bare abort. `go list` tolerates pure type
# errors, so this is not a substitute for `go build` / `go vet` in the suite;
# it checks the import graph, not compilation. Written for bash 3.2 (macOS).
#
# The module set checked is enumerated from go.work by scripts/workspace_modules.sh
# — there is no hardcoded module list here. The direction rules below are still
# keyed to the known module names (proto/backend/companion/plugin); a module
# added to go.work under any other name makes this check FAIL LOUDLY until
# matching rules are added for it here.
set -euo pipefail

command -v go >/dev/null || { echo 'check_deps: go toolchain not found' >&2; exit 1; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

MODPREFIX="github.com/diegovillafuerte1/claudingtin"
fail=0

# deps <module-dir>
# Prints every transitive import path (incl. test deps) of the module's packages.
# On `go list` failure: prints an explained FAIL to stderr and returns 1 without
# killing the caller.
deps() {
	local out
	if ! out="$(cd "$1" && go list -deps -test -f '{{.ImportPath}}' ./... 2>&1)"; then
		echo "FAIL: 'go list' failed for module '$1' — does the tree compile?" >&2
		printf '%s\n' "$out" | sed 's/^/  /' >&2
		return 1
	fi
	# `go list -test` prints the test-augmented package as `path [path.test]` and
	# also lists `path.test` binary pseudo-packages — keep only the first field
	# and drop the `.test` pseudo-packages so the import set is real paths only.
	printf '%s\n' "$out" | awk '{print $1}' | grep -v '\.test$' | sort -u
}

# siblings_used <deps-output>  — sibling module names referenced by a deps list.
siblings_used() {
	printf '%s\n' "$1" \
		| { grep "^${MODPREFIX}/" || true; } \
		| sed "s#^${MODPREFIX}/##" \
		| cut -d/ -f1 \
		| sort -u
}

# thirdparty_used <deps-output> — imports that are neither stdlib nor this repo.
thirdparty_used() {
	printf '%s\n' "$1" \
		| { grep -E '^[^/]+\.[^/]+/' || true; } \
		| { grep -v "^${MODPREFIX}/" || true; } \
		| sort -u
}

has() { printf '%s\n' "$1" | grep -qx "$2"; }

# Resolve each module's dependency list once, failing loudly on a compile error.
# The module set is the go.work-derived list shared with CI — strip the leading
# `./` and trailing `/...` that workspace_modules.sh wraps each directory in.
proto_deps=""; backend_deps=""; companion_deps=""; plugin_deps=""
modules="$(bash "$repo_root/scripts/workspace_modules.sh" | sed 's#\./##g; s#/\.\.\.##g')"

# The direction rules further down are keyed to these known module names. If
# go.work grows a module with a different name, enumerate-then-silently-pass
# would be a false OK — fail loudly instead until rules are written for it.
known_module() {
	local k
	for k in proto backend companion plugin; do
		[ "$1" = "$k" ] && return 0
	done
	return 1
}
for m in $modules; do
	if ! known_module "$m"; then
		echo "check_deps: no dependency-direction rules defined for module '$m' — add rules to this script" >&2
		exit 1
	fi
done

for m in $modules; do
	if out="$(deps "$m")"; then
		eval "${m}_deps=\$out"
	else
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "dependency-direction check FAILED (could not enumerate dependencies)" >&2
	exit 1
fi

# check_no_sibling_beyond <module> <deps> <allowed...>
check_no_sibling_beyond() {
	local mod="$1" mdeps="$2"; shift 2
	local used s a ok
	used="$(siblings_used "$mdeps")"
	for s in $used; do
		[ "$s" = "$mod" ] && continue
		ok=0
		for a in "$@"; do [ "$s" = "$a" ] && ok=1; done
		if [ "$ok" -eq 0 ]; then
			echo "FAIL: ${mod} imports sibling '${s}' — not an allowed edge" >&2
			fail=1
		fi
	done
}

# proto: no siblings, no third-party.
check_no_sibling_beyond proto "$proto_deps"
proto_tp="$(thirdparty_used "$proto_deps")"
if [ -n "$proto_tp" ]; then
	echo "FAIL: proto imports third-party packages:" >&2
	printf '%s\n' "$proto_tp" | sed 's/^/  /' >&2
	fail=1
fi

# backend / companion: proto only.
check_no_sibling_beyond backend "$backend_deps" proto
check_no_sibling_beyond companion "$companion_deps" proto

# plugin: no siblings at all.
check_no_sibling_beyond plugin "$plugin_deps"

# nothing imports plugin.
if has "$(siblings_used "$proto_deps")" plugin; then
	echo "FAIL: proto imports plugin — plugin must be unreferenced" >&2; fail=1
fi
if has "$(siblings_used "$backend_deps")" plugin; then
	echo "FAIL: backend imports plugin — plugin must be unreferenced" >&2; fail=1
fi
if has "$(siblings_used "$companion_deps")" plugin; then
	echo "FAIL: companion imports plugin — plugin must be unreferenced" >&2; fail=1
fi

# the allowed edges must actually exist.
if ! has "$(siblings_used "$backend_deps")" proto; then
	echo "FAIL: backend does not import proto — expected edge is missing" >&2; fail=1
fi
if ! has "$(siblings_used "$companion_deps")" proto; then
	echo "FAIL: companion does not import proto — expected edge is missing" >&2; fail=1
fi

if [ "$fail" -ne 0 ]; then
	echo "dependency-direction check FAILED" >&2
	exit 1
fi

echo "dependency-direction check OK — verified edges:"
echo "  companion → proto"
echo "  backend   → proto"
echo "  proto     → ∅   (no sibling, no third-party)"
echo "  plugin        unreferenced by proto/backend/companion; imports no sibling"
