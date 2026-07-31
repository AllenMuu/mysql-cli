---
name: mysql-query
version: 2.0.0
description: >
  Run SQL with mysql-cli: SELECT queries, DML (INSERT/UPDATE/DELETE), DDL
  (CREATE/ALTER/DROP), and multi-statement atomic transactions. Use when the user
  asks to run SQL, query data, insert/update/delete rows, execute transactions,
  or perform DDL. Read-only JSON output by default, stable exit codes, and
  tiered gating for write operations.
metadata:
  binary: mysql-cli
  requires:
    bins: ["mysql-cli"]
  cliHelp: "mysql-cli query --help"
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  license: MIT
---

# mysql-query Skill

**CRITICAL - Before starting, you MUST Read [`../mysql-shared/SKILL.md`](../mysql-shared/SKILL.md)**.
It contains config and datasource, global flags, safety model, exit codes, error
recovery, and output formats.

> Convention: assume `mysql-cli` is on `PATH`.

This skill covers SQL execution: read-only queries, multi-statement transactions,
and writes (DML/DDL). For schema exploration and data sampling, use the
`mysql-schema` skill.

---

## Trigger Conditions

Use this skill when the user asks about any of the following:

- Database queries or running SQL (`SELECT`/`INSERT`/`UPDATE`/`DELETE`)
- Multi-statement atomic transactions (`txn`)
- DDL (`CREATE`/`ALTER`/`DROP` and other structural changes)

---

## Commands

All commands share global flags (see `mysql-shared`): `-d/--datasource`,
`-f/--format` (default `json`), `--write`, `--ddl`, `--yes`, `--limit`,
`--timeout`, `--config`, and connection overrides.

### Query

| Command | Description |
| --- | --- |
| `mysql-cli query "<sql>"` | Run one SQL statement. Read by default; `--write` for DML, `--write --ddl` for DDL. |

- Read queries route through `QueryContext` and return rows.
- DML/DDL route through a transactional write path and return `rows_affected`.
- Do **not** send write SQL without `--write`; the driver rejects writes in the
  read path and you get exit `3`.

### Transaction

| Command | Description |
| --- | --- |
| `mysql-cli txn "<sql1>" ["<sql2>"...]` | Run multiple statements in one atomic transaction. |

- Use this whenever you have more than one statement - `query` rejects
  multi-statement input (exit `7`).
- Needs `--write` (and `--ddl`/`--yes` as appropriate) for any write inside.

### Write

Writes are gated by safety flags (see `mysql-shared` Security Model).
Destructive ops additionally need `--yes`.

> **Human confirmation required**: every write flag (`--write`/`--ddl`/`--yes`)
> triggers a Claude Code permission prompt. `--yes` marks the op as destructive
> -- it does **not** skip review. Add the flags the op needs; a human approves
> the actual execution.

| Intent | Command |
| --- | --- |
| DML (INSERT/UPDATE/DELETE) | `mysql-cli query "<dml>" --write` |
| DDL (CREATE/ALTER) | `mysql-cli query "<ddl>" --write --ddl` |
| DROP / TRUNCATE, or UPDATE/DELETE without WHERE | `mysql-cli query "<sql>" --write --yes` (add `--ddl` for DDL-class drops) |
| Multi-statement atomic write | `mysql-cli txn "<s1>" "<s2>" --write [--ddl] [--yes]` |

> Safety flags at a glance:
> `--write` unlocks DML · `--ddl` unlocks DDL (**requires** `--write`) ·
> `--yes` marks a destructive op. **Every write flag (`--write`/`--ddl`/`--yes`)
> triggers a human confirmation prompt** -- `--yes` requests execution, it does
> not self-confirm. Never add `--yes` to bypass human review.

---

## Typical Workflow

The safe path is **explore -> read -> write**. Always confirm schema and row
shape before writing, so DML targets the right columns and `WHERE` clauses.
(Structural exploration commands live in the `mysql-schema` skill.)

```bash
# 1. Orient: what databases/tables exist?
mysql-cli explore -f json

# 2. Inspect a table's structure + a data sample in one call.
mysql-cli analyze users -f json

# 3. Precise read with a limit (always bound large results).
mysql-cli query "SELECT id, email FROM users WHERE status = 'active' LIMIT 50" -f json

# 4. Validate the WHERE clause on read-only data first.
mysql-cli query "SELECT COUNT(*) FROM users WHERE status = 'pending'" -f json

# 5. Apply the write with the matching safety flag.
mysql-cli query "UPDATE users SET status = 'active' WHERE status = 'pending'" --write -f json

# 6. Multi-step change atomically.
mysql-cli txn \
  "INSERT INTO audit_log(action) VALUES ('activate_users')" \
  "UPDATE users SET status = 'active' WHERE status = 'pending'" \
  --write -f json
```

- DDL example: `mysql-cli query "ALTER TABLE users ADD COLUMN nickname VARCHAR(64)" --write --ddl`
- Destructive example: `mysql-cli query "TRUNCATE TABLE staging_imports" --write --yes`

---

## Notes

- One statement per `query`: split multi-statement work into `txn` for
  atomicity; never chain statements in `query`.
- For error recovery, exit codes, and output formats, see `mysql-shared`.
- Run `mysql-cli query --help` for the full set of flags.

### Default cap and truncation

- SELECT returns at most 1000 rows by default; `meta.truncated=true` means the
  result was truncated - use `--no-limit` or run `COUNT(*)` first to assess the
  full size before deciding.
- Save tokens with `--format jsonl` (compact) or `--format csv`; avoid
  `--format table` (very token-expensive for agents).
- Only use `--no-limit` when you knowingly want the full table and can handle it
  (measured: a 44k-row table run raw costs ~9M tokens).
