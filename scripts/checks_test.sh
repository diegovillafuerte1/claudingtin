#!/usr/bin/env bash
# Negative-path regression tests for the go.work-derived guards:
#   scripts/workspace_modules.sh  (--check failure modes + default output)
#   scripts/check_deps.sh         (fail-loud on a module with no direction rules)
#
# No bats. Written for bash 3.2 (macOS). The real repo tree is never mutated:
# every mutating case runs against a throwaway copy of the two helper scripts
# under a temp dir, so each copy resolves that temp dir as its repo root.
#
# Prints PASS/FAIL per assertion; exits non-zero if any assertion fails.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
wm="$repo_root/scripts/workspace_modules.sh"

tests_run=0
tests_failed=0

report() { # report <0=ok|1=bad> <description>
	tests_run=$((tests_run + 1))
	if [ "$1" -eq 0 ]; then
		echo "PASS: $2"
	else
		echo "FAIL: $2"
		tests_failed=$((tests_failed + 1))
	fi
}

tmproot="$(mktemp -d "${TMPDIR:-/tmp}/claudingtin-checks.XXXXXX")"
trap 'rm -rf "$tmproot"' EXIT

# make_fixture <name> — create <tmproot>/<name>/scripts/{workspace_modules,check_deps}.sh
# from the real scripts and echo the fixture root path.
make_fixture() {
	local d="$tmproot/$1"
	mkdir -p "$d/scripts"
	cp "$repo_root/scripts/workspace_modules.sh" "$d/scripts/workspace_modules.sh"
	cp "$repo_root/scripts/check_deps.sh" "$d/scripts/check_deps.sh"
	printf '%s\n' "$d"
}

stub_gomod() { # stub_gomod <dir> <module-path>
	mkdir -p "$1"
	printf 'module %s\n\ngo 1.27\n' "$2" > "$1/go.mod"
}

# --------------------------------------------------------------------------
# 1. --check fails AND names a top-level <dir>/go.mod that is absent from go.work
# --------------------------------------------------------------------------
f="$(make_fixture unwired)"
printf 'go 1.27\n\nuse (\n\t./mod_a\n)\n' > "$f/go.work"
stub_gomod "$f/mod_a" example.com/mod_a
stub_gomod "$f/mod_b" example.com/mod_b
rc=0
out="$(bash "$f/scripts/workspace_modules.sh" --check 2>&1)" || rc=$?
if [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q 'mod_b'; then
	report 0 "workspace_modules.sh --check fails and names an unwired top-level module"
else
	report 1 "workspace_modules.sh --check fails and names an unwired top-level module (rc=$rc, out=<$out>)"
fi

# --------------------------------------------------------------------------
# 2. --check fails when a go.work `use` entry points at a dir with no go.mod
# --------------------------------------------------------------------------
f="$(make_fixture missing_gomod)"
printf 'go 1.27\n\nuse (\n\t./mod_a\n\t./ghost\n)\n' > "$f/go.work"
stub_gomod "$f/mod_a" example.com/mod_a
rc=0
out="$(bash "$f/scripts/workspace_modules.sh" --check 2>&1)" || rc=$?
if [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q 'ghost'; then
	report 0 "workspace_modules.sh --check fails when a use entry lacks a go.mod"
else
	report 1 "workspace_modules.sh --check fails when a use entry lacks a go.mod (rc=$rc, out=<$out>)"
fi

# --------------------------------------------------------------------------
# 3. --check exits 0 against the real repo tree
# --------------------------------------------------------------------------
rc=0
out="$(bash "$wm" --check 2>&1)" || rc=$?
if [ "$rc" -eq 0 ]; then
	report 0 "workspace_modules.sh --check passes against the real repo tree"
else
	report 1 "workspace_modules.sh --check passes against the real repo tree (rc=$rc, out=<$out>)"
fi

# --------------------------------------------------------------------------
# 4. default output = exactly the four ./<dir>/... globs, in go.work order
# --------------------------------------------------------------------------
rc=0
out="$(bash "$wm" 2>&1)" || rc=$?
expected='./backend/... ./companion/... ./plugin/... ./proto/...'
if [ "$rc" -eq 0 ] && [ "$out" = "$expected" ]; then
	report 0 "workspace_modules.sh prints the four go.work globs in order"
else
	report 1 "workspace_modules.sh prints the four go.work globs in order (rc=$rc, out=<$out>)"
fi

# --------------------------------------------------------------------------
# 5. check_deps.sh fails loudly when go.work yields a module with no rules
#    (PATCH 1 path). Fixture go.work adds `use ./frontend`; the guard fires
#    before any `go list`, so no real Go build is stood up.
# --------------------------------------------------------------------------
f="$(make_fixture unknown_module)"
printf 'go 1.27\n\nuse (\n\t./proto\n\t./backend\n\t./companion\n\t./plugin\n\t./frontend\n)\n' > "$f/go.work"
stub_gomod "$f/frontend" example.com/frontend
rc=0
out="$(bash "$f/scripts/check_deps.sh" 2>&1)" || rc=$?
if [ "$rc" -ne 0 ] && printf '%s\n' "$out" | grep -q 'frontend'; then
	report 0 "check_deps.sh fails loudly on a module with no direction rules"
else
	report 1 "check_deps.sh fails loudly on a module with no direction rules (rc=$rc, out=<$out>)"
fi

echo
if [ "$tests_failed" -eq 0 ]; then
	echo "all $tests_run guard self-tests passed"
	exit 0
fi
echo "$tests_failed of $tests_run guard self-tests FAILED"
exit 1
