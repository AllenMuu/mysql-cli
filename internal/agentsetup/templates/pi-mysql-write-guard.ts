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
 *     and newlines) with shell comments dropped; each segment is analyzed
 *     independently, so `ls; mysql-cli ...`, `(mysql-cli ...)` and
 *     `cmd # trailing comment` are all handled.
 *   - Token matching: a write flag counts only as a standalone token
 *     (`--write`) or a `--flag=value` form (`--write=true`, `--ddl=1`), so a
 *     flag-looking string inside a quoted SQL literal (e.g. "SELECT '--write'
 *     ...") stays one token and is NOT mistaken for the flag.
 *   - Wrapped invocations: `bash/sh/zsh -c "<script>"` payloads are analyzed
 *     recursively (anywhere in the token list, covering `sudo bash -lc ...`
 *     and `docker exec ... bash -c ...`), so `bash -c "mysql-cli ... --write"`
 *     is caught, including multi-command payloads.
 *   - Segments whose command word is echo/printf are skipped: their arguments
 *     are data to print, not commands to run (`echo mysql-cli --write` does
 *     not write).
 *   - Locating the command skips rtk/sudo/env/nohup/command prefixes; a bare
 *     `mysql-cli` token anywhere in the segment also counts, which covers
 *     pass-through wrappers such as xargs/timeout and `env VAR=... mysql-cli`.
 *   - If a segment cannot be tokenized (unclosed quote), a regex fallback
 *     matches mysql-cli anchored at ^/whitespace/quote and the flag as a
 *     standalone token or =value form.
 *   - Fail-open: any thrown error in the handler is swallowed so a buggy
 *     extension never blocks all bash usage.
 *
 * 覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/shell 执行路径。
 * agent 若通过非 Bash tool（如 Python subprocess、直接 exec 系统调用）调用 mysql-cli，
 * 或先把写调用写进已有脚本文件再执行（bash fix.sh），不会触发本 hook。
 * 如需更强保障，应在 agent 配置中禁用非 Bash 执行路径，
 * 或依赖 mysql-cli 内部的退出码契约（写操作缺 --write/--ddl/--yes 时以 exit 3/4/5 拒绝）。
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
const SEGMENT_SEPS = new Set([";", "&", "|", "(", ")", "\n"]);
const MAX_DEPTH = 4; // `bash -c 'bash -c ...'` recursion guard

/**
 * Tokenize a shell command string. Returns null on parse failure (unclosed
 * quote), in which case callers fall back to the regex path. Implements just
 * enough POSIX-ish shell lexing: single quotes, double quotes (no expansion),
 * backslash escapes inside double quotes, and whitespace splitting.
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
        // Preserve backslash escapes (e.g. \" stays as \" so the flag-back
        // boundary regex can still match `--write"`).
        cur += c;
        if (i + 1 < s.length) {
          cur += s[i + 1];
          i++;
        }
      } else {
        cur += c;
      }
      i++;
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
 * Return the first real command token, skipping rtk/sudo/env/nohup/command
 * wrappers. Mirrors `_command_word` in the Python guard.
 */
function commandWord(tokens: string[]): string {
  let i = 0;
  while (i < tokens.length) {
    const base = basename(tokens[i]);
    if (PREFIXES.has(base)) {
      i++;
      if (base === "rtk" && i < tokens.length && RTK_SUB.has(tokens[i])) {
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
  return tokens.some((t) =>
    WRITE_FLAGS.some((f) => t === f || t.startsWith(f + "=")),
  );
}

/**
 * Split on unquoted shell control characters (; & | ( ) newline) and drop
 * shell comments (an unquoted # at the start of a word, extending to the end
 * of the line -- text inside comments never splits segments, so
 * `# note; mysql-cli --write` stays one inert comment). Quotes stay intact so
 * each segment remains independently analyzable; backslash escapes are
 * honored so `\;` never splits. Mirrors `_split_segments` in the Python guard.
 */
function splitShellSegments(command: string): string[] {
  const segments: string[] = [];
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
      } else if (c === "'") {
        inSingle = true;
        cur += c;
      } else if (c === '"') {
        inDouble = true;
        cur += c;
      } else if (c === "#" && (i === 0 || " \t;|&()\n".includes(command[i - 1]))) {
        // comment: consume through end of line; the newline itself is then
        // re-processed as a segment separator
        const nl = command.indexOf("\n", i);
        i = nl === -1 ? command.length - 1 : nl - 1;
      } else if (SEGMENT_SEPS.has(c)) {
        segments.push(cur);
        cur = "";
      } else {
        cur += c;
      }
    }
    i++;
  }
  segments.push(cur);
  return segments.map((s) => s.trim()).filter((s) => s.length > 0);
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
    if (!SHELLS.has(basename(tokens[i]))) continue;
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

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Last-resort regex for segments the tokenizer cannot parse (unclosed
 * quotes): mysql-cli must start at ^/whitespace/quote (so wrapped
 * `bash -c "mysql-cli ..."` still matches), and the flag must be a standalone
 * token or =value form. Err on the side of blocking here. Mirrors
 * `_regex_mysql_cli_write` in the Python guard -- the character classes are
 * intentionally identical.
 */
function regexMysqlCliWrite(segment: string): boolean {
  if (!/(?:^|[\s"'])mysql-cli\b/.test(segment)) return false;
  return WRITE_FLAGS.some((f) => {
    const re = new RegExp(`(?:^|[\\s"'])${escapeRegex(f)}(?=[\\s"';|&=]|$)`);
    return re.test(segment);
  });
}

/**
 * True if the command runs mysql-cli with a write gate flag. Mirrors
 * `_is_mysql_cli_write` in the Python guard. Exported for the shared
 * detection-matrix tests (hook_guard_test.go).
 */
export function isMysqlCliWrite(command: string, depth = 0): boolean {
  if (depth >= MAX_DEPTH) return false;
  for (const segment of splitShellSegments(command)) {
    const tokens = splitShellTokens(segment);
    if (tokens === null) {
      if (regexMysqlCliWrite(segment)) return true;
      continue;
    }
    const cmd = commandWord(tokens);
    if (DATA_SINKS.has(cmd)) {
      // echo/printf merely print their arguments; `echo mysql-cli --write`
      // is display text, not an invocation.
      continue;
    }
    if ((cmd === "mysql-cli" || tokens.includes("mysql-cli")) && hasWriteFlag(tokens)) {
      return true;
    }
    const payload = shellCPayload(tokens);
    if (payload && isMysqlCliWrite(payload, depth + 1)) return true;
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
