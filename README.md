<div align="center">

# mysql-cli

**A Go CLI that lets any shell-capable AI agent query MySQL - no MCP runtime required.**

A drop-in replacement for [`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server):
all of its read/write capabilities, re-exposed as plain subcommands. If your agent
can run a shell, it can query MySQL.

[English](./README.md) · [简体中文](./README-zh.md)

[![Version](https://img.shields.io/github/v/release/AllenMuu/mysql-cli?label=version)](https://github.com/AllenMuu/mysql-cli/releases)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#install)
[![Output](https://img.shields.io/badge/output-JSON%20%7C%20table%20%7C%20CSV%20%7C%20TSV-blue)](#output)

</div>

---

## Why

The original MCP server is great - until you want to use it from an agent that doesn't
speak MCP. `mysql-cli` keeps the safety model and feature set, but ships as a single
binary with **JSON by default** and **stable exit codes**, so any agent
(Claude Code, Cursor, Codex, Aider, …) can drive it directly over a shell.

- **Agent-first** - stable JSON envelope + numeric exit codes, designed to be parsed, not read.
- **Safe by default** - read-only out of the box; writes/DDL/destructive ops need explicit flags.
- **Zero-config migration** - drop-in for the MCP server's `MYSQL_*` env vars.
- **Multi-datasource** - named profiles in TOML, with optional SSH tunneling.
- **One binary** - `go install` and you're done.

## Install

Pick the path that matches who you are.

### For Human Users

**One-shot installer** (binary + agent skills + write-confirmation configs, macOS/Linux/Windows):

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/AllenMuu/mysql-cli/main/install.sh -o install.sh
bash install.sh                 # run directly (not curl|bash) so prompts keep their TTY
# Windows (PowerShell)
.\install.ps1
```

The installer runs three steps and prompts you at each interactive one:
1. Downloads the prebuilt binary to `~/.local/bin` (or `%USERPROFILE%\.local\bin`).
2. Runs `npx skills add AllenMuu/mysql-cli` so your agent can discover `mysql-cli`.
3. Runs `mysql-cli agent init` so writes pop up a confirmation prompt (see [Write Confirmation](#write-confirmation-agent-init)).

**Alternatives** if you prefer minimal steps:

```bash
# npm wrapper (no Go toolchain needed; prebuilt binary)
npx @allenmuu/mysql-cli install

# Go install
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest

# Build from source
git clone https://github.com/AllenMuu/mysql-cli.git && cd mysql-cli
go build -o mysql-cli ./cmd/mysql-cli
```

Then configure a datasource and query (see [Configure](#configure)):

```bash
mysql-cli config init --global      # writes ~/.config/mysql-cli/config.toml
# edit the file: host/user/password/database
mysql-cli query "SELECT * FROM users LIMIT 10"
mysql-cli                           # enter REPL (human debugging only)
```

### For AI Agents

`mysql-cli` has **no browser-based auth**. An agent can complete the entire setup
over the shell in four steps. Keep `-f json` (the default) when driving programmatically - the
JSON envelope + exit codes are the contract you parse.

**Step 1 - Install the binary**

```bash
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest
# `mysql-cli` MUST be on PATH - the skills invoke it by name.
# Fallback: go build -o mysql-cli ./cmd/mysql-cli
```

**Step 2 - Install the Agent Skills**

`mysql-cli` ships skills via the [vercel-labs/skills](https://github.com/vercel-labs/skills)
ecosystem so agents can discover and call it correctly the first time.

```bash
# Interactive (TTY)
npx skills add AllenMuu/mysql-cli
# Non-interactive (CI / agent)
npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -g -y
```

> Install **all three** skills (`mysql-shared`, `mysql-query`, `mysql-schema`).
> The latter two reference `../mysql-shared/SKILL.md`; installing only one breaks the reference.
>
> No Node.js? Manually copy the repo's `skills/` directory into your agent's skill
> directory (e.g. `~/.claude/skills/`).

**Step 3 - Configure a datasource**

Write `~/.config/mysql-cli/config.toml` (full format in [Configure](#configure)):

```toml
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
password = "${MYSQL_DEV_PASSWORD}"
database = "app"
```

**Step 4 - Verify & run**

```bash
mysql-cli query "SELECT * FROM users LIMIT 10"      # JSON by default
```

See [Output](#output) and [Exit codes](#exit-codes) for the response contract.

## Write Confirmation (`agent init`)

`--write` / `--ddl` / `--yes` are flags the **AI passes itself** when it wants to
mutate data. The CLI alone can't pull a human into the loop. `mysql-cli agent init`
fixes this by installing per-agent configs that **intercept any command containing
those flags and prompt a human before it runs**. Read-only commands pass through
uninterrupted.

### What it installs

| Agent | `--project` (cwd) | `--global` (user-level) | Capability |
| --- | --- | --- | --- |
| `claude` | `.claude/settings.json` + `.claude/hooks/mysql-write-guard.py` | `~/.claude/...` | **enforce** (PreToolUse hook → ask) |
| `cursor` | `.cursor/rules/mysql-cli-write-guard.mdc` | _not supported_ (IDE setting) | **guide** (rule only) |
| `opencode` | `opencode.json` | `~/.config/opencode/opencode.json` | **enforce** (permission glob → ask) |
| `copilot` | `.vscode/settings.json` + `.github/copilot-instructions.md` | VS Code User `settings.json` | **enforce** (`autoApprove` regex → false) |
| `codebuddy` | `.codebuddy/settings.json` + `.codebuddy/hooks/mysql-write-guard.py` | `~/.codebuddy/...` | **enforce** (PreToolUse hook → ask) |
| `trae` | `.trae/hooks.json` + `.trae/hooks/mysql-write-guard.py` | `~/.trae-cn/hooks.json` + `~/.trae-cn/hooks/mysql-write-guard.py` | **enforce** (PreToolUse hook → ask; matcher `RunCommand`) |
| `pi` | `.pi/extensions/mysql-write-guard.ts` | `~/.pi/agent/extensions/mysql-write-guard.ts` | **enforce** (`tool_call` hook → `ctx.ui.confirm` → block; matcher `bash`) |

- **enforce** = engine-level gate: only commands with `--write` / `--ddl` / `--yes`
  trigger the prompt; reads pass silently.
- **guide** = context instruction; relies on the model honoring it (no engine gate).
- Codex is **not supported** - its hook story is incomplete and `.rules` can't match flags precisely.
- Merge-class configs (`settings.json`, `opencode.json`, `.vscode/settings.json`,
  TRAE `hooks.json`) are **deep-merged** into the existing file with a `.bak` backup;
  re-running is idempotent. Single-file configs (`.mdc`, `.md`, Pi `.ts`) skip
  existing files unless `--force` is given.
- TRAE's asymmetric paths (`.trae` project vs `~/.trae-cn` global, the `-cn` suffix)
  are TRAE's official design - identical for international and China editions.
- Pi auto-discovers `extensions/*.ts`; no `settings.json` change is needed. Reload
  inside pi with `/reload`. Project-local extensions load only after the project is
  `/trust`-ed; global extensions work immediately. Pi's built-in shell tool is
  `bash` (lowercase), and the decision shape is `{ block: true, reason? }`.

### How to run it

```bash
# Human (interactive TTY): multi-select agents + scope
mysql-cli agent init

# CI / agent (non-interactive)
mysql-cli agent init --agents claude,opencode,copilot --project
mysql-cli agent init --agents codebuddy --global
mysql-cli agent init --agents cursor --project --dry-run    # preview, write nothing
mysql-cli agent init --agents claude --project --json       # JSON result
```

Flags:

| Flag | Purpose |
| --- | --- |
| `--agents <a,b,c>` | Comma-separated agent names (required when not a TTY) |
| `--project` / `--global` | Write to the current project or to the user-level config (exactly one required when not a TTY) |
| `--force` | Overwrite single-file configs (`.mdc`, `.md`) that already exist |
| `--dry-run` | Print actions without writing |
| `-j, --json` | Emit JSON result |

### Verify

After installing, in a **fresh** agent session, ask the agent to run these two
commands. The read should pass; the write should pop a confirmation prompt.

```bash
mysql-cli query "SELECT 1"                                         # passes silently
mysql-cli query "UPDATE t SET a=1 WHERE id=1" --write              # prompts a human
```

The hook script can be tested standalone (TRAE uses `RunCommand`; Claude/CodeBuddy use `Bash`):

```bash
echo '{"tool_name":"RunCommand","tool_input":{"command":"mysql-cli query \"DROP TABLE x\" --write --yes"}}' \
  | python3 .trae/hooks/mysql-write-guard.py
# expect: {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",...}}
```

Full capability matrix, per-agent write paths, and design notes:
[`docs/agent-integration.md`](./docs/agent-integration.md).

## Configure

`~/.config/mysql-cli/config.toml` (override with `--config`):

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

### Config resolution

mysql-cli discovers config files in a chain and merges them:

- **File priority (high → low)**: `--config <file>` > `MYSQL_CLI_CONFIG` env > project-level `<project>/.config/mysql-cli/config.toml` (only if [trusted](#project-level-config-trust)) > global `~/.config/mysql-cli/config.toml`.
- **Short-circuit**: setting `--config` or `MYSQL_CLI_CONFIG` reads only that file; auto-discovery is skipped.
- **Same-named datasource**: the higher-priority file replaces it wholesale (including its `[ssh]` subtable) -- fields are not merged one by one.
- **Different-named datasources**: union -- all names from all files are available.
- **`default` / `default_limit`**: the higher-priority file wins.
- **Field overrides**: `MYSQL_*` env vars and `--host/--port/--user/--password/--db` flags override the datasource fields from any file.

Example: global defines `[datasource.dev]` + `[datasource.prod]`; a trusted project redefines `[datasource.dev]` (different host) and adds `[datasource.ci]`. Effective config: `dev` (project's), `prod` (global's), `ci` (project's).

### Project-level config trust

A project-level `<project>/.config/mysql-cli/config.toml` is **not loaded** by
default (prevents a cloned malicious repo from injecting credentials). When
untrusted, mysql-cli falls back to the global config and prints a **stderr
warning** naming the skipped file (suppress with `--no-trust-warn` or
`MYSQL_CLI_NO_TRUST_WARN=1`; make it a hard error with `--strict-trust`).
Trust explicitly with `mysql-cli config trust --yes` (non-interactive) or an
interactive `y/N` -- **AI agents must not auto-trust**.

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
| `version` | Print the mysql-cli binary version |
| `config <sub>` | Manage config (`init` / `trust` / `path` / `show`) |
| `agent init` | Install per-agent write-confirmation configs (see [above](#write-confirmation-agent-init)) |
| `help [command]` | Help about any command (also `--help` / `-h`) |
| *(none)* | Enter the interactive REPL (human debugging) |

## Flags

| Flag | Description |
| --- | --- |
| `-d, --datasource <name>` | Named datasource from config |
| `-f, --format json\|table\|csv\|tsv\|jsonl` | Output format (default `json`) |
| `--write` | Allow DML |
| `--ddl` | Allow DDL (requires `--write`) |
| `--yes` | Mark destructive operations (triggers human confirmation when an `agent init` config is installed) |
| `--limit N` | Row limit for `SELECT` |
| `--no-limit` | Disable the default 1000-row SELECT cap |
| `--timeout 30s` | Query timeout |
| `--host/--port/--user/--password/--db` | Connection overrides |

## Output

JSON by default (agent-friendly):

```json
{"success":true,"data":{"columns":["id"],"rows":[[1]]},"rows_affected":0,"meta":{}}
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

Switch the human-readable formats with `-f table`, `-f csv`, `-f tsv`, or `-f jsonl`.

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
| `11` | Internal error (panic) |

## Safety

Default read-only. Writes are gated in tiers:

- DML needs `--write`
- DDL needs `--write --ddl`
- `DROP`/`TRUNCATE` and `UPDATE`/`DELETE` without a `WHERE` need `--yes`

Identifiers are validated against a strict allowlist (`^[a-zA-Z0-9_$]+$`);
multi-statement input is rejected (use `txn`). The read-only / multi-statement
checks run **before** a connection is opened, so agents get the right exit code
without touching the database.

> `--yes` is a **marker**, not a waiver: it tells the CLI "this is a destructive
> op, please ask a human." When an [`agent init`](#write-confirmation-agent-init)
> config is installed, that's where the human prompt fires.

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

## Agent Skills

`mysql-cli` ships [Agent Skills](./skills/) so agents can discover and drive
it without an MCP runtime. Skills encode trigger conditions, pre-flight
checks, command reference, the safety model, and error self-repair - so the
agent calls `mysql-cli` correctly the first time.

There are three skills, following the shared-skill pattern from `larksuite/cli`:

| Skill | Purpose |
| --- | --- |
| [`mysql-shared`](./skills/mysql-shared/SKILL.md) | Config, datasource, global flags, safety model, exit codes, error recovery, output formats - referenced by the other two |
| [`mysql-query`](./skills/mysql-query/SKILL.md) | Run SQL: `query`, `txn`, DML/DDL |
| [`mysql-schema`](./skills/mysql-schema/SKILL.md) | Explore schema: `tables`, `databases`, `schema`, `sample`, `read`, `explore`, `analyze` |

### Other agents

`mysql-cli` works with **any agent that can run shell commands and parse
JSON**. The `npx skills add` installer (vercel-labs/skills) supports Claude
Code, Cursor, Codex, and 70+ more agents; it symlinks each skill into the
agent's skill directory.

| Agent | Config format | Install |
| --- | --- | --- |
| **Claude Code** | `.claude/skills/*/SKILL.md` | `npx skills add AllenMuu/mysql-cli -a claude-code` |
| **Cursor** | `.cursor/rules/*.mdc` | `npx skills add AllenMuu/mysql-cli -a cursor` |
| **Codex CLI** | `AGENTS.md` | `npx skills add AllenMuu/mysql-cli -a codex` |
| **OpenCode** | `.opencode/instructions.md` | `npx skills add AllenMuu/mysql-cli -a opencode` |
| **GitHub Copilot** | `.github/copilot-instructions.md` | `npx skills add AllenMuu/mysql-cli -a github-copilot` |
| **Windsurf** | `.windsurfrules` | `npx skills add AllenMuu/mysql-cli -a windsurf` |
| **Aider** | `.aider.instructions.md` | `npx skills add AllenMuu/mysql-cli -a aider` (then add `read:` to `.aider.conf.yml`) |

### Setup notes

- **`mysql-cli` must be on `PATH`** - install with
  `go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest`, or edit the
  skill to point at your built binary.
- **Config file** - the skill expects `~/.config/mysql-cli/config.toml`
  (override with `--config`). See [Configure](#configure).
- **Default JSON output** - the skill relies on the JSON envelope + exit codes;
  keep `-f json` (the default) when driving programmatically.

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
| `result` | Shared `Result{Columns, Rows, RowsAffected, LastInsertID}` - the neutral contract |
| `safety` | SQL classification, read-only gate, identifier validation, multi-statement & destructive-op detection (pure, fully unit-tested) |
| `config` | TOML named datasources + `MYSQL_*` env compat |
| `conn` | DSN rendering, connection pool, SSH tunnel lifecycle |
| `query` | Read / write / transaction execution, each statement gated by `safety` |
| `schema` | Read-only exploration commands |
| `format` | `result` -> json/table/csv/tsv |
| `cli` | cobra subcommands + `mapError` (errors -> exit codes) |
| `repl` | readline shell for human debugging |
| `agentsetup` | Per-agent write-confirmation configs (templates embedded in the binary) |

## Acknowledgements

This project is inspired by and builds upon
[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server).
Much of the safety model and feature set traces back to that work.

## License

Released under the [MIT License](./LICENSE).
