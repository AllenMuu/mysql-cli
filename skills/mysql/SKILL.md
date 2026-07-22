---
name: mysql
description: >
  Query MySQL and explore schema with mysql-cli. Use when user asks about
  database queries, table structures, MySQL data, running SQL, inspecting rows,
  listing tables/databases, or executing transactions/DML/DDL. Defaults to
  read-only JSON output with stable exit codes designed to be parsed by agents.
version: 1.0.0
metadata:
  binary: mysql-cli
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  output_formats: json | table | csv | tsv
  safety_model: read-only by default; --write (DML), --write --ddl (DDL), --yes (destructive)
  license: MIT
  replaces: designcomputer/mysql_mcp_server
---

# mysql-cli Skill / mysql-cli 技能

`mysql-cli` is a Go CLI that lets any shell-capable AI agent query MySQL without
an MCP runtime. It is a drop-in replacement for `designcomputer/mysql_mcp_server`,
re-exposing all read/write capabilities as plain subcommands with **JSON by
default** and **stable exit codes**. Agents are the primary caller; the REPL is
only for human debugging.

`mysql-cli` 是一个 Go CLI,让任何能跑 shell 的 AI agent 无需 MCP runtime 即可查询 MySQL。
它替代 `designcomputer/mysql_mcp_server`,把全部读写能力下沉为命令行子命令,
**默认 JSON 输出**、**稳定退出码**。agent 是首要调用方,REPL 仅供人类调试。

> Convention / 约定: assume the `mysql-cli` binary is on `PATH`. If not, install
> with `go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest` or point
> commands at the built binary. / 假设 `mysql-cli` 已在 `PATH` 中;否则用
> `go install` 安装或指向编译产物。

---

## Trigger Conditions / 触发条件

Use this skill when the user asks about any of the following:

- Database queries or running SQL (`SELECT`, `INSERT`, `UPDATE`, `DELETE`)
- Table structures, columns, types, indexes (`schema`, `analyze`)
- Listing tables or databases (`tables`, `databases`, `explore`)
- Sampling or reading rows from a table (`sample`, `read`)
- Running multiple statements atomically (`txn`)
- Anything involving MySQL data and a shell-capable agent

当用户提出以下需求时使用本技能:

- 数据库查询或运行 SQL(`SELECT`/`INSERT`/`UPDATE`/`DELETE`)
- 表结构、列、类型、索引(`schema`、`analyze`)
- 列出库表(`tables`、`databases`、`explore`)
- 表数据采样或读取(`sample`、`read`)
- 多语句原子事务(`txn`)
- 任何涉及 MySQL 数据且 agent 能跑 shell 的场景

---

## Before Running / 前置检查

Run these checks before the first command in a session. They are cheap and
prevent the most common failures (missing config, unreachable datasource).

首次调用前先做以下轻量检查,可避免最常见的失败(缺配置、数据源不可达)。

### 1. Config file exists / 配置文件存在

```bash
ls ~/.config/mysql-cli/config.toml
```

If missing, `mysql-cli` still works via `MYSQL_*` env vars or `--host/--port/...`
overrides, but a config file is the normal path. Resolution priority is
**CLI flag > env > file > default**. Passwords support `${ENV}` placeholders.

若不存在,仍可通过 `MYSQL_*` 环境变量或 `--host/--port/...` 覆盖运行,
但配置文件是常规路径。解析优先级:**CLI flag > env > file > default**。
密码支持 `${ENV}` 占位符。

### 2. Datasource reachable / 数据源可达

A lightweight probe via the read-only `databases` command (no table scan):

```bash
mysql-cli databases -f json
```

- Exit `0` + `{"success":true,...}` -> reachable, proceed.
  / 退出 `0` 且 `success:true` -> 可达,继续。
- Exit `2` (CONN_FAILED) -> check host/port/credentials/SSH tunnel.
  / 退出 `2` -> 检查 host/port/凭据/SSH 隧道。
- Exit `10` (CONFIG_ERROR) -> check `config.toml` syntax or datasource name.
  / 退出 `10` -> 检查 `config.toml` 语法或数据源名。

### 3. (Optional) Pick a datasource / 选择数据源

If the config defines multiple `[datasource.<name>]` profiles, select one with
`-d <name>`. Otherwise the `default` entry is used.

若配置了多个 `[datasource.<name>]`,用 `-d <name>` 指定;否则用 `default` 条目。

---

## Commands Reference / 命令参考

All commands share global flags: `-d/--datasource`, `-f/--format` (default
`json`), `--write`, `--ddl`, `--yes`, `--limit`, `--timeout` (default `30s`),
`--config`, and connection overrides `--host/--port/--user/--password/--db`.

