// Package cli wires cobra subcommands, global flags, and exit-code mapping.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	ExitInitFailed             = 11
)

// Globals carries parsed global flags shared by all subcommands.
type Globals struct {
	Datasource string
	Format     string
	Write      bool
	DDL        bool
	Yes        bool
	Limit      int
	Timeout    string
	ConfigPath string
	Host       string
	Port       int
	User       string
	Password   string
	Database   string
	out        io.Writer
}

// Run parses args and executes; returns the process exit code.
func Run(args []string) int {
	g := &Globals{Format: "json", out: os.Stdout}
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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if g.Format != "json" && g.Format != "table" && g.Format != "csv" && g.Format != "tsv" {
				return fmt.Errorf("invalid format %q (want json|table|csv|tsv)", g.Format)
			}
			if _, err := time.ParseDuration(g.Timeout); err != nil {
				return fmt.Errorf("invalid timeout %q: %w", g.Timeout, err)
			}
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&g.Datasource, "datasource", "d", "", "named datasource from config")
	pf.StringVarP(&g.Format, "format", "f", "json", "output format: json|table|csv|tsv")
	pf.BoolVar(&g.Write, "write", false, "allow DML (INSERT/UPDATE/DELETE)")
	pf.BoolVar(&g.DDL, "ddl", false, "allow DDL (requires --write)")
	pf.BoolVar(&g.Yes, "yes", false, "confirm destructive operations")
	pf.IntVar(&g.Limit, "limit", 0, "row limit for SELECT queries")
	pf.StringVar(&g.Timeout, "timeout", "30s", "query timeout")
	pf.StringVar(&g.ConfigPath, "config", defaultConfigPath(), "config file path")
	pf.StringVar(&g.Host, "host", "", "MySQL host")
	pf.IntVar(&g.Port, "port", 0, "MySQL port")
	pf.StringVar(&g.User, "user", "", "MySQL user")
	pf.StringVar(&g.Password, "password", "", "MySQL password")
	pf.StringVar(&g.Database, "db", "", "MySQL database")

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
		newSkillCmd(),
		newInitCmd(),
	)
	// No subcommand -> interactive REPL (human debug; not the agent path).
	root.RunE = func(cmd *cobra.Command, args []string) error {
		pool, err := g.openPool()
		if err != nil {
			g.emitResult(result.Empty(), err)
			return err
		}
		defer pool.Close()
		code := repl.Start(repl.Config{Pool: pool, Opts: g.opts(), Out: g.out, Format: g.Format})
		if code == 0 {
			return nil
		}
		return fmt.Errorf("repl exited with code %d", code)
	}
	return root
}
