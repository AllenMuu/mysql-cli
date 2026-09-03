#!/usr/bin/env python3
"""PreToolUse hook: force a human confirmation prompt for mysql-cli writes.

Shared by agents whose hook system is compatible with Claude Code's PreToolUse
contract (Claude Code, CodeBuddy, TRAE). The hook inspects the shell command
from stdin; if it is a mysql-cli invocation carrying any write gate flag
(--write / --ddl / --yes), it returns permissionDecision="ask" so the agent
surfaces a permission prompt -- pulling a human into the loop instead of
letting the AI self-confirm via --yes.

Read-only mysql-cli calls (no write flag) pass through silently.

Detection notes (mirrored by templates/pi-mysql-write-guard.ts -- keep the two
implementations behaviorally identical, they are tested against the same
matrix in hook_guard_test.go):
- The command is split on unquoted shell control characters (; & | ( ) and
  newlines) with shell comments dropped; each segment is analyzed
  independently, so `ls; mysql-cli ...`, `(mysql-cli ...)` and
  `cmd # trailing comment` are all handled.
- Token matching: a write flag counts only as a standalone token (`--write`)
  or a `--flag=value` form (`--write=true`, `--ddl=1`), so a flag-looking
  string inside a quoted SQL literal (e.g. "SELECT '--write' ...") stays one
  token and is NOT mistaken for the flag.
- Wrapped invocations: `bash/sh/zsh -c "<script>"` payloads are analyzed
  recursively (anywhere in the token list, covering `sudo bash -lc ...` and
  `docker exec ... bash -c ...`), so `bash -c "mysql-cli ... --write"` is
  caught, including multi-command payloads like `bash -c "echo hi; ... --write"`.
- Segments whose command word is echo/printf are skipped: their arguments are
  data to print, not commands to run (`echo mysql-cli --write` does not write).
- Locating the command skips rtk/sudo/env/nohup/command prefixes; a bare
  `mysql-cli` token anywhere in the segment also counts, which covers
  pass-through wrappers such as xargs/timeout and `env VAR=... mysql-cli`.
- If a segment cannot be tokenized (unclosed quote), a regex fallback matches
  mysql-cli anchored at ^/whitespace/quote and the flag as a standalone token
  or =value form (back boundary allows whitespace/quote/;/|/&/=/end).
- Fail-open on internal errors: a broken hook must not block all Bash usage.

覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/RunCommand 执行路径。
agent 若通过非 Bash tool（如 Python subprocess、直接 exec 系统调用）调用 mysql-cli，
或先把写调用写进已有脚本文件再执行（bash fix.sh），不会触发本 hook。
如需更强保障，应在 agent 配置中禁用非 Bash 执行路径，
或依赖 mysql-cli 内部的退出码契约（写操作缺 --write/--ddl/--yes 时以 exit 3/4/5 拒绝）。
"""
import json
import re
import shlex
import sys

WRITE_FLAGS = ("--write", "--ddl", "--yes")
PREFIXES = {"rtk", "sudo", "env", "nohup", "command"}
RTK_SUB = {"proxy"}  # `rtk proxy <cmd>` form
SHELLS = {"bash", "sh", "zsh", "dash", "ksh", "ash"}
DATA_SINKS = {"echo", "printf"}  # cmd words whose arguments are data, not commands
SEGMENT_SEPS = set(";&|()\n")
MAX_DEPTH = 4  # `bash -c 'bash -c ...'` recursion guard
# Claude Code/CodeBuddy use "Bash"; TRAE's standardized tool name is
# "RunCommand" (agentsetup generates matcher "RunCommand" for TRAE).
TOOL_NAMES = {"Bash", "RunCommand"}


def _basename(token):
    """Basename handling both / and \\ separators (matches the TS guard)."""
    token = token.rsplit("/", 1)[-1]
    return token.rsplit("\\", 1)[-1]


def _command_word(tokens):
    """Return the first real command token, skipping rtk/sudo/env wrappers."""
    i = 0
    while i < len(tokens):
        base = _basename(tokens[i])
        if base in PREFIXES:
            i += 1
            if base == "rtk" and i < len(tokens) and tokens[i] in RTK_SUB:
                i += 1
            continue
        return base
    return ""


