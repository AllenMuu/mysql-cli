package cli

import (
	"context"
	"fmt"
	"os"
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
	var cfg *config.Config
	if _, err := os.Stat(g.ConfigPath); err == nil {
		cfg, err = config.LoadFile(g.ConfigPath)
		if err != nil {
			return config.Datasource{}, err
		}
	}
	over := config.Datasource{
		Host: g.Host, Port: g.Port, User: g.User, Password: g.Password, Database: g.Database,
	}
	ds, err := config.Resolve(cfg, g.Datasource, over)
	if err != nil {
		return config.Datasource{}, err
	}
	return ds, nil
}

func (g *Globals) openPool() (*conn.Pool, error) {
	ds, err := g.resolve()
	if err != nil {
		return nil, err
	}
	return conn.Open(context.Background(), ds)
}

func (g *Globals) opts() query.Options {
	to, _ := time.ParseDuration(g.Timeout)
	return query.Options{Write: g.Write, DDL: g.DDL, Yes: g.Yes, Limit: g.Limit, Timeout: to}
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
		Use:  "query <sql>",
		Args: cobra.ExactArgs(1),
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
				r, err = query.Execute(ctx, pool, sqlText, g.opts())
			default:
				r, err = query.ExecuteWrite(ctx, pool, sqlText, g.opts())
			}
			g.emitResult(r, err)
			return err
		},
	}
}

func newTxnCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:  "txn <sql1> [sql2...]",
		Args: cobra.MinimumNArgs(1),
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
		Use:  "schema [table]",
		Args: cobra.MaximumNArgs(1),
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
		Use:  "sample <table>",
		Args: cobra.ExactArgs(1),
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
		Use:  "tables [db]",
		Args: cobra.MaximumNArgs(1),
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
		Use:  "databases",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Databases(context.Background(), p)
			})
		},
	}
}

func newReadCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:  "read <table>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Read(context.Background(), p, args[0])
			})
		},
	}
}

func newExploreCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:  "explore",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runSchema(func(p *conn.Pool) (result.Result, error) {
				return schema.Explore(context.Background(), p)
			})
		},
	}
}

func newAnalyzeCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:  "analyze <table>",
		Args: cobra.ExactArgs(1),
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
