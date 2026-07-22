package cli

import (
	"testing"

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
