---
name: mysql-schema
version: 2.0.0
description: >
  Explore MySQL schema with mysql-cli: list databases/tables, table structure,
  sample/read rows, database overview, one-shot analyze. Use when the user asks
  about table structures, columns, types, indexes, listing tables/databases, or
  sampling data. All read-only; no safety flags needed. For DML/DDL/transactions
  use the mysql-query skill.
metadata:
  binary: mysql-cli
  requires:
    bins: ["mysql-cli"]
  cliHelp: "mysql-cli schema --help"
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  license: MIT
---

# mysql-schema Skill

**CRITICAL - Before starting, you MUST Read [`../mysql-shared/SKILL.md`](../mysql-shared/SKILL.md)**.
It contains config and datasource, global flags, identifier validation, exit
codes, error recovery, and output formats.

> Convention: assume `mysql-cli` is on `PATH`.

This skill covers schema exploration and data sampling - all read-only, no
safety flags needed. Identifiers are validated against `^[a-zA-Z0-9_$]+$` before
any SQL is built. For SQL writes/DML/DDL/transactions, use the `mysql-query`
skill.

---

## Trigger Conditions

Use this skill when the user asks about any of the following:

- Table structure, columns, types, indexes (`schema`, `analyze`)
- Listing tables/databases (`tables`, `databases`, `explore`)
- Sampling or reading table data (`sample`, `read`)
- Database/table overview (`explore`)

---

## Commands

All commands share global flags (see `mysql-shared`): `-d/--datasource`,
`-f/--format` (default `json`), `--limit`, `--timeout`, `--config`, and
connection overrides. Schema commands are read-only - no `--write`/`--ddl`/`--yes` needed.

All read-only; no safety flags needed. Identifiers are validated against
`^[a-zA-Z0-9_$]+$` before any SQL is built.

| Command | Args | Description |
| --- | --- | --- |
| `mysql-cli databases` | (none) | List databases. |
| `mysql-cli tables [db]` | `[db]` | List tables (current db, or given db). |
| `mysql-cli schema [table]` | `[table]` | Table structure, or whole database when omitted. |
| `mysql-cli sample <table>` | `<table>` | Sample rows. `-n N` (default 5, max 20). |
| `mysql-cli read <table>` | `<table>` | First 100 rows. |
| `mysql-cli explore` | (none) | Database + table overview. |
| `mysql-cli analyze <table>` | `<table>` | Schema + sample in one shot. |

---

## Typical Workflow

The safe path is **explore -> read -> (write via mysql-query)**. Always confirm
schema and row shape before writing.

```bash
# 1. Orient: what databases/tables exist?
mysql-cli explore -f json

# 2. Inspect a table's structure + a data sample in one call.
mysql-cli analyze users -f json

# 3. Look at a specific table's columns/types/indexes.
mysql-cli schema users -f json

# 4. Sample a few rows to understand data shape.
mysql-cli sample users -n 5 -f json
```

---

## Notes

- `analyze` preserves native cell types (NULL/number/string); values are
  rendered as-is, not stringified.
- Invalid identifiers exit `6` (IDENTIFIER_INVALID); use a valid name or the
  `db.table` qualified form.
- For error recovery, exit codes, and output formats, see `mysql-shared`.
- Run `mysql-cli schema --help` for the full set of flags.
