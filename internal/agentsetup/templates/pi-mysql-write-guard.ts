/**
 * Pi Coding Agent extension: force a human confirmation prompt for mysql-cli writes.
 *
 * Loaded by `pi` (https://pi.dev) via auto-discovery from
 *   ~/.pi/agent/extensions/mysql-write-guard.ts   (global)
 *   .pi/extensions/mysql-write-guard.ts            (project, requires /trust)
 *
 * Mechanism: subscribes to Pi's `tool_call` event (fires before the tool
 * executes, bail dispatch). When the `bash` tool is about to run a mysql-cli
 * invocation carrying any of `--write` / `--ddl` / `--yes`, the extension
 * calls `ctx.ui.confirm()` to pull a human into the loop. If the human
 * declines (or there is no interactive UI), it returns `{ block: true }`,
 * which short-circuits execution.
 *
 * Read-only mysql-cli calls (no write flag) pass through silently.
 *
 * Detection mirrors `mysql-write-guard.py` (both implementations are tested
 * against the same matrix in hook_guard_test.go -- keep them behaviorally
 * identical):
 *   - The command is split on unquoted shell control characters (; & | ( )
 *     and newlines) with shell comments dropped; segments joined by `|` stay
 *     in one pipeline group. Each segment is analyzed independently, so
 *     `ls; mysql-cli ...`, `(mysql-cli ...)` and `cmd # trailing comment`
 *     are all handled.
 *   - Token matching: a write flag counts only as a standalone token
 *     (`--write`) or a `--flag=value` form (`--write=true`, `--ddl=1`), so a
 *     flag-looking string inside a quoted SQL literal (e.g. "SELECT '--write'
 *     ...") stays one token and is NOT mistaken for the flag.
 *   - Wrapped invocations: `bash/sh/zsh -c "<script>"` payloads are analyzed
 *     recursively (anywhere in the token list, covering `sudo bash -lc ...`
 *     and `docker exec ... bash -c ...`), so `bash -c "mysql-cli ... --write"`
 *     is caught, including multi-command payloads.
 *   - eval is treated like a wrapper: the concatenated arguments of
 *     `eval <tokens...>` are analyzed recursively.
 *   - Command substitution ($(...) and `...`) is covered two ways: every
 *     substitution body (at any nesting level) is analyzed recursively, and
 *     the segment text combined with all bodies is regex-checked, so both a
 *     flag produced by substitution (`mysql-cli ... $(echo --write)`) and
 *     `bash -c "$(echo 'mysql-cli ... --write')"` are caught. Substitution
 *     never happens inside single quotes, so those spans are ignored.
 *   - Pipelines into a bare shell: when a pipeline group contains a
 *     stdin-reading shell segment (bash/sh/... with no -c option and no
 *     positional arguments, e.g. `... | bash`, `... | sh -s`), every earlier
 *     segment of the group -- including echo/printf data sinks that are
 *     normally skipped -- is regex-checked for the mysql-cli + write-flag
 *     signature, so `echo "mysql-cli ... --write" | bash` is blocked while
 *     plain `echo "usage: mysql-cli query --write"` (no pipe) still passes.
 *   - env -S "<string>" split-string payloads are analyzed recursively, and
 *     `python* -c` / `node -e` code payloads are regex-scanned for a
 *     coexisting mysql-cli + write-flag signature.
 *   - Command words and token comparisons are case-insensitive: macOS
 *     case-insensitive filesystems resolve MYSQL-CLI to mysql-cli.
 *   - Recursion is capped at MAX_DEPTH (16); hitting the cap fails SAFE
 *     (blocks), because only adversarial input nests that deeply.
 *   - Segments whose command word is echo/printf are skipped outside such
 *     pipelines: their arguments are data to print, not commands to run.
 *   - Locating the command skips rtk/sudo/env/nohup/command prefixes; a bare
 *     `mysql-cli` token anywhere in the segment also counts, which covers
 *     pass-through wrappers such as xargs/timeout and `env VAR=... mysql-cli`.
 *   - If a segment cannot be tokenized (unclosed quote), a regex fallback
 *     matches mysql-cli anchored at ^/whitespace/quote and the flag as a
 *     standalone token or =value form.
 *   - Fail-open: any thrown error in the handler is swallowed so a buggy
 *     extension never blocks all bash usage.
 *
 * 覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/shell 执行路径，但会对该
 * 路径内的常见注入形态做静态检测：命令替换（$(...) 与反引号）、eval、管道
 * 注入（echo ... | bash）、env -S 拆串、解释器代码载荷（python -c / node
 * -e）、大小写变体（MYSQL-CLI）均已防护。变量间接（M="mysql-cli --write";
 * $M、$F、"$@"）、执行已有脚本文件（bash fix.sh）、xargs 从文件注参属固有
 * 限制，不在防护范围内。agent 若通过非 Bash tool（如直接 subprocess/exec
 * 系统调用）调用 mysql-cli，同样不会触发本 hook。如需更强保障，应在 agent
 * 配置中禁用非 Bash 执行路径，或依赖 mysql-cli 内部的退出码契约（写操作缺
 * --write/--ddl/--yes 时以 exit 3/4/5 拒绝）。
 *
 * Pi-specific notes:
 *   - Tool name is `bash` (lowercase), not `Bash` (Claude Code) or
 *     `RunCommand` (TRAE).
 *   - Pi has no native `ask` permission decision; the equivalent UX is
 *     `ctx.ui.confirm()`. When no interactive UI is available (e.g. `pi -p`
 *     or `--mode json`), `ctx.ui` is undefined and we block by default
 *     (matching `@diegopetrucci/pi-permission-gate`'s conservative behavior).
 *   - The decision object shape is `{ block: true, reason?: string }`.
 *     Returning undefined (or void) allows the call.
 */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const WRITE_FLAGS = ["--write", "--ddl", "--yes"];
