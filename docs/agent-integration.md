# 多 Agent 写操作确认

mysql-cli 默认只读；写操作由 `--write`/`--ddl`/`--yes` 解锁。但这些 flag 是 AI 在命令行里自己传的--`--yes` 原义是“AI 已确认”，不是“找人类确认”。`mysql-cli agent init` 把写操作的确认权从 AI 手里拿回给人类：为各 agent 安装配置，命中写 flag 即弹窗，由用户批准才执行。

## 安装

```bash
mysql-cli agent init
```

交互式选择你用的 agent + 层级（项目/全局）。非交互环境用 flag：

```bash
mysql-cli agent init --agents claude,opencode,copilot --project
mysql-cli agent init --agents codebuddy --global
mysql-cli agent init --agents cursor --project --dry-run   # 预览不写
```

支持的 agent（逗号分隔）：`claude` `cursor` `opencode` `copilot` `codebuddy` `trae` `pi`。

## 能力对照

| Agent | name | 能力 | 机制 |
|---|---|---|---|
| Claude Code | `claude` | 精确强制 | PreToolUse hook -> `ask` |
| opencode | `opencode` | 精确强制 | `permission.bash` glob + `ask` |
| GitHub Copilot | `copilot` | 精确强制 | `autoApprove` 正则 `false` |
| CodeBuddy | `codebuddy` | 精确强制 | PreToolUse hook（兼容 Claude Code） |
| TRAE | `trae` | 精确强制 | PreToolUse hook（Claude Code 兼容，matcher=`RunCommand`） |
| Pi | `pi` | 精确强制 | `tool_call` hook -> `ctx.ui.confirm` -> `block`（matcher=`bash`） |
| Cursor | `cursor` | 仅引导 | `.cursor/rules` 注入上下文 |

> “精确”= 只对含 `--write`/`--ddl`/`--yes` 的命令弹窗，只读放行；“仅引导”= 依赖模型遵守规则，非引擎级闸门。
> 不含 Codex（hook 未坐实，`.rules` 无法按 flag 精确拦）。

## 写入位置

| Agent | 项目级（`--project`） | 全局（`--global`） |
|---|---|---|
| claude | `.claude/settings.json` + `.claude/hooks/mysql-write-guard.py` | `~/.claude/...` |
| cursor | `.cursor/rules/mysql-cli-write-guard.mdc` | 不支持（IDE 设置） |
| opencode | `opencode.json` | `~/.config/opencode/opencode.json` |
| copilot | `.vscode/settings.json` + `.github/copilot-instructions.md` | VS Code 用户 `settings.json` |
| codebuddy | `.codebuddy/settings.json` + `.codebuddy/hooks/mysql-write-guard.py` | `~/.codebuddy/...` |
| trae | `.trae/hooks.json` + `.trae/hooks/mysql-write-guard.py` | `~/.trae-cn/hooks.json` + `~/.trae-cn/hooks/mysql-write-guard.py` |
| pi | `.pi/extensions/mysql-write-guard.ts` | `~/.pi/agent/extensions/mysql-write-guard.ts` |

