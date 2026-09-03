package agentsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardCase is one detection-matrix case. The SAME table drives both the
// Python hook (mysql-write-guard.py, via stdin JSON + python3) and the Pi TS
// extension (pi-mysql-write-guard.ts, via node --experimental-strip-types),
// so the two implementations can never silently drift apart. Fields must
// stay exported: the table is JSON-marshaled into the TS driver.
type guardCase struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	WantHit bool   `json:"wantHit"` // whether the guard should flag this command
}

// guardCases is the shared detection matrix. It must cover at minimum:
// read-only passthrough, each write flag (bare and =value forms), wrapped
// bash -c invocations, the known false-positive shapes (echo/printf, SQL
// string literals, comments), and shell chaining.
var guardCases = []guardCase{
	// --- read-only traffic passes through ---
	{Name: "readonly passthrough", Command: `mysql-cli query "SELECT 1"`, WantHit: false},
	{Name: "readonly with flags", Command: `mysql-cli --datasource prod query "SELECT * FROM users LIMIT 5"`, WantHit: false},
	{Name: "readonly schema explore", Command: `mysql-cli schema users --format json`, WantHit: false},
	{Name: "readonly wrapped passthrough", Command: `bash -c "mysql-cli query 'SELECT 1'"`, WantHit: false},

	// --- direct write flags must be caught ---
	{Name: "write flag blocks", Command: `mysql-cli query "UPDATE users SET name='x' WHERE id=1" --write`, WantHit: true},
	{Name: "ddl flag blocks", Command: `mysql-cli txn "ALTER TABLE users ADD COLUMN age INT" --write --ddl`, WantHit: true},
	{Name: "yes flag blocks", Command: `mysql-cli query "DELETE FROM users WHERE id=1" --write --yes`, WantHit: true},
	{Name: "write=true blocks (C1)", Command: `mysql-cli query "UPDATE users SET name='x' WHERE id=1" --write=true`, WantHit: true},
	{Name: "write=false still blocks (flag present)", Command: `mysql-cli query "UPDATE users SET name='x'" --write=false`, WantHit: true},
	{Name: "ddl=1 blocks (C1)", Command: `mysql-cli txn "ALTER TABLE users ADD COLUMN age INT" --ddl=1`, WantHit: true},
	{Name: "yes=true blocks", Command: `mysql-cli query "TRUNCATE TABLE logs" --write --yes=true`, WantHit: true},
	{Name: "flag before args", Command: `mysql-cli --write query "UPDATE t SET a=1"`, WantHit: true},
	{Name: "trailing semicolon", Command: `mysql-cli query "UPDATE users SET age=9" --write;`, WantHit: true},

	// --- wrapped / chained invocations must be caught (C2) ---
	{Name: "bash -c wrapped blocks (C2)", Command: `bash -c "mysql-cli query 'UPDATE users SET age=2 WHERE id=1' --write"`, WantHit: true},
	{Name: "bash -lc wrapped blocks", Command: `bash -lc "mysql-cli query 'UPDATE users SET age=2' --write"`, WantHit: true},
	{Name: "sh -c wrapped blocks", Command: `sh -c 'mysql-cli query "UPDATE users SET age=2" --write'`, WantHit: true},
	{Name: "wrapped multi-command blocks", Command: `bash -c "echo hi; mysql-cli query 'UPDATE t SET a=1' --write"`, WantHit: true},
	{Name: "wrapped equals form blocks (C1+C2)", Command: `bash -c "mysql-cli query 'UPDATE t SET a=1' --write=true"`, WantHit: true},
	{Name: "sudo bash -c wrapped blocks", Command: `sudo bash -c "mysql-cli query 'UPDATE t SET a=1' --write"`, WantHit: true},
	{Name: "unclosed quote falls back to regex", Command: `mysql-cli query "UPDATE users SET age=10 --write`, WantHit: true},
	{Name: "unclosed wrapped falls back to regex", Command: `bash -c "mysql-cli query 'UPDATE users SET age=11' --write`, WantHit: true},

	// --- prefixes / pass-through wrappers / chaining ---
	{Name: "sudo prefix blocks", Command: `sudo mysql-cli query "UPDATE users SET age=3" --write`, WantHit: true},
	{Name: "env with assignments blocks", Command: `env MYSQL_HOST=prod mysql-cli query "UPDATE users SET age=4" --write`, WantHit: true},
	{Name: "chained with && blocks", Command: `cd /app && mysql-cli query "UPDATE users SET age=5" --write`, WantHit: true},
	{Name: "chained with ; blocks", Command: `ls -la; mysql-cli query "UPDATE users SET age=6" --write`, WantHit: true},
	{Name: "xargs passthrough blocks", Command: `cat ids.txt | xargs mysql-cli query "UPDATE users SET age=7 WHERE id=?" --write`, WantHit: true},
	{Name: "subshell blocks", Command: `(mysql-cli query "UPDATE users SET age=8" --write)`, WantHit: true},

	// --- false positives that must NOT prompt (C14 and friends) ---
	{Name: "echo does not block (C14)", Command: `echo mysql-cli query "UPDATE t SET a=1" --write`, WantHit: false},
	{Name: "echo quoted does not block (C14)", Command: `echo "mysql-cli query 'UPDATE t SET a=1' --write"`, WantHit: false},
	{Name: "printf does not block (C14)", Command: `printf '%s' "mysql-cli --write"`, WantHit: false},
	{Name: "echo of wrapped command does not block", Command: `echo bash -c "mysql-cli --write"`, WantHit: false},
	{Name: "SQL literal flag not a token", Command: `mysql-cli query "SELECT * FROM docs WHERE body = '--write'"`, WantHit: false},
	{Name: "SQL literal spaced flag not a token", Command: `mysql-cli query "SELECT 'please do not --write anything' AS note"`, WantHit: false},
	{Name: "comment line is not a command", Command: `# mysql-cli query "UPDATE t SET a=1" --write`, WantHit: false},
	{Name: "comment with semicolon is not a command", Command: `# note; mysql-cli --write`, WantHit: false},
	{Name: "trailing comment flag ignored", Command: `mysql-cli query "SELECT 1" # pass --write to modify`, WantHit: false},
	{Name: "other tool flags ignored", Command: `other-tool --write-cache /tmp/x`, WantHit: false},
	{Name: "readonly chained before other tool", Command: `mysql-cli query "SELECT 1" && other-tool --write-cache`, WantHit: false},
	{Name: "readonly wrapped with semicolon in SQL", Command: `bash -c "mysql-cli query 'SELECT a; SELECT b'"`, WantHit: false},
}

