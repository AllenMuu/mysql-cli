package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/AllenMuu/mysql-cli/internal/conn"
	"github.com/AllenMuu/mysql-cli/internal/format"
	"github.com/AllenMuu/mysql-cli/internal/query"
	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/AllenMuu/mysql-cli/internal/safety"
	"github.com/AllenMuu/mysql-cli/internal/schema"
	"github.com/spf13/cobra"
)

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return home + "/.config/mysql-cli/config.toml"
}

func (g *Globals) resolve() (config.Datasource, error) {
	cwd, _ := os.Getwd()
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cfgFlag := ""
	if g.ConfigExplicit {
		cfgFlag = g.ConfigPath
	}
	merged, entries, err := config.Load(config.LoadOpts{
		ConfigFlag: cfgFlag,
		EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
		Cwd:        cwd,
		Home:       home,
		IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
	})
	if err != nil {
		return config.Datasource{}, err
	}
	if merged != nil {
		g.DefaultLimit = merged.DefaultLimit
	}
	if err := g.warnUntrustedProject(entries, merged); err != nil {
		return config.Datasource{}, err
	}
	over := config.Datasource{
		Host: g.Host, Port: g.Port, User: g.User, Password: g.Password, Database: g.Database,
	}
	return config.Resolve(merged, g.Datasource, over)
}

func (g *Globals) openPool() (*conn.Pool, error) {
	ds, err := g.resolve()
	if err != nil {
		return nil, err
	}
	return conn.Open(context.Background(), ds)
}

// warnUntrustedProject warns on stderr (or errors under --strict-trust) when a
// project-level config exists but is untrusted, so the CLI silently fell back
// to the global config. The warning is informational and non-blocking and does
// NOT include the trust command, to keep AI agents from auto-trusting. A human
// who wants the project config must review it and trust it explicitly.
// Suppressed by --no-trust-warn or MYSQL_CLI_NO_TRUST_WARN=1.
func (g *Globals) warnUntrustedProject(entries []config.PathEntry, merged *config.Config) error {
	var untrusted string
	for _, e := range entries {
		if e.Kind == "project" && e.Exists && !e.Trusted {
			untrusted = e.Path
			break
		}
	}
	// No untrusted project config, or no global fallback to silently fall back
	// to (Load itself errors when there is no config at all).
	if untrusted == "" || merged == nil {
		return nil
	}
	if g.NoTrustWarn || os.Getenv("MYSQL_CLI_NO_TRUST_WARN") == "1" {
		return nil
	}
	msg := fmt.Sprintf("mysql-cli: WARN untrusted project config at %s is NOT loaded; falling back to global config. "+
		"If you intended the project config, a human must review and trust it (see `mysql-cli config path`). "+
		"Do not auto-trust.", untrusted)
	if g.StrictTrust {
		return errors.New(msg)
	}
	fmt.Fprintln(g.eout, msg)
	return nil
}

func (g *Globals) opts() query.Options {
	to, _ := time.ParseDuration(g.Timeout)
	return query.Options{Write: g.Write, DDL: g.DDL, Yes: g.Yes, Limit: g.Limit, Timeout: to}
}

// defaultCap resolves the default row cap: config > env > built-in 1000.
func (g *Globals) defaultCap() int {
	if g.DefaultLimit > 0 {
		return g.DefaultLimit
	}
	if v, err := strconv.Atoi(os.Getenv("MYSQL_CLI_DEFAULT_LIMIT")); err == nil && v > 0 {
		return v
	}
	return 1000
}

// resolveCap decides (limit, probe) for a read query:
//
//	--limit explicit -> (g.Limit, false)   exact N, no probe
//	--no-limit       -> (0, false)          no cap
//	otherwise        -> (defaultCap, true)  default cap with cap+1 probe
func (g *Globals) resolveCap(cmd *cobra.Command) (int, bool) {
	if cmd.Flags().Changed("limit") {
		return g.Limit, false
	}
	if g.NoLimit {
		return 0, false
	}
	return g.defaultCap(), true
}

