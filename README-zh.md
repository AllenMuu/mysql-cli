<div align="center">

# mysql-cli

**让任何能跑 shell 的 AI agent 直接查询 MySQL 的 Go CLI - 无需 MCP runtime。**

[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server) 的替代方案:
保留其全部读写能力,以普通子命令重新暴露。只要你的 agent 能跑 shell,就能查询 MySQL。

[English](./README.md) · [简体中文](./README-zh.md)

[![版本](https://img.shields.io/github/v/release/AllenMuu/mysql-cli?label=version)](https://github.com/AllenMuu/mysql-cli/releases)
[![Go 版本](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![平台](https://img.shields.io/badge/平台-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#安装)
[![输出](https://img.shields.io/badge/输出-JSON%20%7C%20table%20%7C%20CSV%20%7C%20TSV-blue)](#输出)

</div>

---

## 为什么需要它

原 MCP server 很好用 -- 直到你想在不支持 MCP 的 agent 里调用它。`mysql-cli` 保留了同样的
安全模型和功能集,但作为一个**默认输出 JSON**、**退出码稳定**的单二进制分发,任何 agent
(Claude Code、Cursor、Codex、Aider ……)都能直接通过 shell 驱动它。

- **Agent 优先** - 稳定的 JSON 信封 + 数字退出码,设计目标是被解析,而非被阅读。
- **默认安全** - 开箱即只读;写 / DDL / 破坏性操作需要显式 flag。
- **零配置迁移** - 直接兼容 MCP server 的 `MYSQL_*` 环境变量。
- **多数据源** - TOML 命名 profile,可选 SSH 隧道。
- **单一二进制** - `go install` 即用。

## 安装

根据你的身份选择对应路径。

### 人类用户

**一键脚本**(二进制 + agent skills + 写操作确认配置,支持 macOS/Linux/Windows):

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/AllenMuu/mysql-cli/main/install.sh -o install.sh
bash install.sh                 # 直接运行(不要 curl|bash),以保留交互提示
# Windows (PowerShell)
.\install.ps1
```

脚本会跑三步,每一步的交互提示都会问你:
1. 下载预编译二进制到 `~/.local/bin`(Windows 上是 `%USERPROFILE%\.local\bin`)。
2. 跑 `npx skills add AllenMuu/mysql-cli`,让你的 agent 能发现 `mysql-cli`。
3. 跑 `mysql-cli agent init`,让写操作弹出人类确认(见 [写操作确认](#写操作确认agent-init))。

**其他方式**(想精简步骤):

```bash
# npm wrapper(无需 Go 工具链,下载预编译二进制)
npx @allenmuu/mysql-cli install

# Go install
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest

# 源码构建
git clone https://github.com/AllenMuu/mysql-cli.git && cd mysql-cli
go build -o mysql-cli ./cmd/mysql-cli
```

然后配置数据源并查询(见[配置](#配置)):

```bash
mysql-cli config init --global      # 写入 ~/.config/mysql-cli/config.toml
# 编辑文件:host/user/password/database
mysql-cli query "SELECT * FROM users LIMIT 10"
mysql-cli                           # 进入 REPL(仅人类调试用)
```

### AI Agent

`mysql-cli` **没有浏览器认证流程**,agent 可在 shell 内分四步完成全部设置。编程式调用时
保持 `-f json`(默认值)-- JSON 信封 + 退出码是你解析的契约。

**第 1 步 - 安装二进制**

```bash
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest
# `mysql-cli` 必须在 PATH 中 -- skill 按名字调用它。
# 备选:go build -o mysql-cli ./cmd/mysql-cli
```

**第 2 步 - 安装 Agent Skills**

`mysql-cli` 通过 [vercel-labs/skills](https://github.com/vercel-labs/skills) 生态提供 skills,
让 agent 第一次就能正确发现并调用它。

```bash
# 交互式(TTY)
npx skills add AllenMuu/mysql-cli
# 非交互(CI / agent)
npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -g -y
```

> 务必安装**全部 3 个** skill(`mysql-shared`、`mysql-query`、`mysql-schema`)。
> 后两个引用 `../mysql-shared/SKILL.md`,只装单个会导致引用断裂。
>
> 无 Node.js?手动把仓库的 `skills/` 目录复制到 agent 的 skill 目录(如 `~/.claude/skills/`)。

**第 3 步 - 配置数据源**

写入 `~/.config/mysql-cli/config.toml`(完整格式见[配置](#配置)):

```toml
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
password = "${MYSQL_DEV_PASSWORD}"
database = "app"
```

**第 4 步 - 验证并执行**

```bash
mysql-cli query "SELECT * FROM users LIMIT 10"      # 默认 JSON 输出
```

响应契约见[输出](#输出)与[退出码](#退出码)。

## 写操作确认(`agent init`)

`--write` / `--ddl` / `--yes` 是 **AI 自己传**的 flag,表示它要修改数据。CLI 自身无法
把人类拉进确认环节。`mysql-cli agent init` 解决这个问题:为各 agent 安装配置,
**拦截任何包含这些 flag 的命令,在执行前弹窗找人类确认**。只读命令直接放行。

### 它装了什么

| Agent | `--project`(当前项目) | `--global`(用户级) | 能力 |
| --- | --- | --- | --- |
| `claude` | `.claude/settings.json` + `.claude/hooks/mysql-write-guard.py` | `~/.claude/...` | **强制**(PreToolUse hook → ask) |
| `codex` | `.codex/hooks.json` + `.codex/hooks/mysql-write-guard.py` + `.codex/rules/mysql-cli-write-guard.rules` | `~/.codex/...`(同构三件) | **强制**(Rules 粗门 prompt + PermissionRequest hook → 人类审批) |
| `cursor` | `.cursor/rules/mysql-cli-write-guard.mdc` | _不支持_(IDE 设置) | **引导**(仅规则) |
| `opencode` | `opencode.json` | `~/.config/opencode/opencode.json` | **强制**(permission glob → ask) |
| `copilot` | `.vscode/settings.json` + `.github/copilot-instructions.md` | VS Code User `settings.json` | **强制**(`autoApprove` 正则 → false) |
| `codebuddy` | `.codebuddy/settings.json` + `.codebuddy/hooks/mysql-write-guard.py` | `~/.codebuddy/...` | **强制**(PreToolUse hook → ask) |
| `trae` | `.trae/hooks.json` + `.trae/hooks/mysql-write-guard.py` | `~/.trae-cn/hooks.json` + `~/.trae-cn/hooks/mysql-write-guard.py` | **强制**(PreToolUse hook → ask;matcher `RunCommand`) |
| `pi` | `.pi/extensions/mysql-write-guard.ts` | `~/.pi/agent/extensions/mysql-write-guard.ts` | **强制**(`tool_call` hook → `ctx.ui.confirm` → block;matcher `bash`) |

- **强制** = 引擎级闸门:只有带 `--write` / `--ddl` / `--yes` 的命令才弹窗,只读静默放行。
- **引导** = 上下文指令,依赖模型自觉遵守(无引擎闸门)。
- Codex 无 `ask` 决策(返回它会被视为 hook 失败且命令**继续执行**),因此采用
  `.rules` 粗门 prompt + PermissionRequest hook 精过滤(只读子命令白名单自动放行、写操作
  及 `agent`/`config` 等写本地文件的子命令保持原生审批、fail-to-prompt)。
  边界详见 [Codex 特殊说明](./docs/agent-integration.md#codex-特殊说明)。
- 合并类配置(`settings.json`、`opencode.json`、`.vscode/settings.json`、TRAE `hooks.json`)
  会**深合并**进现有文件并备份 `.bak`;重复安装幂等。单文件配置(`.mdc`、`.md`、Pi `.ts`)
  默认跳过已存在文件,`--force` 覆盖。
- TRAE 路径不对称(项目级 `.trae` vs 全局 `~/.trae-cn`,带 `-cn` 后缀)是 TRAE 官方设计,
  国际版 / 中国版相同。
- Pi 自动发现 `extensions/*.ts`,无需改 `settings.json`;装好后在 pi 里 `/reload` 即可生效。
  项目级扩展首次启动 pi 时需 `/trust` 当前项目才加载;全局扩展立即可用。Pi 内置 shell 工具名为
  `bash`(小写),决策对象形状为 `{ block: true, reason? }`。

### 怎么运行

```bash
# 人类(交互式 TTY):多选 agent + 层级
mysql-cli agent init

# CI / agent(非交互)
mysql-cli agent init --agents claude,opencode,copilot --project
mysql-cli agent init --agents codebuddy --global
mysql-cli agent init --agents cursor --project --dry-run    # 预览不写
mysql-cli agent init --agents claude --project --json       # JSON 结果
```

Flags:

| Flag | 用途 |
| --- | --- |
| `--agents <a,b,c>` | 逗号分隔的 agent 名(非 TTY 时必填) |
| `--project` / `--global` | 写到当前项目或用户级配置(非 TTY 时二选一必填) |
| `--force` | 覆盖已存在的单文件配置(`.mdc`、`.md`) |
| `--dry-run` | 只打印动作,不写文件 |
| `-j, --json` | 输出 JSON 结果 |

### 验证

装完后,在**新开**的 agent 会话里让它跑两条对照:读应放行,写应弹窗。

```bash
mysql-cli query "SELECT 1"                                         # 静默放行
mysql-cli query "UPDATE t SET a=1 WHERE id=1" --write              # 弹窗找人类
```

hook 脚本可单独测(TRAE 用 `RunCommand`;claude/codebuddy 用 `Bash`):

```bash
echo '{"tool_name":"RunCommand","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write --yes"}}' \
  | python3 .trae/hooks/mysql-write-guard.py
# 期望:{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",...}}
```

完整能力对照、各 agent 写入路径与设计说明见
[`docs/agent-integration.md`](./docs/agent-integration.md)。

## 配置

`~/.config/mysql-cli/config.toml`(`--config` 可覆盖):

```toml
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
password = "${MYSQL_DEV_PASSWORD}"
database = "app"

[datasource.prod]
host = "db.prod.internal"
user = "ro_user"
password = "${MYSQL_PROD_PASSWORD}"
database = "main"
ssl_mode = "REQUIRED"
```

解析优先级:**CLI flag > 环境变量 > 配置文件 > 默认值**。密码支持 `${ENV}` 占位符。
原 MCP 的全部 `MYSQL_*` 环境变量同样支持,迁移零配置。

### 配置解析

mysql-cli 按链发现配置文件并合并:

- **文件优先级(高 → 低)**:`--config <file>` > `MYSQL_CLI_CONFIG` 环境变量 > 项目级 `<project>/.config/mysql-cli/config.toml`(仅当已 [trust](#项目级-config-信任)) > 全局 `~/.config/mysql-cli/config.toml`。
- **短路**:设了 `--config` 或 `MYSQL_CLI_CONFIG` 时只读该文件,跳过自动发现。
- **同名 datasource**:高优先级文件整体替换(含其 `[ssh]` 子表)--字段不逐个合并。
- **不同名 datasource**:并集--所有文件的所有名字都可用。
- **`default` / `default_limit`**:高优先级文件胜出。
- **字段覆盖**:`MYSQL_*` 环境变量与 `--host/--port/--user/--password/--db` flag 覆盖任意文件里的 datasource 字段。

示例:全局定义 `[datasource.dev]` + `[datasource.prod]`;已 trust 的项目重定义 `[datasource.dev]`(不同 host)并新增 `[datasource.ci]`。生效: `dev`(项目的)、`prod`(全局的)、`ci`(项目的)。

### 项目级 config 信任

项目级 `<project>/.config/mysql-cli/config.toml` **默认不加载**(防止 clone 来的恶意 repo 注入凭据)。
未 trust 时回落全局 config,并在 **stderr 告警**指出被跳过的文件(`--no-trust-warn`
或 `MYSQL_CLI_NO_TRUST_WARN=1` 静默;`--strict-trust` 升级为报错)。用
`mysql-cli config trust --yes`(非交互)或交互 `y/N` 显式信任--**AI 不得自动 trust**。

## 命令

| 命令 | 说明 |
| --- | --- |
| `query <sql>` | 执行 SQL(默认只读;DML 需 `--write`) |
| `txn <sql1> [sql2…]` | 在单个原子事务中执行多条语句 |
| `schema [table]` | 查看表结构;不指定表则查看整个数据库 |
| `sample <table>` | 采样行(`-n`,最多 20) |
| `tables [db]` | 列出表 |
| `databases` | 列出数据库 |
| `read <table>` | 前 100 行 |
| `explore` | 数据库 + 表概览 |
| `analyze <table>` | 一次返回 schema + sample |
| `version` | 打印 mysql-cli 二进制版本 |
| `config <sub>` | 管理配置(`init` / `trust` / `path` / `show`) |
| `agent init` | 安装 per-agent 写操作确认配置(见[上文](#写操作确认agent-init)) |
| `help [command]` | 查看任意命令的帮助(也可用 `--help` / `-h`) |
| *(无)* | 进入交互式 REPL(人类调试) |

## Flags

| Flag | 说明 |
| --- | --- |
| `-d, --datasource <name>` | 配置中的命名数据源 |
| `-f, --format json\|table\|csv\|tsv\|jsonl` | 输出格式(默认 `json`) |
| `--write` | 允许 DML |
| `--ddl` | 允许 DDL(需 `--write`) |
| `--yes` | 标记破坏性操作(装了 `agent init` 配置时会触发人类确认弹窗) |
| `--limit N` | `SELECT` 行数上限 |
| `--no-limit` | 关闭默认的 1000 行 SELECT cap |
| `--timeout 30s` | 查询超时 |
| `--host/--port/--user/--password/--db` | 连接参数覆盖 |

## 输出

默认 JSON(agent 友好):

```json
{"success":true,"data":{"columns":["id"],"rows":[[1]]},"rows_affected":0,"meta":{}}
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

用 `-f table`、`-f csv`、`-f tsv` 或 `-f jsonl` 切换为其他格式。

### 退出码

| 码 | 含义 |
| ---: | --- |
| `0` | 正常 |
| `2` | 连接错误 |
| `3` | 只读违规 |
| `4` | DDL 需 `--write` |
| `5` | 破坏性操作需 `--yes` |
| `6` | 标识符非法 |
| `7` | 拒绝多语句输入 |
| `8` | SQL 错误 |
| `9` | 超时 |
| `10` | 配置错误 |
| `11` | 内部错误（panic） |

## 安全

默认只读。写操作按层级放行:

- DML 需 `--write`
- DDL 需 `--write --ddl`
- `DROP`/`TRUNCATE` 以及无 `WHERE` 的 `UPDATE`/`DELETE` 需 `--yes`

标识符按严格白名单校验(`^[a-zA-Z0-9_$]+$`);多语句输入被拒绝(请用 `txn`)。
只读 / 多语句检查在**打开连接之前**执行,因此 agent 无需触碰数据库即可拿到正确退出码。

> `--yes` 是**标记**,不是豁免:它告诉 CLI "这是破坏性操作,请找人类确认"。
> 装了 [`agent init`](#写操作确认agent-init) 配置时,弹窗就在那里触发。

## SSH 隧道

数据源可以通过 SSH 堡垒机建立隧道,而非直连:

```toml
[datasource.prod]
host = "127.0.0.1"
port = 3330
user = "ro_user"
password = "${MYSQL_PROD_PASSWORD}"
database = "main"

[datasource.prod.ssh]
enable = true
host = "bastion.prod"
user = "deploy"
key_path = "~/.ssh/id_ed25519"
remote_host = "db.prod.internal"
remote_port = 3306
local_port = 3330
```

隧道在建立数据库连接前打开,并与之一起关闭。

## Agent 技能

`mysql-cli` 内置 [Agent Skills](./skills/),让 agent 无需 MCP runtime 即可发现并驱动它。
Skills 编码了触发条件、前置检查、命令参考、安全模型与错误自修复 -- 让 agent 第一次就
能正确调用 `mysql-cli`。

共有三个 skill,沿用 `larksuite/cli` 的 shared-skill 模式:

| Skill | 用途 |
| --- | --- |
| [`mysql-shared`](./skills/mysql-shared/SKILL.md) | 配置、数据源、全局 flag、安全模型、退出码、错误自修复、输出格式 -- 被另外两个引用 |
| [`mysql-query`](./skills/mysql-query/SKILL.md) | 执行 SQL:`query`、`txn`、DML/DDL |
| [`mysql-schema`](./skills/mysql-schema/SKILL.md) | 探索 schema:`tables`、`databases`、`schema`、`sample`、`read`、`explore`、`analyze` |

### 其他 agent

`mysql-cli` 兼容**任何能跑 shell 命令并解析 JSON 的 agent**。`npx skills add` 安装器
(vercel-labs/skills)支持 Claude Code、Cursor、Codex 以及 70+ 种 agent;它以 symlink
方式安装每个 skill,因此更新仓库即可自动同步。

| Agent | 配置格式 | 安装 |
| --- | --- | --- |
| **Claude Code** | `.claude/skills/*/SKILL.md` | `npx skills add AllenMuu/mysql-cli -a claude-code` |
| **Cursor** | `.cursor/rules/*.mdc` | `npx skills add AllenMuu/mysql-cli -a cursor` |
| **Codex CLI** | `AGENTS.md` | `npx skills add AllenMuu/mysql-cli -a codex` |
| **OpenCode** | `.opencode/instructions.md` | `npx skills add AllenMuu/mysql-cli -a opencode` |
| **GitHub Copilot** | `.github/copilot-instructions.md` | `npx skills add AllenMuu/mysql-cli -a github-copilot` |
| **Windsurf** | `.windsurfrules` | `npx skills add AllenMuu/mysql-cli -a windsurf` |
| **Aider** | `.aider.instructions.md` | `npx skills add AllenMuu/mysql-cli -a aider`(然后在 `.aider.conf.yml` 加 `read:`) |

### 安装须知

- **`mysql-cli` 必须在 `PATH` 中** -- 用
  `go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest` 安装,或编辑 skill
  指向你构建的二进制。
- **配置文件** -- skill 期望 `~/.config/mysql-cli/config.toml`(`--config` 可覆盖)。见
  [配置](#配置)。
- **默认 JSON 输出** -- skill 依赖 JSON 信封 + 退出码;编程式调用时保持 `-f json`(默认值)。

## 架构

严格单向分层;`result` 是无依赖的中立契约,解耦生产者与消费者。

```
cmd/mysql-cli/main  ->  cli   (cobra 装配 + 退出码映射)
                          │
        config ─-> conn ─-> query ─-> result
          │        │       └─> safety   (纯逻辑,零依赖)
          │        └─> schema ─> result/safety
          └ env/file        repl  (聚合 query + schema + format)
                            format ← result
```

| 包 | 职责 |
| --- | --- |
| `result` | 共享 `Result{Columns, Rows, RowsAffected, LastInsertID}` - 中立契约 |
| `safety` | SQL 分类、只读闸门、标识符校验、多语句与破坏性操作识别(纯逻辑,完全可单测) |
| `config` | TOML 命名数据源 + `MYSQL_*` 环境变量兼容 |
| `conn` | DSN 渲染、连接池、SSH 隧道生命周期 |
| `query` | 读 / 写 / 事务执行,每条语句过 `safety` 闸门 |
| `schema` | 只读探索命令 |
| `format` | `result` -> json/table/csv/tsv |
| `cli` | cobra 子命令 + `mapError`(error -> 退出码) |
| `repl` | readline 交互壳,供人类调试 |
| `agentsetup` | per-agent 写操作确认配置(模板内嵌于二进制) |

## 致谢

本项目灵感来源于并基于
[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server)
构建。安全模型与功能集很大程度上承袭自该项目。

## 许可证

基于 [MIT 协议](./LICENSE) 发布。
