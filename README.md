# mysql-cli

A Go CLI that replaces `designcomputer/mysql_mcp_server`, preserving all MCP features for direct AI agent invocation. Any agent that can run shell commands (Claude Code, Cursor, Codex, Aider) can query MySQL without an MCP runtime.

## Install

```bash
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest
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

All `MYSQL_*` environment variables from the original MCP are also supported (migration is zero-config).

## Commands

```bash
mysql-cli query "SELECT * FROM users LIMIT 10"        # read (default)
mysql-cli query "UPDATE users SET active=1 WHERE id=5" --write
mysql-cli query "DROP TABLE old" --write --ddl --yes   # destructive needs --yes
mysql-cli txn "INSERT INTO t VALUES (1)" "INSERT INTO t VALUES (2)" --write
mysql-cli schema users                                 # table structure
mysql-cli schema                                        # whole database
mysql-cli sample users -n 5                             # sample rows (max 20)
mysql-cli tables                                        # list tables
mysql-cli databases                                     # list databases
mysql-cli read users                                    # first 100 rows
mysql-cli explore                                       # db+table overview
mysql-cli analyze users                                 # schema + sample
mysql-cli                                               # enter REPL (human debug)
```

## Flags

- `-d, --datasource <name>` named datasource from config
- `-f, --format json|table|csv|tsv` output format (default json)
- `--write` allow DML
- `--ddl` allow DDL (requires `--write`)
- `--yes` confirm destructive operations
- `--limit N` row limit for SELECT
- `--timeout 30s` query timeout
- `--host/--port/--user/--password/--db` connection overrides

## Output

JSON by default (agent-friendly):

```json
{"success":true,"data":{"columns":["id"],"rows":[[1]]},"rows_affected":0,"meta":{}}
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

Exit codes: 0 OK, 2 conn, 3 readonly, 4 ddl-needs-write, 5 destructive-needs-yes, 6 identifier, 7 multi-statement, 8 sql, 9 timeout, 10 config.

## Safety

Default read-only. DML needs `--write`; DDL needs `--write --ddl`; `DROP/TRUNCATE` and `UPDATE/DELETE` without `WHERE` need `--yes`. Identifiers are validated; multi-statement input is rejected (use `txn`).

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
