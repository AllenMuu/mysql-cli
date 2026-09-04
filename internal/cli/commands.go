package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// defaultConfigPath 返回 --config flag 的默认值（~/.config/mysql-cli/config.toml）。
// home 解析失败时返回 config 错误而非退化成 cwd 相对路径——后者会被 loader
// 当作 Trusted 全局配置加载，形成「cwd 预置钓鱼 config」的攻击面（B6）。
// 调用方（newRootCmd）在注册 flag 时忽略该错误（用空默认值兜底），真正需要
// 解析 config 时 resolve() 会再次报错并以 exit 10 退出。
func defaultConfigPath() (string, error) {
	home, err := mustGetHome()
	if err != nil {
		return "", fmt.Errorf("%w: %w", config.ErrConfig, err)
	}
	// 用 filepath.Join 而非字符串拼接 "/"，避免在 Windows 上生成混用分隔符的路径
	// （C:\Users\foo/.config/mysql-cli/config.toml）。os.Stat 能容忍，但不规范。
	return filepath.Join(home, config.RelConfigPath), nil
}

// explicitConfigSource 返回显式指定的 config 路径（--config 优先于
// MYSQL_CLI_CONFIG，与 config.ResolvePathChain 的短路语义一致），未显式
// 指定时返回 ""。
func explicitConfigSource(g *Globals) string {
	if g.ConfigExplicit {
		return g.ConfigPath
	}
	return os.Getenv("MYSQL_CLI_CONFIG")
}

