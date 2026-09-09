#!/usr/bin/env bash
#
# Scan for connector-shaped credentials. One scanner, four callers.
#
#   scripts/scan-secrets.sh FILE...     scan those files, report file:line:value
#   … | scripts/scan-secrets.sh         scan stdin, report the value
#   … | scripts/scan-secrets.sh -q      scan stdin, report nothing, exit 1 on a hit
#
# Exits 1 when it found something, 0 when it did not, so a caller can add its
# own framing around the hit. The shapes and the allow-list come from
# .githooks/lib.sh, which is the one home for both: the pre-commit hook, the
# commit-msg hook, the `secrets` job in ci.yml and the pull-request text check
# all end up here, and a placeholder legal in one has to be legal in all four.
#
# -q exists for the pull-request check. A hit there is text somebody typed into
# a box, and the run log of a public repository is world-readable — printing the
# value would publish the credential a second time. Locally, and for a file
# already tracked in a public repo, the value is what makes the hit findable, so
# it is printed.

set -uo pipefail

quiet=0
if [ "${1:-}" = "-q" ]; then
	quiet=1
	shift
fi

root=$(git rev-parse --show-toplevel 2>/dev/null) ||
	root=$(cd "$(dirname "$0")/.." && pwd)
. "$root/.githooks/lib.sh"

# -H and -n on every call, so a match reads the same whether one file was named
# or a hundred. A connector shape holds no colon, so the value is always what
# follows the last one — and for stdin, where there is no prefix, that is the
# whole line.
if [ "$#" -gt 0 ]; then
	matches=$(grep -HnoE "$CONNECTOR_RE" -- "$@" 2>/dev/null)
else
	matches=$(grep -oE "$CONNECTOR_RE" 2>/dev/null)
fi

hits=$(
	printf '%s\n' "$matches" | while IFS= read -r line; do
		[ -z "$line" ] && continue
		value=${line##*:}
		printf '%s\n' "$value" | grep -qiE "$PLACEHOLDER_RE" && continue
		printf '%s\n' "$line"
	done | sort -u
)

[ -z "$hits" ] && exit 0

[ "$quiet" -eq 0 ] && printf '%s\n' "$hits"
exit 1
