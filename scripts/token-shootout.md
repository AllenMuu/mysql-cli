# token-shootout

A/B comparison between **mysql-mcp** (the MCP server Claude Code calls natively)
and **mysql-cli** (the shell-agent path) for the same query tasks against the
same database. Measures **latency**, **response bytes**, and an **estimated
token count**.

## Why

`mysql-cli` is a drop-in replacement for `mysql-mcp-server`. This script gives
you hard evidence for the tradeoffs: which path is faster, which returns less
data to the model, and how `mysql-cli`'s output formats change the bill.

## Prerequisites

- `mysql-cli` on disk (auto-detected: `PATH` → `~/go/bin` → `~/.local/bin` →
  repo root). Override with `--cli /path/to/mysql-cli`.
- `uvx` on `PATH` (used to launch the MCP server).
- A populated `.mcp.json` at the repo root (the script reads `MYSQL_*` creds
  from it and injects them into **both** paths so they hit the same DB).

## Run

```bash
# default (uses information_schema.tables so it runs anywhere)
python3 scripts/token-shootout.py

# real table, write markdown to file
python3 scripts/token-shootout.py --table users --out result.md

# debug MCP tool/param discovery
python3 scripts/token-shootout.py -v

# add a heavy full-table-scan task (bare SELECT vs --limit guard).
# pulls the WHOLE table -- run on a table you know, mind the DB load.
python3 scripts/token-shootout.py --table sd_cx_order --no-limit
```

## How to read the output

Each task produces a table: `method | latency (ms) | bytes | ≈tokens | status`.
A final **Summary** table compares every method's ≈tokens against the MCP
baseline for that task (`baseline` / `-N%` = smaller / `+N%` = larger).

## Fairness notes

- **Same DB, same creds**: `MYSQL_*` from `.mcp.json` is injected into both the
  MCP server process and the `mysql-cli` subprocess.
- **MCP is warmed up** before measuring (a throwaway `SELECT 1`), so its numbers
  reflect the hot path Claude Code actually sees (resident process, warm
  connection). Without this, the first call is dominated by `uvx` cold start
  (~700 ms) and is not representative.
- **CLI is NOT warmed up** on purpose. It is a short-lived process by design;
  its per-call fork + connect cost is the real shell-agent cost model.
- **MCP param names are auto-probed** via `tools/list`, so the script adapts to
  different `mysql-mcp-server` versions (`execute_sql`→`query`,
  `get_schema_info`→`table_name`, …).

## Token estimate caveat

`≈tokens = response bytes ÷ 4`. This is a rough proxy:

- It is accurate-ish for ASCII JSON (the common case).
- It **under-counts** for CJK / emoji content (UTF-8 multibyte chars cost more
  tokens per byte).
- Real token counts live only in the **Claude Code transcript** (what the model
  actually consumed, including tool-call overhead and the surrounding envelope),
  which this script cannot see.

**Treat `bytes` as the honest comparison axis; treat `≈tokens` as an
order-of-magnitude hint.** For authoritative token numbers, run the same query
both ways from a real Claude Code session and diff the transcript costs.

## What the default run typically shows

(On `information_schema.tables` against a real DB; your numbers will vary.)

- **Latency**: `mysql-cli` (compiled Go binary) is usually faster per call than
  the warm MCP server (Python). The MCP server's resident-process advantage
  does not always beat Go's lower per-call overhead at agent query cadence.
- **Bytes/tokens**: the MCP server's default output is already compact. `mysql-cli`
  `--format json` is slightly larger (full envelope + field names); `--format csv`
  matches MCP closely; `--format table` is the most expensive (avoid it when
  token cost matters — it is for humans, not agents).

So the CLI's win over MCP is **speed + portability (any shell agent) +
multi-datasource + exit-code contract + debuggability** — not raw token
savings, unless you pick `csv`.

## The `--no-limit` task: cost of an unguarded full-table scan

`--no-limit` appends a task that runs a bare `SELECT * FROM <table>` (no LIMIT)
three ways, to expose what each path does when the agent forgets to cap rows:

| method | behavior |
|---|---|
| MCP `execute_sql` (no LIMIT) | mysql-mcp-server does **not** auto-cap; full table returned |
| CLI `query` (no `--limit`) | `applyLimit` is a no-op when `--limit` is unset; full table returned |
| CLI `query --limit 20` | `applyLimit` wraps as `SELECT * FROM (<sql>) AS _q LIMIT 20`; capped |

Measured on `sd_cx_order` (44,516 rows):

| method | ≈tokens | vs MCP bare |
|---|---:|---:|
| MCP bare | 6,809,371 | baseline |
| CLI bare | 9,085,333 | +33% |
| CLI `--limit 20` | 3,977 | -100% (saves 99.94%) |

Takeaways:

- **Both paths blow up bare.** MCP returns ~6.8M tokens, CLI ~9.1M - roughly
  **34x and 45x a 200K context window**. One forgotten LIMIT instantly
  overflows any agent session. The MCP server has **no** built-in row cap.
- **CLI bare is *worse* than MCP bare** (+33%) because CLI's JSON envelope is
  fatter. So "CLI saves tokens" does **not** hold for unguarded scans.
- **CLI's `--limit` is an explicit opt-in guard, not a default.** `applyLimit`
  is a no-op without `--limit`, so forgetting it is identical to MCP. Its value
  is a structured flag independent of the SQL string that a skill can enforce
  or recommend - the tool itself does not protect you.

**`vs MCP` column sign**: `+` = more tokens than MCP (worse), `-` = fewer
(better). `-100%` is rounding of -99.94%.
