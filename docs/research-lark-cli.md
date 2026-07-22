# lark-cli (yjwong/lark-cli) 调研报告

## 1. 项目概况

| 维度 | 内容 |
|------|------|
| 仓库 | https://github.com/yjwong/lark-cli |
| 语言 | Go |
| Stars | ~53 |
| 定位 | Lark/Feishu API 的 CLI 工具，专为 Claude Code 等 AI agent 设计 |
| 核心动机 | 官方 Lark MCP server 的 tool 返回过于冗长，token 效率低；改为 CLI 返回紧凑 JSON |

**设计理念与 mysql-cli 高度相似**：
- Agent 是首要调用方（默认 JSON 输出）
- 替代 MCP server，无需 MCP runtime
- 命令行子命令组织功能
- 紧凑、可编程的输出格式

---

## 2. 安装到 AI Agents 的方式

### 2.1 技能目录分发（skills/）

lark-cli 在项目根目录提供 `skills/` 文件夹，内含 8 个预置 skill：

```
skills/
├── bitable/      → SKILL.md
├── calendar/     → SKILL.md
├── contacts/     → SKILL.md
├── documents/    → SKILL.md
├── email/        → SKILL.md
├── messages/     → SKILL.md
├── minutes/      → SKILL.md
└── sheets/       → SKILL.md
```

**安装方式**（README 明确说明）：

```bash
# 项目级（推荐）
cp -r skills/* /path/to/your/project/.claude/skills/

# 用户级
cp -r skills/* ~/.claude/skills/
```

安装后，Claude Code 会自动读取这些 SKILL.md，在对应场景下调用 `lark` 命令。

### 2.2 运行环境假设

- 假设 `lark` 二进制在 PATH 中；否则需编辑 SKILL.md 中的路径
- 通过 `LARK_CONFIG_DIR` 环境变量指定配置目录（默认 `~/.lark/`）
- 配置文件：`.lark/config.yaml`

---

## 3. Skill 定义模式深度分析

### 3.1 文件结构

每个 skill 只有一个 `SKILL.md`，采用 **YAML frontmatter + Markdown** 格式：

```yaml
---
name: calendar
description: Manage Lark calendar - view schedule, create/update/delete events, ...
---
```

`description` 字段极其重要：它告诉 Claude Code **何时应该使用这个 skill**（触发条件）。

### 3.2 内容组织模式

| 板块 | 作用 | 示例 |
|------|------|------|
| **Running Commands** | 如何调用 CLI、环境变量 | `lark cal <command>` |
| **Before Running** | 前置检查（如时间、认证） | `date '+%Y-%m-%d'`、`lark auth status` |
| **Commands Reference** | 完整命令列表及参数 | 按子命令分组，带 flag 说明 |
| **Output Formats** | 输出结构示例 | JSON 字段解释 |
| **Error Handling** | 常见错误码及修复 | `AUTH_ERROR` → `lark auth login` |
| **Required Permissions** | 权限/scope 说明 | 如何增量添加权限 |
| **Typical Workflow** | 典型操作流 | 1. sync → 2. search → 3. show |
| **Best Practices / EA Best Practices** | Agent 行为指导 | 调度前检查冲突、维护缓冲时间 |
| **Integration** | 与其他 skill 联动 | calendar + contacts 联查 |

### 3.3 设计亮点

1. **触发条件明确**：`description` 中包含 "Use when user asks about..."，让 agent 知道何时加载该 skill
2. **前置检查引导**：calendar skill 要求 agent **总是先检查当前时间**，避免时区/日期错误
3. **错误自修复**：每个 skill 都列出常见错误及对应的修复命令，agent 可自动重试
4. **工作流封装**：不只罗列命令，而是给出 "典型工作流"（如邮件：status → sync → search → show）
5. **跨 skill 联动**：calendar skill 说明如何与 contacts skill 联用（查参会人信息）
6. **EA Best Practices**：针对特定领域的 agent 行为准则（如日程管理中的缓冲时间、过载检测）

---

## 4. CLI 设计对比（lark-cli vs mysql-cli）

| 维度 | lark-cli | mysql-cli |
|------|----------|-----------|
| **默认输出** | JSON | JSON |
| **错误格式** | `{"error":true,"code":"...","message":"..."}` | `{"success":false,"error":{"code":"...","message":"..."}}` |
| **成功格式** | 按命令不同，通常直接返回数据对象 | 严格信封 `{"success":true,"data":{...}}` |
| **子命令分组** | `lark cal/contact/doc/msg/mail/minutes` | `mysql-cli query/txn/schema/sample/tables/...` |
| **退出码** | 未明确强调（靠 JSON error 识别） | **稳定退出码是核心契约**（2~10） |
| **认证管理** | 内置 `lark auth login/status/logout` | 依赖配置文件 + 环境变量 |
| **配置** | `.lark/config.yaml` | `~/.config/mysql-cli/config.toml` |
| **Scope/权限** | 增量授权 `--add --scopes <group>` | 安全闸门（--write/--ddl/--yes） |
| **skill 文件** | ✅ 8 个 SKILL.md | ❌ 无 |
| **Agent 使用指南** | ✅ 每个 skill 都有 | ❌ 只有 AGENTS.md（面向开发者） |