const PREFIXES = new Set(["rtk", "sudo", "env", "nohup", "command"]);
const RTK_SUB = new Set(["proxy"]);
const SHELLS = new Set(["bash", "sh", "zsh", "dash", "ksh", "ash"]);
// cmd words whose arguments are data, not commands
const DATA_SINKS = new Set(["echo", "printf"]);
// `|` ends a segment but stays within a pipeline group
const SEGMENT_SEPS = new Set([";", "&", "(", ")", "\n"]);
const MAX_DEPTH = 16; // `bash -c 'bash -c ...'` recursion guard; the cap fails SAFE

/**
 * Tokenize a shell command string. Returns null on parse failure (unclosed
 * quote), in which case callers fall back to the regex path. Implements
 * shlex-POSIX-compatible lexing (mirrors Python's shlex.split, the reference
 * behavior asserted by the shared matrix tests):
 *   - unquoted `\x` yields the literal `x` (`--wri\te` -> `--write`);
 *     `\<newline>` is a line continuation (both dropped); a trailing
 *     backslash is a parse failure (matches shlex's error)
 *   - single quotes have no escapes; `'\''` closes, escapes the quote and
 *     reopens, producing a literal `'`
 *   - inside double quotes only `\"` and `\\` lose the backslash (shlex
 *     semantics); other backslash sequences stay verbatim
 *   - whitespace separates tokens; quotes are stripped
 */
