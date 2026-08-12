// Package cli wires cobra subcommands, global flags, and exit-code mapping.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
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
		if strings.HasPrefix(err.Error(), "repl exited") {
			return ExitOK
		}
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
			// limit 校验：负数拒绝（exit 10）。0 和未设置都表示"用默认 cap"，
			// 不拒绝。超大 limit（>1_000_000）给 stderr 警告但不拒绝，用户可能真需要。
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
	pf.StringVar(&g.ConfigPath, "config", defaultConfigPath(), "config file path")
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
		pool, err := g.openPool()
		if err != nil {
			g.emitResult(result.Empty(), err)
			return err
		}
		defer pool.Close()
		code := repl.Start(repl.Config{
		Pool: pool, Opts: g.opts(), Out: g.out, Format: g.Format,
		DefaultCap: g.defaultCap(),
	})
		if code == 0 {
			return nil
		}
		return fmt.Errorf("repl exited with code %d", code)
	}
	applyHelpGrouping(root)
	return root
}
