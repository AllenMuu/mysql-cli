package cli

import (
	"bytes"
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
	code := Run([]string{"txn", "SELECT 1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code)
}

func TestInvalidFormatErrors(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--format", "xml"})
	assert.Equal(t, ExitConfigError, code)
}

func TestInvalidTimeoutErrors(t *testing.T) {
	code := Run([]string{"query", "SELECT 1", "--timeout", "abc"})
	assert.Equal(t, ExitConfigError, code)
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
