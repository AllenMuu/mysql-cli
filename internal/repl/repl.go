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
	"github.com/AllenMuu/mysql-cli/internal/schema"
	"github.com/chzyer/readline"
)

// Config carries everything the REPL needs without importing cli.
type Config struct {
	Pool   *conn.Pool
	Opts   query.Options
	Out    io.Writer
	Format string
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
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\\") {
			code, msg := dispatch(line, cfg)
			if msg != "" {
				fmt.Fprintln(cfg.Out, msg)
			}
			if isExit(code) {
				return 0
			}
			continue
		}
		if looksLikeSQL(line) {
			runSQL(cfg, line)
			continue
		}
		fmt.Fprintln(cfg.Out, "unknown input")
	}
}

func runSQL(cfg Config, sqlText string) {
	r, err := query.Execute(context.Background(), cfg.Pool, sqlText, cfg.Opts)
	if err != nil {
		fmt.Fprintln(cfg.Out, err)
		return
	}
	out, err := format.Format(r, cfg.Format)
	if err != nil {
		fmt.Fprintln(cfg.Out, err)
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

func looksLikeSQL(s string) bool {
	w := strings.ToUpper(strings.TrimLeft(s, " \t"))
	prefixes := []string{"SELECT", "SHOW", "DESC", "EXPLAIN", "WITH", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP"}
	for _, p := range prefixes {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

func isExit(code int) bool { return code == exitCode }
