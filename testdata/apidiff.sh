#!/bin/bash
#
# Report changes to the exported API surface against a baseline commit.
#
# The backward-compatibility constraint is otherwise enforced only by review,
# and nearly every phase of this project added fields to exported structs. This
# makes an incompatible change visible rather than incidental.
#
# Usage:
#   ./apidiff.sh [baseline-ref]        # default: HEAD
#
# Exit status is 1 when an incompatible change is found, so CI can gate on it.
#
# apidiff classifies changes for us:
#   "Incompatible changes" — removals and signature changes; these break callers
#   "Compatible changes"   — additions; these are fine
#
# Note that adding a field to an exported struct is reported as compatible, and
# is: it only affects unkeyed composite literals, which Go's own compatibility
# promise already excludes.

set -uo pipefail

BASE="${1:-HEAD}"
PKG="github.com/aoiflux/libext"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

command -v go >/dev/null || { echo "missing go" >&2; exit 1; }

if ! command -v apidiff >/dev/null; then
	echo "installing apidiff..." >&2
	go install golang.org/x/exp/cmd/apidiff@latest || {
		echo "could not install apidiff; skipping" >&2
		exit 0
	}
	export PATH="$PATH:$(go env GOPATH)/bin"
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cd "$REPO_ROOT"

# Export the baseline tree into a scratch worktree so the working copy is left
# untouched — this script must never modify the repository it inspects.
git archive "$BASE" | (mkdir -p "$WORK/base" && tar -x -C "$WORK/base") || {
	echo "could not export $BASE" >&2
	exit 1
}

echo "baseline: $BASE"
apidiff -w "$WORK/old.api" "$WORK/base" 2>/dev/null || {
	echo "could not read baseline API" >&2
	exit 1
}

OUT=$(apidiff "$WORK/old.api" . 2>&1)
echo "$OUT"

if echo "$OUT" | grep -q '^Incompatible changes:'; then
	echo
	echo "FAIL: the exported surface changed incompatibly against $BASE" >&2
	exit 1
fi

echo
echo "OK: no incompatible changes to $PKG against $BASE"