// --- Python hook (mysql-write-guard.py) ---

// runPythonGuard feeds one PreToolUse event to the Python hook via stdin and
// reports whether it asked for confirmation.
func runPythonGuard(t *testing.T, scriptPath, toolName, command string) bool {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"command": command},
	})
	require.NoError(t, err)

	cmd := exec.Command("python3", scriptPath)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	require.NoError(t, err, "python3 %s must exit 0 (fail-open contract)", scriptPath)
	if len(bytes.TrimSpace(out)) == 0 {
		return false // no output = allow
	}
	var resp struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	require.NoError(t, json.Unmarshal(out, &resp), "output must be valid JSON: %s", out)
	return resp.HookSpecificOutput.PermissionDecision == "ask"
}

func TestPythonWriteGuardMatrix(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping Python guard matrix")
	}
	scriptPath := filepath.Join("templates", "mysql-write-guard.py")
	for _, tc := range guardCases {
		t.Run(tc.Name, func(t *testing.T) {
			got := runPythonGuard(t, scriptPath, "Bash", tc.Command)
			assert.Equal(t, tc.WantHit, got, "command: %s", tc.Command)
		})
	}
}

// TestPythonWriteGuardToolNames covers the tool_name routing: Claude Code and
// CodeBuddy send "Bash", TRAE sends "RunCommand" (C3) -- both must be gated.
func TestPythonWriteGuardToolNames(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping Python guard tool-name tests")
	}
	scriptPath := filepath.Join("templates", "mysql-write-guard.py")
	const writeCmd = `mysql-cli query "UPDATE users SET name='x' WHERE id=1" --write`

	cases := []struct {
		toolName string
		wantHit  bool
	}{
		{"Bash", true},       // Claude Code / CodeBuddy matcher
		{"RunCommand", true}, // TRAE matcher (C3)
		{"Read", false},      // unrelated tools pass through
		{"Edit", false},
	}
	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			got := runPythonGuard(t, scriptPath, tc.toolName, writeCmd)
			assert.Equal(t, tc.wantHit, got)
		})
	}
}

