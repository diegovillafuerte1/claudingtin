#!/usr/bin/env bash
# SessionStart hook — PLACEHOLDER (Story 1.7 fills this in).
#
# The real launcher must:
#   - return in ~50ms
#   - resolve ${CLAUDE_PLUGIN_ROOT}/plugin/bin/<os>-<arch>/ and spawn the
#     companion detached with a hard timeout
#   - swallow every failure (spawn error, missing/non-executable binary,
#     unreachable backend, crashed companion) with nothing on stderr
#   - be a silent no-op when an opt-out marker is present
#
# Until then it does nothing and exits cleanly so a fresh clone stays fail-open.
exit 0
