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

支持的 agent（逗号分隔）：`claude` `codex` `cursor` `opencode` `copilot` `codebuddy` `trae` `pi`。

## 能力对照

| Agent | name | 能力 | 机制 |
|---|---|---|---|
| Claude Code | `claude` | 精确强制 | PreToolUse hook -> `ask` |
| Codex | `codex` | 精确强制 | Rules 粗门 `prompt` + PermissionRequest hook 精过滤 |
| opencode | `opencode` | 精确强制 | `permission.bash` glob + `ask` |
| GitHub Copilot | `copilot` | 精确强制 | `autoApprove` 正则 `false` |
| CodeBuddy | `codebuddy` | 精确强制 | PreToolUse hook（兼容 Claude Code） |
| TRAE | `trae` | 精确强制 | PreToolUse hook（Claude Code 兼容，matcher=`RunCommand`） |
| Pi | `pi` | 精确强制 | `tool_call` hook -> `ctx.ui.confirm` -> `block`（matcher=`bash`） |
| Cursor | `cursor` | 仅引导 | `.cursor/rules` 注入上下文 |

> “精确”= 只对含 `--write`/`--ddl`/`--yes` 的命令弹窗，只读放行；“仅引导”= 依赖模型遵守规则，非引擎级闸门。
>
> Codex 的强制实现**不是** Claude PreToolUse `ask` 的完全等价（Codex 不支持该决策），而是“Rules 把 mysql-cli 调用全部路由进审批路径 + PermissionRequest hook 自动放行已证明只读的调用”，详见下方 Codex 特殊说明。

## 写入位置

| Agent | 项目级（`--project`） | 全局（`--global`） |
|---|---|---|
| claude | `.claude/settings.json` + `.claude/hooks/mysql-write-guard.py` | `~/.claude/...` |
| codex | `.codex/hooks.json` + `.codex/hooks/mysql-write-guard.py` + `.codex/rules/mysql-cli-write-guard.rules` | `~/.codex/...`（同构三件） |
| cursor | `.cursor/rules/mysql-cli-write-guard.mdc` | 不支持（IDE 设置） |
| opencode | `opencode.json` | `~/.config/opencode/opencode.json` |
| copilot | `.vscode/settings.json` + `.github/copilot-instructions.md` | VS Code 用户 `settings.json` |
| codebuddy | `.codebuddy/settings.json` + `.codebuddy/hooks/mysql-write-guard.py` | `~/.codebuddy/...` |
| trae | `.trae/hooks.json` + `.trae/hooks/mysql-write-guard.py` | `~/.trae-cn/hooks.json` + `~/.trae-cn/hooks/mysql-write-guard.py` |
| pi | `.pi/extensions/mysql-write-guard.ts` | `~/.pi/agent/extensions/mysql-write-guard.ts` |

