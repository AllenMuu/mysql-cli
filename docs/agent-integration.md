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

支持的 agent（逗号分隔）：`claude` `cursor` `opencode` `copilot` `codebuddy`。

## 能力对照

| Agent | name | 能力 | 机制 |
|---|---|---|---|
| Claude Code | `claude` | 精确强制 | PreToolUse hook -> `ask` |
| opencode | `opencode` | 精确强制 | `permission.bash` glob + `ask` |
| GitHub Copilot | `copilot` | 精确强制 | `autoApprove` 正则 `false` |
| CodeBuddy | `codebuddy` | 精确强制 | PreToolUse hook（兼容 Claude Code） |
| Cursor | `cursor` | 仅引导 | `.cursor/rules` 注入上下文 |

> “精确”= 只对含 `--write`/`--ddl`/`--yes` 的命令弹窗，只读放行；“仅引导”= 依赖模型遵守规则，非引擎级闸门。
> 不含 Codex（hook 未坐实，`.rules` 无法按 flag 精确拦）、TRAE（规则文件格式未坐实）。

## 写入位置

| Agent | 项目级（`--project`） | 全局（`--global`） |
|---|---|---|
| claude | `.claude/settings.json` + `.claude/hooks/mysql-write-guard.py` | `~/.claude/...` |
| cursor | `.cursor/rules/mysql-cli-write-guard.mdc` | 不支持（IDE 设置） |
| opencode | `opencode.json` | `~/.config/opencode/opencode.json` |
| copilot | `.vscode/settings.json` + `.github/copilot-instructions.md` | VS Code 用户 `settings.json` |
| codebuddy | `.codebuddy/settings.json` + `.codebuddy/hooks/mysql-write-guard.py` | `~/.codebuddy/...` |

合并类配置（`settings.json` / `opencode.json` / `.vscode/settings.json`）会深合并进现有文件并备份 `.bak`，不破坏既有内容；重复安装会按 command/键去重，幂等。单文件类（`.mdc` / instructions / `.md`）默认跳过已存在文件，`--force` 覆盖。

## 验证

每个 agent 配置后，新会话里让 AI 跑两条对照：

- 只读（应**放行**）：`mysql-cli query "SELECT 1"`
- 写操作（应**弹窗**）：`mysql-cli query "UPDATE t SET a=1 WHERE id=1" --write`

hook 脚本可单独测：
```bash
echo '{"tool_name":"Bash","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write --yes"}}' \
  | python3 .codebuddy/hooks/mysql-write-guard.py
# 期望: {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",...}}
```

## 原理

- `--yes` 是 AI 传的 flag，CLI 内部无人类确认环节；`agent init` 装的配置在 agent 执行命令前拦截，把含 `--write`/`--ddl`/`--yes` 的执行交给人类弹窗批准。
- hook 脚本用 shlex 精确匹配 flag token，SQL 字面量里的 `--write` 文本不会误伤；回退正则只认独立 token，兼容 `bash -c` 包裹。
- CodeBuddy / Claude Code 依赖其兼容 Claude Code PreToolUse hook（CodeBuddy 已交叉验证；若某版本不认 `ask` JSON，回退 `exit 2` 阻断）。
- 配置模板内嵌于 mysql-cli 二进制（`internal/agentsetup/templates/`），随版本发布；升级 mysql-cli 后重跑 `agent init` 即可刷新。