func (g *Globals) resolve() (config.Datasource, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Datasource{}, fmt.Errorf("%w: cannot determine working directory: %w", config.ErrConfig, err)
	}
	home, err := mustGetHome()
	if err != nil {
		// home 不可用时不能退化为空串：全局 config 路径会退化成 cwd 相对路径
		// 且被 loader 当 Trusted 加载（B6），trust 检查也无法定位信任文件。
		return config.Datasource{}, fmt.Errorf("%w: %w", config.ErrConfig, err)
	}
	cfgFlag := ""
	if g.ConfigExplicit {
		cfgFlag = g.ConfigPath
	}
	envCfg := os.Getenv("MYSQL_CLI_CONFIG")
	merged, entries, err := config.Load(config.LoadOpts{
		ConfigFlag: cfgFlag,
		EnvConfig:  envCfg,
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
	g.warnUntrustedExplicitConfig(explicitConfigSource(g), cwd, home)
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

// warnUntrustedExplicitConfig 在 stderr 告警（非阻断）：显式 --config /
// MYSQL_CLI_CONFIG 指向的路径是相对路径、或解析后位于 cwd / 项目根之下
// （含经 symlink 指回其中的绝对路径），且该项目未被 trust 时，该文件完全
// 绕过 trust 机制被 loader 直接加载（loader 把显式路径标记 Trusted:true）。
// 恶意 repo 可以诱导 agent 传相对 --config 加载钓鱼配置（如
// password="${MYSQL_PASSWORD}" 展开后凭据外泄）。保守方案：只告警不阻断，
// 仍加载该文件。
// 信任判定复用 config 的 trust 存储：取从 cwd 向上发现的项目根（找不到
// 时退化为 cwd 本身）。--no-trust-warn / MYSQL_CLI_NO_TRUST_WARN=1 可抑制。
func (g *Globals) warnUntrustedExplicitConfig(explicit, cwd, home string) {
	if explicit == "" || cwd == "" {
		return
	}
	if g.NoTrustWarn || os.Getenv("MYSQL_CLI_NO_TRUST_WARN") == "1" {
		return
	}
	// 相对路径按传入的 cwd 解析（生产环境二者一致；显式传参使函数可单测）。
	abs := explicit
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	// 只告警真实存在的文件：不存在的显式 config 不会被加载，无钓鱼面。
	if _, err := os.Stat(abs); err != nil {
		return
	}
	// 项目根：agent 常在项目子目录运行，--config 指向项目根下的文件时同样
	// 要告警；找不到项目根时退回 cwd（原行为）。
	root := cwd
	if r, _, found := config.DiscoverProject(cwd, home); found {
		root = r
	}
	// 触发条件：相对路径（相对 cwd 解析），或绝对路径但位于 cwd 或项目根
	// 之下。symlink 归一后再比较，防止绝对路径本身在目录外、但经 symlink
	// 指回目录内文件从而绕过告警。指向 home 等用户自有区域的绝对路径不告警。
	if filepath.IsAbs(explicit) && !underDirAny(abs, cwd, root) {
		return
	}
	if config.IsTrusted(home, root) {
		return
	}
	msg := fmt.Sprintf("mysql-cli: WARN explicit config %s is NOT covered by the trust mechanism and is being loaded anyway. "+
		"If this file comes from an untrusted repo, a human must review its contents (it may redirect credentials via ${ENV} placeholders) before use. "+
		"Do not auto-trust.", explicit)
	fmt.Fprintln(g.eout, msg)
}

// underDirAny 报告 path（symlink 归一后）是否位于任一基准目录之下。基准
// 目录也需归一：cwd / 项目根自身含 symlink 组件时（如 macOS 的
// /var -> /private/var），一侧解析一侧不解析会永远比较不相等。
func underDirAny(path string, dirs ...string) bool {
	resolved := evalSymlinksBestEffort(path)
	for _, d := range dirs {
		if isUnderDir(resolved, evalSymlinksBestEffort(d)) {
			return true
		}
	}
	return false
}

// evalSymlinksBestEffort 归一路径中的 symlink；失败（路径不存在等）时
// 返回原路径。
func evalSymlinksBestEffort(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// isUnderDir 报告 path 是否位于 dir 之下（或等于 dir）。path 与 dir 都
// 必须是绝对路径（本函数的调用方保证）。
func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
				err := fmt.Errorf("%w: %w", query.ErrMultiStatement, safety.ErrMultiStatement)
				g.emitResult(result.Empty(), err)
				return err
			}
			if _, err := safety.Check(sqlText, safety.CheckOptions{Write: g.Write, DDL: g.DDL, Yes: g.Yes}); err != nil {
				err = fmt.Errorf("%w: %w", query.ErrGuard, err)
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
			// DML/DDL use ExecuteWrite (txn-wrapped Exec -> rows affected).
			// NOTE: go-sql-driver does NOT reject write statements sent
			// through QueryContext -- the statement is actually executed
			// (autocommit) and an empty result set comes back, so
			// RowsAffected is silently dropped. Unknown statements (WITH
			// CTE / EXPLAIN ANALYZE / SET / ...) ride the read route because
			// they can return rows (e.g. EXPLAIN ANALYZE's plan output); an
			// agent running a WITH-prefixed DML under --write should prefer
			// the bare DML form (or `txn`) so rows_affected is reported.
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
			// Validate before connecting: mirror ExecuteTxn's internal checks
			// (txn requires --write, plus per-statement multi-statement and
			// guard) so readonly/multi-statement/destructive cases return
			// their correct exit codes without attempting a connection.
			if !g.Write {
				err := fmt.Errorf("%w: txn requires --write", query.ErrGuard)
				g.emitResult(result.Empty(), err)
				return err
			}
			for _, s := range args {
				if safety.HasMultiStatement(s) {
					err := fmt.Errorf("%w: %w", query.ErrMultiStatement, safety.ErrMultiStatement)
					g.emitResult(result.Empty(), err)
					return err
				}
				if _, err := safety.Check(s, safety.CheckOptions{Write: g.Write, DDL: g.DDL, Yes: g.Yes}); err != nil {
					err = fmt.Errorf("%w: %w", query.ErrGuard, err)
					g.emitResult(result.Empty(), err)
					return err
				}
			}
			pool, err := g.openPool()
			if err != nil {
				g.emitResult(result.Empty(), err)
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Schema(ctx, p, table)
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
		return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
			return schema.Sample(ctx, p, args[0], n)
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Tables(ctx, p, db)
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Databases(ctx, p)
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Read(ctx, p, args[0])
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Explore(ctx, p)
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
			return g.runSchema(func(ctx context.Context, p *conn.Pool) (result.Result, error) {
				return schema.Analyze(ctx, p, args[0])
			})
		},
	}
}

// schemaTimeoutCtx 构造 schema 命令使用的 ctx。Timeout<=0 时回退到
// context.Background()（与 query 命令行为对齐）。schema 包本身不会自己加
// 超时保护，必须在 cli 层包一层，避免在慢库上挂起。
func (g *Globals) schemaTimeoutCtx() (context.Context, context.CancelFunc) {
	to, _ := time.ParseDuration(g.Timeout)
	if to <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), to)
}

func (g *Globals) runSchema(fn func(ctx context.Context, p *conn.Pool) (result.Result, error)) error {
	pool, err := g.openPool()
	if err != nil {
		g.emitResult(result.Empty(), err)
		return err
	}
	defer pool.Close()
	ctx, cancel := g.schemaTimeoutCtx()
	defer cancel()
	r, err := fn(ctx, pool)
	g.emitResult(r, err)
	return err
}
