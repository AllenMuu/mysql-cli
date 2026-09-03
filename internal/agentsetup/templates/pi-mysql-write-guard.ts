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
 * Detection mirrors `mysql-write-guard.py`:
 *   - shell-token matching: a flag-looking string inside a quoted SQL literal
 *     (e.g. "SELECT '--write' ...") stays one quoted token and is NOT mistaken
 *     for the flag.
 *   - regex fallback anchors flags as standalone tokens; the back boundary
 *     also allows quote/shell-separator close so `bash -c "mysql-cli ... --write"`
 *     is caught.
 *   - rtk / sudo / env / nohup / command prefixes are skipped when locating the cmd.
 *   - Fail-open: any thrown error in the handler is swallowed so a buggy
 *     extension never blocks all bash usage.
 *
 * 覆盖范围限制：本 hook 仅拦截 agent 框架的 Bash/shell 执行路径。
 * agent 若通过非 Bash tool（如 Python subprocess、直接 exec 系统调用）调用 mysql-cli，
 * 不会触发本 hook。如需更强保障，应在 agent 配置中禁用非 Bash 执行路径，
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

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * True if the token list carries a write gate flag, as whole tokens or in
 * pflag's `--flag=value` form (`--write=true`). Exact-token matching alone
 * would let `--write=true` slip through the guard as a "read".
 */
function hasWriteFlag(tokens: string[]): boolean {
  return tokens.some(
    (t) => WRITE_FLAGS.some((f) => t === f || t.startsWith(f + "=")),
  );
}

/**
 * True if the command runs mysql-cli with a write gate flag. Mirrors
 * `_is_mysql_cli_write` in the Python guard.
 */
function isMysqlCliWrite(command: string): boolean {
  const tokens = splitShellTokens(command);
  if (tokens) {
    const cmd = commandWord(tokens);
    // cmd 是 basename 后的结果，已剥掉目录；endsWith("/mysql-cli") 在 basename
    // 后永远为 false（原代码冗余，已清理）。只比较 === "mysql-cli"。
    const isMysql = cmd === "mysql-cli";
    if (isMysql && hasWriteFlag(tokens)) return true;
  }
  // Fallback: 仅在 splitShellTokens 解析失败时触发（极罕见，通常是未闭合引号）。
  // 收紧 mysql-cli 的前边界到 ^ 或空白，避免 `echo "mysql-cli --write"` 这类
  // 把 mysql-cli 当字符串字面量传给其他命令的误报（原 \b 把 "-mysql-cli" 也
  // 算边界，导致引号内的 mysql-cli 字面量被误判为调用）。
  // 权衡：会漏掉 `bash -c "mysql-cli ... --write"` 这种 wrapped 调用——但仅在
  // splitShellTokens 失败时才漏；正常时走上面的 token 化路径会正确识别。
  // lookahead 同时接受 `=`，覆盖 `--write=true`（pflag 的 --flag=value 形式）。
  if (/(?:^|\s)mysql-cli\b/.test(command)) {
    for (const flag of WRITE_FLAGS) {
      const re = new RegExp(`(?:^|\\s)${escapeRegex(flag)}(?=[\\s"'\\';|&=]|$)`);
      if (re.test(command)) return true;
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
