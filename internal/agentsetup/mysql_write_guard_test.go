package agentsetup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runHookScript writes a hook template to a temp file, feeds input JSON on
// stdin, and returns stdout. Fails the test unless the script exits 0.
func runHookScript(t *testing.T, scriptContent []byte, input string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hook-under-test.py")
	require.NoError(t, os.WriteFile(script, scriptContent, 0o755))

	cmd := exec.Command("python3", script)
	cmd.Stdin = strings.NewReader(input)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	require.NoError(t, err, "hook must always exit 0 (stderr: %s)", errOut.String())
	return out.String()
}

// jsonString is a minimal JSON string encoder for test readability.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// runClaudeHook runs the embedded Claude-compatible PreToolUse hook with the
// given Bash command and returns stdout.
func runClaudeHook(t *testing.T, command string) string {
	t.Helper()
	return runHookScript(t, hookScript, `{"tool_name":"Bash","tool_input":{"command":`+jsonString(command)+`}}`)
}

func TestClaudeHookWriteFlagForms(t *testing.T) {
	// pflag accepts --flag=value: the guard must catch --write=true, not just
	// the bare token (exact-token matching would classify it as a read).
	for _, cmd := range []string{
		`mysql-cli query "UPDATE t SET a=1" --write`,
		`mysql-cli query "UPDATE t SET a=1" --write=true`,
		`mysql-cli query "UPDATE t SET a=1" --ddl=1`,
		`mysql-cli query "UPDATE t SET a=1" --yes=TRUE`,
		`mysql-cli --write query "UPDATE t SET a=1"`,
	} {
		t.Run(cmd, func(t *testing.T) {
			out := runClaudeHook(t, cmd)
			assert.Contains(t, out, `"permissionDecision": "ask"`,
				"write flag in any pflag form must trigger ask")
		})
	}
}

func TestClaudeHookReadsPassSilently(t *testing.T) {
	for _, cmd := range []string{
		`mysql-cli query "SELECT 1"`,
		`mysql-cli query "SELECT '--write'"`, // quoted literal, not the flag
		`mysql-cli --limit=100 query "SELECT 1"`,
	} {
		t.Run(cmd, func(t *testing.T) {
			out := runClaudeHook(t, cmd)
			assert.Empty(t, strings.TrimSpace(out), "read-only calls pass silently")
		})
	}
}

func TestPiTemplateCatchesEqualsForm(t *testing.T) {
	// The Pi extension is in-process TS (not run here); at minimum lock the
	// detection shape so the --flag=value form stays covered.
	require.Contains(t, string(piExtensionScript), `startsWith(f + "=")`,
		"Pi detection must match pflag's --flag=value form")
}
