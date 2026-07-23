---
name: mysql-query
version: 1.0.0
description: >
  Run SQL with mysql-cli: SELECT 查询、DML(INSERT/UPDATE/DELETE)、DDL(CREATE/ALTER/DROP)、
  多语句原子事务。Use when user asks to run SQL, query data, insert/update/delete rows,
  execute transactions, or perform DDL. 默认只读 JSON 输出,稳定退出码,写操作分层闸门。
metadata:
  binary: mysql-cli
  requires:
    bins: ["mysql-cli"]
  cliHelp: "mysql-cli query --help"
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  license: MIT
---

# mysql-query 技能 / mysql-query Skill

**CRITICAL - 开始前 MUST 先用 Read 工具读取 [`../mysql-shared/SKILL.md`](../mysql-shared/SKILL.md)**,
其中包含配置与数据源、全局 flag、安全模型、稳定退出码、错误自修复与输出格式。
/ Contains config & datasource, global flags, safety model, exit codes, error recovery, output formats.

> Convention / 约定: assume `mysql-cli` is on `PATH`. / 假设 `mysql-cli` 已在 `PATH` 中。

本技能覆盖 SQL 执行:只读查询、多语句事务、写入(DML/DDL)。
结构探索与数据采样请用 `mysql-schema` 技能。

---

## Trigger Conditions / 触发条件

Use this skill when the user asks about any of the following:
当用户提出以下需求时使用本技能:

- 数据库查询或运行 SQL(`SELECT`/`INSERT`/`UPDATE`/`DELETE`)
- 多语句原子事务(`txn`)
- DDL(`CREATE`/`ALTER`/`DROP` 等结构变更)

---

## Commands / 命令

All commands share global flags (see `mysql-shared`): `-d/--datasource`,
`-f/--format` (default `json`), `--write`, `--ddl`, `--yes`, `--limit`,
`--timeout`, `--config`, and connection overrides.

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
- / 多语句必须走 `txn`(`query` 会以退出 `7` 拒绝)。写操作仍需对应安全 flag。

### Write / 写入

Writes are gated by safety flags (see `mysql-shared` Security Model).
Destructive ops additionally need `--yes`.

写操作受安全 flag 闸门控制(见 `mysql-shared` 安全模型),破坏性操作另需 `--yes`。

| Intent / 意图 | Command / 命令 |
| --- | --- |
| DML (INSERT/UPDATE/DELETE) | `mysql-cli query "<dml>" --write` |
| DDL (CREATE/ALTER) | `mysql-cli query "<ddl>" --write --ddl` |
| DROP / TRUNCATE, or UPDATE/DELETE without WHERE | `mysql-cli query "<sql>" --write --yes` (add `--ddl` for DDL-class drops) |
| Multi-statement atomic write | `mysql-cli txn "<s1>" "<s2>" --write [--ddl] [--yes]` |

> Safety flags at a glance / 安全 flag 速查:
> `--write` unlocks DML · `--ddl` unlocks DDL (**requires** `--write`) ·
> `--yes` confirms destructive ops.

---

## Typical Workflow / 典型工作流

The safe path is **explore -> read -> write**. Always confirm schema and row
shape before writing, so DML targets the right columns and `WHERE` clauses.
(结构探索命令见 `mysql-schema` 技能。)

安全路径是**探索 -> 读取 -> 写入**。写之前先确认结构和数据形态,
确保 DML 命中正确列与 `WHERE` 条件。(结构探索命令见 `mysql-schema` 技能。)

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

## Notes / 备注

- One statement per `query` / `query` 只放单条: split multi-statement work into
  `txn` for atomicity; never chain statements in `query`. / 多语句拆到 `txn`, `query` 内不要串语句。
- 错误修复、退出码、输出格式见 `mysql-shared`。/ For error recovery, exit codes, output formats, see `mysql-shared`.
- 用 `mysql-cli query --help` 查看完整 flag。/ Run `mysql-cli query --help` for full flags.
