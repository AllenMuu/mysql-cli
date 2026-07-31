package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chdir changes to dir for the test and restores cwd on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func newAgentInitCmdForTest(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	c := newAgentInitCmd(&Globals{})
	c.SetOut(buf)
	c.SetErr(buf)
	return c, buf
}

func TestAgentInitDryRunProject(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	c, buf := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "claude", "--project", "--dry-run"})
	require.NoError(t, c.Execute())
	assert.Contains(t, buf.String(), "claude")
	// dry-run wrote nothing
	_, err := os.Stat(filepath.Join(dir, ".claude", "settings.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestAgentInitWriteOpencode(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	c, _ := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "opencode", "--project"})
	require.NoError(t, c.Execute())
	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "mysql-cli *--write*")
}

func TestAgentInitUnknownAgent(t *testing.T) {
	chdir(t, t.TempDir())
	c, _ := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "nope", "--project"})
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent")
}

func TestAgentInitProjectGlobalMutex(t *testing.T) {
	chdir(t, t.TempDir())
	c, _ := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "claude", "--project", "--global"})
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one of")
}

func TestAgentInitJSON(t *testing.T) {
	chdir(t, t.TempDir())
	c, buf := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "opencode", "--project", "--dry-run", "--json"})
	require.NoError(t, c.Execute())
	assert.Contains(t, buf.String(), `"results"`)
	assert.Contains(t, buf.String(), `"opencode"`)
	assert.Contains(t, buf.String(), `"dry_run": true`)
}

func TestAgentInitNotTTYNoFlags(t *testing.T) {
	chdir(t, t.TempDir())
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })
	c, _ := newAgentInitCmdForTest(t)
	c.SetArgs([]string{})
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a TTY")
}

func TestAgentInitCursorGlobalErrorInResults(t *testing.T) {
	chdir(t, t.TempDir())
	c, buf := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "cursor", "--global"})
	// cursor global is unsupported, but reported in results (not as a cmd error)
	require.NoError(t, c.Execute())
	assert.Contains(t, buf.String(), "cursor")
	assert.Contains(t, buf.String(), "global scope not supported")
}