function splitShellTokens(s: string): string[] | null {
  const tokens: string[] = [];
  let cur = "";
  let i = 0;
  let inSingle = false;
  let inDouble = false;
  let hasToken = false;
  while (i < s.length) {
    const c = s[i];
    if (inSingle) {
      if (c === "'") {
        inSingle = false;
      } else {
        cur += c;
      }
      i++;
      continue;
    }
    if (inDouble) {
      if (c === '"') {
        inDouble = false;
      } else if (c === "\\") {
        if (i + 1 >= s.length) {
          cur += c; // trailing backslash inside quotes: unterminated anyway
        } else {
          const nxt = s[i + 1];
          // shlex: inside double quotes only \" and \\ lose the backslash
          if (nxt === '"' || nxt === "\\") cur += nxt;
          else cur += c + nxt;
          i++;
        }
      } else {
        cur += c;
      }
      i++;
      continue;
    }
    if (c === "\\") {
      if (i + 1 >= s.length) return null; // trailing backslash: shlex errors
      const nxt = s[i + 1];
      if (nxt !== "\n") {
        cur += nxt; // unquoted \x -> literal x
        hasToken = true;
      }
      // \<newline> is a line continuation: drop both
      i += 2;
      continue;
    }
    if (c === "'") {
      inSingle = true;
      hasToken = true;
      i++;
      continue;
    }
    if (c === '"') {
      inDouble = true;
      hasToken = true;
      i++;
      continue;
    }
    if (c === " " || c === "\t" || c === "\n" || c === "\r") {
      if (hasToken) {
        tokens.push(cur);
        cur = "";
        hasToken = false;
      }
      i++;
      continue;
    }
    cur += c;
    hasToken = true;
    i++;
  }
  if (inSingle || inDouble) return null; // unterminated quote
  if (hasToken) tokens.push(cur);
  return tokens;
}

/** Basename of a path, cross-platform (handles both / and \). */
function basename(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i === -1 ? p : p.slice(i + 1);
}

/**
 * Return the first real command token (lowercased: macOS case-insensitive
 * filesystems run MYSQL-CLI as mysql-cli), skipping rtk/sudo/env/nohup/
 * command wrappers. Mirrors `_command_word` in the Python guard.
 */
function commandWord(tokens: string[]): string {
  let i = 0;
  while (i < tokens.length) {
    const base = basename(tokens[i]).toLowerCase();
    if (PREFIXES.has(base)) {
      i++;
      if (base === "rtk" && i < tokens.length && RTK_SUB.has(tokens[i].toLowerCase())) {
        i++;
      }
      continue;
    }
    return base;
  }
  return "";
}

/** True if any token is exactly a write flag or a `--flag=value` form. */
function hasWriteFlag(tokens: string[]): boolean {
  return tokens.some((t) => {
    const tl = t.toLowerCase();
    return WRITE_FLAGS.some((f) => tl === f || tl.startsWith(f + "="));
  });
}

/**
 * Split the command into pipeline groups: segments joined by an unquoted `|`
 * form one group (a pipeline whose right side may be a shell reading stdin);
 * unquoted ; & ( ) and newlines terminate the group (`||`/`&&` are list
 * separators, not pipes). Shell comments are dropped (an unquoted # at the
 * start of a word, extending to the end of the line -- text inside comments
 * never splits segments, so `# note; mysql-cli --write` stays one inert
 * comment). Quotes stay intact so each segment remains independently
 * analyzable; backslash escapes are honored so `\;` never splits. Mirrors
 * `_split_pipeline_groups` in the Python guard.
 */
function splitPipelineGroups(command: string): string[][] {
  const groups: string[][] = [];
  let group: string[] = [];
  let cur = "";
  let inSingle = false;
  let inDouble = false;
  let i = 0;
  while (i < command.length) {
    const c = command[i];
    if (inSingle) {
      cur += c;
      if (c === "'") inSingle = false;
    } else if (inDouble) {
      if (c === "\\" && i + 1 < command.length) {
        cur += c + command[i + 1];
        i++;
      } else {
        cur += c;
        if (c === '"') inDouble = false;
      }
    } else {
      if (c === "\\" && i + 1 < command.length) {
        cur += c + command[i + 1];
        i++;
      } else if (c === "$" && i + 1 < command.length && command[i + 1] === "(") {
        // command substitution, not a subshell: keep it whole so the
        // substitution scanner can extract its body later; its inner
        // separators must not split segments either
        const end = findSubstClose(command, i + 2);
        const stop = end === -1 ? command.length : end + 1;
        cur += command.slice(i, stop);
        i = stop - 1;
      } else if (c === "`") {
        // backtick substitution: same treatment as $(...)
        const end = findBacktickClose(command, i + 1);
        const stop = end === -1 ? command.length : end + 1;
        cur += command.slice(i, stop);
        i = stop - 1;
      } else if (c === "'") {
        inSingle = true;
        cur += c;
      } else if (c === '"') {
        inDouble = true;
        cur += c;
      } else if (
        c === "#" &&
        (i === 0 || " \t;|&()\n".includes(command[i - 1]))
      ) {
        // comment: consume through end of line; the newline itself is then
        // re-processed as a segment separator
        const nl = command.indexOf("\n", i);
        i = nl === -1 ? command.length - 1 : nl - 1;
      } else if (c === "|" && command[i + 1] === "|") {
        // `||` is a list separator, not a pipe
        group.push(cur);
        cur = "";
        groups.push(group);
        group = [];
        i++;
      } else if (c === "&" && command[i + 1] === "&") {
        group.push(cur);
        cur = "";
        groups.push(group);
        group = [];
        i++;
      } else if (c === "|") {
        group.push(cur);
        cur = "";
      } else if (SEGMENT_SEPS.has(c)) {
        group.push(cur);
        cur = "";
        groups.push(group);
        group = [];
      } else {
        cur += c;
      }
    }
    i++;
  }
  group.push(cur);
  groups.push(group);
  const out: string[][] = [];
  for (const g of groups) {
    const segs = g.map((s) => s.trim()).filter((s) => s.length > 0);
    if (segs.length > 0) out.push(segs);
  }
  return out;
}

