#!/usr/bin/env bash
# =============================================================================
# mysql-cli Skill Installer - Multi-Agent Support
# =============================================================================
# Installs mysql-cli skill definitions (mysql-shared / mysql-query / mysql-schema)
# for AI agents that support them. Defaults to auto-detection.
#
# Usage:
#   ./scripts/install-skills.sh [--agent <agent>] [--project-dir <path>] [--no-global]
#
# Agents:
#   auto     - Auto-detect installed agents (default)
#   claude   - Claude Code  (.claude/skills/  + ~/.claude/skills/)
#   cursor   - Cursor       (.cursor/rules/*.mdc)
#   all      - Install for all natively supported agents (claude + cursor)
#
# Other agents (Codex CLI, OpenCode, Aider, Copilot, Windsurf) read AGENTS.md or
# their own rule files; this script prints guidance for them.
#
# Examples:
#   ./scripts/install-skills.sh                          # auto-detect
#   ./scripts/install-skills.sh --agent claude           # Claude Code only
#   ./scripts/install-skills.sh --agent all --no-global  # all agents, project only
#   ./scripts/install-skills.sh --project-dir ~/my-project
# =============================================================================

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT="auto"
PROJECT_DIR=""
NO_GLOBAL=0

SKILLS=(mysql-shared mysql-query mysql-schema)

while [[ $# -gt 0 ]]; do
    case "$1" in
        --agent) AGENT="$2"; shift 2 ;;
        --project-dir) PROJECT_DIR="$2"; shift 2 ;;
        --no-global) NO_GLOBAL=1; shift ;;
        -h|--help)
            sed -n '3,26p' "$0"
            exit 0
            ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$PROJECT_DIR" ]]; then
    PROJECT_DIR="$REPO_DIR"
fi

# --- helpers ----------------------------------------------------------------

# make_mdc <src_SKILL.md> <out.mdc> <cursor_description>
# Strips the YAML frontmatter from a SKILL.md and wraps the body in a Cursor
# .mdc frontmatter (description / globs / alwaysApply).
make_mdc() {
    local src="$1" out="$2" desc="$3"
    {
        echo "---"
        echo "description: $desc"
        echo "globs: *.sql"
        echo "alwaysApply: false"
        echo "---"
        # Body = content after the second '---' line of the source frontmatter.
        awk 'BEGIN{c=0} /^---[[:space:]]*$/ {c++; if(c==2){f=1; next}} f' "$src"
    } > "$out"
}

detect_agents() {
    local found=()
    if [[ -d "$HOME/.claude" || -d "${PROJECT_DIR}/.claude" ]]; then
        found+=("claude")
    fi
    if [[ -d "$HOME/.cursor" || -d "${PROJECT_DIR}/.cursor" ]]; then
        found+=("cursor")
    fi
    echo "${found[@]}"
}

# --- installers -------------------------------------------------------------

install_claude() {
    local target="${PROJECT_DIR}/.claude/skills"
    mkdir -p "$target"
    for skill in "${SKILLS[@]}"; do
        rm -rf "${target}/${skill}"
        cp -r "${REPO_DIR}/skills/${skill}" "$target/"
    done
    echo "   ✅ Claude Code (project): $target"

    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        local gtarget="$HOME/.claude/skills"
        mkdir -p "$gtarget"
        for skill in "${SKILLS[@]}"; do
            rm -rf "${gtarget}/${skill}"
            cp -r "${REPO_DIR}/skills/${skill}" "$gtarget/"
        done
        echo "   ✅ Claude Code (global):  $gtarget"
    fi
}

install_cursor() {
    local target="${PROJECT_DIR}/.cursor/rules"
    mkdir -p "$target"
    make_mdc "${REPO_DIR}/skills/mysql-shared/SKILL.md" \
        "${target}/mysql-shared.mdc" \
        "mysql-cli shared rules: config, datasource, safety model, exit codes, error recovery, output formats"
    make_mdc "${REPO_DIR}/skills/mysql-query/SKILL.md" \
        "${target}/mysql-query.mdc" \
        "Run SQL with mysql-cli: SELECT query, txn, DML (INSERT/UPDATE/DELETE), DDL"
    make_mdc "${REPO_DIR}/skills/mysql-schema/SKILL.md" \
        "${target}/mysql-schema.mdc" \
        "Explore MySQL schema with mysql-cli: tables, databases, schema, sample, read, explore, analyze"
    echo "   ✅ Cursor (project):      $target"

    if [[ "$NO_GLOBAL" -eq 0 && -d "$HOME/.cursor" ]]; then
        local gtarget="$HOME/.cursor/rules"
        mkdir -p "$gtarget"
        cp "${target}/mysql-shared.mdc" "${target}/mysql-query.mdc" "${target}/mysql-schema.mdc" "$gtarget/"
        echo "   ✅ Cursor (global):        $gtarget"
    fi
}

print_other_agents() {
    cat <<'EOF'
   ℹ️  Other agents (Codex CLI, OpenCode, Aider, Copilot, Windsurf):
      These agents read AGENTS.md or their own rule files. Reference the skill
      files directly, e.g. append to your project's AGENTS.md:

        See skills/mysql-shared/SKILL.md, skills/mysql-query/SKILL.md,
        and skills/mysql-schema/SKILL.md for mysql-cli usage.

      Or copy the SKILL.md bodies into your agent's instruction file.
EOF
}

# --- main -------------------------------------------------------------------

echo "🔧 mysql-cli Skill Installer"
echo "   Agent:       $AGENT"
echo "   Project:     $PROJECT_DIR"
echo ""

case "$AGENT" in
    auto)
        detected="$(detect_agents)"
        if [[ -z "$detected" ]]; then
            echo "   No agent detected; defaulting to Claude Code."
            install_claude
        else
            for a in $detected; do
                case "$a" in
                    claude) install_claude ;;
                    cursor) install_cursor ;;
                esac
            done
        fi
        print_other_agents
        ;;
    claude)  install_claude; print_other_agents ;;
    cursor)  install_cursor; print_other_agents ;;
    all)     install_claude; install_cursor; print_other_agents ;;
    *)
        echo "❌ Unknown agent: $AGENT" >&2
        echo "   Supported: auto, claude, cursor, all" >&2
        exit 1
        ;;
esac

echo ""
echo "✅ Done. Verify with: mysql-cli databases -f json"
