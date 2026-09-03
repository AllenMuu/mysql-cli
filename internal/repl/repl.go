// Package repl provides a minimal interactive shell for human debugging.
// It is NOT the primary agent path; agents call subcommands directly.
package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AllenMuu/mysql-cli/internal/config"
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

// ErrInitFailed 表示 readline 初始化失败（如终端不可用）。cli 层用
// errors.Is 识别该哨兵并返回非零退出码（exit 11），而非把初始化失败
// 伪装成正常退出（exit 0）。
var ErrInitFailed = errors.New("repl: initialization failed")

// newReadline 是 readline 构造器的注入点，测试可替换以模拟初始化失败。
var newReadline = func(cfg *readline.Config) (*readline.Instance, error) {
	return readline.NewEx(cfg)
}

// historyPath 返回 REPL 历史文件路径（~/.config/mysql-cli/history），并确保
// 目录以 0700 创建、文件以 0600 创建。历史记录可能包含敏感 SQL，不能写在
// 多用户可读的 /tmp（symlink 预置、多实例互踩、信息泄露）。home 不可用或
// 文件系统失败时返回 ""：降级为不落盘历史，REPL 照常运行。
func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := filepath.Join(home, filepath.Dir(config.RelConfigPath))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	p := filepath.Join(dir, "history")
	if _, err := os.Stat(p); err == nil {
		_ = os.Chmod(p, 0o600) // 收紧历史遗留的宽权限（best-effort）
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(p, []byte{}, 0o600); err != nil {
			return ""
		}
	} else {
		return "" // 无法确认文件状态（权限等），放弃落盘历史
	}
	return p
}

// Start runs the REPL loop. Returns (exitCode, err)：err 非 nil 仅当
// readline 初始化失败（错误链携带 ErrInitFailed 与原因）；正常退出
// （EOF、\q）返回 (0, nil)。
func Start(cfg Config) (int, error) {
	rl, err := newReadline(&readline.Config{
		Prompt:      "mysql> ",
		HistoryFile: historyPath(),
	})
	if err != nil {
		return 1, fmt.Errorf("%w: %w", ErrInitFailed, err)
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == io.EOF {
			return 0, nil
		}
		if err != nil {
			return 0, nil
		}
		if runOnce(line, cfg) {
			return 0, nil
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
	// 非 \ 命令的输入一律交给 runSQL，与 CLI query 子命令的分流对齐：
	// unknown 类语句（WITH CTE / SET / USE 等）走 Execute，由 safety 闸门
	// 或服务端给出明确错误，不再被静默丢弃。
	runSQL(cfg, line)
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

func isExit(code int) bool { return code == exitCode }