/**
 * Return the script string of a `shell -c <script>` invocation, scanning for
 * shell tokens anywhere in the list (covers `sudo bash -c ...`, `docker exec
 * ... bash -c ...`). Combined short options ending in c (bash -lc) are
 * recognized. Only the token right after -c is the script; later tokens are
 * positional parameters ($0, $1, ...), not more script. Mirrors
 * `_shell_c_payload` in the Python guard.
 */
function shellCPayload(tokens: string[]): string | null {
  for (let i = 0; i < tokens.length - 1; i++) {
    if (!SHELLS.has(basename(tokens[i]).toLowerCase())) continue;
    let j = i + 1;
    while (j < tokens.length) {
      const opt = tokens[j];
      if (opt === "--") break; // end of options: script-file form
      const isC =
        opt === "-c" ||
        (opt.length > 1 &&
          opt.startsWith("-") &&
          !opt.startsWith("--") &&
          opt.endsWith("c"));
      if (isC) return j + 1 < tokens.length ? tokens[j + 1] : null;
      if (opt.startsWith("-")) {
        j++;
        continue;
      }
      break; // first non-option token: script-file form
    }
  }
  return null;
}

/**
 * Return the split-string payload of `env -S <string>` (GNU env splits the
 * string and executes the result, so the string is effectively a whole
 * command line). Mirrors `_env_s_payload` in the Python guard.
 */
function envSPayload(tokens: string[]): string | null {
  for (let i = 0; i < tokens.length - 1; i++) {
    if (basename(tokens[i]).toLowerCase() !== "env") continue;
    let j = i + 1;
    while (j < tokens.length) {
      const opt = tokens[j];
      if (opt === "-S" || opt === "--split-string") {
        return j + 1 < tokens.length ? tokens[j + 1] : null;
      }
      if (opt === "-u" || opt === "--unset") {
        j += 2; // -u consumes a NAME argument
        continue;
      }
      if (opt.startsWith("-")) {
        j++;
        continue;
      }
      break; // first non-option token: plain `env [opts] cmd args`
    }
  }
  return null;
}

/**
 * Return the concatenated arguments of `eval <tokens...>` -- eval joins them
 * with spaces and parses the result as a shell command. Mirrors
 * `_eval_payload` in the Python guard.
 */
function evalPayloadOf(tokens: string[]): string | null {
  for (let i = 0; i < tokens.length; i++) {
    if (basename(tokens[i]).toLowerCase() !== "eval") continue;
    const args = tokens.slice(i + 1).join(" ");
    return args.length > 0 ? args : null;
  }
  return null;
}

/**
 * Return the code string of a `python* -c <code>` / `node -e <code>`
 * invocation (token scanned anywhere in the list). Combined short options
 * ending in c (python -qc) are recognized for python. Mirrors
 * `_interpreter_code_payload` in the Python guard.
 */