> TRAE 路径不对称：项目级用 `.trae`，全局用 `~/.trae-cn`（带 `-cn` 后缀，国际版/中国版相同）。这是 TRAE 官方设计，非 bug。TRAE `hooks.json` 顶层有 `version: 1`，matcher 用标准化工具名 `RunCommand`（非 Claude Code 的 `Bash`），hook 定义带 `timeout`。详见 [TRAE Hook 配置参考](https://docs.trae.cn/ide_hook-configuration-reference)。
>
> Pi 走 TypeScript 扩展（in-process，不走子进程），与 Claude/CodeBuddy/TRAE 的 command-type hook 不同，**不能复用 `mysql-write-guard.py`**——单独维护 `templates/pi-mysql-write-guard.ts`。Pi 自动发现 `extensions/*.ts`，**无需改 `settings.json`**；装好后在 pi 内 `/reload` 即可。项目级扩展首次启动 pi 时需 `/trust` 当前项目才加载（全局立即可用）。Pi 内置 shell 工具名是 `bash`（小写，非 `Bash`/`RunCommand`）；决策对象是 `{ block: true, reason? }`（无原生 `ask`，人机确认 UX 由扩展自己调 `ctx.ui.confirm()` 实现）。非交互模式（`pi -p` / `--mode json`）下 `ctx.ui` 不可用时扩展默认 block，避免静默自动放行写操作。详见 [Pi Extensions docs](https://pi.dev/docs/latest/extensions)。

合并类配置（`settings.json` / `opencode.json` / `.vscode/settings.json` / TRAE `.trae/hooks.json`）会深合并进现有文件并备份 `.bak`，不破坏既有内容；重复安装会按 command/键去重，幂等。单文件类（`.mdc` / instructions / `.md` / Pi `.ts`）默认跳过已存在文件，`--force` 覆盖。

## 验证

每个 agent 配置后，新会话里让 AI 跑两条对照：

- 只读（应**放行**）：`mysql-cli query "SELECT 1"`
- 写操作（应**弹窗**）：`mysql-cli query "UPDATE t SET a=1 WHERE id=1" --write`

hook 脚本可单独测（以 trae 为例，注意 TRAE 用 `RunCommand` 作为 `tool_name`；claude/codebuddy 用 `Bash`）：
```bash
echo '{"tool_name":"RunCommand","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write --yes"}}' \
  | python3 .trae/hooks/mysql-write-guard.py
# 期望: {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",...}}
```

> Pi 扩展是 in-process TypeScript，不走 stdin/stdout 协议，**不能用上述方式单独测**。验证方式：在 pi 内 `/reload` 后跑两条对照命令（读放行、写弹窗），或用 `node` 直接 import 扩展并 mock `ExtensionAPI` 调用 `tool_call` handler。

## 原理

- `--yes` 是 AI 传的 flag，CLI 内部无人类确认环节；`agent init` 装的配置在 agent 执行命令前拦截，把含 `--write`/`--ddl`/`--yes` 的执行交给人类弹窗批准。
- hook 脚本用 shlex 精确匹配 flag token，SQL 字面量里的 `--write` 文本不会误伤；回退正则只认独立 token，兼容 `bash -c` 包裹。Pi 扩展把同一检测逻辑以 TS 重写（`splitShellTokens` + 正则回退），语义对齐。
- CodeBuddy / Claude Code / TRAE 均依赖 Claude Code 兼容 PreToolUse hook（CodeBuddy 已交叉验证；TRAE 官方提供“导入 Claude Code Hook”开关，stdin/stdout JSON 兼容，但 matcher 用 TRAE 标准化工具名 `RunCommand` 而非 `Bash`；若某版本不认 `ask` JSON，回退 `exit 2` 阻断）。
- Pi 走独立的 `tool_call` 事件扩展机制（`pi.on("tool_call", handler)`，bail 派发），返回 `{ block: true, reason? }` 阻断。与 command-type hook 不同：扩展是 in-process TS 模块，直接拿到事件对象作参数，不走子进程 stdin/stdout。Pi 无原生 `ask` 决策，扩展自己调 `ctx.ui.confirm()` 弹窗；非交互模式下 `ctx.ui` 不可用时默认 block（保守）。
- 配置模板内嵌于 mysql-cli 二进制（`internal/agentsetup/templates/`），随版本发布；升级 mysql-cli 后重跑 `agent init` 即可刷新。
- **hook 覆盖范围有限**：写操作确认 hook 仅覆盖 agent 框架的 Bash/shell 执行路径；agent 若通过非 Bash tool（如 Python subprocess、直接 exec）调用 mysql-cli，不会触发人类确认。建议在 agent 配置中禁用非 Bash 执行路径，或依赖 CLI 内部的退出码契约（写操作仍需 `--write`/`--ddl`/`--yes`，缺 flag 时以 exit 3/4/5 拒绝）。
