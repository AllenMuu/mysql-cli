---
name: mysql-shared
version: 2.0.0
description: >
  mysql-cli shared rules: config and datasources, global flags, safety model,
  stable exit codes, error self-repair, and output formats. MUST be loaded with
  Read before using the mysql-query or mysql-schema skill. Also used directly when
  the user asks about mysql-cli config, connection failures, read-only/permission
  errors, exit code meanings, or output formats.
metadata:
  binary: mysql-cli
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  output_formats: json | table | csv | tsv | jsonl
  safety_model: read-only by default; --write (DML), --write --ddl (DDL), --yes (destructive); SELECT default cap 1000 (--no-limit to disable)
  license: MIT
  replaces: designcomputer/mysql_mcp_server
---

# mysql-cli Shared Rules

> **CRITICAL**: This skill is referenced by `mysql-query` and `mysql-schema`.
> Read this file with the Read tool before using either.

`mysql-cli` is a Go CLI that lets any shell-capable AI agent query MySQL without
an MCP runtime. It is a drop-in replacement for `designcomputer/mysql_mcp_server`,
re-exposing all read/write capabilities as plain subcommands with **JSON by
default** and **stable exit codes**. Agents are the primary caller; the REPL is
only for human debugging.

> Convention: assume the `mysql-cli` binary is on `PATH`. If not, install with
> `go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest` or point
> commands at the built binary.

---

## Before Running

Run these checks before the first command in a session. They are cheap and
prevent the most common failures (missing config, unreachable datasource).

### 1. Config file exists

```bash
ls ~/.config/mysql-cli/config.toml
```

If missing, `mysql-cli` still works via `MYSQL_*` env vars or `--host/--port/...`
overrides, but a config file is the normal path. Resolution priority is
**CLI flag > env > project-level (trusted) > global > default** (see Project-level Config below). Passwords support `${ENV}` placeholders (expanded only in trusted configs).

### 2. Datasource reachable

A lightweight probe via the read-only `databases` command (no table scan):

```bash
mysql-cli databases -f json
```

- Exit `0` + `{"success":true,...}` -> reachable, proceed.
- Exit `2` (CONN_FAILED) -> check host/port/credentials/SSH tunnel.
- Exit `10` (CONFIG_ERROR) -> check `config.toml` syntax or datasource name.

### 3. (Optional) Pick a datasource

If the config defines multiple `[datasource.<name>]` profiles, select one with
`-d <name>`. Otherwise the `default` entry is used.

---

## Project-level Config

