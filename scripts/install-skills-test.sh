#!/usr/bin/env bash
# =============================================================================
# install-skills-test.sh - Tests for install-skills.sh (all 7 agents)
# =============================================================================
# Verifies each agent installs the right files, the merged skill body is
# embedded, installation is idempotent, each --agent <name> works standalone,
# and --no-global skips global paths. Uses a temp dir + temp HOME; never
# touches the real $HOME.
# =============================================================================
set -o pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$HERE/install-skills.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass + 1)); }
ko()  { echo "FAIL $1"; fail=$((fail + 1)); }
assert_exists()   { [[ -e "$1" ]] && ok "$2" || ko "$2 ($1 missing)"; }
assert_missing()  { [[ ! -e "$1" ]] && ok "$2" || ko "$2 ($1 should not exist)"; }
assert_contains() { grep -qF "$2" "$1" 2>/dev/null && ok "$3" || ko "$3 ($1 missing content)"; }
assert_count() {
    local n; n=$(grep -cF "$2" "$1" 2>/dev/null) || true
    [[ "$n" == "$3" ]] && ok "$4 (count=$n)" || ko "$4 (count=$n, want $3)"
}

# Run installer into a temp project (no global, to keep the real $HOME clean).
run() { bash "$SCRIPT" --agent "$1" --project-dir "$TMP/proj" --no-global >/dev/null 2>&1; }

echo "== 1. all agents produce expected files =="
run all
for s in mysql-shared mysql-query mysql-schema; do
    assert_exists  "$TMP/proj/.claude/skills/$s/SKILL.md" "claude $s"
    assert_exists  "$TMP/proj/.cursor/rules/$s.mdc"       "cursor $s"
    assert_contains "$TMP/proj/.cursor/rules/$s.mdc" "alwaysApply:" "cursor $s .mdc frontmatter"
done
assert_exists   "$TMP/proj/AGENTS.md"                          "codex AGENTS.md"
assert_contains "$TMP/proj/AGENTS.md" "mysql-cli skill: begin" "codex marker"
assert_exists   "$TMP/proj/.opencode/instructions.md"          "opencode file"
assert_contains "$TMP/proj/.opencode/instructions.md" "mysql-cli skill: begin" "opencode marker"
assert_exists   "$TMP/proj/.github/copilot-instructions.md"    "copilot file"
assert_contains "$TMP/proj/.github/copilot-instructions.md" "mysql-cli skill: begin" "copilot marker"
assert_exists   "$TMP/proj/.windsurfrules"                     "windsurf file"
assert_contains "$TMP/proj/.windsurfrules" "mysql-cli skill: begin" "windsurf marker"
assert_exists   "$TMP/proj/.aider.instructions.md"             "aider file"
assert_contains "$TMP/proj/.aider.instructions.md" "mysql-cli skill: begin" "aider marker"

echo ""
echo "== 2. merged skill body is embedded =="
assert_contains "$TMP/proj/AGENTS.md" "## mysql-cli skill: mysql-shared" "shared heading"
assert_contains "$TMP/proj/AGENTS.md" "## mysql-cli skill: mysql-query"  "query heading"
assert_contains "$TMP/proj/AGENTS.md" "## mysql-cli skill: mysql-schema" "schema heading"
assert_contains "$TMP/proj/AGENTS.md" "READONLY_VIOLATION" "shared exit-code table"
assert_contains "$TMP/proj/AGENTS.md" "mysql-cli query"    "query command ref"

echo ""
echo "== 3. idempotent (re-run keeps one marker block) =="
run all
for f in AGENTS.md .opencode/instructions.md .github/copilot-instructions.md .windsurfrules .aider.instructions.md; do
    assert_count "$TMP/proj/$f" "mysql-cli skill: begin" 1 "idempotent $f"
done

echo ""
echo "== 4. each --agent <name> works standalone =="
for agent in claude cursor codex opencode copilot windsurf aider; do
    bash "$SCRIPT" --agent "$agent" --project-dir "$TMP/indiv" --no-global >/dev/null 2>&1
done
assert_exists "$TMP/indiv/.claude/skills/mysql-shared/SKILL.md"   "standalone claude"
assert_exists "$TMP/indiv/.cursor/rules/mysql-shared.mdc"         "standalone cursor"
assert_exists "$TMP/indiv/AGENTS.md"                              "standalone codex"
assert_exists "$TMP/indiv/.opencode/instructions.md"              "standalone opencode"
assert_exists "$TMP/indiv/.github/copilot-instructions.md"        "standalone copilot"
assert_exists "$TMP/indiv/.windsurfrules"                         "standalone windsurf"
assert_exists "$TMP/indiv/.aider.instructions.md"                 "standalone aider"

echo ""
echo "== 5. --no-global skips global; default writes global (temp HOME) =="
rm -rf "$TMP/h1" "$TMP/h2"
HOME="$TMP/h1" bash "$SCRIPT" --agent codex --project-dir "$TMP/p1" --no-global >/dev/null 2>&1
assert_missing "$TMP/h1/.codex/instructions.md" "no-global skips global codex"
HOME="$TMP/h2" bash "$SCRIPT" --agent codex --project-dir "$TMP/p2" >/dev/null 2>&1
assert_exists  "$TMP/h2/.codex/instructions.md" "default writes global codex"

echo ""
echo "Results: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]] && exit 0 || exit 1
