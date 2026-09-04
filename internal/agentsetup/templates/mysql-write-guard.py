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
  newlines) with shell comments dropped; segments joined by `|` stay in one
  pipeline group. Each segment is analyzed independently, so `ls; mysql-cli
  ...`, `(mysql-cli ...)` and `cmd # trailing comment` are all handled.
- Token matching: a write flag counts only as a standalone token (`--write`)
  or a `--flag=value` form (`--write=true`, `--ddl=1`), so a flag-looking
  string inside a quoted SQL literal (e.g. "SELECT '--write' ...") stays one
  token and is NOT mistaken for the flag.
- Wrapped invocations: `bash/sh/zsh -c "<script>"` payloads are analyzed
  recursively (anywhere in the token list, covering `sudo bash -lc ...` and
  `docker exec ... bash -c ...`), so `bash -c "mysql-cli ... --write"` is
  caught, including multi-command payloads like `bash -c "echo hi; ... --write"`.
- eval is treated like a wrapper: the concatenated arguments of
  `eval <tokens...>` are analyzed recursively (`eval "mysql-cli ... --write"`).
- Command substitution ($(...) and `...`) is covered two ways: every
  substitution body (at any nesting level) is analyzed recursively, and the
  segment text combined with all bodies is regex-checked, so both a flag
  produced by substitution (`mysql-cli ... $(echo --write)`) and
  `bash -c "$(echo 'mysql-cli ... --write')"` are caught. Substitution never
  happens inside single quotes, so those spans are ignored.
- Pipelines into a bare shell: when a pipeline group contains a stdin-reading
  shell segment (bash/sh/... with no -c option and no positional arguments,
  e.g. `... | bash`, `... | sh -s`), every earlier segment of the group --
  including echo/printf data sinks that are normally skipped -- is
  regex-checked for the mysql-cli + write-flag signature, so
  `echo "mysql-cli ... --write" | bash` is blocked while plain
  `echo "usage: mysql-cli query --write"` (no pipe) still passes.
- env -S "<string>" split-string payloads are analyzed recursively, and
  `python* -c` / `node -e` code payloads are regex-scanned for a coexisting
  mysql-cli + write-flag signature (guest-language string quoting makes
  token-level matching impossible there).
- Command words and token comparisons are case-insensitive: macOS
  case-insensitive filesystems resolve MYSQL-CLI to mysql-cli.
- Recursion is capped at MAX_DEPTH (16); hitting the cap fails SAFE (blocks),
  because only adversarial input nests that deeply.
- Segments whose command word is echo/printf are skipped outside such
  pipelines: their arguments are data to print, not commands to run
  (`echo mysql-cli --write` does not write).
- Locating the command skips rtk/sudo/env/nohup/command prefixes; a bare
  `mysql-cli` token anywhere in the segment also counts, which covers
  pass-through wrappers such as xargs/timeout and `env VAR=... mysql-cli`.
- If a segment cannot be tokenized (unclosed quote), a regex fallback matches
  mysql-cli anchored at ^/whitespace/quote and the flag as a standalone token
  or =value form (back boundary allows whitespace/quote/;/|/&/=/end).
- Fail-open on internal errors: a broken hook must not block all Bash usage.

覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/RunCommand 执行路径，但会对
该路径内的常见注入形态做静态检测：命令替换（$(...) 与反引号）、eval、管道
注入（echo ... | bash）、env -S 拆串、解释器代码载荷（python -c / node -e）、
大小写变体（MYSQL-CLI）均已防护。变量间接（M="mysql-cli --write"; $M、$F、
"$@"）、执行已有脚本文件（bash fix.sh）、xargs 从文件注参属固有限制，不在
防护范围内。agent 若通过非 Bash tool（如 Python subprocess、直接 exec 系统
调用）调用 mysql-cli，同样不会触发本 hook。如需更强保障，应在 agent 配置中
禁用非 Bash 执行路径，或依赖 mysql-cli 内部的退出码契约（写操作缺
--write/--ddl/--yes 时以 exit 3/4/5 拒绝）。
"""
import json
import re
import sys

WRITE_FLAGS = ("--write", "--ddl", "--yes")
PREFIXES = {"rtk", "sudo", "env", "nohup", "command"}
RTK_SUB = {"proxy"}  # `rtk proxy <cmd>` form
SHELLS = {"bash", "sh", "zsh", "dash", "ksh", "ash"}
DATA_SINKS = {"echo", "printf"}  # cmd words whose arguments are data, not commands
SEGMENT_SEPS = set(";&()\n")  # `|` ends a segment but stays within a pipeline group
MAX_DEPTH = 16  # `bash -c 'bash -c ...'` recursion guard; the cap fails SAFE
# Claude Code/CodeBuddy use "Bash"; TRAE's standardized tool name is
# "RunCommand" (agentsetup generates matcher "RunCommand" for TRAE).
TOOL_NAMES = {"Bash", "RunCommand"}


def _basename(token):
    """Basename handling both / and \\ separators (matches the TS guard)."""
    token = token.rsplit("/", 1)[-1]
    return token.rsplit("\\", 1)[-1]


def _command_word(tokens):
    """Return the first real command token (lowercased: macOS case-insensitive
    filesystems run MYSQL-CLI as mysql-cli), skipping rtk/sudo/env wrappers."""
    i = 0
    while i < len(tokens):
        base = _basename(tokens[i]).lower()
        if base in PREFIXES:
            i += 1
            if base == "rtk" and i < len(tokens) and tokens[i].lower() in RTK_SUB:
                i += 1
            continue
        return base
    return ""


def _has_write_flag(tokens):
    """True if any token is exactly a write flag or a `--flag=value` form."""
    for t in tokens:
        tl = t.lower()
        for flag in WRITE_FLAGS:
            if tl == flag or tl.startswith(flag + "="):
                return True
    return False


def _split_tokens(segment):
    """shlex-POSIX-compatible tokenizer, mirroring splitShellTokens in the
    TS guard (the two must stay behaviorally identical). Written by hand
    instead of calling shlex.split because shlex's per-character token
    concatenation is O(n^2) on a single large token, and adversarial deeply
    nested payloads would use that to stall the hook past its timeout.
    Returns None on parse failure (unclosed quote / trailing backslash) so
    callers fall back to the regex path, exactly like shlex's ValueError:
    - unquoted `\\x` yields the literal `x` (`--wri\\te` -> `--write`);
      `\\<newline>` is a line continuation (both dropped)
    - single quotes have no escapes; `'\\''` closes, escapes the quote and
      reopens, producing a literal `'`
    - inside double quotes only `\\"` and `\\\\` lose the backslash; other
      backslash sequences stay verbatim"""
    tokens, cur = [], []
    has_token = False
    in_single = in_double = False
    i, n = 0, len(segment)
    while i < n:
        c = segment[i]
        if in_single:
            if c == "'":
                in_single = False
            else:
                cur.append(c)
            i += 1
        elif in_double:
            if c == '"':
                in_double = False
            elif c == "\\":
                if i + 1 >= n:
                    cur.append(c)  # trailing backslash inside quotes: unterminated
                else:
                    nxt = segment[i + 1]
                    if nxt in ('"', "\\"):
                        cur.append(nxt)
                    else:
                        cur.append(c)
                        cur.append(nxt)
                    i += 1
            else:
                cur.append(c)
            i += 1
        elif c == "\\":
            if i + 1 >= n:
                return None  # trailing backslash: shlex errors here
            nxt = segment[i + 1]
            if nxt != "\n":
                cur.append(nxt)  # unquoted \x -> literal x
                has_token = True
            i += 2  # \<newline> is a line continuation: drop both
        elif c == "'":
            in_single = True
            has_token = True
            i += 1
        elif c == '"':
            in_double = True
            has_token = True
            i += 1
        elif c in " \t\r\n":
            if has_token:
                tokens.append("".join(cur))
                cur = []
                has_token = False
            i += 1
        else:
            cur.append(c)
            has_token = True
            i += 1
    if in_single or in_double:
        return None  # unterminated quote
    if has_token:
        tokens.append("".join(cur))
    return tokens


def _split_pipeline_groups(command):
    """Split the command into pipeline groups: segments joined by an unquoted
    `|` form one group (a pipeline whose right side may be a shell reading
    stdin); unquoted ; & ( ) and newlines terminate the group (`||`/`&&` are
    list separators, not pipes). Shell comments are dropped (an unquoted # at
    the start of a word, extending to the end of the line -- text inside
    comments never splits segments, so `# note; mysql-cli --write` stays one
    inert comment). Quotes stay intact so each segment remains independently
    analyzable; backslash escapes are honored so `\\;` never splits."""
    groups, group, cur = [], [], []
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
            elif c == "$" and i + 1 < n and command[i + 1] == "(":
                # command substitution, not a subshell: keep it whole so the
                # substitution scanner can extract its body later; its inner
                # separators must not split segments either
                end = _find_subst_close(command, i + 2)
                stop = n if end == -1 else end + 1
                cur.append(command[i:stop])
                i = stop - 1
            elif c == "`":
                # backtick substitution: same treatment as $(...)
                end = _find_backtick_close(command, i + 1)
                stop = n if end == -1 else end + 1
                cur.append(command[i:stop])
                i = stop - 1
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
            elif c == "|" and i + 1 < n and command[i + 1] == "|":
                # `||` is a list separator, not a pipe
                group.append("".join(cur))
                cur = []
                groups.append(group)
                group = []
                i += 1
            elif c == "&" and i + 1 < n and command[i + 1] == "&":
                group.append("".join(cur))
                cur = []
                groups.append(group)
                group = []
                i += 1
            elif c == "|":
                group.append("".join(cur))
                cur = []
            elif c in SEGMENT_SEPS:
                group.append("".join(cur))
                cur = []
                groups.append(group)
                group = []
            else:
                cur.append(c)
        i += 1
    group.append("".join(cur))
    groups.append(group)
    out = []
    for g in groups:
        segs = [s for s in (seg.strip() for seg in g) if s]
        if segs:
            out.append(segs)
    return out


def _shell_c_payload(tokens):
    """Return the script string of a `shell -c <script>` invocation, scanning
    for shell tokens anywhere in the list (covers `sudo bash -c ...`,
    `docker exec ... bash -c ...`). Combined short options ending in c
    (bash -lc) are recognized. Only the token right after -c is the script;
    later tokens are positional parameters ($0, $1, ...), not more script."""
    for i, tok in enumerate(tokens[:-1]):
        if _basename(tok).lower() not in SHELLS:
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


def _env_s_payload(tokens):
    """Return the split-string payload of `env -S <string>` (GNU env splits
    the string and executes the result, so the string is effectively a whole
    command line)."""
    for i, tok in enumerate(tokens[:-1]):
        if _basename(tok).lower() != "env":
            continue
        j = i + 1
        while j < len(tokens):
            opt = tokens[j]
            if opt in ("-S", "--split-string"):
                return tokens[j + 1] if j + 1 < len(tokens) else None
            if opt in ("-u", "--unset"):
                j += 2  # -u consumes a NAME argument
                continue
            if opt.startswith("-"):
                j += 1
                continue
            break  # first non-option token: plain `env [opts] cmd args`
    return None


def _eval_payload(tokens):
    """Return the concatenated arguments of `eval <tokens...>` -- eval joins
    them with spaces and parses the result as a shell command."""
    for i, tok in enumerate(tokens):
        if _basename(tok).lower() == "eval":
            return " ".join(tokens[i + 1:]) or None
    return None


def _interpreter_code_payload(tokens):
    """Return the code string of a `python* -c <code>` / `node -e <code>`
    invocation (token scanned anywhere in the list). Combined short options
    ending in c (python -qc) are recognized for python."""
    for i, tok in enumerate(tokens[:-1]):
        base = _basename(tok).lower()
        if not (base.startswith("python") or base in ("node", "nodejs")):
            continue
        want = "-e" if base in ("node", "nodejs") else "-c"
        j = i + 1
        while j < len(tokens):
            opt = tokens[j]
            if opt == "--":
                break  # end of options
            is_payload_opt = opt == want or (
                want == "-c"
                and len(opt) > 1
                and opt.startswith("-")
                and not opt.startswith("--")
                and opt.endswith("c")
            )
            if is_payload_opt:
                return tokens[j + 1] if j + 1 < len(tokens) else None
            if opt.startswith("-"):
                j += 1
                continue
            break  # first non-option token: script-file form
    return None


def _code_payload_mysql_write(code):
    """Loose plaintext scan of interpreter code payloads (`python -c`,
    `node -e`): mysql-cli and a write gate flag coexisting in the code is a
    hit. Guest-language string quoting makes token-level matching impossible
    here, so this errs on the side of blocking."""
    return bool(
        re.search(r"mysql.?cli", code, re.IGNORECASE)
        and re.search(r"--(write|ddl|yes)\b", code, re.IGNORECASE)
    )


def _find_subst_close(text, start):
    """Index of the `)` closing a $( opened before `start`, tracking nested
    parentheses and quoted strings; -1 if unterminated."""
    depth = 1
    i, n = start, len(text)
    while i < n:
        c = text[i]
        if c == "'":
            j = text.find("'", i + 1)
            i = n if j == -1 else j + 1
            continue
        if c == '"':
            i += 1
            while i < n:
                if text[i] == "\\":
                    i += 2
                elif text[i] == '"':
                    i += 1
                    break
                else:
                    i += 1
            continue
        if c == "\\":
            i += 2
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    return -1


def _find_backtick_close(text, start):
    """Index of the closing backtick; -1 if unterminated."""
    i, n = start, len(text)
    while i < n:
        c = text[i]
        if c == "\\":
            i += 2
            continue
        if c == "`":
            return i
        i += 1
    return -1


def _extract_command_substitutions(text):
    """Return the bodies of $(...) and `...` command substitutions that the
    shell would expand, at every nesting level (a nested substitution's
    output feeds the enclosing one, so any level's text can end up on the
    final command line). Substitution does not occur inside single quotes;
    everywhere else (unquoted or inside double quotes) it does. Unterminated
    substitutions yield the rest of the text (fail-safe: scan more, not
    less)."""
    subs = []
    _scan_command_substitutions(text, subs, 0)
    return subs


def _scan_command_substitutions(text, subs, depth):
    if depth >= MAX_DEPTH:
        return
    i, n = 0, len(text)
    in_single = in_double = False
    while i < n:
        c = text[i]
        if in_single:
            if c == "'":
                in_single = False
            i += 1
        elif in_double:
            if c == "\\" and i + 1 < n:
                i += 2
            elif c == '"':
                in_double = False
                i += 1
            elif c == "$" and i + 1 < n and text[i + 1] == "(":
                end = _find_subst_close(text, i + 2)
                stop = n if end == -1 else end
                subs.append(text[i + 2: stop])
                _scan_command_substitutions(text[i + 2: stop], subs, depth + 1)
                i = n if end == -1 else end + 1
            elif c == "`":
                end = _find_backtick_close(text, i + 1)
                stop = n if end == -1 else end
                subs.append(text[i + 1: stop])
                _scan_command_substitutions(text[i + 1: stop], subs, depth + 1)
                i = n if end == -1 else end + 1
            else:
                i += 1
        else:
            if c == "\\" and i + 1 < n:
                i += 2
            elif c == "'":
                in_single = True
                i += 1
            elif c == '"':
                in_double = True
                i += 1
            elif c == "$" and i + 1 < n and text[i + 1] == "(":
                end = _find_subst_close(text, i + 2)
                stop = n if end == -1 else end
                subs.append(text[i + 2: stop])
                _scan_command_substitutions(text[i + 2: stop], subs, depth + 1)
                i = n if end == -1 else end + 1
            elif c == "`":
                end = _find_backtick_close(text, i + 1)
                stop = n if end == -1 else end
                subs.append(text[i + 1: stop])
                _scan_command_substitutions(text[i + 1: stop], subs, depth + 1)
                i = n if end == -1 else end + 1
            else:
                i += 1


def _is_bare_shell_segment(tokens):
    """True if the segment runs a shell that reads commands from stdin: a
    bash/sh/... token followed only by options, none of them -c-style, and
    no positional arguments (`bash`, `sh`, `sudo bash -s`). Such a segment
    executes whatever the pipeline feeds it."""
    for i, tok in enumerate(tokens):
        if _basename(tok).lower() not in SHELLS:
            continue
        for opt in tokens[i + 1:]:
            if opt == "--":
                return False  # end of options: script-file form follows
            is_c = opt == "-c" or (
                len(opt) > 1
                and opt.startswith("-")
                and not opt.startswith("--")
                and opt.endswith("c")
            )
            if is_c:
                return False  # -c form carries its script as an argument
            if not opt.startswith("-"):
                return False  # positional argument: script file or args
        return True
    return False


def _regex_mysql_cli_write(segment):
    """Last-resort regex for segments the tokenizer cannot parse (unclosed
    quotes) and for degraded scans (command substitution bodies, pipeline
    sources): mysql-cli must start at ^/whitespace/quote (so wrapped
    `bash -c \"mysql-cli ...\"` still matches), and the flag must be a
    standalone token or =value form. Case-insensitive (macOS filesystems).
    Err on the side of blocking here."""
    if not re.search(r"(?:^|[\s\"'])mysql-cli\b", segment, re.IGNORECASE):
        return False
    for flag in WRITE_FLAGS:
        pattern = r"(?:^|[\s\"'])" + re.escape(flag) + r"(?=[\s\"';|&=]|$)"
        if re.search(pattern, segment, re.IGNORECASE):
            return True
    return False


def _is_mysql_cli_write(command, _depth=0):
    """True if command runs mysql-cli with a write gate flag."""
    if _depth >= MAX_DEPTH:
        return True  # fail-safe: only adversarial input nests this deeply
    for group in _split_pipeline_groups(command):
        bare_shell_idx = None
        for idx, segment in enumerate(group):
            tokens = _split_tokens(segment)
            if tokens is None:
                if _regex_mysql_cli_write(segment):
                    return True
                continue
            cmd = _command_word(tokens)
            if cmd in DATA_SINKS:
                # echo/printf merely print their arguments; `echo mysql-cli
                # --write` is display text, not an invocation. Pipelines
                # feeding a bare shell are handled at the group level below.
                continue
            if (
                cmd == "mysql-cli"
                or any(t.lower() == "mysql-cli" for t in tokens)
            ) and _has_write_flag(tokens):
                return True
            payload = _shell_c_payload(tokens)
            if payload and _is_mysql_cli_write(payload, _depth + 1):
                return True
            env_payload = _env_s_payload(tokens)
            if env_payload and _is_mysql_cli_write(env_payload, _depth + 1):
                return True
            eval_payload = _eval_payload(tokens)
            if eval_payload and _is_mysql_cli_write(eval_payload, _depth + 1):
                return True
            code = _interpreter_code_payload(tokens)
            if code and _code_payload_mysql_write(code):
                return True
            subs = _extract_command_substitutions(segment)
            if subs:
                # The shell expands substitutions into the surrounding
                # command line, so scan the segment combined with all bodies,
                # and run each body through the full detector (it is exactly
                # what the shell would execute to produce the expansion).
                combined = segment + " " + " ".join(subs)
                if _regex_mysql_cli_write(combined):
                    return True
                for sub in subs:
                    if _is_mysql_cli_write(sub, _depth + 1):
                        return True
            if (
                bare_shell_idx is None
                and idx > 0
                and cmd in SHELLS
                and _is_bare_shell_segment(tokens)
            ):
                bare_shell_idx = idx
        if bare_shell_idx is not None:
            # A stdin-reading shell executes what earlier pipeline stages
            # print, so regex-check those segments (echo/printf included).
            for segment in group[:bare_shell_idx]:
                if _regex_mysql_cli_write(segment):
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
