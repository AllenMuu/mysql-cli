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
        is_mysql = cmd == "mysql-cli" or cmd.endswith("/mysql-cli")
        if is_mysql and WRITE_FLAGS.intersection(tokens):
            return True

    # Fallback: strict front boundary (start/whitespace) so a flag inside a
    # quoted SQL literal is not matched; back boundary allows quote/separator
    # close so wrapped calls like `bash -c "mysql-cli ... --write"` are caught.
    if re.search(r"\bmysql-cli\b", command):
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
        sys.stderr.write(f"mysql-write-guard: fail-open: {e}\n")
        sys.exit(0)
