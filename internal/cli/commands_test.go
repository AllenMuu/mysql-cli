package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/result"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestReadonlyViolationExitCode(t *testing.T) {
	code := Run([]string{"query", "UPDATE t SET a=1 WHERE id=1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitReadonlyViolation, code)
}

func TestMultiStatementExitCode(t *testing.T) {
	code := Run([]string{"query", "SELECT 1; SELECT 2", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitMultiStatement, code)
}

func TestConfigErrorExitCode(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "-d", "nonexistent", "--config", "/no/such/file.toml"})
	assert.Equal(t, ExitConfigError, code)
}

func TestUnknownCommandPrintsHelp(t *testing.T) {
	code := Run([]string{"--help"})
	assert.Equal(t, 0, code)
}

func TestQueryConnectionFailure(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code)
}

func TestTxnConnectionFailure(t *testing.T) {
	// txn 现在在连接前做 readonly/multi-statement 预检（对齐 query 子命令）。
	// 要测"连接失败"路径，必须传 --write 让预检放行，才会真正尝试 openPool。
	code := Run([]string{"txn", "SELECT 1", "--write", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code)
}

// TestTxnReadonlyPrecheck 验证 P1-#10：无 --write 的 txn 在连接前预检阶段即返回 readonly，
// 不消耗连接（对齐 query 子命令的预检行为）。
func TestTxnReadonlyPrecheck(t *testing.T) {
	code := Run([]string{"txn", "SELECT 1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitReadonlyViolation, code)
}

func TestInvalidFormatErrors(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--format", "xml"})
	assert.Equal(t, ExitConfigError, code)
}

func TestInvalidTimeoutErrors(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--timeout", "abc"})
	assert.Equal(t, ExitConfigError, code)
}

// TestNegativeLimitErrors（任务 3）：--limit 负数在 PersistentPreRunE 即被拒，
// 返回 exit 10（ExitConfigError），不进入查询路径。
func TestNegativeLimitErrors(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--limit", "-5"})
	assert.Equal(t, ExitConfigError, code)
}

// TestLargeLimitWarnsButRuns：超大 limit（>1_000_000）给 stderr 警告但不拒绝，
// 仍继续执行（这里因连接失败落到 exit 2，证明校验放行）。
func TestLargeLimitWarnsButRuns(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--limit", "2000000", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code)
}

func TestSchemaCommandsFailOnConnection(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"sample", []string{"sample", "t", "--host", "127.0.0.1", "--port", "1"}},
		{"tables", []string{"tables", "--host", "127.0.0.1", "--port", "1"}},
		{"databases", []string{"databases", "--host", "127.0.0.1", "--port", "1"}},
		{"read", []string{"read", "t", "--host", "127.0.0.1", "--port", "1"}},
		{"explore", []string{"explore", "--host", "127.0.0.1", "--port", "1"}},
		{"analyze", []string{"analyze", "t", "--host", "127.0.0.1", "--port", "1"}},
		{"schema", []string{"schema", "t", "--host", "127.0.0.1", "--port", "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := Run(tc.args)
			assert.Equal(t, ExitConnFailed, code)
		})
	}
}

func TestDefaultCapFallbackTo1000(t *testing.T) {
	g := &Globals{}
	assert.Equal(t, 1000, g.defaultCap())
}

func TestDefaultCapFromConfig(t *testing.T) {
	g := &Globals{DefaultLimit: 500}
	assert.Equal(t, 500, g.defaultCap())
}

func TestDefaultCapFromEnv(t *testing.T) {
	t.Setenv("MYSQL_CLI_DEFAULT_LIMIT", "200")
	g := &Globals{}
	assert.Equal(t, 200, g.defaultCap())
}

func TestResolveCapDefaultProbe(t *testing.T) {
	g := &Globals{DefaultLimit: 500}
	cmd := &cobra.Command{}
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 500, limit)
	assert.True(t, probe)
}

func TestResolveCapNoLimitFlag(t *testing.T) {
	g := &Globals{NoLimit: true}
	cmd := &cobra.Command{}
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 0, limit)
	assert.False(t, probe)
}

func TestResolveCapExplicitLimit(t *testing.T) {
	g := &Globals{Limit: 50}
	cmd := &cobra.Command{}
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Set("limit", "50")
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 50, limit)
	assert.False(t, probe)
}

func TestResolveCapLimitWinsOverNoLimit(t *testing.T) {
	g := &Globals{NoLimit: true, Limit: 50}
	cmd := &cobra.Command{}
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Set("limit", "50")
	limit, probe := g.resolveCap(cmd)
	assert.Equal(t, 50, limit)
	assert.False(t, probe)
}

func TestEmitReadJSONOmitsRowsAffected(t *testing.T) {
	var out bytes.Buffer
	g := &Globals{Format: "json", out: &out}
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	g.emitReadResult(r, nil, 1000)
	assert.Contains(t, out.String(), `"truncated":true`)
	assert.NotContains(t, out.String(), "rows_affected")
}

func TestEmitReadJSONLTruncatedStderr(t *testing.T) {
	var out, eout bytes.Buffer
	g := &Globals{Format: "jsonl", out: &out, eout: &eout}
	r := result.Result{Columns: []string{"id"}, Rows: [][]any{{1}}, Truncated: true}
	g.emitReadResult(r, nil, 1000)
	assert.Contains(t, out.String(), `{"id":1}`)
	assert.Contains(t, eout.String(), "# truncated:true limit:1000")
}

// Behavioral compat: no project + no env + no explicit --config behaves as today.
func TestResolveCompatNoConfig(t *testing.T) {
	// HOME isolated -> no global config -> env/default fallback.
	t.Setenv("HOME", t.TempDir())
	code := Run([]string{"query", "SELECT 1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code) // reached connection stage (config ok, conn fails)
}

// --config single-file still works and is the only source.
func TestResolveCompatExplicitConfigFlag(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.toml")
	os.WriteFile(cfg, []byte(`[datasource.x]
host = "h"
`), 0o600)
	t.Setenv("HOME", t.TempDir())
	code := Run([]string{"query", "SELECT 1", "-d", "nonexistent", "--config", cfg})
	assert.Equal(t, ExitConfigError, code) // unknown datasource -> config error (file loaded, name missing)
}
