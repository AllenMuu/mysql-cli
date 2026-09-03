// Package cli wires cobra subcommands, global flags, and exit-code mapping.
package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/AllenMuu/mysql-cli/internal/format"
	"github.com/AllenMuu/mysql-cli/internal/repl"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/spf13/cobra"
)

// Exit codes (see plan Global Constraints).
const (
	ExitOK                     = 0
	ExitConnFailed             = 2
	ExitReadonlyViolation      = 3
	ExitDDLRequiresWrite       = 4
	ExitDestructiveRequiresYes = 5
	ExitIdentifierInvalid      = 6
	ExitMultiStatement         = 7
	ExitSQLError               = 8
	ExitQueryTimeout           = 9
	ExitConfigError            = 10
	// ExitInternalError 用于恢复的 panic：保证 agent 始终拿到 JSON 信封
	// 而非 Go 堆栈输出。
	ExitInternalError = 11
)

// Globals carries parsed global flags shared by all subcommands.
type Globals struct {
	Datasource     string
	Format         string
	Write          bool
	DDL            bool
	Yes            bool
	Limit          int
	NoLimit        bool
	DefaultLimit   int
	Timeout        string
	ConfigPath     string
	ConfigExplicit bool // true when --config was explicitly set on the command line
	Host           string
	Port           int
	User           string
	Password       string
	Database       string
	NoTrustWarn    bool
	StrictTrust    bool
	out            io.Writer
	eout           io.Writer
}

// Run parses args and executes; returns the process exit code.
//
// recover 兜底：任一子命令 panic 时，进程不输出 Go 堆栈，而是向 stderr
// 写一条与现有 format 错误信封同结构的 JSON，并返回 ExitInternalError。
// 这是给 AI agent 的契约保证——它始终拿到 JSON 而非崩溃输出。
func Run(args []string) (code int) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// 尽量复用现有错误信封格式，与 format.ErrorJSON 一致。
		msg := fmt.Sprintf("panic: %v", r)
		fmt.Fprintln(os.Stderr, format.ErrorJSON("INTERNAL_ERROR", msg))
		code = ExitInternalError
	}()

	g := &Globals{Format: "json", out: os.Stdout, eout: os.Stderr}
	root := newRootCmd(g)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return mapError(err)
	}
	return ExitOK
}

func newRootCmd(g *Globals) *cobra.Command {
	root := &cobra.Command{
		Use:           "mysql-cli",
		Short:         "MySQL CLI for AI agents",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			g.ConfigExplicit = cmd.Flags().Changed("config")
			if g.Format != "json" && g.Format != "table" && g.Format != "csv" && g.Format != "tsv" && g.Format != "jsonl" {
				return fmt.Errorf("invalid format %q (want json|table|csv|tsv|jsonl)", g.Format)
			}
			if _, err := time.ParseDuration(g.Timeout); err != nil {
				return fmt.Errorf("invalid timeout %q: %w", g.Timeout, err)
			}
			// limit 校验：负数拒绝（exit 10）。未设置 --limit 时用默认 cap；
			// 显式 --limit 0 表示无限制（等同 --no-limit，见 resolveCap），均不
			// 拒绝。超大 limit（>1_000_000）给 stderr 警告但不拒绝，用户可能真需要。
			if g.Limit < 0 {
				return fmt.Errorf("%w: limit must be >= 0", config.ErrConfig)
			}
			if g.Limit > 1_000_000 {
				fmt.Fprintf(g.eout, "mysql-cli: WARN large limit (%d) may cause memory pressure\n", g.Limit)
			}
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&g.Datasource, "datasource", "d", "", "named datasource from config")
	pf.StringVarP(&g.Format, "format", "f", "json", "output format: json|table|csv|tsv|jsonl")
	pf.BoolVar(&g.Write, "write", false, "allow DML (INSERT/UPDATE/DELETE)")
	pf.BoolVar(&g.DDL, "ddl", false, "allow DDL (requires --write)")
	pf.BoolVar(&g.Yes, "yes", false, "confirm destructive operations")
	pf.IntVar(&g.Limit, "limit", 0, "row limit for SELECT queries")
	pf.BoolVar(&g.NoLimit, "no-limit", false, "disable default row cap for SELECT (returns full result set)")
	pf.StringVar(&g.Timeout, "timeout", "30s", "query timeout")
	// home 解析失败时 defaultConfigPath 返回错误（不退化为 cwd 相对路径，
	// 见 B6）。此处只注册 flag 默认值，用空串兜底；真正解析 config 时
	// resolve() 会返回同样的 config 错误并以 exit 10 退出。
	defConfig, cfgErr := defaultConfigPath()
	if cfgErr != nil {
		defConfig = ""
	}
	pf.StringVar(&g.ConfigPath, "config", defConfig, "config file path")
	pf.StringVar(&g.Host, "host", "", "MySQL host")
	pf.IntVar(&g.Port, "port", 0, "MySQL port")
	pf.StringVar(&g.User, "user", "", "MySQL user")
	pf.StringVar(&g.Password, "password", "", "MySQL password")
	pf.StringVar(&g.Database, "db", "", "MySQL database")
	pf.BoolVar(&g.NoTrustWarn, "no-trust-warn", false, "suppress the untrusted-project-config warning")
	pf.BoolVar(&g.StrictTrust, "strict-trust", false, "error out (instead of warn) when an untrusted project config is present")

	root.SetOut(g.out)
	root.AddCommand(
		newQueryCmd(g),
		newTxnCmd(g),
		newSchemaCmd(g),
		newSampleCmd(g),
		newTablesCmd(g),
		newDatabasesCmd(g),
		newReadCmd(g),
		newExploreCmd(g),
		newAnalyzeCmd(g),
		newConfigCmd(g),
		newAgentCmd(g),
		newVersionCmd(),
	)
	// No subcommand -> interactive REPL (human debug; not the agent path).
	root.RunE = func(cmd *cobra.Command, args []string) error {
		// 无子命令且 stdin 非 TTY（典型 agent 误用：管道/空输入）时直接报
		// 用法错误退出非零。否则 readline 会立即 EOF -> 静默 exit 0，agent
		// 无法区分成功与误用（B5）。
		if !stdinIsTerminal() {
			err := fmt.Errorf("%w: no subcommand given and stdin is not a terminal; run `mysql-cli --help` for usage", config.ErrConfig)
			g.emitResult(result.Empty(), err)
			return err
		}
		pool, err := g.openPool()
		if err != nil {
			g.emitResult(result.Empty(), err)
			return err
		}
		defer pool.Close()
		code, rerr := repl.Start(repl.Config{
			Pool:       pool,
			Opts:       g.opts(),
			Out:        g.out,
			Format:     g.Format,
			DefaultCap: g.defaultCap(),
		})
		if rerr != nil {
			// readline 初始化失败：向 stderr 输出原因；错误链携带
			// repl.ErrInitFailed 哨兵，mapError 将其映射为 exit 11。
			fmt.Fprintln(g.eout, formatErr(rerr, g.Format))
			return rerr
		}
		if code != 0 {
			return fmt.Errorf("repl exited with code %d", code)
		}
		return nil
	}
	applyHelpGrouping(root)
	return root
}
