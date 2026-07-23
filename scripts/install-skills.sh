#!/usr/bin/env bash
# =============================================================================
# mysql-cli Skill Installer - Multi-Agent Support
# =============================================================================
# Installs mysql-cli skill definitions (mysql-shared / mysql-query / mysql-schema)
# for AI agents. Claude Code and Cursor use the native SKILL.md / .mdc formats;
# Codex, OpenCode, Copilot, Windsurf, and Aider receive the merged skill body
# appended (idempotently, between markers) to their instruction files.
#
# Usage:
#   ./scripts/install-skills.sh [--agent <agent>] [--project-dir <path>] [--no-global]
#
# Agents:
#   auto      - Auto-detect installed agents (default)
#   claude    - Claude Code    (.claude/skills/              + ~/.claude/skills/)
#   cursor    - Cursor         (.cursor/rules/*.mdc)
#   codex     - Codex CLI      (AGENTS.md                    + ~/.codex/instructions.md)
#   opencode  - OpenCode       (.opencode/instructions.md    + ~/.config/opencode/instructions.md)
#   copilot   - GitHub Copilot (.github/copilot-instructions.md)
#   windsurf  - Windsurf       (.windsurfrules               + ~/.windsurfrules)
#   aider     - Aider          (.aider.instructions.md; needs read: config - see hint)
#   all       - Install for every agent above
#
# Examples:
#   ./scripts/install-skills.sh                          # auto-detect
#   ./scripts/install-skills.sh --agent all --no-global  # all agents, project only
#   ./scripts/install-skills.sh --agent copilot --project-dir ~/my-project
# =============================================================================

set -euo pipefail
echo "⚠️  install-skills.sh is deprecated; use \`mysql-cli init\` instead." >&2
echo "    This script will be removed in a future release." >&2
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
AGENT="auto"
PROJECT_DIR=""
NO_GLOBAL=0

SKILLS=(mysql-shared mysql-query mysql-schema)
BEGIN_MARKER="<!-- mysql-cli skill: begin (auto-generated) -->"
END_MARKER="<!-- mysql-cli skill: end -->"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --agent) AGENT="$2"; shift 2 ;;
        --project-dir) PROJECT_DIR="$2"; shift 2 ;;
        --no-global) NO_GLOBAL=1; shift ;;
        -h|--help) sed -n '3,29p' "$0"; exit 0 ;;
        *) echo "Unknown arg: $1" >&2; exit 1 ;;
    esac
done

if [[ -z "$PROJECT_DIR" ]]; then
    PROJECT_DIR="$REPO_DIR"
fi

# --- helpers ----------------------------------------------------------------

# Body of a SKILL.md = content after the second '---' frontmatter delimiter.
skill_body() {
    awk 'BEGIN{c=0} /^---[[:space:]]*$/ {c++; if(c==2){f=1; next}} f' "$1"
}

# Concatenate all three skill bodies with headings.
skill_body_concat() {
    for skill in "${SKILLS[@]}"; do
        echo ""
        echo "## mysql-cli skill: ${skill}"
        echo ""
        skill_body "${REPO_DIR}/skills/${skill}/SKILL.md"
    done
}

# Write the merged skill body to an instruction file, idempotently. Re-running
# replaces the marked block in place instead of appending duplicates.
write_instruction_file() {
    local file="$1"
    mkdir -p "$(dirname "$file")"
    if [[ -f "$file" ]] && grep -qF "$BEGIN_MARKER" "$file"; then
        awk -v b="$BEGIN_MARKER" -v e="$END_MARKER" \
            '$0==b{f=1;next} $0==e{f=0;next} !f' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
    fi
    {
        echo ""
        echo "$BEGIN_MARKER"
        echo "<!-- Re-run mysql-cli install-skills.sh to update. Do not edit between markers. -->"
        skill_body_concat
        echo ""
        echo "$END_MARKER"
    } >> "$file"
}

# make_mdc <src_SKILL.md> <out.mdc> <cursor_description>
make_mdc() {
    local src="$1" out="$2" desc="$3"
    {
        echo "---"
        echo "description: $desc"
        echo "globs: *.sql"
        echo "alwaysApply: false"
        echo "---"
        skill_body "$src"
    } > "$out"
}

