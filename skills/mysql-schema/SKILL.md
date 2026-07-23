---
name: mysql-schema
version: 1.0.0
description: >
  Explore MySQL schema with mysql-cli: list databases/tables, table structure,
  sample/read rows, database overview, one-shot analyze. Use when user asks about
  table structures, columns, types, indexes, listing tables/databases, or sampling
  data. 全部只读,无需安全 flag。运行 DML/DDL/事务请用 mysql-query 技能。
metadata:
  binary: mysql-cli
  requires:
    bins: ["mysql-cli"]
  cliHelp: "mysql-cli schema --help"
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  license: MIT
---

# mysql-schema 技能 / mysql-schema Skill

**CRITICAL - 开始前 MUST 先用 Read 工具读取 [`../mysql-shared/SKILL.md`](../mysql-shared/SKILL.md)**,
其中包含配置与数据源、全局 flag、标识符校验、稳定退出码、错误自修复与输出格式。
/ Contains config & datasource, global flags, identifier validation, exit codes, error recovery, output formats.

> Convention / 约定: assume `mysql-cli` is on `PATH`. / 假设 `mysql-cli` 已在 `PATH` 中。

本技能覆盖结构探索与数据采样,全部只读,无需安全 flag。所有标识符在拼 SQL 前按
`^[a-zA-Z0-9_$]+$` 校验。运行 SQL 写入/DML/DDL/事务请用 `mysql-query` 技能。

---

## Trigger Conditions / 触发条件

Use this skill when the user asks about any of the following:
当用户提出以下需求时使用本技能:

- 表结构、列、类型、索引(`schema`、`analyze`)
- 列出库表(`tables`、`databases`、`explore`)
- 表数据采样或读取(`sample`、`read`)
- 库表总览(`explore`)

---

## Commands / 命令

All commands share global flags (see `mysql-shared`): `-d/--datasource`,
`-f/--format` (default `json`), `--limit`, `--timeout`, `--config`, and
connection overrides. Schema commands are read-only - no `--write`/`--ddl`/`--yes` needed.

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

---

## Typical Workflow / 典型工作流

The safe path is **explore -> read -> (write via mysql-query)**. Always confirm
schema and row shape before writing.

安全路径是**探索 -> 读取 -> (用 mysql-query 写入)**。写之前先确认结构和数据形态。

```bash
# 1. Orient: what databases/tables exist? / 定向:有哪些库表?
mysql-cli explore -f json

# 2. Inspect a table's structure + a data sample in one call. / 一次看结构 + 采样
mysql-cli analyze users -f json

# 3. Look at a specific table's columns/types/indexes. / 看具体表的列/类型/索引
mysql-cli schema users -f json

# 4. Sample a few rows to understand data shape. / 采样几行了解数据形态
mysql-cli sample users -n 5 -f json
```

---

## Notes / 备注

- `analyze` 的 padRow 保留原值: NULL/数字/字符串原样渲染,不会被字符串化。/ `analyze` preserves native cell types (NULL/number/string).
- 标识符非法会退出 `6`(IDENTIFIER_INVALID);改用合法标识符或 `db.table` 限定形式。/ Invalid identifiers exit `6`; use a valid name or `db.table` form.
- 错误修复、退出码、输出格式见 `mysql-shared`。/ For error recovery, exit codes, output formats, see `mysql-shared`.
- 用 `mysql-cli schema --help` 查看完整 flag。/ Run `mysql-cli schema --help` for full flags.
