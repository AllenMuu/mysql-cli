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

// TestAgentInitCursorGlobalErrorInResults（B3 回归）：部分 agent 失败时命令
// 返回非零退出码（exit 10），错误同时体现在输出与返回的 error 里。
func TestAgentInitCursorGlobalErrorInResults(t *testing.T) {
	chdir(t, t.TempDir())
	c, buf := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "cursor", "--global"})
	// cursor global is unsupported: reported in results AND as a cmd error
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, buf.String(), "cursor")
	assert.Contains(t, buf.String(), "global scope not supported")
	assert.Contains(t, err.Error(), "cursor")
	assert.Equal(t, ExitConfigError, mapError(err))
}

// TestAgentInitJSONPartialFailure（B3）：--json 下部分失败输出
// success:false + error 字段（信封格式与 format 包对齐），且返回非零退出码。
func TestAgentInitJSONPartialFailure(t *testing.T) {
	chdir(t, t.TempDir())
	c, buf := newAgentInitCmdForTest(t)
	c.SetArgs([]string{"--agents", "cursor", "--global", "--json"})
	err := c.Execute()
	require.Error(t, err)
	assert.Contains(t, buf.String(), `"success": false`)
	assert.Contains(t, buf.String(), `"error"`)
	assert.Contains(t, buf.String(), `"code": "CONFIG_ERROR"`)
	assert.Contains(t, buf.String(), "agent init failed for: cursor")
	// data 保留 results 供诊断
	assert.Contains(t, buf.String(), `"results"`)
	assert.Equal(t, ExitConfigError, mapError(err))
}
