# MySQL CLI 设计文档

> 日期：2026-07-22
> 状态：设计稿，待实现计划

## 1. 背景与目标

当前使用社区版 MySQL MCP（`designcomputer/mysql_mcp_server`，Python 单文件实现）供 AI agent 通过 MCP 协议查询 MySQL。本设计将其替换为一个 Go 实现的 `mysql-cli` 命令行工具，**保留 MCP 的全部功能**，主要服务 AI agent 直接调用。

**动机**：
- MCP 绑定特定协议与运行时，仅支持 MCP 的客户端能用；CLI 可被任何能执行命令的 agent（Claude Code、Cursor、Codex、Aider 等）直接调用，通用性更强。
- 单二进制部署，无 Python 运行时依赖。
- 借机补强原 MCP 的安全短板（无查询超时、无行数上限、写操作无拦截）。

**成功标准**：
- 覆盖原 MCP 全部 tools/resources/prompts 能力，agent 可无缝迁移。
- 默认只读安全，写操作显式开启。
- agent 调用输出 JSON，易于解析；退出码区分错误类型。
- core 逻辑单测覆盖率 80%+。

## 2. 现有 MCP 功能基线

基于 `designcomputer/mysql_mcp_server` 源码分析：

### 2.1 Tools（3 个）
- `execute_sql(query)`：执行任意单语句 SQL，支持 SELECT/DML/SHOW/DESCRIBE，支持 `database.table` 跨库，可写，autocommit。
- `get_schema_info(table_name?)`：查 `information_schema.COLUMNS`，不传 table 为整库结构。
- `get_table_sample(table_name, limit?)`：样本数据，默认 5 行，上限 20。

### 2.2 Resources
- 动态 list：单库模式（配 `MYSQL_DATABASE`）列表（`mysql://{table}/data`）；多库模式列库（`mysql://database/{db}`），过滤系统库 `{information_schema, mysql, performance_schema, sys}`。
- read_resource：读库（`USE` + `SHOW TABLES`）；读表（`SELECT * LIMIT 100`）。

### 2.3 Prompts（2 个）
- `explore_database`、`analyze_table`：纯文本引导，编排现有 tools。

### 2.4 读写与安全
- 可写，无 SQL 类型限制，autocommit，无事务，无连接池。
- 多语句分号检测拦截；标识符正则 `^[a-zA-Z0-9_$]+$`；连接超时 10s；无查询超时；sample≤20、read=100、sql 无上限；SSL/TLS、SSH 隧道（subprocess）。

### 2.5 配置与输出
- 全套 `MYSQL_*` 环境变量 + `.env`。
- 输出 CSV-like 纯文本。

## 3. 设计决策总览

| 维度 | 决策 |
|---|---|
| 语言/技术栈 | Go，单二进制 |
| 架构 | 分层单体（方案 A），不碰 MCP，从零直连 |
| 使用形态 | 一次性子命令为主（agent），REPL 为人调试可选 |
| 读写边界 | 默认只读；`--write` 开 DML；`--ddl`（配 `--write`）开 DDL；高危需 `--yes` |
| 连接管理 | 多命名数据源 + 配置文件，完整兼容 `MYSQL_*` 环境变量 |
| 输出格式 | JSON 默认，`table`/`csv`/`tsv` 可选 |
| 事务 | 新增 `txn` 子命令，多条 SQL 原子执行 |
| Prompts | 转为 `explore`/`analyze` 子命令直接执行返回结构化结果 |
| 安全补强 | 查询超时、`query` 行数上限、写操作拦截（原 MCP 无） |

## 4. 架构与目录结构

分层单体，单向依赖。`cmd` 极薄只装配；核心逻辑不依赖前端，便于单测。

```
mysql-cli/
├── cmd/mysql-cli/main.go      # 极薄入口，装配依赖、路由到 cli 或 repl
├── internal/
│   ├── config/                # 加载 config.toml + MYSQL_* 环境变量，命名数据源
│   ├── conn/                  # DSN 构造、连接池(sql.DB)、ping、切换、SSH 隧道
│   ├── safety/                # SQL 分类、只读守卫、标识符校验、多语句检测、高危检测
│   ├── query/                 # 执行器：只读守卫、写模式、事务、超时、行数上限
│   ├── schema/                # 探索封装：tables/databases/schema/sample/read/explore/analyze
│   ├── format/                # 输出：json(默认)/table/csv/tsv
│   ├── repl/                  # 交互：readline、补全、历史、多行、\指令
│   └── cli/                   # 一次性命令：cobra 子命令装配
├── go.mod
└── README.md
```

**依赖方向**：`cmd -> cli/repl -> query/schema -> conn/safety/format -> config`（单向，无环）

**库选型**：
- `spf13/cobra` 子命令
- `go-sql-driver/mysql` 驱动
- `chzyer/readline` REPL
- `olekukonko/tablewriter` 表格
- `BurntSushi/toml` 配置
- `golang.org/x/crypto/ssh` SSH 隧道
- `testcontainers-go` 集成测试

## 5. 命令接口

全部子命令支持全局 flag：`-d/--datasource`、`-f/--format`、`--write`、`--ddl`、`--yes`、`--limit`、连接参数 `--host/--port/--user/--db/--password`。

```
mysql-cli query <sql>              # 单语句，默认只读
mysql-cli txn <sql1> <sql2> ...    # 多条原子执行，需 --write
mysql-cli schema [table]           # 表/整库结构
mysql-cli sample <table> [-n N]    # 样本，默认5，上限20
mysql-cli tables [db]              # 列表
mysql-cli databases                # 列库，过滤系统库
mysql-cli read <table>             # 读表 LIMIT 100
mysql-cli explore                  # 探索流程，返回结构化结果
mysql-cli analyze <table>          # 表分析，返回结构化结果
mysql-cli                          # 进入 REPL（人调试，非 agent 主路径）
```