detect_agents() {
    local found=()
    [[ -d "$HOME/.claude" || -d "${PROJECT_DIR}/.claude" ]] && found+=("claude")
    [[ -d "$HOME/.cursor" || -d "${PROJECT_DIR}/.cursor" ]] && found+=("cursor")
    [[ -d "$HOME/.codex" || -f "${PROJECT_DIR}/AGENTS.md" ]] && found+=("codex")
    [[ -d "$HOME/.config/opencode" || -d "${PROJECT_DIR}/.opencode" ]] && found+=("opencode")
    [[ -d "${PROJECT_DIR}/.github" ]] && found+=("copilot")
    [[ -f "${PROJECT_DIR}/.windsurfrules" || -d "$HOME/.codeium" || -d "$HOME/.windsurf" ]] && found+=("windsurf")
    [[ -f "${PROJECT_DIR}/.aider.conf.yml" || -f "$HOME/.aider.conf.yml" ]] && found+=("aider")
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
    make_mdc "${REPO_DIR}/skills/mysql-shared/SKILL.md" "${target}/mysql-shared.mdc" \
        "mysql-cli shared rules: config, datasource, safety model, exit codes, error recovery, output formats"
    make_mdc "${REPO_DIR}/skills/mysql-query/SKILL.md" "${target}/mysql-query.mdc" \
        "Run SQL with mysql-cli: SELECT query, txn, DML (INSERT/UPDATE/DELETE), DDL"
    make_mdc "${REPO_DIR}/skills/mysql-schema/SKILL.md" "${target}/mysql-schema.mdc" \
        "Explore MySQL schema with mysql-cli: tables, databases, schema, sample, read, explore, analyze"
    echo "   ✅ Cursor (project):      $target"
    if [[ "$NO_GLOBAL" -eq 0 && -d "$HOME/.cursor" ]]; then
        local gtarget="$HOME/.cursor/rules"
        mkdir -p "$gtarget"
        cp "${target}"/mysql-*.mdc "$gtarget/"
        echo "   ✅ Cursor (global):        $gtarget"
    fi
}

install_codex() {
    write_instruction_file "${PROJECT_DIR}/AGENTS.md"
    echo "   ✅ Codex CLI (project):   ${PROJECT_DIR}/AGENTS.md"
    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        mkdir -p "$HOME/.codex"
        write_instruction_file "$HOME/.codex/instructions.md"
        echo "   ✅ Codex CLI (global):    $HOME/.codex/instructions.md"
    fi
}

install_opencode() {
    write_instruction_file "${PROJECT_DIR}/.opencode/instructions.md"
    echo "   ✅ OpenCode (project):    ${PROJECT_DIR}/.opencode/instructions.md"
    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        mkdir -p "$HOME/.config/opencode"
        write_instruction_file "$HOME/.config/opencode/instructions.md"
        echo "   ✅ OpenCode (global):     $HOME/.config/opencode/instructions.md"
    fi
    echo "   ℹ️  OpenCode also reads AGENTS.md; adjust the path if your setup differs."
}

install_copilot() {
    write_instruction_file "${PROJECT_DIR}/.github/copilot-instructions.md"
    echo "   ✅ GitHub Copilot:        ${PROJECT_DIR}/.github/copilot-instructions.md"
    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        echo "   ℹ️  Copilot has no global file; project-level only (per-repo)."
    fi
}

install_windsurf() {
    write_instruction_file "${PROJECT_DIR}/.windsurfrules"
    echo "   ✅ Windsurf (project):    ${PROJECT_DIR}/.windsurfrules"
    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        write_instruction_file "$HOME/.windsurfrules"
        echo "   ✅ Windsurf (global):     $HOME/.windsurfrules"
    fi
}

install_aider() {
    local file="${PROJECT_DIR}/.aider.instructions.md"
    write_instruction_file "$file"
    echo "   ✅ Aider (project):       $file"
    echo "   ℹ️  Add to .aider.conf.yml:  read: [.aider.instructions.md]"
    echo "      Or run: aider --read .aider.instructions.md"
    if [[ "$NO_GLOBAL" -eq 0 ]]; then
        local gfile="$HOME/.aider.instructions.md"
        write_instruction_file "$gfile"
        echo "   ✅ Aider (global):        $gfile"
        echo "      Add to ~/.aider.conf.yml:  read: [~/.aider.instructions.md]"
    fi
}

# --- main -------------------------------------------------------------------

echo "🔧 mysql-cli Skill Installer"
echo "   Agent:       $AGENT"
echo "   Project:     $PROJECT_DIR"
echo ""

run_install() {
    case "$1" in
        claude)   install_claude ;;
        cursor)   install_cursor ;;
        codex)    install_codex ;;
        opencode) install_opencode ;;
        copilot)  install_copilot ;;
        windsurf) install_windsurf ;;
        aider)    install_aider ;;
    esac
}

case "$AGENT" in
    auto)
        detected="$(detect_agents)"
        if [[ -z "$detected" ]]; then
            echo "   No agent detected; defaulting to Claude Code."
            install_claude
        else
            for a in $detected; do run_install "$a"; done
        fi
        ;;
    claude|cursor|codex|opencode|copilot|windsurf|aider) run_install "$AGENT" ;;
    all)
        for a in claude cursor codex opencode copilot windsurf aider; do run_install "$a"; done
        ;;
    *)
        echo "❌ Unknown agent: $AGENT" >&2
        echo "   Supported: auto, claude, cursor, codex, opencode, copilot, windsurf, aider, all" >&2
        exit 1
        ;;
esac

echo ""
echo "✅ Done. Verify with: mysql-cli databases -f json"