function interpreterCodePayload(tokens: string[]): string | null {
  for (let i = 0; i < tokens.length - 1; i++) {
    const base = basename(tokens[i]).toLowerCase();
    if (!base.startsWith("python") && base !== "node" && base !== "nodejs") {
      continue;
    }
    const want = base === "node" || base === "nodejs" ? "-e" : "-c";
    let j = i + 1;
    while (j < tokens.length) {
      const opt = tokens[j];
      if (opt === "--") break; // end of options
      const isPayloadOpt =
        opt === want ||
        (want === "-c" &&
          opt.length > 1 &&
          opt.startsWith("-") &&
          !opt.startsWith("--") &&
          opt.endsWith("c"));
      if (isPayloadOpt) {
        return j + 1 < tokens.length ? tokens[j + 1] : null;
      }
      if (opt.startsWith("-")) {
        j++;
        continue;
      }
      break; // first non-option token: script-file form
    }
  }
  return null;
}

/**
 * Loose plaintext scan of interpreter code payloads (`python -c`, `node -e`):
 * mysql-cli and a write gate flag coexisting in the code is a hit.
 * Guest-language string quoting makes token-level matching impossible here,
 * so this errs on the side of blocking. Mirrors `_code_payload_mysql_write`
 * in the Python guard.
 */
function codePayloadMysqlWrite(code: string): boolean {
  return /mysql.?cli/i.test(code) && /--(write|ddl|yes)\b/i.test(code);
}

/**
 * Index of the `)` closing a $( opened before `start`, tracking nested
 * parentheses and quoted strings; -1 if unterminated. Mirrors
 * `_find_subst_close` in the Python guard.
 */
function findSubstClose(text: string, start: number): number {
  let depth = 1;
  let i = start;
  const n = text.length;
  while (i < n) {
    const c = text[i];
    if (c === "'") {
      const j = text.indexOf("'", i + 1);
      i = j === -1 ? n : j + 1;
      continue;
    }
    if (c === '"') {
      i++;
      while (i < n) {
        if (text[i] === "\\") i += 2;
        else if (text[i] === '"') {
          i++;
          break;
        } else i++;
      }
      continue;
    }
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === "(") depth++;
    else if (c === ")") {
      depth--;
      if (depth === 0) return i;
    }
    i++;
  }
  return -1;
}

/**
 * Index of the closing backtick; -1 if unterminated. Mirrors
 * `_find_backtick_close` in the Python guard.
 */
function findBacktickClose(text: string, start: number): number {
  let i = start;
  const n = text.length;
  while (i < n) {
    const c = text[i];
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === "`") return i;
    i++;
  }
  return -1;
}

/**
 * Return the bodies of $(...) and `...` command substitutions that the shell
 * would expand, at every nesting level (a nested substitution's output feeds
 * the enclosing one, so any level's text can end up on the final command
 * line). Substitution does not occur inside single quotes; everywhere else
 * (unquoted or inside double quotes) it does. Unterminated substitutions
 * yield the rest of the text (fail-safe: scan more, not less). Mirrors
 * `_extract_command_substitutions` in the Python guard.
 */
function extractCommandSubstitutions(text: string): string[] {
  const subs: string[] = [];
  scanCommandSubstitutions(text, subs, 0);
  return subs;
}