### MCP 能力映射

| MCP 能力 | CLI 子命令 |
|---|---|
| `execute_sql` | `query` |
| `get_schema_info` | `schema` |
| `get_table_sample` | `sample` |
| list_resources（列表） | `tables` |
| list_resources（列库） | `databases` |
| read_resource（读表） | `read` |
| `explore_database` prompt | `explore`（直接执行） |
| `analyze_table` prompt | `analyze`（直接执行） |

## 6. 输出格式

JSON 默认信封：

```json
{"success": true, "data": {"columns": ["id","name"], "rows": [[1,"a"],[2,"b"]]}, "rows_affected": 0, "meta": {"datasource": "prod", "elapsed_ms": 12}}
```

```json
{"success": false, "error": {"code": "READONLY_VIOLATION", "message": "UPDATE requires --write"}}
```

- NULL 值在 JSON 中为 `null`，csv 中为空字符串（对齐原 MCP），table 中为 `NULL`。
- `--format table` 人类可读；`--format csv` 对齐原 MCP；`--format tsv` 管道友好。
- agent 据退出码 + `success` 字段判断成败。

## 7. 只读/写安全模型

- **SQL 分类**（`safety` 包，按首词）：
  - 只读：`SELECT/SHOW/DESCRIBE/DESC/EXPLAIN/WITH`
  - DML：`INSERT/UPDATE/DELETE/REPLACE` → 需 `--write`
  - DDL：`CREATE/ALTER/DROP/TRUNCATE/RENAME` → 需 `--write --ddl`
- **不支持 `USE` 切库**：CLI 单命令无状态，跨库用 `database.table` 全限定名或 `-d` 指定数据源。
- **高危拒绝**（默认）：`DROP/TRUNCATE`、无 `WHERE` 的 `UPDATE/DELETE`。agent 用 `--yes` 显式放行；TTY 下可交互确认。
- **标识符校验**：正则 `^[a-zA-Z0-9_$]+$`，支持 `database.table` 单点分隔，应用于 schema/sample/read/tables 的库名表名。
- **多语句**：`query` 单语句（分号检测拦截，与原 MCP 一致）；`txn` 显式允许多条，内部各语句分别过 safety 分类校验（DDL 仍需 `--ddl`）。
- **查询超时**：`MAX_EXECUTION_TIME` hint + context 超时（默认 30s，可配）。
- **行数上限**：`query` 可选 `--limit N`（默认不限，agent 按需设）。

## 8. 连接与配置

- **环境变量兼容**（迁移零成本）：`MYSQL_HOST/PORT/USER/PASSWORD/DATABASE`、`MYSQL_SSL_MODE/SSL_CA`、`MYSQL_CONNECT_TIMEOUT`、`MYSQL_SQL_MODE`、`MYSQL_CHARSET/COLLATION`、`MYSQL_AUTH_PLUGIN`、`MYSQL_SSH_*`。
- **配置文件** `~/.config/mysql-cli/config.toml`：
  ```toml
  [datasource.prod]
  host = "db.prod.internal"
  port = 3306
  user = "ro_user"
  password = "${MYSQL_PROD_PASSWORD}"   # 环境变量占位符
  database = "main"
  ssl_mode = "REQUIRED"

  [datasource.dev]
  host = "127.0.0.1"
  ```
- **优先级**：命令行 flag > 环境变量 > 配置文件 > 默认值。
- **连接池**：`sql.DB` 复用连接（原 MCP 每次新建）。
- **SSH 隧道**：`golang.org/x/crypto/ssh` 常驻（原 MCP 每次 subprocess）。

## 9. 错误处理与退出码

| code | 退出码 | 触发 |
|---|---|---|
| `CONN_FAILED` | 2 | 连接失败/超时 |
| `READONLY_VIOLATION` | 3 | 无 `--write` 跑写语句 |
| `DDL_REQUIRES_WRITE` | 4 | `--ddl` 未配 `--write` |
| `DESTRUCTIVE_REQUIRES_YES` | 5 | 高危未 `--yes` |
| `IDENTIFIER_INVALID` | 6 | 标识符正则不过 |
| `MULTI_STATEMENT` | 7 | `query` 含多语句 |
| `SQL_ERROR` | 8 | MySQL 返回错误 |
| `QUERY_TIMEOUT` | 9 | 查询超时 |
| `CONFIG_ERROR` | 10 | 配置缺失/格式错 |

密码不记录日志；MySQL 错误信息原样返回（便于 agent 排错，与原 MCP 一致）。

## 10. 测试策略

- **core 单测**（80%+）：`safety`（SQL 分类、标识符、多语句、无 WHERE 检测）、`format`（JSON/table/csv、NULL）、`query`（只读守卫、写模式、事务、超时）、`schema`、`conn`（DSN、配置优先级、SSH 隧道建立）。
- **集成测试**：`testcontainers-go` 起 MySQL 8，跑 query/schema/sample/txn/tables/databases/read 全链路，含只读拦截、写模式、高危拒绝。
- CLI 工具不做独立 E2E，集成测试覆盖端到端。

## 11. 范围与非目标（YAGNI）

- **不做**：SSE/HTTP 传输（原 MCP 有，但 CLI 场景 agent 直接执行命令，不需要）；MCP prompts 的纯文本引导（转为执行型子命令）；多账号权限管理 UI；查询结果可视化。
- **REPL 保持精简**：仅人调试用，不做重度补全/插件。
- **MCP 前端**：未来若需重新提供 MCP 接口，可基于同一 core 另起前端，当前不做。

## 12. 后续

本设计批准后，进入 `writing-plans` 制定分阶段实现计划。
