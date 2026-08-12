// Package repl provides a minimal interactive shell for human debugging.
// It is NOT the primary agent path; agents call subcommands directly.
package repl

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/format"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/AllenMuu/mysql-cli/internal/safety"
	"github.com/AllenMuu/mysql-cli/internal/schema"
	"github.com/chzyer/readline"
)

// Config carries everything the REPL needs without importing cli.
type Config struct {
	Pool   *conn.Pool
	Opts   query.Options
	Out    io.Writer
	Format string

	// DefaultCap 是 REPL 下 SELECT 的默认行数上限（与 CLI 子命令行为对齐）。
	// 0 表示不应用默认 cap（保持原行为，仅供测试或显式不限制时使用）。
	// 由调用方（cli）填入 cli.Globals.defaultCap() 的结果。
	DefaultCap int
}

const exitCode = -1

// Start runs the REPL loop. Returns a process exit code (0 on normal exit).
func Start(cfg Config) int {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "mysql> ",
		HistoryFile: "/tmp/mysql-cli.history",
	})
	if err != nil {
		return 1
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			return 0
		}
		if runOnce(line, cfg) {
			return 0
		}
	}
}

func runOnce(line string, cfg Config) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "\\") {
		code, msg := dispatch(line, cfg)
		if msg != "" {
			fmt.Fprintln(cfg.Out, msg)
		}
		return isExit(code)
	}
	if looksLikeSQL(line) {
		runSQL(cfg, line)
	}
	return false
}

func runSQL(cfg Config, sqlText string) {
	if cfg.Pool == nil {
		fmt.Fprintln(cfg.Out, "not connected")
		return
	}
	ctx := context.Background()
	var r result.Result
	var err error
	switch safety.Classify(sqlText) {
	case safety.CategoryRead, safety.CategoryUnknown:
		// 与 CLI 子命令行为对齐：未显式指定 limit 时应用默认 cap 并开启 cap+1
		// 探测，避免 REPL 里 `SELECT * FROM huge_table` 拉全表。
		// 仅当 Opts.Limit==0 且 Probe==false（即调用方未设置）时填默认值；
		// 调用方显式传 Opts.Limit>0 或 Opts.Probe=true 时不覆盖。
		opts := cfg.Opts
		if opts.Limit == 0 && !opts.Probe && cfg.DefaultCap > 0 {
			opts.Limit = cfg.DefaultCap
			opts.Probe = true
		}
		r, err = query.Execute(ctx, cfg.Pool, sqlText, opts)
	default:
		r, err = query.ExecuteWrite(ctx, cfg.Pool, sqlText, cfg.Opts)
	}
	if err != nil {
		fmt.Fprintln(cfg.Out, err)
		return
	}
	out, ferr := format.Format(r, cfg.Format)
	if ferr != nil {
		fmt.Fprintln(cfg.Out, "format error:", ferr)
		return
	}
	fmt.Fprint(cfg.Out, out)
}

// dispatch handles \-prefixed commands. Returns (code, message).
// exitCode (-1) means "exit the loop".
func dispatch(line string, cfg Config) (int, string) {
	parts := strings.Fields(line)
	switch parts[0] {
	case "\\q", "\\quit":
		return exitCode, ""
	case "\\d", "\\tables":
		return runSlash(cfg, func(p *conn.Pool) (result.Result, error) {
			return schema.Tables(context.Background(), p, "")
		})
	case "\\schema":
		if len(parts) < 2 {
			return 0, "usage: \\schema <table>"
		}
		return runSlash(cfg, func(p *conn.Pool) (result.Result, error) {
			return schema.Schema(context.Background(), p, parts[1])
		})
	}
	return 0, "unknown command: " + parts[0]
}

func runSlash(cfg Config, fn func(*conn.Pool) (result.Result, error)) (int, string) {
	if cfg.Pool == nil {
		return 0, "not connected"
	}
	r, err := fn(cfg.Pool)
	if err != nil {
		return 0, err.Error()
	}
	out, err := format.Format(r, cfg.Format)
	if err != nil {
		return 0, err.Error()
	}
	return 0, strings.TrimSpace(out)
}

// looksLikeSQL 判断输入是否像 SQL。直接复用 safety.Classify，避免与 safety
// 的前缀表（read/dml/ddl）重复维护导致漂移。注意：WITH 开头的 CTE 语句在
// safety 中被保守归入 CategoryUnknown，因此这里也不认为它"像 SQL"，由
// runOnce 走非 SQL 分支处理。
func looksLikeSQL(s string) bool {
	return safety.Classify(s) != safety.CategoryUnknown
}

func isExit(code int) bool { return code == exitCode }