**结论**：mysql-cli 的 CLI 底层设计（JSON 信封、退出码、子命令、安全模型）**已经比 lark-cli 更完善**；但在 **agent 使用层**（skill 定义、安装分发、工作流指导）上，lark-cli 提供了完整的范例。

---

## 5. 对 mysql-cli 的借鉴建议

### 5.1 建议一：提供 `skills/` 目录（高优先级）

在 mysql-cli 项目根目录增加 `skills/mysql/` 或 `skills/query/`、`skills/schema/` 等 skill 定义。

**理由**：
- 降低 agent 用户的接入成本：一条 `cp` 命令即可让 Claude Code 具备 MySQL 查询能力
- 与 lark-cli 形成生态对齐，用户心智统一
- 是项目从 "好用" 到 "易用" 的关键一步

**推荐结构**：

```
skills/
├── mysql-query/      → SKILL.md  # SQL 查询、事务、写入
└── mysql-schema/     → SKILL.md  # 库表结构探索、采样
```

或按功能聚合为单个 `skills/mysql/SKILL.md`（更简洁）。

### 5.2 建议二：Skill 内容设计（中优先级）

参照 lark-cli 模式，mysql-cli 的 SKILL.md 应包含：

1. **触发条件**（description）
   > "Use when user asks about database queries, table structures, MySQL data, or wants to run SQL."

2. **前置检查**
   - 检查配置文件是否存在
   - 检查 datasource 是否可达（可选 `mysql-cli schema` 轻量探测）

3. **命令参考**
   - 按场景分组：查询、事务、结构探索
   - 明确 `--write` / `--ddl` / `--yes` 的使用时机

4. **安全模型说明**
   - 默认只读，DML 需 `--write`
   - DDL 需 `--write --ddl`
   - 破坏性操作需 `--yes`
   - 这是 mysql-cli 的核心差异点，skill 中必须强调

5. **典型工作流**
   ```
   1. explore / tables → 了解库表
   2. schema <table> → 看表结构
   3. sample <table> → 采样数据
   4. query "SELECT ..." → 精确查询
   5. --write 时执行 DML
   ```

6. **错误自修复**
   - Exit 3 (READONLY_VIOLATION) → 加 `--write`
   - Exit 4 (DDL_NEEDS_WRITE) → 加 `--write --ddl`
   - Exit 5 (DESTRUCTIVE) → 加 `--yes`
   - Exit 7 (MULTI_STATEMENT) → 改用 `txn`

7. **输出处理提示**
   - JSON 默认，可用 jq 提取
   - 大结果集建议加 `--limit`

### 5.3 建议三：README 增加 "Usage with Claude Code" 章节（低优先级）

lark-cli 的 README 有专门的 "Usage with Claude Code" 章节，包含：
- 安装 skills 的命令
- 可用 skill 列表
- 配置注意事项

mysql-cli 的 README 目前侧重人类用户，可增加一小节说明 agent 使用方式。

### 5.4 建议四：AGENTS.md vs SKILL.md 职责分离（已完成，需扩展）

mysql-cli 已有 `AGENTS.md`，但它**面向开发该项目的 AI agent**（代码规范、架构说明）。

lark-cli 的 `SKILL.md`**面向使用 CLI 的 AI agent**（命令调用、工作流）。

两者职责不同，应共存：
- `AGENTS.md` → 继续维护，指导 AI 修改代码
- `skills/*/SKILL.md` → 新增，指导 AI 调用 mysql-cli

---

## 6. 风险与注意事项

1. **skill 维护成本**：mysql-cli 子命令和功能会演进，skill 文档需要同步更新，否则 agent 会得到过时信息。
2. **不同 agent 平台的兼容性**：`.claude/skills/` 是 Claude Code 的约定；Cursor、Codex、Aider 等可能使用不同机制（如 Cursor Rules、MCP、自定义工具定义）。
3. **skill 粒度**：mysql-cli 功能相对内聚（围绕 SQL），是否拆分多个 skill 还是放一个，需要权衡。

---

## 7. 结论

| 评估项 | 结论 |
|--------|------|
| **是否需要提供 skills/ 目录？** | **是**。这是降低 agent 用户接入门槛的最有效方式，也是与 lark-cli 生态对齐的关键动作。 |
| **lark-cli 最值得借鉴的是什么？** | **Skill 定义模式**（触发条件 + 前置检查 + 工作流 + 错误自修复 + 最佳实践），而非底层 CLI 设计。mysql-cli 的 CLI 设计（JSON 信封、退出码、安全模型）已经更完善。 |
| **立即行动项** | 1. 创建 `skills/mysql/SKILL.md`<br>2. 在 README 增加 agent 使用说明<br>3. 建立 skill 与 CLI 变更的同步机制 |
