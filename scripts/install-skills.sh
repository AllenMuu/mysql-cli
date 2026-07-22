#!/usr/bin/env bash
# =============================================================================
# mysql-cli Skill Installer — Multi-Agent Support
# =============================================================================
# Installs mysql-cli skill definitions for various AI agents.
#
# Usage:
#   ./scripts/install-skills.sh [--agent <agent>] [--project-dir <path>]
#
# Agents:
#   claude     — Claude Code (.claude/skills/)          [default]
#   cursor     — Cursor (.cursor/rules/)
#   opencode   — OpenCode (.claude/skills/, same format)
#   all        — Install for all detected agents
#
# Examples:
#   ./scripts/install-skills.sh                          # install for Claude Code (project)
#   ./scripts/install-skills.sh --agent cursor           # install for Cursor
#   ./scripts/install-skills.sh --agent all              # install for all agents
#   ./scripts/install-skills.sh --project-dir ~/my-project
# =============================================================================

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT="${1:-claude}"
PROJECT_DIR_FLAG=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --agent) AGENT="$2"; shift 2 ;;
        --project-dir) PROJECT_DIR_FLAG="$2"; shift 2 ;;
        *) shift ;;
    esac
done

if [[ -n "$PROJECT_DIR_FLAG" ]]; then
    PROJECT_DIR="$PROJECT_DIR_FLAG"
fi

echo "🔧 mysql-cli Skill Installer"
echo "   Agent:       $AGENT"
echo "   Project:     $PROJECT_DIR"
echo ""

install_claude() {
    local target="${PROJECT_DIR}/.claude/skills/mysql"
    mkdir -p "$(dirname "$target")"
    if [[ -d "$target" ]]; then
        echo "   ℹ️  $target already exists, updating..."
    fi
    cp -r "${PROJECT_DIR}/skills/mysql" "$target"
    echo "   ✅ Installed for Claude Code: $target"

    # Also install user-wide
    local global_target="$HOME/.claude/skills/mysql"
    mkdir -p "$(dirname "$global_target")"
    cp -r "${PROJECT_DIR}/skills/mysql" "$global_target"
    echo "   ✅ Installed globally: $global_target"
}

install_cursor() {
    local target="${PROJECT_DIR}/.cursor/rules/mysql-cli.mdc"
    mkdir -p "$(dirname "$target")"
    if [[ ! -f "${PROJECT_DIR}/.cursor/rules/mysql-cli.mdc" ]]; then
        echo "   ⚠️  Cursor rule file (.cursor/rules/mysql-cli.mdc) not found."
        echo "      Create it from the skill definition, or use --agent claude instead."
    else
        echo "   ✅ Cursor rule exists: $target"
    fi
}

install_opencode() {
    # OpenCode uses the same .claude/skills/ convention
    install_claude
}

install_all() {
    install_claude
    install_cursor
    echo ""
    echo "   📝 For other agents (Copilot, Aider, Continue, Windsurf),"
    echo "      see README.md → Usage with AI Agents section."
}

case "$AGENT" in
    claude)   install_claude ;;
    cursor)   install_cursor ;;
    opencode) install_opencode ;;
    all)      install_all ;;
    *)
        echo "❌ Unknown agent: $AGENT"
        echo "   Supported: claude, cursor, opencode, all"
        exit 1
        ;;
esac

echo ""
echo "✅ Done. mysql-cli is now available for $AGENT!"
echo "   Run \`mysql-cli databases -f json\` to verify connectivity."
