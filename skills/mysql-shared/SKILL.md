---
name: mysql-shared
version: 1.0.0
description: >
  mysql-cli 共享规则:配置与数据源、全局 flag、安全模型、稳定退出码、错误自修复、输出格式。
  使用 mysql-query 或 mysql-schema 技能前 MUST 先用 Read 加载本技能。也在用户询问
  mysql-cli 配置、连接失败、只读/权限错误、退出码含义、输出格式时直接使用。
metadata:
  binary: mysql-cli
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  output_formats: json | table | csv | tsv
  safety_model: read-only by default; --write (DML), --write --ddl (DDL), --yes (destructive)
  license: MIT
  replaces: designcomputer/mysql_mcp_server
---

# mysql-cli 共享规则 / mysql-cli Shared Rules

> **CRITICAL**: 本技能被 `mysql-query` 与 `mysql-schema` 引用。使用任一技能前,先用 Read
> 工具加载本文件。/ Referenced by `mysql-query` and `mysql-schema`; read this before either.

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

- Exit `0` + `{"success":true,...}` -> reachable, proceed. / 退出 `0` 且 `success:true` -> 可达,继续。
- Exit `2` (CONN_FAILED) -> check host/port/credentials/SSH tunnel. / 退出 `2` -> 检查 host/port/凭据/SSH 隧道。
- Exit `10` (CONFIG_ERROR) -> check `config.toml` syntax or datasource name. / 退出 `10` -> 检查 `config.toml` 语法或数据源名。

### 3. (Optional) Pick a datasource / 选择数据源

If the config defines multiple `[datasource.<name>]` profiles, select one with
`-d <name>`. Otherwise the `default` entry is used.

若配置了多个 `[datasource.<name>]`,用 `-d <name>` 指定;否则用 `default` 条目。

---

## Global Flags / 全局 flag

All commands share global flags: `-d/--datasource`, `-f/--format` (default
`json`), `--write`, `--ddl`, `--yes`, `--limit`, `--timeout` (default `30s`),
`--config`, and connection overrides `--host/--port/--user/--password/--db`.

所有命令共享全局 flag:`-d/--datasource`、`-f/--format`(默认 `json`)、
`--write`、`--ddl`、`--yes`、`--limit`、`--timeout`(默认 `30s`)、`--config`,
以及连接覆盖 `--host/--port/--user/--password/--db`。

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
| `error.code` | Stable machine-readable code (see Error Handling). / 稳定的机器可读码(见错误自修复)。 |
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

> Safety flags at a glance / 安全 flag 速查:
> `--write` unlocks DML · `--ddl` unlocks DDL (**requires** `--write`) ·
> `--yes` confirms destructive ops. / `--write` 放行 DML · `--ddl` 放行 DDL
> (需配合 `--write`) · `--yes` 确认破坏性操作。

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