def _has_write_flag(tokens):
    """True if any token is exactly a write flag or a `--flag=value` form."""
    for t in tokens:
        for flag in WRITE_FLAGS:
            if t == flag or t.startswith(flag + "="):
                return True
    return False


def _split_segments(command):
    """Split on unquoted shell control chars (; & | ( ) newline) and drop
    shell comments (an unquoted # at the start of a word, extending to the
    end of the line -- text inside comments never splits segments, so
    `# note; mysql-cli --write` stays one inert comment). Quotes stay intact
    so each segment remains independently analyzable; backslash escapes are
    honored so `\\;` never splits."""
    segments, cur = [], []
    in_single = in_double = False
    i, n = 0, len(command)
    while i < n:
        c = command[i]
        if in_single:
            cur.append(c)
            if c == "'":
                in_single = False
        elif in_double:
            if c == "\\" and i + 1 < n:
                cur.append(c)
                cur.append(command[i + 1])
                i += 1
            else:
                cur.append(c)
                if c == '"':
                    in_double = False
        else:
            if c == "\\" and i + 1 < n:
                cur.append(c)
                cur.append(command[i + 1])
                i += 1
            elif c == "'":
                in_single = True
                cur.append(c)
            elif c == '"':
                in_double = True
                cur.append(c)
            elif c == "#" and (i == 0 or command[i - 1] in " \t;|&()\n"):
                # comment: consume through end of line; the newline itself is
                # then re-processed as a segment separator
                nl = command.find("\n", i)
                i = n - 1 if nl == -1 else nl - 1
            elif c in SEGMENT_SEPS:
                segments.append("".join(cur))
                cur = []
            else:
                cur.append(c)
        i += 1
    segments.append("".join(cur))
    return [s for s in (seg.strip() for seg in segments) if s]


def _shell_c_payload(tokens):
    """Return the script string of a `shell -c <script>` invocation, scanning
    for shell tokens anywhere in the list (covers `sudo bash -c ...`,
    `docker exec ... bash -c ...`). Combined short options ending in c
    (bash -lc) are recognized. Only the token right after -c is the script;
    later tokens are positional parameters ($0, $1, ...), not more script."""
    for i, tok in enumerate(tokens[:-1]):
        if _basename(tok) not in SHELLS:
            continue
        j = i + 1
        while j < len(tokens):
            opt = tokens[j]
            if opt == "--":
                break  # end of options: script-file form, no -c payload
            is_c = opt == "-c" or (
                len(opt) > 1
                and opt.startswith("-")
                and not opt.startswith("--")
                and opt.endswith("c")
            )
            if is_c:
                return tokens[j + 1] if j + 1 < len(tokens) else None
            if opt.startswith("-"):
                j += 1
                continue
            break  # first non-option token: script-file form
    return None


def _regex_mysql_cli_write(segment):
    """Last-resort regex for segments the tokenizer cannot parse (unclosed
    quotes): mysql-cli must start at ^/whitespace/quote (so wrapped
    `bash -c \"mysql-cli ...\"` still matches), and the flag must be a
    standalone token or =value form. Err on the side of blocking here."""
    if not re.search(r"(?:^|[\s\"'])mysql-cli\b", segment):
        return False
    for flag in WRITE_FLAGS:
        pattern = r"(?:^|[\s\"'])" + re.escape(flag) + r"(?=[\s\"';|&=]|$)"
        if re.search(pattern, segment):
            return True
    return False


def _is_mysql_cli_write(command, _depth=0):
    """True if command runs mysql-cli with a write gate flag."""
    if _depth >= MAX_DEPTH:
        return False
    for segment in _split_segments(command):
        try:
            tokens = shlex.split(segment)
        except ValueError:
            tokens = None
        if tokens is None:
            if _regex_mysql_cli_write(segment):
                return True
            continue
        cmd = _command_word(tokens)
        if cmd in DATA_SINKS:
            # echo/printf merely print their arguments; `echo mysql-cli
            # --write` is display text, not an invocation.
            continue
        if (cmd == "mysql-cli" or "mysql-cli" in tokens) and _has_write_flag(tokens):
            return True
        payload = _shell_c_payload(tokens)
        if payload and _is_mysql_cli_write(payload, _depth + 1):
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

    if event.get("tool_name") not in TOOL_NAMES:
        return
    tool_input = event.get("tool_input")
    if not isinstance(tool_input, dict):
        return
    command = tool_input.get("command", "")
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
