#!/usr/bin/env bash
# Self-test for skill-format-check.sh using good/bad fixtures.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/../skill-format-check.sh"
DATA="$HERE/testdata"
fail=0

# A valid skill must be accepted.
if "$SCRIPT" "$DATA/good/SKILL.md" >/dev/null; then
    echo "PASS good skill accepted"
else
    echo "FAIL good skill was rejected"
    fail=1
fi

# Each malformed skill must be rejected.
for bad in "$DATA"/bad-*/SKILL.md; do
    [[ -f "$bad" ]] || continue
    name="$(basename "$(dirname "$bad")")"
    if "$SCRIPT" "$bad" >/dev/null 2>&1; then
        echo "FAIL $name should have been rejected"
        fail=1
    else
        echo "PASS $name rejected"
    fi
done

exit "$fail"