// emitReadResult renders a read query result: json -> ReadJSON (slim envelope),
// jsonl -> line stream + stderr truncated notice, else -> Format.
func (g *Globals) emitReadResult(r result.Result, err error, limit int) {
	if err != nil {
		fmt.Fprintln(g.out, formatErr(err, g.Format))
		return
	}
	switch g.Format {
	case "json":
		fmt.Fprint(g.out, format.ReadJSON(r, limit))
	case "jsonl":
		out, _ := format.Format(r, "jsonl")
		fmt.Fprint(g.out, out)
		if r.Truncated {
			fmt.Fprintf(g.eout, "# truncated:true limit:%d\n", limit)
		}
	default:
		out, _ := format.Format(r, g.Format)
		fmt.Fprint(g.out, out)
	}
}

func (g *Globals) emitResult(r result.Result, err error) {
	if err != nil {
		fmt.Fprintln(g.out, formatErr(err, g.Format))
		return
	}
	out, _ := format.Format(r, g.Format)
	fmt.Fprint(g.out, out)
}

func newQueryCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "query <sql>",
		Short: "Run a SQL statement (read by default; --write for DML)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sqlText := args[0]
			// Validate before connecting: multi-statement and guard checks
			// need no database, so their exit codes are returned without
			// attempting (and failing on) a connection.
			if safety.HasMultiStatement(sqlText) {
				err := fmt.Errorf("%w: %v", query.ErrMultiStatement, safety.ErrMultiStatement)
				g.emitResult(result.Empty(), err)
				return err
			}
			if _, err := safety.Check(sqlText, safety.CheckOptions{Write: g.Write, DDL: g.DDL, Yes: g.Yes}); err != nil {
				err = fmt.Errorf("%w: %v", query.ErrGuard, err)
				g.emitResult(result.Empty(), err)
				return err
			}
			pool, err := g.openPool()
			if err != nil {
				g.emitResult(result.Empty(), err)
				return err
			}
			defer pool.Close()
			ctx := context.Background()
			var r result.Result
			// Route by classification: read queries use Execute (rows),
			// DML/DDL use ExecuteWrite (rows affected). This avoids running
			// writes through QueryContext, which the driver rejects.
			switch safety.Classify(sqlText) {
			case safety.CategoryRead, safety.CategoryUnknown:
				opts := g.opts()
				opts.Limit, opts.Probe = g.resolveCap(cmd)
				r, err = query.Execute(ctx, pool, sqlText, opts)
				g.emitReadResult(r, err, opts.Limit)
			default:
				r, err = query.ExecuteWrite(ctx, pool, sqlText, g.opts())
				g.emitResult(r, err)
			}
			return err
		},
	}
}

func newTxnCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "txn <sql1> [sql2...]",
		Short: "Run multiple statements in one atomic transaction",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pool, err := g.openPool()
			if err != nil {
				return err
			}
			defer pool.Close()
			r, err := query.ExecuteTxn(context.Background(), pool, args, g.opts())
			g.emitResult(r, err)
			return err
		},
	}
}

func newSchemaCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "schema [table]",
		Short: "Show table structure, or whole database if no table given",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			table := ""
			if len(args) == 1 {
				table = args[0]
			}
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Schema(context.Background(), p, table)
			})
		},
	}
}

func newSampleCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "sample <table>",
		Short: "Sample rows from a table (-n, max 20)",
		Args:  cobra.ExactArgs(1),
	}
	c.Flags().IntP("n", "n", 5, "sample row count (max 20)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		n, _ := cmd.Flags().GetInt("n")
		return g.runSchema(func(p *conn.Pool) (result.Result, error) {
			return schema.Sample(context.Background(), p, args[0], n)
		})
	}
	return c
}

func newTablesCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "tables [db]",
		Short: "List tables in a database",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db := ""
			if len(args) == 1 {
				db = args[0]
			}
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Tables(context.Background(), p, db)
			})
		},
	}
}

func newDatabasesCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "databases",
		Short: "List databases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Databases(context.Background(), p)
			})
		},
	}
}

func newReadCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "read <table>",
		Short: "First 100 rows of a table",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Read(context.Background(), p, args[0])
			})
		},
	}
}

func newExploreCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "explore",
		Short: "Database and table overview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Explore(context.Background(), p)
			})
		},
	}
}

func newAnalyzeCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "analyze <table>",
		Short: "Schema + sample in one shot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Analyze(context.Background(), p, args[0])
			})
		},
	}
}

func (g *Globals) runSchema(fn func(*conn.Pool) (result.Result, error)) error {
	pool, err := g.openPool()
	if err != nil {
		g.emitResult(result.Empty(), err)
		return err
	}
	defer pool.Close()
	r, err := fn(pool)
	g.emitResult(r, err)
	return err
}