> TRAE 路径不对称：项目级用 `.trae`，全局用 `~/.trae-cn`（带 `-cn` 后缀，国际版/中国版相同）。这是 TRAE 官方设计，非 bug。TRAE `hooks.json` 顶层有 `version: 1`，matcher 用标准化工具名 `RunCommand`（非 Claude Code 的 `Bash`），hook 定义带 `timeout`。详见 [TRAE Hook 配置参考](https://docs.trae.cn/ide_hook-configuration-reference)。
>
> Pi 走 TypeScript 扩展（in-process，不走子进程），与 Claude/CodeBuddy/TRAE 的 command-type hook 不同，**不能复用 `mysql-write-guard.py`**——单独维护 `templates/pi-mysql-write-guard.ts`。Pi 自动发现 `extensions/*.ts`，**无需改 `settings.json`**；装好后在 pi 内 `/reload` 即可。项目级扩展首次启动 pi 时需 `/trust` 当前项目才加载（全局立即可用）。Pi 内置 shell 工具名是 `bash`（小写，非 `Bash`/`RunCommand`）；决策对象是 `{ block: true, reason? }`（无原生 `ask`，人机确认 UX 由扩展自己调 `ctx.ui.confirm()` 实现）。非交互模式（`pi -p` / `--mode json`）下 `ctx.ui` 不可用时扩展默认 block，避免静默自动放行写操作。详见 [Pi Extensions docs](https://pi.dev/docs/latest/extensions)。

### Codex 特殊说明

Codex 没有 Claude 兼容的 PreToolUse `ask`（该决策被解析但**不支持**：hook 会被标记为 failed 且 tool call **继续执行**），所以**不能**把 `mysql-write-guard.py` 原样装给 Codex。mysql-cli 采用两层组合：

1. **Rules 粗门**（`.codex/rules/mysql-cli-write-guard.rules`）：`prefix_rule(pattern=["mysql-cli"], decision="prompt")` 把每条 mysql-cli 调用（含 `rtk`/`rtk proxy`/`sudo`/`command`/`nohup` 包装前缀）路由进 Codex 审批路径。`prefix_rule` 只做有序前缀 token 匹配，无法表达“任意位置的 `--write`”，所以规则只负责粗门，不判断读写。
2. **PermissionRequest hook 精过滤**（`.codex/hooks/mysql-write-guard.py` + `hooks.json`）：仅在 Codex 准备发起审批时运行。已证明只读的 mysql-cli 调用（白名单子命令：`query`/`schema`/`sample`/`tables`/`databases`/`read`/`explore`/`analyze`/`version`）返回 `allow`（跳过弹窗）；写 flag 命令、复合命令（管道/`;`/`&&`）、重定向、命令替换、解析失败——一律**不做决策**（exit 0 + 空 stdout），保持 Codex 原生人类审批框。非只读子命令也不放行：`agent`/`config` 会写本地文件（hook/规则/信任状态）、`txn` 在 CLI 层强制 `--write`、裸 `mysql-cli` 进入交互 REPL。失败方向是 fail-to-prompt：宁可多弹一次窗，绝不静默放行写操作。

已知边界（不宣称与 Claude `PreToolUse -> ask` 100% 等价）：

- `env VAR=... mysql-cli ...` 无法用前缀模式表达，不进 Rules 粗门（hook 侧同样无法证明，保持原生审批）；
- 绝对路径调用（如 `/usr/local/bin/mysql-cli ...`、`$GOBIN/mysql-cli ...`）不匹配任何前缀模式，不进 Rules 粗门，PermissionRequest hook 也不会为其触发——退化为 Codex 原生审批策略 + CLI 内部退出码契约兜底（写操作缺 `--write`/`--ddl`/`--yes` 时 exit 3/4/5 拒绝）；
- 复杂 shell 嵌套（如 `bash -c "mysql-cli ... --write"`）不保证被 rules 引擎拆分评估；
- PermissionRequest 只在 Codex 原本准备发起 approval 时触发；`approval_policy=never` 等特殊模式下 prompt 规则会导致拒绝（安全方向，非静默放行）；
- hooks 被关闭、project 未 trust、hook 未 trust、rules 未加载、`bypassPermissions` 等情形下不生效；
- 项目级 hooks/rules 仅在用户 trust 项目 `.codex` 层后加载——`agent init` **不会自动 trust**（人工信任边界是安全设计的一部分）。

Codex 官方定位 tool hooks 为 useful guardrail 而非完整 security boundary；兜底仍有 mysql-cli 内部退出码契约（写操作缺 `--write`/`--ddl`/`--yes` 时 exit 3/4/5 拒绝）。hooks.json 的 schema 声明（`PermissionRequest` 事件、`decision.behavior: "allow"` 输出协议、matcher 为作用在工具名上的正则、timeout 单位为秒）已对照官方 Codex Hooks 文档与本机 codex-cli 0.153.0 核验；官方文档另注明多个 hook 同时决策时 **any deny wins**、否则 `allow` 跳过审批弹窗。Rules 语法与 hooks 协议见 [Codex Execution Policy](https://developers.openai.com/codex/exec-policy) 与 [Codex Hooks](https://developers.openai.com/codex/hooks)（以当前 release 文档为准）。

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

Codex 的 hook 用 `Bash` 作为 `tool_name`，输出协议不同（只读 -> `allow`，其余 -> 空）：
```bash
echo '{"tool_name":"Bash","tool_input":{"command":"mysql-cli query \"SELECT 1\""}}' \
  | python3 .codex/hooks/mysql-write-guard.py
# 期望: {"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "allow"}}}
echo '{"tool_name":"Bash","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write"}}' \
  | python3 .codex/hooks/mysql-write-guard.py
# 期望: 无输出（exit 0）-> Codex 走原生审批
```

Codex rules 可用 `codex execpolicy check` 离线验证：
```bash
codex execpolicy check --pretty \
  --rules .codex/rules/mysql-cli-write-guard.rules \
  -- mysql-cli query "SELECT 1"
# 期望 decision 为 prompt
```

> Pi 扩展是 in-process TypeScript，不走 stdin/stdout 协议，**不能用上述方式单独测**。验证方式：在 pi 内 `/reload` 后跑两条对照命令（读放行、写弹窗），或用 `node` 直接 import 扩展并 mock `ExtensionAPI` 调用 `tool_call` handler。

## 原理

- `--yes` 是 AI 传的 flag，CLI 内部无人类确认环节；`agent init` 装的配置在 agent 执行命令前拦截，把含 `--write`/`--ddl`/`--yes` 的执行交给人类弹窗批准。
- hook 脚本用 shlex 精确匹配 flag token，SQL 字面量里的 `--write` 文本不会误伤；回退正则只认独立 token，兼容 `bash -c` 包裹。Pi 扩展把同一检测逻辑以 TS 重写（`splitShellTokens` + 正则回退），语义对齐。
- CodeBuddy / Claude Code / TRAE 均依赖 Claude Code 兼容 PreToolUse hook（CodeBuddy 已交叉验证；TRAE 官方提供“导入 Claude Code Hook”开关，stdin/stdout JSON 兼容，但 matcher 用 TRAE 标准化工具名 `RunCommand` 而非 `Bash`；若某版本不认 `ask` JSON，回退 `exit 2` 阻断）。
- Codex 不支持 `ask` 决策（返回它会被视为 hook 失败且命令继续执行），因此检测语义复用、**输出协议分离**：单独维护 `templates/codex-mysql-write-guard.py`（PermissionRequest，只输出 `allow` 或不输出，fail-to-prompt），外加 `templates/codex-mysql-cli.rules` 粗门前缀规则。
- Pi 走独立的 `tool_call` 事件扩展机制（`pi.on("tool_call", handler)`，bail 派发），返回 `{ block: true, reason? }` 阻断。与 command-type hook 不同：扩展是 in-process TS 模块，直接拿到事件对象作参数，不走子进程 stdin/stdout。Pi 无原生 `ask` 决策，扩展自己调 `ctx.ui.confirm()` 弹窗；非交互模式下 `ctx.ui` 不可用时默认 block（保守）。
- 配置模板内嵌于 mysql-cli 二进制（`internal/agentsetup/templates/`），随版本发布；升级 mysql-cli 后重跑 `agent init` 即可刷新。
- **hook 覆盖范围有限**：写操作确认 hook 仅覆盖 agent 框架的 Bash/shell 执行路径；agent 若通过非 Bash tool（如 Python subprocess、直接 exec）调用 mysql-cli，不会触发人类确认。建议在 agent 配置中禁用非 Bash 执行路径，或依赖 CLI 内部的退出码契约（写操作仍需 `--write`/`--ddl`/`--yes`，缺 flag 时以 exit 3/4/5 拒绝）。
