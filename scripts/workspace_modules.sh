#!/usr/bin/env bash
# Single source of truth for the claudingtin workspace module set.
#
# The set is derived from the `use (...)` directives in go.work — there is no
# second hardcoded list anywhere. CI's go build/vet/test calls and
# scripts/check_deps.sh both consume this helper, so adding a module to go.work
# automatically extends every workspace-wide check, and forgetting to wire a new
# module into go.work is caught by `--check`.
#
# Usage:
#   scripts/workspace_modules.sh          # print `./<dir>/...` globs, in go.work order
#   scripts/workspace_modules.sh --check  # assert go.work and the module tree agree
#
# `--check` exits non-zero, naming the offender, when:
#   - a `use` entry in go.work has no go.mod on disk, or
#   - a top-level `*/go.mod` in the repo is missing from go.work's use block.
#
# Written for bash 3.2 (macOS).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

[ -f go.work ] || { echo 'workspace_modules: go.work not found at repo root' >&2; exit 1; }

# Directory names named by go.work's `use` directives, in file order, one per
# line, with no leading `./` or trailing `/`. Handles the block form
# (`use (\n\t./dir\n)`) and the single-line form (`use ./dir`).
list_use_dirs() {
	awk '
		/^[[:space:]]*use[[:space:]]+[^([:space:]]/ {
			line = $0
			sub(/^[[:space:]]*use[[:space:]]+/, "", line)
			sub(/\/\/.*$/, "", line)
			gsub(/[[:space:]]/, "", line)
			if (line != "") print line
			next
		}
		/^[[:space:]]*use[[:space:]]*\(/ { in_block = 1; next }
		in_block && /^[[:space:]]*\)/     { in_block = 0; next }
		in_block {
			line = $0
			sub(/\/\/.*$/, "", line)
			gsub(/[[:space:]]/, "", line)
			if (line != "") print line
		}
	' go.work
}

use_dirs=()
while IFS= read -r d; do
	d="${d#./}"
	d="${d%/}"
	[ -n "$d" ] && use_dirs+=("$d")
done < <(list_use_dirs)

if [ "${#use_dirs[@]}" -eq 0 ]; then
	echo 'workspace_modules: no use directives found in go.work' >&2
	exit 1
fi

if [ "${1:-}" = "--check" ]; then
	fail=0

	# Every use entry must resolve to a real go.mod.
	for d in "${use_dirs[@]}"; do
		if [ ! -f "$d/go.mod" ]; then
			echo "FAIL: go.work names 'use ./$d' but $d/go.mod does not exist" >&2
			fail=1
		fi
	done

	# Every top-level module must be wired into go.work.
	for gomod in */go.mod; do
		[ -e "$gomod" ] || continue
		dir="${gomod%/go.mod}"
		listed=0
		for d in "${use_dirs[@]}"; do
			[ "$d" = "$dir" ] && listed=1
		done
		if [ "$listed" -eq 0 ]; then
			echo "FAIL: module '$dir' has a go.mod but is not in go.work's use block" >&2
			fail=1
		fi
	done

	if [ "$fail" -ne 0 ]; then
		echo 'workspace-module check FAILED — go.work and the module tree disagree' >&2
		exit 1
	fi
	echo "workspace-module check OK — go.work lists ${#use_dirs[@]} modules, all present and wired"
	exit 0
fi

if [ "${1:-}" != "" ]; then
	echo "workspace_modules: unknown argument '$1' (expected no args or --check)" >&2
	exit 2
fi

# Default: the Story 1.1 explicit multi-module pattern, on one line, in go.work
# order — a drop-in for `./proto/... ./backend/... ./companion/... ./plugin/...`.
out=""
for d in "${use_dirs[@]}"; do
	out="$out ./$d/..."
done
printf '%s\n' "${out# }"
