package agentsetup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCodexHook feeds input JSON to the embedded Codex hook template.
func runCodexHook(t *testing.T, input string) string {
	t.Helper()
	return runHookScript(t, codexHookScript, input)
}

// hookInput builds a PermissionRequest stdin payload for a Bash command.
func hookInput(command string) string {
	return `{"session_id":"s","tool_name":"Bash","tool_input":{"command":` + jsonString(command) + `}}`
}

const allowJSON = `{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "allow"}}}`

func TestCodexHookClassifier(t *testing.T) {
	cases := []struct {
		name    string
		command string
		allow   bool // true: must allow; false: must emit no decision
	}{
		{"read query", `mysql-cli query "SELECT 1"`, true},
		{"schema", `mysql-cli schema users`, true},
		{"read with parens in SQL", `mysql-cli query "SELECT * FROM t WHERE id IN (1,2)"`, true},
		{"quoted flag literal", `mysql-cli query "SELECT '--write'"`, true},
		{"quoted glob literal in SQL", `mysql-cli query "SELECT * FROM t"`, true},
		{"read with value flag", `mysql-cli --limit=100 query "SELECT 1"`, true},
		{"DML with write", `mysql-cli query "UPDATE user SET name='a' WHERE id=1" --write`, false},
		{"pflag equals form write", `mysql-cli query "UPDATE t SET a=1" --write=true`, false},
		{"pflag equals form yes", `mysql-cli query "UPDATE t SET a=1" --yes=1`, false},
		{"DDL with write ddl", `mysql-cli query "CREATE TABLE x (id int)" --write --ddl`, false},
		{"destructive with yes", `mysql-cli query "UPDATE user SET a=1" --write --yes`, false},
		{"flag before subcommand", `mysql-cli --write query "UPDATE t SET a=1"`, false},
		{"txn always write path", `mysql-cli txn "UPDATE t SET a=1" --write`, false},
		{"rtk wrapper read", `rtk mysql-cli query "SELECT 1"`, true},
		{"rtk proxy wrapper read", `rtk proxy mysql-cli query "SELECT 1"`, true},
		{"sudo wrapper read", `sudo mysql-cli query "SELECT 1"`, true},
		{"spaced short value flag before subcommand", `mysql-cli -d mydb query "SELECT 1"`, true},
		{"spaced long value flag before subcommand", `mysql-cli --format json query "SELECT 1"`, true},
		{"version subcommand read", `mysql-cli version`, true},
		{"agent init not auto-allowed", `mysql-cli agent init codex`, false},
		{"agent init with scope flag", `mysql-cli agent init codex --project`, false},
		{"config trust not auto-allowed", `mysql-cli config trust`, false},
		{"config show not auto-allowed", `mysql-cli config show`, false},
		{"txn without write flag not auto-allowed", `mysql-cli txn "UPDATE t SET a=1"`, false},
		{"bare mysql-cli REPL not auto-allowed", `mysql-cli`, false},
		{"unknown subcommand not auto-allowed", `mysql-cli shell "SELECT 1"`, false},
		{"spaced value flag before agent subcommand", `mysql-cli -d mydb agent init codex`, false},
		{"unrelated command", `git status`, false},
		{"pipe compound", `mysql-cli schema users | grep name`, false},
		{"semicolon compound", `mysql-cli query "SELECT 1"; rm -rf /tmp/x`, false},
		{"newline compound", "mysql-cli query \"SELECT 1\"\nrm -rf /tmp/x", false},
		{"carriage return compound", "mysql-cli query \"SELECT 1\"\rrm -rf /tmp/x", false},
		{"and compound with write", `mysql-cli query "SELECT 1" && mysql-cli query "UPDATE t SET a=1" --write`, false},
		{"command substitution", `mysql-cli query "$(cat q.sql)"`, false},
		{"backtick substitution", "mysql-cli query \"`cat q.sql`\"", false},
		{"unquoted backtick substitution", "mysql-cli query `cat q.sql`", false},
		{"brace expansion injects write flag", `mysql-cli query "UPDATE t SET a=1" {--write,}`, false},
		{"brace expansion alternatives", `mysql-cli query "SELECT 1" {--write,--ddl}`, false},
		{"glob argument injection", `mysql-cli query "UPDATE t SET a=1" *`, false},
		{"glob question mark", `mysql-cli query "SELECT 1" ?.sql`, false},
		{"tilde path expansion", `~/bin/mysql-cli query "SELECT 1"`, false},
		{"attached stderr redirect", `mysql-cli query "SELECT 1" 2>/dev/null`, false},
		{"standalone redirect", `mysql-cli schema users > schema.json`, false},
		{"env with var cannot prove", `env MYSQL_HOST=x mysql-cli query "SELECT 1"`, false},
		{"unbalanced quotes", `mysql-cli query "SELECT 1`, false},
		{"empty command", ``, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runCodexHook(t, hookInput(tc.command))
			if tc.allow {
				assert.JSONEq(t, allowJSON, out, "read-only call must be auto-allowed")
			} else {
				assert.Empty(t, strings.TrimSpace(out), "must emit no decision (empty stdout)")
			}
		})
	}
}

func TestCodexHookIgnoresNonBashTools(t *testing.T) {
	out := runCodexHook(t, `{"tool_name":"apply_patch","tool_input":{"command":"mysql-cli query \"SELECT 1\""}}`)
	assert.Empty(t, strings.TrimSpace(out), "non-Bash tools are not our business")
}

func TestCodexHookMalformedInput(t *testing.T) {
	cases := []string{
		``,
		`not json at all`,
		`{"tool_name":"Bash"}`,                 // missing tool_input
		`{"tool_input":{"command":"ls"}}`,      // missing tool_name
		`{"tool_name":"Bash","tool_input":{}}`, // empty command
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			out := runCodexHook(t, in)
			assert.Empty(t, strings.TrimSpace(out), "malformed input must never allow")
		})
	}
}

func TestCodexHookNeverEmitsAsk(t *testing.T) {
	// Regression guard: Codex parses permissionDecision=ask but treats it as
	// a failed hook and CONTINUES the tool call -- absolutely forbidden here.
	require.NotContains(t, string(codexHookScript), `permissionDecision`,
		"the Codex hook must not use Claude's permissionDecision protocol")
	require.Contains(t, string(codexHookScript), `"behavior": "allow"`,
		"the only decision ever emitted is allow")
}
