#!/usr/bin/env bash
# =============================================================================
# skill-format-check.sh - Validate SKILL.md frontmatter for mysql-cli skills.
# =============================================================================
# Checks SKILL.md file(s) for:
#   - opening and closing '---' frontmatter delimiters
#   - required fields: name, version, description, metadata
#   - name matches its parent directory
#   - version is semver (N.N.N)
#
# Usage: ./scripts/skill-format-check.sh [skills_dir | SKILL.md]   (default: skills)
#   - a directory is scanned for <dir>/*/SKILL.md
#   - a single SKILL.md file is checked directly
# Exit: 0 = all valid, 1 = at least one invalid, 2 = no skills / target missing
# =============================================================================
set -euo pipefail

TARGET="${1:-skills}"
if [[ ! -e "$TARGET" ]]; then
    echo "error: not found: $TARGET" >&2
    exit 2
fi

check_skill() {
    local file="$1"
    local dir
    dir="$(basename "$(dirname "$file")")"
    local errs=0

    if ! head -1 "$file" 2>/dev/null | grep -q '^---[[:space:]]*$'; then
        echo "FAIL $file: missing opening '---'"
        return 1
    fi

    # Frontmatter body = lines between the first and second '---'.
    local fm
    fm=$(awk 'NR==1{next} /^---[[:space:]]*$/{exit} {print}' "$file")
    if [[ -z "$fm" ]]; then
        echo "FAIL $file: missing closing '---'"
        return 1
    fi

    local field
    for field in name version description metadata; do
        if ! grep -qE "^${field}:" <<<"$fm"; then
            echo "FAIL $file: missing field '$field'"
            errs=$((errs + 1))
        fi
    done

    local name ver
    name=$(grep -E '^name:' <<<"$fm" | head -1 | sed -E 's/^name:[[:space:]]*//; s/[[:space:]]*$//; s/^"//; s/"$//')
    ver=$(grep -E '^version:' <<<"$fm" | head -1 | sed -E 's/^version:[[:space:]]*//; s/[[:space:]]*$//; s/^"//; s/"$//')

    if [[ -n "$name" && "$name" != "$dir" ]]; then
        echo "FAIL $file: name '$name' != directory '$dir'"
        errs=$((errs + 1))
    fi
    if ! grep -qE '^[0-9]+\.[0-9]+\.[0-9]+' <<<"$ver"; then
        echo "FAIL $file: invalid version '$ver' (want semver N.N.N)"
        errs=$((errs + 1))
    fi

    if [[ $errs -gt 0 ]]; then
        return 1
    fi
    echo "OK   $file"
    return 0
}

shopt -s nullglob
files=()
if [[ -f "$TARGET" ]]; then
    files=("$TARGET")
elif [[ -d "$TARGET" ]]; then
    files=("$TARGET"/*/SKILL.md)
    if [[ ${#files[@]} -eq 0 ]]; then
        echo "error: no SKILL.md found under $TARGET/*/" >&2
        exit 2
    fi
fi

fail=0
for f in "${files[@]}"; do
    check_skill "$f" || fail=1
done

exit "$fail"
