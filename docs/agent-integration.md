# 多 Agent 写操作确认适配

mysql-cli 默认只读；写操作由 `--write`/`--ddl`/`--yes` 三个 flag 解锁（见 `skills/mysql-shared/SKILL.md` 的 Security Model）。但这些 flag 是 AI 在命令行里自己传的——`--yes` 的原义是"AI 已确认"，不是"找人类确认"。本目录的各 agent 配置把写操作的确认权**从 AI 手里拿回给人类**：命中写 flag 即弹窗，由用户批准才执行。

## 能力对照

| Agent | 配置文件 | 机制 | 精度 |
| --- | --- | --- | --- |
| Claude Code | `~/.claude/settings.json` + `~/.claude/hooks/mysql-write-guard.py` | PreToolUse hook → `permissionDecision: ask` | 精确（按 flag） |
| opencode | `opencode.json` | `permission.bash` 字符级 glob + `ask` | 精确（按 flag） |
| GitHub Copilot | `.vscode/settings.json` | `chat.tools.terminal.autoApprove` 正则设 `false` | 精确（按 flag） |
| CodeBuddy | `.codebuddy/settings.json` + `scripts/mysql-write-guard.py` | PreToolUse hook → `ask`（兼容 Claude Code hook） | 精确（按 flag） |
| Cursor | `.cursor/rules/mysql-cli-write-guard.mdc` | 规则注入模型上下文 | **仅引导**（非硬性） |
| Codex CLI | 待定（见下） | — | — |
| TRAE | 暂未适配（官方文档为 JS 渲染 SPA，未坐实规则文件格式） | — | — |

> "精确"= 只对含 `--write`/`--ddl`/`--yes` 的命令弹窗，只读放行；"仅引导"= 依赖模型遵守规则，非引擎级闸门。

## 配置层级

默认全部为**项目级**配置，随本 repo 分发，多人多机共享。若希望仅在本机生效（不随 repo），把对应文件移到各 agent 的用户级路径：

| Agent | 项目级（默认） | 用户级 |
| --- | --- | --- |
| opencode | `opencode.json` | `~/.config/opencode/opencode.json` |
| Copilot | `.vscode/settings.json` | VS Code 用户 `settings.json` |
| CodeBuddy | `.codebuddy/settings.json` | `~/.codebuddy/settings.json` |
| Cursor | `.cursor/rules/*.mdc` | Cursor 用户规则（IDE 设置） |
| Claude Code | `~/.claude/settings.json`（已全局） | — |

> opencode 的 `permission` 在用户级与项目级同时存在时按 last-match-wins 合并；Copilot 的 `autoApprove` 用户级可被项目级覆盖。项目级是推荐默认。

## Codex CLI（待定）

Codex 的命令拦截只有 `~/.codex/rules/*.rules`（Starlark `prefix_rule`），**位置前缀匹配，无 regex/无通配/无自定义脚本**，无法精确按 flag 拦截。官方 docs 目录（15 个文件）无 `hooks.md`，`config.md`/`execpolicy`/`example-config` 均无可编程命令拦截 hook；仅 `requirements.toml` 的 `allow_managed_hooks_only` 暗示存在某种 hook configs，但官方未文档化其能力。

两条可用路径，二选一（或叠加）：

1. **粗粒度强制**——对所有 `mysql-cli` 调用 prompt（只读也会弹窗）：
   ```python
   # ~/.codex/rules/mysql.rules
   prefix_rule(
       pattern = ["mysql-cli"],
       decision = "prompt",
       justification = "mysql-cli may perform destructive writes; require human approval.",
   )
   ```
2. **仅引导**——在 `AGENTS.md`（Codex 读）写规则，依赖模型自觉（非强制）。本 repo 的 `AGENTS.md` 已含该指引。

若你确认 Codex 某版本有可编程命令拦截 hook（能跑脚本检测 flag），把文档/字段名补上，我即刻改用 hook 方案。

## 验证

每个 agent 配置后，在新会话里让 AI 跑两条命令对照：

- 只读（应**放行**，无弹窗）：`mysql-cli query "SELECT 1"`
- 写操作（应**弹窗**）：`mysql-cli query "UPDATE t SET a=1 WHERE id=1" --write`

CodeBuddy/Claude Code 的 hook 脚本可单独测：
```bash
echo '{"tool_name":"Bash","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write --yes"}}' \
  | python3 scripts/mysql-write-guard.py
# 期望输出: {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",...}}
```

## 文件清单

- `scripts/mysql-write-guard.py` — 公共 PreToolUse hook 脚本（CodeBuddy 用；Claude Code 全局有同名副本）。
- `opencode.json` — opencode 写操作 `permission.bash` 规则。
- `.vscode/settings.json` — Copilot 终端命令 `autoApprove` 正则。
- `.codebuddy/settings.json` — CodeBuddy PreToolUse hook 注册。
- `.cursor/rules/mysql-cli-write-guard.mdc` — Cursor 引导规则。
- `.github/copilot-instructions.md` — Copilot 仓库级指令（引导补充）。
- `CODEBUDDY.md` — CodeBuddy 主规则（引导补充）。
- `AGENTS.md` — 通用 agent 入口，已追加写操作确认指引（Codex/opencode 等读）。
