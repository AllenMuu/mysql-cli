#!/usr/bin/env python3
"""mysql-cli vs mysql-mcp: A/B shootout.

Measures latency, response bytes, and estimated tokens (bytes / 4) for the same
query tasks via two paths to the SAME database:
  - MCP:  mysql-mcp-server over stdio JSON-RPC (the path Claude Code uses)
  - CLI:  mysql-cli subcommands over subprocess (the shell-agent path)

Env is read from .mcp.json and injected into BOTH paths so they hit the same DB
with the same credentials -- zero config drift.

Token estimate is bytes / 4, a rough proxy. Real token counts live only in the
Claude Code transcript, which this script cannot see. Use BYTES as the honest
comparison axis; treat the tokens column as an order-of-magnitude hint.

Usage:
  python3 scripts/token-shootout.py                 # default table
  python3 scripts/token-shootout.py --table users   # real table
  python3 scripts/token-shootout.py --out result.md -v
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from collections import defaultdict
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MCP_JSON = REPO / ".mcp.json"
DEFAULT_TABLE = "information_schema.tables"
BYTES_PER_TOKEN = 4


# ---------- locate mysql-cli ----------
def find_cli(override):
    if override:
        return override
    candidates = [
        shutil.which("mysql-cli"),
        str(Path.home() / "go" / "bin" / "mysql-cli"),
        str(Path.home() / ".local" / "bin" / "mysql-cli"),
        str(REPO / "mysql-cli"),
    ]
    for c in candidates:
        if c and Path(c).exists():
            return c
    sys.exit("mysql-cli not found. Pass --cli /path/to/mysql-cli or build it "
             "with `go build -o mysql-cli ./cmd/mysql-cli`.")


# ---------- load .mcp.json ----------
def load_mcp_config():
    if not MCP_JSON.exists():
        sys.exit(f".mcp.json not found at {MCP_JSON}")
    cfg = json.loads(MCP_JSON.read_text())
    srv = cfg["mcpServers"]["mysql"]
    return srv["command"], srv["args"], dict(srv.get("env", {}))


# ---------- MCP stdio JSON-RPC client ----------
class McpClient:
    def __init__(self, cmd, args, env, verbose=False):
        self.verbose = verbose
        self.proc = subprocess.Popen(
            [cmd] + args,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, text=True, bufsize=1,
        )
        self._id = 0
        self._tool_params = {}      # tool name -> set(param names)
        self._stderr_lines = []
        threading.Thread(target=self._drain_stderr, daemon=True).start()
        self._init()
        self._probe_tools()

    def _drain_stderr(self):
        for line in self.proc.stderr:
            self._stderr_lines.append(line)
            if self.verbose:
                sys.stderr.write(f"[mcp stderr] {line}")

    def _next(self):
        self._id += 1
        return self._id

    def _send(self, obj):
        self.proc.stdin.write(json.dumps(obj) + "\n")
        self.proc.stdin.flush()

    def _recv(self, want_id, timeout=180):
        end = time.time() + timeout
        while time.time() < end:
            line = self.proc.stdout.readline()
            if not line:
                tail = "".join(self._stderr_lines[-10:])
                raise RuntimeError(f"MCP EOF waiting for id={want_id}. stderr tail:\n{tail}")
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                if self.verbose:
                    sys.stderr.write(f"[mcp non-json] {line[:200]}\n")
                continue
            if obj.get("id") == want_id:
                return obj
        raise TimeoutError(f"MCP timeout waiting for id={want_id}")

    def _init(self):
        i = self._next()
        self._send({"jsonrpc": "2.0", "id": i, "method": "initialize",
                    "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                               "clientInfo": {"name": "shootout", "version": "0.1"}}})
        r = self._recv(i)
        if "error" in r:
            raise RuntimeError(f"initialize failed: {r['error']}")
        self._send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def _probe_tools(self):
        i = self._next()
        self._send({"jsonrpc": "2.0", "id": i, "method": "tools/list", "params": {}})
        r = self._recv(i)
        for t in r.get("result", {}).get("tools", []):
            props = t.get("inputSchema", {}).get("properties", {})
            self._tool_params[t["name"]] = set(props.keys())
        if self.verbose:
            sys.stderr.write(f"[mcp tools] {sorted(self._tool_params)}\n")
            for n, p in self._tool_params.items():
                sys.stderr.write(f"  {n}: {sorted(p)}\n")

    def call(self, tool, args):
        i = self._next()
        self._send({"jsonrpc": "2.0", "id": i, "method": "tools/call",
                    "params": {"name": tool, "arguments": args}})
        return self._recv(i)

    def param_of(self, tool, candidates):
        """Pick the real param name from candidates using the probed schema."""
        have = self._tool_params.get(tool, set())
        for c in candidates:
            if c in have:
                return c
        return candidates[0]  # best-effort fallback

    def close(self):
        try:
            self.proc.terminate()
            self.proc.wait(timeout=5)
        except Exception:
            self.proc.kill()


def mcp_extract(resp):
    """Return (text, ok, err_msg) from a tools/call response."""
    if "error" in resp:
        return "", False, json.dumps(resp["error"])[:160]
    result = resp.get("result", {})
    text = "\n".join(c.get("text", "") for c in result.get("content", [])
                     if c.get("type") == "text")
    if result.get("isError"):
        return text, False, text[:160]
    return text, True, ""


# ---------- CLI client ----------
def run_cli(cli, args, env, timeout=180):
    t0 = time.perf_counter()
    p = subprocess.run([cli] + args, capture_output=True, text=True,
                       env=env, timeout=timeout)
    dt = (time.perf_counter() - t0) * 1000
    return dt, p.stdout, p.returncode, p.stderr


# ---------- task definitions ----------
def build_tasks(table, include_nolimit=False):
    sample_sql = f"SELECT * FROM {table} LIMIT 20"
    tasks = [
        {"name": "list_tables",
         "runs": [
            ("MCP execute_sql",          "mcp_sql",    "SHOW TABLES"),
            ("CLI tables",               "cli_tables", None),
            ("CLI query --format table", "cli_query",  ("SHOW TABLES", "table")),
         ]},
        {"name": f"schema({table})",
         "runs": [
            ("MCP get_schema_info", "mcp_schema", table),
            ("CLI schema",          "cli_schema", table),
         ]},
        {"name": f"sample({table} LIMIT 20)",
         "runs": [
            ("MCP execute_sql",          "mcp_sql",   sample_sql),
            ("CLI query --format json",  "cli_query", (sample_sql, "json")),
            ("CLI query --format table", "cli_query", (sample_sql, "table")),
            ("CLI query --format csv",   "cli_query", (sample_sql, "csv")),
         ]},
    ]
    if include_nolimit:
        # Full-table scan with NO LIMIT. Reveals whether each path self-limits:
        #   MCP execute_sql  -> does the server cap rows on its own?
        #   CLI query        -> applyLimit is a no-op without --limit, so bare.
        #   CLI query --limit 20 -> the explicit guard, for contrast.
        full_sql = f"SELECT * FROM {table}"
        tasks.append({
            "name": f"no_limit_full_scan({table})",
            "runs": [
                ("MCP execute_sql (no LIMIT)", "mcp_sql",       full_sql),
                ("CLI query (no --limit)",     "cli_query_raw", full_sql),
                ("CLI query --limit 20",       "cli_query_limited", (full_sql, "json", 20)),
            ],
        })
    return tasks


# ---------- measure one run ----------
def measure(run, mcp, cli, env):
    label, kind, payload = run
    try:
        if kind == "mcp_sql":
            p = mcp.param_of("execute_sql", ["query", "sql", "statement"])
            t0 = time.perf_counter()
            r = mcp.call("execute_sql", {p: payload})
            dt = (time.perf_counter() - t0) * 1000
            out, ok, err = mcp_extract(r)
        elif kind == "mcp_schema":
            p = mcp.param_of("get_schema_info", ["table_name", "table"])
            t0 = time.perf_counter()
            r = mcp.call("get_schema_info", {p: payload})
            dt = (time.perf_counter() - t0) * 1000
            out, ok, err = mcp_extract(r)
        elif kind == "cli_tables":
            dt, out, rc, err = run_cli(cli, ["tables", "--format", "json"], env)
            ok = rc == 0
            err = err[:160]
        elif kind == "cli_schema":
            dt, out, rc, err = run_cli(cli, ["schema", payload, "--format", "json"], env)
            ok = rc == 0
            err = err[:160]
        elif kind == "cli_query":
            sql, fmt = payload
            dt, out, rc, err = run_cli(cli, ["query", sql, "--format", fmt], env)
            ok = rc == 0
            err = err[:160]
        elif kind == "cli_query_raw":
            # Bare SELECT with NO --limit: tests whether CLI (like MCP) returns
            # the full result set when the guard is absent. applyLimit is a
            # no-op when --limit is unset, so this is a true full-table scan.
            # --timeout extends the CLI's internal query timeout past the 30s
            # default so a large table doesn't trip it.
            sql = payload
            dt, out, rc, err = run_cli(cli, ["query", sql, "--format", "json",
                                             "--timeout", "120s"], env)
            ok = rc == 0
            err = err[:160]
        elif kind == "cli_query_limited":
            # Same bare SQL, but with the explicit --limit guard. applyLimit
            # wraps it as `SELECT * FROM (<sql>) AS _q LIMIT N`. Contrast with
            # cli_query_raw to quantify the guard's value.
            sql, fmt, limit = payload
            dt, out, rc, err = run_cli(cli, ["query", sql, "--format", fmt,
                                             "--limit", str(limit)], env)
            ok = rc == 0
            err = err[:160]
        else:
            return label, "", 0, 0, False, "unknown kind"
    except Exception as e:
        return label, "", 0, 0, False, f"exc: {e}"[:160]
    n = len(out.encode("utf-8")) if out else 0
    return label, out, dt, n, ok, err


# ---------- markdown render ----------
def fmt_num(n):
    return f"{n:,}"


def render(tasks, results, out_file, env):
    lines = []
    lines.append("# mysql-cli vs mysql-mcp: A/B shootout\n")
    lines.append(f"- DB: `{env.get('MYSQL_HOST', '?')}` / `{env.get('MYSQL_DATABASE', '?')}`")
    lines.append(f"- Token estimate = response bytes ÷ {BYTES_PER_TOKEN} (rough proxy; "
                 "real tokens live in the Claude Code transcript).")
    lines.append("- Latency is end-to-end per call (ms). CLI pays process fork + connect "
                 "each call; MCP reuses a warm process + connection pool.\n")

    summary = []
    for task, runs in zip(tasks, results):
        lines.append(f"## {task['name']}\n")
        lines.append("| method | latency (ms) | bytes | ≈tokens | status |")
        lines.append("|---|---:|---:|---:|---|")
        for (label, _out, dt, n, ok, err) in runs:
            tok = n // BYTES_PER_TOKEN
            status = "ok" if ok else f"FAIL: {err}"
            lines.append(f"| {label} | {dt:.0f} | {fmt_num(n)} | {fmt_num(tok)} | {status} |")
            summary.append((task["name"], label, dt, n, tok, ok))
        lines.append("")

    lines.append("## Summary (≈tokens, vs MCP baseline per task)\n")
    lines.append("| task | method | ≈tokens | vs MCP |")
    lines.append("|---|---|---:|---:|")
    by_task = defaultdict(list)
    for row in summary:
        by_task[row[0]].append(row)
    for tname, items in by_task.items():
        mcp_tok = next((it[4] for it in items if it[1].startswith("MCP")), None)
        for (_t, label, _dt, _n, tok, _ok) in items:
            if label.startswith("MCP"):
                vs = "baseline"
            elif mcp_tok and mcp_tok > 0:
                # + = more tokens than MCP (worse), - = fewer (better).
                vs = f"{(tok / mcp_tok - 1) * 100:+.0f}%"
            else:
                vs = "n/a"
            lines.append(f"| {tname} | {label} | {fmt_num(tok)} | {vs} |")
    lines.append("")

    out = "\n".join(lines)
    if out_file:
        Path(out_file).write_text(out)
        print(f"written: {out_file}", file=sys.stderr)
    print(out)


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--table", default=DEFAULT_TABLE,
                    help=f"table for schema/sample tasks (default: {DEFAULT_TABLE})")
    ap.add_argument("--cli", default=None, help="path to mysql-cli binary (default: auto-detect)")
    ap.add_argument("--out", default=None, help="also write markdown to this file")
    ap.add_argument("--no-limit", action="store_true",
                    help="add a full-table-scan task: bare SELECT via MCP/CLI "
                         "vs CLI --limit guard (heavy; can pull the whole table)")
    ap.add_argument("-v", "--verbose", action="store_true",
                    help="show MCP tool list / stderr / non-json lines")
    args = ap.parse_args()

    cli = find_cli(args.cli)
    mcp_cmd, mcp_args, mcp_env = load_mcp_config()
    # Both subprocesses need PATH (to find uvx / mysql-cli) PLUS the MYSQL_*
    # creds from .mcp.json. Build one shared env so both hit the same DB.
    proc_env = dict(os.environ)
    proc_env.update(mcp_env)

    print(f"mysql-cli : {cli}", file=sys.stderr)
    print(f"mcp server: {mcp_cmd} {' '.join(mcp_args)}", file=sys.stderr)
    print(f"db        : {mcp_env.get('MYSQL_HOST')}/{mcp_env.get('MYSQL_DATABASE')}", file=sys.stderr)

    mcp = McpClient(mcp_cmd, mcp_args, proc_env, verbose=args.verbose)
    try:
        # Warm up MCP so measured calls reflect the hot path (Claude Code keeps
        # the server resident + connection warm). CLI is a short-lived process
        # by design, so its per-call fork+connect cost stays in the numbers on
        # purpose -- that is the real shell-agent cost model.
        try:
            qp = mcp.param_of("execute_sql", ["query", "sql", "statement"])
            mcp.call("execute_sql", {qp: "SELECT 1"})
            print("  (mcp warm-up: SELECT 1)", file=sys.stderr)
        except Exception as e:
            print(f"  (mcp warm-up failed: {e})", file=sys.stderr)
        tasks = build_tasks(args.table, args.no_limit)
        results = []
        for task in tasks:
            runs = []
            for run in task["runs"]:
                r = measure(run, mcp, cli, proc_env)
                runs.append(r)
                flag = "ok" if r[4] else "FAIL"
                print(f"  {task['name']:<30} {r[0]:<26} {r[2]:>6.0f}ms  "
                      f"{fmt_num(r[3]):>9}B  {flag}", file=sys.stderr)
            results.append(runs)
        render(tasks, results, args.out, mcp_env)
    finally:
        mcp.close()


if __name__ == "__main__":
    main()