mysql-cli supports project-level config, structurally identical to the global
config and merged override-style (similar to MCP's `.mcp.json`).

- **Project-level config location**: `<project-root>/.config/mysql-cli/config.toml`.
  Discovered by walking up from cwd; the first match wins (stops at home/fs
  root). Shares the relative path `.config/mysql-cli/config.toml` with the global
  `~/.config/mysql-cli/config.toml`; only the root differs.
- **Trust mechanism (security)**: project-level config is **not loaded** by
  default. First run `mysql-cli config trust` inside the project directory to
  write the project root into the trust list at `~/.config/mysql-cli/trusted`.
  When untrusted, it **silently falls back to global** (exit 0, no error);
  `${ENV}` password placeholders expand only in trusted project-level configs -
  preventing malicious repos from harvesting local env vars or hijacking
  connections.
- **Priority chain**: `--config` flag > `MYSQL_CLI_CONFIG` env > project-level
  (trusted) > global > `MYSQL_*` field-level overrides > default. When `--config`
  or `MYSQL_CLI_CONFIG` is set, only that file is read and auto-discovery is
  skipped.
- **Override-style merge**: same-named datasources replace wholesale (project
  wins, including SSH subtables); different names form a union; `default` and
  `default_limit` from project override global.

### config subcommands

| Command | Purpose |
|---|---|
| `mysql-cli config path` `[-j]` | Show effective file chain + trust status (project marked `trusted` / `untrusted, skipped` + global) |
| `mysql-cli config show` `[name]` `[-j]` | Show final merged config (passwords redacted: literal -> `***`, `${ENV}` shown as-is) |
| `mysql-cli config trust [dir]` | Trust the project root (defaults to detected root), writing it to the trust list |
| `mysql-cli config init [--project\|--global] [--force]` | Generate a template config.toml |

> **Self-check tip**: when query results or connections don't match expectations,
> first run `mysql-cli config path` to inspect trust status, then
> `mysql-cli config show` to inspect the merged config (passwords redacted).

## Global Flags

All commands share global flags: `-d/--datasource`, `-f/--format` (default
`json`), `--write`, `--ddl`, `--yes`, `--limit`, `--timeout` (default `30s`),
`--config`, and connection overrides `--host/--port/--user/--password/--db`.

---

## Output Formats

Default is JSON (agent-friendly, parse with `jq`). Success and failure share one
strict envelope.

**Success:**

```json
{"success":true,"data":{"columns":["id","email"],"rows":[[1,"a@x.com"]]},"rows_affected":0,"meta":{}}
```

| Field | Meaning |
| --- | --- |
| `success` | `true` on success. |
| `data.columns` | Column names. |
| `data.rows` | Row values (text columns come back as strings, not base64). |
| `rows_affected` | For DML/DDL writes. |
| `meta` | Reserved metadata. |

**Failure:**

```json
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

| Field | Meaning |
| --- | --- |
| `success` | `false` on error. |
| `error.code` | Stable machine-readable code (see Error Handling). |
| `error.message` | Human-readable detail. |

Switch human-readable rendering with `-f table`, `-f csv`, or `-f tsv`. In
non-JSON formats, errors render as `Error [<CODE>]: <message>`.

### Default row cap

When a SELECT has no LIMIT, mysql-cli returns only the first 1000 rows by default
and sets `meta.truncated=true` (detected via a cap+1 probe, with no extra
query).

- `--limit N`: explicitly request N rows; returned exactly, no truncation probe.
- `--no-limit`: disable the default cap and return the full table (dangerous -
  may blow up context).
- `default_limit` (top-level config.toml) / `MYSQL_CLI_DEFAULT_LIMIT` (env):
  tune the default cap value.
- Priority: `--limit` > `--no-limit` > config > env > 1000.
- When you see `truncated:true` and need the full set, use `--no-limit` or first
  run `SELECT COUNT(*)` to assess the size.

`--format jsonl`: one JSON object per line (`{"col":val,...}`), NULL as `null`;
  more token-efficient than json. Truncation info goes to stderr.

---

## Error Handling

`mysql-cli` maps every error to a stable exit code. Parse the exit code (or
`error.code` in JSON) and apply the fix below, then retry. The read-only and
multi-statement checks run **before** a connection is opened, so you get the
correct code without touching the database.

| Exit | Code | Meaning | Fix |
| ---: | --- | --- | --- |
| `2` | `CONN_FAILED` | Cannot reach MySQL | Check host/port/credentials/SSH tunnel in `config.toml`; verify with `mysql-cli databases`. Use `-d <name>` for the right datasource. |
| `3` | `READONLY_VIOLATION` | DML without `--write` | Re-run with `--write`. |
| `4` | `DDL_REQUIRES_WRITE` | DDL missing flags | Re-run with `--write --ddl`. |
| `5` | `DESTRUCTIVE_REQUIRES_YES` | Destructive op needs confirmation | Re-run with `--yes` (and `--write`, plus `--ddl` for DDL-class drops). |
| `6` | `IDENTIFIER_INVALID` | Table/db name not in `^[a-zA-Z0-9_$]+$` | Use a valid identifier; avoid quotes/spaces. For `db.table` use the qualified form. |
| `7` | `MULTI_STATEMENT` | More than one statement passed to `query` | Use `mysql-cli txn "<s1>" "<s2>"` instead. |
| `8` | `SQL_ERROR` | SQL syntax/semantic error | Run `mysql-cli schema <table>` to confirm columns/types, then fix the SQL. |
| `9` | `QUERY_TIMEOUT` | Exceeded `--timeout` | Raise `--timeout 60s`, add `--limit`, or narrow the `WHERE`. |
| `10` | `CONFIG_ERROR` | Config parse error or unknown datasource | Check `config.toml` TOML syntax, the `default` value, and datasource name spelling; point `--config` at the right file. |

Exit `1` is reserved for argument/flag usage errors (cobra); fix the command line.

---

## Security Model

`mysql-cli` is **read-only by default**. Writes are gated in tiers so a missing
flag never silently mutates data.

| Operation class | Required flags |
| --- | --- |
| `SELECT` / read exploration | none (default read-only) |
| DML (`INSERT`/`UPDATE`/`DELETE`) | `--write` |
| DDL (`CREATE`/`ALTER`/`DROP`/...) | `--write --ddl` |
| Destructive (`DROP`/`TRUNCATE`, `UPDATE`/`DELETE` without `WHERE`) | `--yes` (+ `--write`, + `--ddl` for DDL-class) |

> Safety flags at a glance:
> `--write` unlocks DML · `--ddl` unlocks DDL (**requires** `--write`) ·
> `--yes` confirms destructive ops.

Additional guarantees:

- **Identifier allowlist**: table/db names must match `^[a-zA-Z0-9_$]+$`;
  qualified `db.table` is allowed. Prevents injection in schema-exploration SQL.
- **Multi-statement rejection**: `query` accepts a single statement (one
  trailing `;` tolerated); multiple statements must use `txn`.
- **Pre-connection gating**: read-only and multi-statement checks run before any
  DB connection, so the right exit code is returned without touching the
  database.
- **Config safety**: prefer a read-only DB user (`ro_user`) for the default
  datasource; reserve write-capable users for explicit `-d` profiles.

---

## Best Practices

- **Explore before you query**: run `explore`/`analyze`/`schema` first to
  confirm column names and types; this prevents `SQL_ERROR` (exit `8`).
- **Bound large results**: always add `--limit N` to `SELECT`, or use
  `sample`/`read` which are already capped.
- **Default to JSON**: keep `-f json` (the default) so you can parse with `jq`;
  switch to `table` only when showing data to the user.
- **Validate WHERE on reads first**: before an `UPDATE`/`DELETE`, run the same
  `WHERE` as a `SELECT COUNT(*)` to confirm scope.
- **Match flags to intent**: DML -> `--write`; DDL -> `--write --ddl`;
  destructive -> add `--yes`. Don't add `--yes` speculatively.
- **Reuse the connection model**: each invocation opens and closes its own pool
  (and SSH tunnel). Don't try to hold connections across calls; just issue
  separate commands.
- **Prefer read-only DB users**: configure the default datasource with a
  read-only user; force writes to go through an explicit `-d` profile.