function scanCommandSubstitutions(text: string, subs: string[], depth: number): void {
  if (depth >= MAX_DEPTH) return;
  const n = text.length;
  let i = 0;
  let inSingle = false;
  let inDouble = false;
  while (i < n) {
    const c = text[i];
    if (inSingle) {
      if (c === "'") inSingle = false;
      i++;
    } else if (inDouble) {
      if (c === "\\" && i + 1 < n) {
        i += 2;
      } else if (c === '"') {
        inDouble = false;
        i++;
      } else if (c === "$" && i + 1 < n && text[i + 1] === "(") {
        const end = findSubstClose(text, i + 2);
        const stop = end === -1 ? n : end;
        const body = text.slice(i + 2, stop);
        subs.push(body);
        scanCommandSubstitutions(body, subs, depth + 1);
        i = end === -1 ? n : end + 1;
      } else if (c === "`") {
        const end = findBacktickClose(text, i + 1);
        const stop = end === -1 ? n : end;
        const body = text.slice(i + 1, stop);
        subs.push(body);
        scanCommandSubstitutions(body, subs, depth + 1);
        i = end === -1 ? n : end + 1;
      } else {
        i++;
      }
    } else {
      if (c === "\\" && i + 1 < n) {
        i += 2;
      } else if (c === "'") {
        inSingle = true;
        i++;
      } else if (c === '"') {
        inDouble = true;
        i++;
      } else if (c === "$" && i + 1 < n && text[i + 1] === "(") {
        const end = findSubstClose(text, i + 2);
        const stop = end === -1 ? n : end;
        const body = text.slice(i + 2, stop);
        subs.push(body);
        scanCommandSubstitutions(body, subs, depth + 1);
        i = end === -1 ? n : end + 1;
      } else if (c === "`") {
        const end = findBacktickClose(text, i + 1);
        const stop = end === -1 ? n : end;
        const body = text.slice(i + 1, stop);
        subs.push(body);
        scanCommandSubstitutions(body, subs, depth + 1);
        i = end === -1 ? n : end + 1;
      } else {
        i++;
      }
    }
  }
}

/**
 * True if the segment runs a shell that reads commands from stdin: a
 * bash/sh/... token followed only by options, none of them -c-style, and no
 * positional arguments (`bash`, `sh`, `sudo bash -s`). Such a segment
 * executes whatever the pipeline feeds it. Mirrors
 * `_is_bare_shell_segment` in the Python guard.
 */