所有命令共享全局 flag:`-d/--datasource`、`-f/--format`(默认 `json`)、
`--write`、`--ddl`、`--yes`、`--limit`、`--timeout`(默认 `30s`)、`--config`,
以及连接覆盖 `--host/--port/--user/--password/--db`。

### Query / 查询

| Command / 命令 | Description / 说明 |
| --- | --- |
| `mysql-cli query "<sql>"` | Run one SQL statement. Read by default; `--write` for DML, `--write --ddl` for DDL. / 运行单条 SQL。默认只读;DML 需 `--write`,DDL 需 `--write --ddl`。 |

- Read queries route through `QueryContext` and return rows.
- DML/DDL route through a transactional write path and return `rows_affected`.
- Do **not** send write SQL without `--write`; the driver rejects writes in the
  read path and you get exit `3`.
- 读查询走 `QueryContext` 返回行;DML/DDL 走事务写入路径返回 `rows_affected`。
  不要在无 `--write` 时发写语句,否则退出 `3`。

### Transaction / 事务

| Command / 命令 | Description / 说明 |
| --- | --- |
| `mysql-cli txn "<sql1>" ["<sql2>"...]` | Run multiple statements in one atomic transaction. / 在单个原子事务中运行多条语句。 |

- Use this whenever you have more than one statement - `query` rejects
  multi-statement input (exit `7`).
- Needs `--write` (and `--ddl`/`--yes` as appropriate) for any write inside.
  / 多语句必须走 `txn`(`query` 会以退出 `7` 拒绝)。写操作仍需对应安全 flag。

### Schema Exploration / 结构探索

All read-only; no safety flags needed. Identifiers are validated against
`^[a-zA-Z0-9_$]+$` before any SQL is built.

全部只读,无需安全 flag。所有标识符在拼 SQL 前按 `^[a-zA-Z0-9_$]+$` 校验。

| Command / 命令 | Args | Description / 说明 |
| --- | --- | --- |
| `mysql-cli databases` | (none) | List databases. / 列出数据库。 |
| `mysql-cli tables [db]` | `[db]` | List tables (current db, or given db). / 列出表(当前库或指定库)。 |
| `mysql-cli schema [table]` | `[table]` | Table structure, or whole database when omitted. / 表结构;省略则整库。 |
| `mysql-cli sample <table>` | `<table>` | Sample rows. `-n N` (default 5, max 20). / 采样行,`-n N`(默认 5,上限 20)。 |
| `mysql-cli read <table>` | `<table>` | First 100 rows. / 前 100 行。 |
| `mysql-cli explore` | (none) | Database + table overview. / 库表总览。 |
| `mysql-cli analyze <table>` | `<table>` | Schema + sample in one shot. / 一次拿到结构 + 采样。 |

### Write / 写入