// TestPythonWriteGuardMalformedStdin verifies the fail-open contract: broken
// input must exit 0 with no output rather than crashing the agent's Bash.
func TestPythonWriteGuardMalformedStdin(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	scriptPath := filepath.Join("templates", "mysql-write-guard.py")
	for _, in := range []string{"", "   ", "not json at all", `{"tool_name":`} {
		cmd := exec.Command("python3", scriptPath)
		cmd.Stdin = strings.NewReader(in)
		out, err := cmd.Output()
		require.NoError(t, err, "input %q must exit 0 (fail-open)", in)
		assert.Empty(t, string(out), "input %q must produce no decision", in)
	}
}

// --- Pi TS extension (pi-mysql-write-guard.ts) ---

// tsDriverSrc is a tiny ESM driver: it imports the guard script (type
// stripping erases the type-only pi import), runs isMysqlCliWrite over the
// case table read from stdin, and prints the boolean results as JSON.
const tsDriverSrc = `import { readFileSync } from "node:fs";
const mod = await import(new URL(process.argv[2], import.meta.url).href);
const cases = JSON.parse(readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(cases.map((c) => mod.isMysqlCliWrite(c.command))));
`

// nodeVersion returns node's major/minor version.
func nodeVersion(t *testing.T) (major, minor int) {
	t.Helper()
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		return -1, -1
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return -1, -1
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return -1, -1
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return -1, -1
	}
	return major, minor
}

// runTSGuard executes the shared matrix against the TS guard using node's
// type stripping. Node >= 22.6 accepts --experimental-strip-types; newer
// releases (>= 23.6 / 22.18) strip types by default and may drop the flag, so
// the driver retries without it. On any failure the test skips (with the
// reason) instead of failing -- machines without a suitable node simply
// cannot run the TS half of the matrix.
func runTSGuard(t *testing.T) []bool {
	t.Helper()
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	major, minor := nodeVersion(t)
	if major < 22 || (major == 22 && minor < 6) {
		t.Skipf("node %d.%d < 22.6 (type stripping unavailable); skipping TS guard matrix", major, minor)
	}

	scriptPath, err := filepath.Abs(filepath.Join("templates", "pi-mysql-write-guard.ts"))
	require.NoError(t, err)
	driver := filepath.Join(t.TempDir(), "driver.mjs")
	require.NoError(t, os.WriteFile(driver, []byte(tsDriverSrc), 0o644))

	stdin, err := json.Marshal(guardCases)
	require.NoError(t, err)

	run := func(extra ...string) ([]byte, error) {
		args := append(append([]string{}, extra...), driver, scriptPath)
		cmd := exec.Command(node, args...)
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				err = fmt.Errorf("%w; node stderr: %s", err, ee.Stderr)
			}
		}
		return out, err
	}

	out, err := run("--experimental-strip-types")
	if err != nil {
		out, err = run() // node >= 23.6 / 22.18: stripping on by default
	}
	if err != nil {
		t.Skipf("cannot run TS guard via node %d.%d (type stripping failed: %v); skipping TS guard matrix", major, minor, err)
	}

	var results []bool
	require.NoError(t, json.Unmarshal(out, &results))
	require.Len(t, results, len(guardCases), "driver must return one result per case")
	return results
}

func TestTSWriteGuardMatrix(t *testing.T) {
	results := runTSGuard(t)
	for i, tc := range guardCases {
		assert.Equal(t, tc.WantHit, results[i], "case %q: %s", tc.Name, tc.Command)
	}
}
