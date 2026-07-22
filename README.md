<div align="center">

# mysql-cli

**A Go CLI that lets any shell-capable AI agent query MySQL — no MCP runtime required.**

A drop-in replacement for [`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server):
all of its read/write capabilities, re-exposed as plain subcommands. If your agent
can run a shell, it can query MySQL.

[English](./README.md) · [简体中文](./README-zh.md)

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#install)
[![Output](https://img.shields.io/badge/output-JSON%20%7C%20table%20%7C%20CSV%20%7C%20TSV-blue)](#output)

</div>

---

## Why

The original MCP server is great — until you want to use it from an agent that doesn't
speak MCP. `mysql-cli` keeps the safety model and feature set, but ships as a single
binary with **JSON by default** and **stable exit codes**, so any agent
(Claude Code, Cursor, Codex, Aider, …) can drive it directly over a shell.

- **Agent-first** — stable JSON envelope + numeric exit codes, designed to be parsed, not read.
- **Safe by default** — read-only out of the box; writes/DDL/destructive ops need explicit flags.
- **Zero-config migration** — drop-in for the MCP server's `MYSQL_*` env vars.
- **Multi-datasource** — named profiles in TOML, with optional SSH tunneling.
- **One binary** — `go install` and you're done.

## Install

```bash
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest
```

Build from source:

```bash
git clone https://github.com/AllenMuu/mysql-cli.git
cd mysql-cli
go build -o mysql-cli ./cmd/mysql-cli
```

> Requires Go 1.22+.

## Quick start

```bash
mysql-cli query "SELECT * FROM users LIMIT 10"        # read (default)
mysql-cli tables                                       # list tables
mysql-cli schema users                                 # table structure
mysql-cli                                              # enter REPL (human debugging)
```

## Configure

`~/.config/mysql-cli/config.toml`:

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

Resolution priority: **CLI flag > env > file > default**. Passwords support `${ENV}`
placeholders. All `MYSQL_*` environment variables from the original MCP are also
supported, so migration is zero-config.

## Commands

| Command | Description |
| --- | --- |
| `query <sql>` | Run a SQL statement (read by default; `--write` for DML) |
| `txn <sql1> [sql2…]` | Run multiple statements in one atomic transaction |
| `schema [table]` | Show table structure, or the whole database when no table given |
| `sample <table>` | Sample rows (`-n`, max 20) |
| `tables [db]` | List tables |
| `databases` | List databases |
| `read <table>` | First 100 rows |
| `explore` | Database + table overview |
| `analyze <table>` | Schema + sample in one shot |
| *(none)* | Enter the interactive REPL (human debugging) |

## Flags

| Flag | Description |
| --- | --- |
| `-d, --datasource <name>` | Named datasource from config |
| `-f, --format json\|table\|csv\|tsv` | Output format (default `json`) |
| `--write` | Allow DML |
| `--ddl` | Allow DDL (requires `--write`) |
| `--yes` | Confirm destructive operations |
| `--limit N` | Row limit for `SELECT` |
| `--timeout 30s` | Query timeout |
| `--host/--port/--user/--password/--db` | Connection overrides |

## Output

JSON by default (agent-friendly):

```json
{"success":true,"data":{"columns":["id"],"rows":[[1]]},"rows_affected":0,"meta":{}}
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

Switch the human-readable formats with `-f table`, `-f csv`, or `-f tsv`.

### Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | OK |
| `2` | Connection error |
| `3` | Read-only violation |
| `4` | DDL needs `--write` |
| `5` | Destructive op needs `--yes` |
| `6` | Invalid identifier |
| `7` | Multi-statement input rejected |
| `8` | SQL error |
| `9` | Timeout |
| `10` | Config error |

## Safety

Default read-only. Writes are gated in tiers:

- DML needs `--write`
- DDL needs `--write --ddl`
- `DROP`/`TRUNCATE` and `UPDATE`/`DELETE` without a `WHERE` need `--yes`

Identifiers are validated against a strict allowlist (`^[a-zA-Z0-9_$]+$`);
multi-statement input is rejected (use `txn`). The read-only / multi-statement
checks run **before** a connection is opened, so agents get the right exit code
without touching the database.

## SSH tunnel

A datasource can tunnel through an SSH bastion instead of connecting directly:

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

The tunnel is opened before the DB connection and closed together with it.

## Usage with AI Agents

`mysql-cli` ships an [Agent Skill](./skills/mysql/SKILL.md) so agents can
discover and drive it without an MCP runtime. The skill encodes trigger
conditions, pre-flight checks, command reference, the safety model, and
error self-repair — so the agent calls `mysql-cli` correctly the first time.

### Quick install (Claude Code)

`mysql-cli` ships a [Claude Code Skill](./skills/mysql/SKILL.md). Copy it into your project:

```bash
cp -r skills/mysql .claude/skills/
```

Or use the installer (also installs globally):

```bash
./scripts/install-skills.sh --agent claude
./scripts/install-skills.sh --agent claude --project-dir ~/my-project
```

### Other agents

`mysql-cli` works with **any agent that can run shell commands and parse JSON**, but only Claude Code natively supports the SKILL.md format. For other agents, copy the relevant sections of [`skills/mysql/SKILL.md`](./skills/mysql/SKILL.md) into their native configuration files.

| Agent | Config format | How to use `mysql-cli` |
| --- | --- | --- |
| **Claude Code** | `.claude/skills/SKILL.md` (YAML frontmatter + Markdown) | `cp -r skills/mysql .claude/skills/` — auto-loaded |
| **Cursor** | `.cursor/rules/*.mdc` (YAML frontmatter + Markdown) | Create `.cursor/rules/mysql-cli.mdc` (see example below) |
| **OpenCode** | `.opencode.json` (JSON config) | Not SKILL.md compatible; add usage notes to your `.opencode.json` instructions or use shell directly |
| **Codex CLI** | No local skill file; platform-managed skills | Call `mysql-cli query "..." -f json` directly via shell |
| **Aider** | `.aider.conf.yml` (YAML) | Add to `read:` list, or call `mysql-cli` over shell |
| **GitHub Copilot** | `.github/copilot-instructions.md` (Markdown) | Add `mysql-cli` usage examples to instructions file |
| **Windsurf** | `.windsurfrules` (plain text) | Add `mysql-cli` rules inline |

#### Cursor example

Create `.cursor/rules/mysql-cli.mdc`:

```markdown
---
description: MySQL queries and schema exploration via mysql-cli
globs: *.sql
---
Use `mysql-cli` for all database operations. Default read-only.
- Read: `mysql-cli query "<sql>" -f json`
- Write (DML): `mysql-cli query "<sql>" --write -f json`
- DDL: `mysql-cli query "<sql>" --write --ddl -f json`
- Schema: `mysql-cli schema <table> -f json`
- Transaction: `mysql-cli txn "<s1>" "<s2>" --write -f json`
See `~/.config/mysql-cli/config.toml` for datasource config.
```

### Available skill

| Skill | Path | Description |
| --- | --- | --- |
| `mysql` | [`skills/mysql/SKILL.md`](./skills/mysql/SKILL.md) | Query MySQL, explore schema, run transactions/DML/DDL via `mysql-cli` |

### Setup notes

- **`mysql-cli` must be on `PATH`** — install with
  `go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest`, or edit the
  skill to point at your built binary. / `mysql-cli` 必须在 `PATH` 中。
- **Config file** — the skill expects `~/.config/mysql-cli/config.toml`
  (override with `--config`). See [Configure](#configure). / 需配置文件。
- **Default JSON output** — the skill relies on the JSON envelope + exit codes;
  keep `-f json` (the default) when driving programmatically. / 默认 JSON 输出。

## Architecture

Strict one-way layering; `result` is the dependency-free neutral contract that
keeps producers and consumers decoupled.

```
cmd/mysql-cli/main  ->  cli   (cobra wiring + exit-code mapping)
                          │
        config ─-> conn ─-> query ─-> result
          │        │       └─> safety   (pure logic, zero deps)
          │        └─> schema ─> result/safety
          └ env/file        repl  (aggregates query + schema + format)
                            format ← result
```

| Package | Responsibility |
| --- | --- |
| `result` | Shared `Result{Columns, Rows, RowsAffected, LastInsertID}` — the neutral contract |
| `safety` | SQL classification, read-only gate, identifier validation, multi-statement & destructive-op detection (pure, fully unit-tested) |
| `config` | TOML named datasources + `MYSQL_*` env compat |
| `conn` | DSN rendering, connection pool, SSH tunnel lifecycle |
| `query` | Read / write / transaction execution, each statement gated by `safety` |
| `schema` | Read-only exploration commands |
| `format` | `result` → json/table/csv/tsv |
| `cli` | cobra subcommands + `mapError` (errors → exit codes) |
| `repl` | readline shell for human debugging |

## Acknowledgements

This project is inspired by and builds upon
[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server).
Much of the safety model and feature set traces back to that work.

## License

Released under the [MIT License](./LICENSE).