function isBareShellSegment(tokens: string[]): boolean {
  for (let i = 0; i < tokens.length; i++) {
    if (!SHELLS.has(basename(tokens[i]).toLowerCase())) continue;
    for (const opt of tokens.slice(i + 1)) {
      if (opt === "--") return false; // end of options: script-file form
      const isC =
        opt === "-c" ||
        (opt.length > 1 &&
          opt.startsWith("-") &&
          !opt.startsWith("--") &&
          opt.endsWith("c"));
      if (isC) return false; // -c form carries its script as an argument
      if (!opt.startsWith("-")) return false; // positional: script file/args
    }
    return true;
  }
  return false;
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Last-resort regex for segments the tokenizer cannot parse (unclosed
 * quotes) and for degraded scans (command substitution bodies, pipeline
 * sources): mysql-cli must start at ^/whitespace/quote (so wrapped
 * `bash -c "mysql-cli ..."` still matches), and the flag must be a standalone
 * token or =value form. Case-insensitive (macOS filesystems). Err on the
 * side of blocking here. Mirrors `_regex_mysql_cli_write` in the Python
 * guard -- the character classes are intentionally identical.
 */
function regexMysqlCliWrite(segment: string): boolean {
  if (!/(?:^|[\s"'])mysql-cli\b/i.test(segment)) return false;
  return WRITE_FLAGS.some((f) => {
    const re = new RegExp(`(?:^|[\\s"'])${escapeRegex(f)}(?=[\\s"';|&=]|$)`, "i");
    return re.test(segment);
  });
}

/**
 * True if the command runs mysql-cli with a write gate flag. Mirrors
 * `_is_mysql_cli_write` in the Python guard. Exported for the shared
 * detection-matrix tests (hook_guard_test.go).
 */
export function isMysqlCliWrite(command: string, depth = 0): boolean {
  if (depth >= MAX_DEPTH) return true; // fail-safe: absurd nesting is hostile
  for (const group of splitPipelineGroups(command)) {
    let bareShellIdx = -1;
    for (let idx = 0; idx < group.length; idx++) {
      const segment = group[idx];
      const tokens = splitShellTokens(segment);
      if (tokens === null) {
        if (regexMysqlCliWrite(segment)) return true;
        continue;
      }
      const cmd = commandWord(tokens);
      if (DATA_SINKS.has(cmd)) {
        // echo/printf merely print their arguments; pipelines feeding a
        // bare shell are handled at the group level below.
        continue;
      }
      if (
        (cmd === "mysql-cli" || tokens.some((t) => t.toLowerCase() === "mysql-cli")) &&
        hasWriteFlag(tokens)
      ) {
        return true;
      }
      const payload = shellCPayload(tokens);
      if (payload && isMysqlCliWrite(payload, depth + 1)) return true;
      const envPayload = envSPayload(tokens);
      if (envPayload && isMysqlCliWrite(envPayload, depth + 1)) return true;
      const evalPayload = evalPayloadOf(tokens);
      if (evalPayload && isMysqlCliWrite(evalPayload, depth + 1)) return true;
      const code = interpreterCodePayload(tokens);
      if (code && codePayloadMysqlWrite(code)) return true;
      const subs = extractCommandSubstitutions(segment);
      if (subs.length > 0) {
        // The shell expands substitutions into the surrounding command
        // line, so scan the segment combined with all bodies, and run each
        // body through the full detector (it is exactly what the shell
        // would execute to produce the expansion).
        const combined = segment + " " + subs.join(" ");
        if (regexMysqlCliWrite(combined)) return true;
        for (const sub of subs) {
          if (isMysqlCliWrite(sub, depth + 1)) return true;
        }
      }
      if (
        bareShellIdx === -1 &&
        idx > 0 &&
        SHELLS.has(cmd) &&
        isBareShellSegment(tokens)
      ) {
        bareShellIdx = idx;
      }
    }
    if (bareShellIdx !== -1) {
      // A stdin-reading shell executes what earlier pipeline stages print,
      // so regex-check those segments (echo/printf included).
      for (const segment of group.slice(0, bareShellIdx)) {
        if (regexMysqlCliWrite(segment)) return true;
      }
    }
  }
  return false;
}

export default function (pi: ExtensionAPI): void {
  pi.on("tool_call", async (event, ctx) => {
    try {
      // Pi's built-in shell tool is `bash` (lowercase).
      if (event.toolName !== "bash") return;
      const command: string | undefined = event.input?.command;
      if (!command || !isMysqlCliWrite(command)) return;

      // Pull a human into the loop. When no interactive UI is wired (e.g.
      // `pi -p`, `--mode json`, `--mode rpc`), `ctx.ui` may be undefined; we
      // block by default to avoid silently auto-approving writes.
      let ok = false;
      if (ctx?.ui?.confirm) {
        ok = await ctx.ui.confirm(
          "mysql-cli write operation",
          "This mysql-cli call carries --write / --ddl / --yes and will modify the database. Approve?",
        );
      }
      if (!ok) {
        return {
          block: true,
          reason:
            "mysql-cli write operation (--write/--ddl/--yes) requires human approval.",
        };
      }
      // Approved: return undefined to allow the call.
      return;
    } catch (e) {
      // Fail-open: never let a bug in the guard block all bash usage. Log to
      // stderr if available, otherwise stay silent.
      //
      // NOTE (intentional asymmetry, kept as designed): when ctx.ui exists
      // but ctx.ui.confirm() throws, we fail OPEN (allow); when there is no
      // UI at all we fail CLOSED (block, see above). Rationale: a throw from
      // confirm() means the extension/UI layer itself is buggy -- failing
      // closed here would take down ALL bash usage for the session, which is
      // the one outcome worse than letting a flagged write slip through
      // (mysql-cli's own exit-code gate still refuses writes that lack
      // --write/--ddl/--yes, so the blast radius of one slipped call is a
      // failed command, not silent corruption). A missing UI, by contrast,
      // is an expected non-interactive context where silently auto-approving
      // database writes is exactly the risk this extension exists to prevent.
      if (ctx?.ui?.log) {
        try {
          ctx.ui.log(`mysql-write-guard: *** WARNING *** fail-open (confirmation bypassed) due to exception: ${String(e)}`);
        } catch {
          /* swallow */
        }
      }
      return;
    }
  });
}
