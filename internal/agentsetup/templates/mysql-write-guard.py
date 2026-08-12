#!/usr/bin/env python3
"""PreToolUse hook: force a human confirmation prompt for mysql-cli writes.

Shared by agents whose hook system is compatible with Claude Code's PreToolUse
contract (Claude Code, CodeBuddy). The hook inspects the Bash command from
stdin; if it is a mysql-cli invocation carrying any write gate flag
(--write / --ddl / --yes), it returns permissionDecision="ask" so the agent
surfaces a permission prompt -- pulling a human into the loop instead of
letting the AI self-confirm via --yes.

Read-only mysql-cli calls (no write flag) pass through silently.

Detection notes:
- shlex token matching: a flag-looking string inside a quoted SQL literal (e.g.
  "SELECT '--write' ...") stays one quoted token and is NOT mistaken for the flag.
- A regex fallback anchors flags as standalone tokens; the back boundary also
  allows quote/shell-separator close so `bash -c "mysql-cli ... --write"` is caught.
- rtk / sudo / env / nohup / command prefixes are skipped when locating the cmd.
- Fail-open on parse errors: a broken hook must not block all Bash usage.

覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/shell 执行路径。
agent 若通过非 Bash tool（如 Python subprocess、直接 exec 系统调用）调用 mysql-cli，
不会触发本 hook。如需更强保障，应在 agent 配置中禁用非 Bash 执行路径，
或依赖 mysql-cli 内部的退出码契约（写操作缺 --write/--ddl/--yes 时以 exit 3/4/5 拒绝）。
"""
import json
import os
import re
import shlex
import sys

WRITE_FLAGS = {"--write", "--ddl", "--yes"}
PREFIXES = {"rtk", "sudo", "env", "nohup", "command"}
RTK_SUB = {"proxy"}  # `rtk proxy <cmd>` form


def _command_word(tokens):
    """Return the first real command token, skipping rtk/sudo/env wrappers."""
    i = 0
    while i < len(tokens):
        base = os.path.basename(tokens[i])
        if base in PREFIXES:
            i += 1
            if base == "rtk" and i < len(tokens) and tokens[i] in RTK_SUB:
                i += 1
            continue
        return base
    return ""


def _is_mysql_cli_write(command):
    """True if command runs mysql-cli with a write gate flag."""
    try:
        tokens = shlex.split(command)
    except ValueError:
        tokens = None

    if tokens:
        cmd = _command_word(tokens)
        # cmd 是 basename 后的结果，已剥掉目录；endswith("/mysql-cli") 在 basename
        # 后永远为 false（原代码冗余，已清理）。只比较 == "mysql-cli"。
        is_mysql = cmd == "mysql-cli"
        if is_mysql and WRITE_FLAGS.intersection(tokens):
            return True

    # Fallback: 仅在 shlex 解析失败时触发（极罕见，通常是未闭合引号）。
    # 收紧 mysql-cli 的前边界到 ^ 或空白，避免 `echo "mysql-cli --write"` 这类
    # 把 mysql-cli 当字符串字面量传给其他命令的误报（原 \b 把 "-mysql-cli" 也
    # 算边界，导致引号内的 mysql-cli 字面量被误判为调用）。
    # 权衡：会漏掉 `bash -c "mysql-cli ... --write"` 这种 wrapped 调用——但仅在
    # shlex 失败时才漏；shlex 正常时走上面的 token 化路径会正确识别。
    if re.search(r"(?:^|\s)mysql-cli\b", command):
        for flag in WRITE_FLAGS:
            if re.search(r"(?:^|\s)" + re.escape(flag) + r"(?=[\s\"';|&]|$)", command):
                return True
    return False


def main():
    raw = sys.stdin.read()
    if not raw.strip():
        return
    try:
        event = json.loads(raw)
    except json.JSONDecodeError:
        return

    if event.get("tool_name") != "Bash":
        return
    command = (event.get("tool_input") or {}).get("command", "")
    if not command or not _is_mysql_cli_write(command):
        return

    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "ask",
            "permissionDecisionReason": (
                "mysql-cli write operation (--write/--ddl/--yes) requires human "
                "confirmation. Approve only if you intend to modify the database."
            ),
        }
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # never let a bug block all Bash
        sys.stderr.write("mysql-write-guard: *** WARNING *** fail-open (confirmation bypassed) due to exception: %s\n" % e)
        sys.exit(0)