Writes are gated by safety flags (see [Security Model](#security-model--安全模型)).
Destructive ops additionally need `--yes`.

写操作受安全 flag 闸门控制(见[安全模型](#security-model--安全模型)),
破坏性操作另需 `--yes`。

| Intent / 意图 | Command / 命令 |
| --- | --- |
| DML (INSERT/UPDATE/DELETE) | `mysql-cli query "<dml>" --write` |
| DDL (CREATE/ALTER) | `mysql-cli query "<ddl>" --write --ddl` |
| DROP / TRUNCATE, or UPDATE/DELETE without WHERE | `mysql-cli query "<sql>" --write --yes` (add `--ddl` for DDL-class drops) |
| Multi-statement atomic write | `mysql-cli txn "<s1>" "<s2>" --write [--ddl] [--yes]` |

> Safety flags at a glance / 安全 flag 速查:
> `--write` unlocks DML · `--ddl` unlocks DDL (**requires** `--write`) ·
> `--yes` confirms destructive ops. / `--write` 放行 DML · `--ddl` 放行 DDL
> (需配合 `--write`) · `--yes` 确认破坏性操作。

---

## Typical Workflow / 典型工作流

The safe path is **explore -> read -> write**. Always confirm schema and row
shape before writing, so DML targets the right columns and `WHERE` clauses.

安全路径是**探索 -> 读取 -> 写入**。写之前先确认结构和数据形态,
确保 DML 命中正确列与 `WHERE` 条件。

```bash
# 1. Orient: what databases/tables exist? / 定向:有哪些库表?
mysql-cli explore -f json

# 2. Inspect a table's structure + a data sample in one call. / 一次看结构 + 采样
mysql-cli analyze users -f json

# 3. Precise read with a limit (always bound large results). / 带上限精确读取
mysql-cli query "SELECT id, email FROM users WHERE status = 'active' LIMIT 50" -f json

# 4. Validate the WHERE clause on read-only data first. / 先用只读验证 WHERE
mysql-cli query "SELECT COUNT(*) FROM users WHERE status = 'pending'" -f json

# 5. Apply the write with the matching safety flag. / 用对应安全 flag 执行写入
mysql-cli query "UPDATE users SET status = 'active' WHERE status = 'pending'" --write -f json

# 6. Multi-step change atomically. / 多步变更走原子事务
mysql-cli txn \
  "INSERT INTO audit_log(action) VALUES ('activate_users')" \
  "UPDATE users SET status = 'active' WHERE status = 'pending'" \
  --write -f json
```

- DDL example: `mysql-cli query "ALTER TABLE users ADD COLUMN nickname VARCHAR(64)" --write --ddl`
- Destructive example: `mysql-cli query "TRUNCATE TABLE staging_imports" --write --yes`

---

## Output Formats / 输出格式

Default is JSON (agent-friendly, parse with `jq`). Success and failure share one
strict envelope.

默认 JSON(agent 友好,可用 `jq` 解析)。成功与失败共用同一严格信封。

**Success / 成功:**

```json
{"success":true,"data":{"columns":["id","email"],"rows":[[1,"a@x.com"]]},"rows_affected":0,"meta":{}}
```

| Field / 字段 | Meaning / 含义 |
| --- | --- |
| `success` | `true` on success. / 成功为 `true`。 |
| `data.columns` | Column names. / 列名。 |
| `data.rows` | Row values (text columns come back as strings, not base64). / 行值(文本列以字符串返回,非 base64)。 |
| `rows_affected` | For DML/DDL writes. / DML/DDL 写入的受影响行数。 |
| `meta` | Reserved metadata. / 预留元信息。 |

**Failure / 失败:**

```json
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

| Field / 字段 | Meaning / 含义 |
| --- | --- |
| `success` | `false` on error. / 失败为 `false`。 |
| `error.code` | Stable machine-readable code (see [Error Handling](#error-handling--错误自修复)). / 稳定的机器可读码(见错误自修复)。 |
| `error.message` | Human-readable detail. / 人类可读详情。 |

Switch human-readable rendering with `-f table`, `-f csv`, or `-f tsv`. In
non-JSON formats, errors render as `Error [<CODE>]: <message>`.

用 `-f table`/`-f csv`/`-f tsv` 切换人类可读格式。非 JSON 格式下错误渲染为
`Error [<CODE>]: <message>`。

---

## Error Handling / 错误自修复

`mysql-cli` maps every error to a stable exit code. Parse the exit code (or
`error.code` in JSON) and apply the fix below, then retry. The read-only and
multi-statement checks run **before** a connection is opened, so you get the
correct code without touching the database.

`mysql-cli` 把每个错误映射到稳定退出码。解析退出码(或 JSON 的 `error.code`),
按下表修复后重试。只读与多语句检查在**连接建立前**运行,无需触库即可得到正确码。

| Exit / 退出码 | Code / 码 | Meaning / 含义 | Fix / 修复 |
| ---: | --- | --- | --- |
| `2` | `CONN_FAILED` | Cannot reach MySQL / 无法连接 | Check host/port/credentials/SSH tunnel in `config.toml`; verify with `mysql-cli databases`. Use `-d <name>` for the right datasource. / 检查 `config.toml` 中的 host/port/凭据/SSH 隧道;用 `mysql-cli databases` 验证。用 `-d <name>` 选对数据源。 |
| `3` | `READONLY_VIOLATION` | DML without `--write` / 无 `--write` 的 DML | Re-run with `--write`. / 加 `--write` 重跑。 |
| `4` | `DDL_REQUIRES_WRITE` | DDL missing flags / DDL 缺 flag | Re-run with `--write --ddl`. / 加 `--write --ddl` 重跑。 |
| `5` | `DESTRUCTIVE_REQUIRES_YES` | Destructive op needs confirmation / 破坏性操作需确认 | Re-run with `--yes` (and `--write`, plus `--ddl` for DDL-class drops). / 加 `--yes`(及 `--write`;DDL 类 drop 另加 `--ddl`)。 |
| `6` | `IDENTIFIER_INVALID` | Table/db name not in `^[a-zA-Z0-9_$]+$` / 标识符非法 | Use a valid identifier; avoid quotes/spaces. For `db.table` use the qualified form. / 改用合法标识符,去引号/空格;`db.table` 用限定形式。 |
| `7` | `MULTI_STATEMENT` | More than one statement passed to `query` / `query` 收到多语句 | Use `mysql-cli txn "<s1>" "<s2>"` instead. / 改用 `mysql-cli txn`。 |
| `8` | `SQL_ERROR` | SQL syntax/semantic error / SQL 语法/语义错误 | Run `mysql-cli schema <table>` to confirm columns/types, then fix the SQL. / 用 `schema <table>` 确认列/类型后修正 SQL。 |
| `9` | `QUERY_TIMEOUT` | Exceeded `--timeout` / 超时 | Raise `--timeout 60s`, add `--limit`, or narrow the `WHERE`. / 调大 `--timeout`、加 `--limit` 或收窄 `WHERE`。 |
| `10` | `CONFIG_ERROR` | Config parse error or unknown datasource / 配置解析错误或未知数据源 | Check `config.toml` TOML syntax, the `default` value, and datasource name spelling; point `--config` at the right file. / 检查 `config.toml` 语法、`default` 值与数据源名拼写;用 `--config` 指向正确文件。 |

Exit `1` is reserved for argument/flag usage errors (cobra); fix the command line.
/ 退出 `1` 保留给参数/flag 用法错误(cobra);修正命令行即可。

---

## Security Model / 安全模型

`mysql-cli` is **read-only by default**. Writes are gated in tiers so a missing
flag never silently mutates data.

`mysql-cli` **默认只读**。写操作分层闸门,缺 flag 时绝不静默改数据。

| Operation class / 操作类别 | Required flags / 所需 flag |
| --- | --- |
| `SELECT` / read exploration | none (default read-only) / 无(默认只读) |
| DML (`INSERT`/`UPDATE`/`DELETE`) | `--write` |
| DDL (`CREATE`/`ALTER`/`DROP`/...) | `--write --ddl` |
| Destructive (`DROP`/`TRUNCATE`, `UPDATE`/`DELETE` without `WHERE`) | `--yes` (+ `--write`, + `--ddl` for DDL-class) |

Additional guarantees / 额外保证:

- **Identifier allowlist / 标识符白名单**: table/db names must match
  `^[a-zA-Z0-9_$]+$`; qualified `db.table` is allowed. Prevents injection in
  schema-exploration SQL. / 表/库名须匹配 `^[a-zA-Z0-9_$]+$`,允许 `db.table`,
  防 schema 探索 SQL 注入。
- **Multi-statement rejection / 多语句拒绝**: `query` accepts a single statement
  (one trailing `;` tolerated); multiple statements must use `txn`. / `query`
  仅接受单条语句(容忍结尾分号),多语句须用 `txn`。
- **Pre-connection gating / 连接前闸门**: read-only and multi-statement checks
  run before any DB connection, so the right exit code is returned without
  touching the database. / 只读与多语句检查在连接前运行,无需触库即返回正确退出码。
- **Config safety / 配置安全**: prefer a read-only DB user (`ro_user`) for the
  default datasource; reserve write-capable users for explicit `-d` profiles.
  / 默认数据源建议用只读账号(`ro_user`),写权限账号留给显式 `-d` profile。

---

## Best Practices / 最佳实践

- **Explore before you query / 先探索再查询**: run `explore`/`analyze`/`schema`
  first to confirm column names and types; this prevents `SQL_ERROR` (exit `8`).
  / 先 `explore`/`analyze`/`schema` 确认列名与类型,避免 `SQL_ERROR`(退出 `8`)。
- **Bound large results / 大结果集加上限**: always add `--limit N` to `SELECT`,
  or use `sample`/`read` which are already capped. / `SELECT` 一律加 `--limit N`,
  或用已封顶的 `sample`/`read`。
- **Default to JSON / 默认 JSON**: keep `-f json` (the default) so you can parse
  with `jq`; switch to `table` only when showing data to the user.
  / 保持 `-f json`(默认)以便 `jq` 解析;仅向用户展示时切 `table`。
- **Validate WHERE on reads first / 先只读验证 WHERE**: before an `UPDATE`/
  `DELETE`, run the same `WHERE` as a `SELECT COUNT(*)` to confirm scope.
  / `UPDATE`/`DELETE` 前用相同 `WHERE` 跑 `SELECT COUNT(*)` 确认范围。
- **One statement per `query` / `query` 只放单条**: split multi-statement work
  into `txn` for atomicity; never chain statements in `query`.
  / 多语句拆到 `txn` 保证原子性,`query` 内不要串语句。
- **Match flags to intent / flag 对齐意图**: DML -> `--write`; DDL ->
  `--write --ddl`; destructive -> add `--yes`. Don't add `--yes` speculatively.
  / DML 加 `--write`;DDL 加 `--write --ddl`;破坏性再加 `--yes`,勿盲目加 `--yes`。
- **Reuse the connection model / 复用连接模型**: each invocation opens and
  closes its own pool (and SSH tunnel). Don't try to hold connections across
  calls; just issue separate commands. / 每次调用各自开关连接池与 SSH 隧道,
  跨调用不要试图保持连接,直接发独立命令即可。
- **Prefer read-only DB users / 偏好只读账号**: configure the default
  datasource with a read-only user; force writes to go through an explicit
  `-d` profile. / 默认数据源配只读账号,写操作走显式 `-d` profile。
