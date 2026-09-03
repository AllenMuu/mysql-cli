#!/usr/bin/env python3
"""Codex PermissionRequest hook: auto-allow proven read-only mysql-cli calls,
keep everything else in Codex's native human-approval flow.

Codex does NOT support Claude Code's PreToolUse "ask" decision (it is parsed
but unsupported: the hook run is marked failed and the tool call CONTINUES).
So this hook never asks and never denies. Its only job is to approve what it
can PROVE is a read-only mysql-cli invocation:

  tool_name != "Bash"                    -> no decision
  command is not mysql-cli              -> no decision
  shlex parse failure                    -> no decision
  any shell syntax                       -> no decision
  mysql-cli with --write/--ddl/--yes     -> no decision  (human approval)
  mysql-cli read-only, single command    -> allow        (skip the prompt)

"no decision" = exit 0 with empty stdout: Codex then runs its normal approval
flow. The fail direction is fail-to-prompt -- any uncertainty yields an extra
human prompt, never a silently approved write.

This hook pairs with rules/mysql-cli-write-guard.rules, a coarse prefix gate
that routes mysql-cli invocations (plus common wrapper prefixes) into the
approval path with decision="prompt". This script is the precise filter.

Detection notes (shared semantics with the Claude PreToolUse guard):
- Write flags match "--write" as a whole token OR "--write=..." (pflag's
  --flag=value form) -- exact-token matching alone lets "--write=true" slip
  through as a "read".
- Provable-safety uses a character WHITELIST outside quotes, not a blacklist
  of operators: anything other than plain word/path punctuation (operators,
  globs, braces, newlines, backslashes, $, backticks, ~) is unprovable. This
  is why newline-separated compound commands, brace expansion ("{--write,}"),
  and glob injection can never be auto-allowed. shlex alone cannot be trusted
  here: it treats "\n" as plain whitespace, hiding command separators.
- Inside double quotes only the closing quote is special here; $, backtick,
  and backslash are still treated as uncertain because they expand or escape.
- rtk / sudo / env / nohup / command prefixes are skipped when locating the
  command; `env VAR=... mysql-cli` cannot be proven and stays unapproved.

覆盖范围限制（Codex 特有，详见 docs/agent-integration.md）：
- PermissionRequest 仅在 Codex 原本准备发起 approval 时运行，不会为
  "不需要 approval 的命令"自动触发；
- 本 hook 仅拦截 Bash tool 路径，非 Bash 调用 mysql-cli 不经过本 hook；
- hooks/rules 是 guardrail 而非完整 security boundary：hooks 被关闭、
  project/hook 未 trust、rules 未加载、bypassPermissions 等模式下不生效。
"""
import json
import os
import shlex
import string
import sys

WRITE_FLAGS = ("--write", "--ddl", "--yes")
PREFIXES = {"rtk", "sudo", "env", "nohup", "command"}
RTK_SUB = {"proxy"}  # `rtk proxy <cmd>` form
# Characters permitted OUTSIDE quotes in a provable read-only command: word
# characters, path/flag punctuation, and separators. Everything else -- shell
# operators (;|&><), expansion ($ `), globs (*?[]), braces, ~, newlines,
# backslashes -- makes the whole command unprovable. Deny-by-default whitelist.
_SAFE_OUTSIDE = frozenset(string.ascii_letters + string.digits + "-_./:,=@%+ \t")
# Inside double quotes these still expand or escape -> uncertain.
_DOUBLE_UNSAFE = frozenset("$`\\")


def _is_provable_read(command):
    """Quote-aware whitelist scan: anything not provably literal -> False."""
    in_single = in_double = False
    for c in command:
        if in_single:
            if c == "'":
                in_single = False
        elif in_double:
            if c == '"':
                in_double = False
            elif c in _DOUBLE_UNSAFE:
                return False
        else:
            if c == "'":
                in_single = True
            elif c == '"':
                in_double = True
            elif c not in _SAFE_OUTSIDE:
                return False
    return not (in_single or in_double)  # unbalanced quotes -> unprovable


def _has_write_flag(tokens):
    """Match --write/--ddl/--yes as whole tokens or pflag's --flag=value form."""
    return any(
        tok == flag or tok.startswith(flag + "=")
        for flag in WRITE_FLAGS
        for tok in tokens
    )


def _is_proven_mysql_cli_read(command):
    """True only if command is provably a single read-only mysql-cli call."""
    if not _is_provable_read(command):
        return False
    try:
        tokens = shlex.split(command)
    except ValueError:
        return False  # unreachable after the scan; belt and suspenders
    if not tokens:
        return False
    # Locate the real command word, skipping wrapper prefixes.
    i = 0
    while i < len(tokens):
        base = os.path.basename(tokens[i])
        if base in PREFIXES:
            i += 1
            if base == "rtk" and i < len(tokens) and tokens[i] in RTK_SUB:
                i += 1
            continue
        break
    if i >= len(tokens) or os.path.basename(tokens[i]) != "mysql-cli":
        return False  # first real command is not mysql-cli
    if _has_write_flag(tokens):
        return False  # write gate flag present -> human approval
    return True


def _allow():
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PermissionRequest",
            "decision": {"behavior": "allow"},
        }
    }))


def main():
    raw = sys.stdin.read()
    if not raw.strip():
        return  # no/empty input -> no decision
    try:
        event = json.loads(raw)
    except json.JSONDecodeError:
        return  # malformed input -> no decision
    if event.get("tool_name") != "Bash":
        return
    command = (event.get("tool_input") or {}).get("command", "")
    if not command:
        return
    if _is_proven_mysql_cli_read(command):
        _allow()
    # everything else: empty stdout + exit 0 -> Codex normal approval flow


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # fail-to-prompt, never fail-open
        sys.stderr.write(
            "mysql-write-guard (codex): no decision (fail-to-prompt) "
            "due to exception: %s\n" % e)
        sys.exit(0)
