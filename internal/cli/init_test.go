package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/agents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runInit(t *testing.T, home string, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	// isolate HOME so detection is deterministic
	t.Setenv("HOME", home)
	root.SetArgs(append([]string{"init"}, args...))
	code := ExitOK
	if err := root.Execute(); err != nil {
		code = mapError(err)
	}
	return code, buf.String()
}

func TestInit_AutoDefaultClaude_JSON(t *testing.T) {
	home := t.TempDir()
	code, out := runInit(t, home, "-j")
	require.Equal(t, ExitOK, code)
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Agents []struct {
				Agent  string `json:"agent"`
				Status string `json:"status"`
			} `json:"agents"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	require.Len(t, env.Data.Agents, 1)
	assert.Equal(t, "claude", env.Data.Agents[0].Agent)
	assert.Equal(t, "installed", env.Data.Agents[0].Status)
	assert.FileExists(t, filepath.Join(home, ".claude", "skills", "mysql-shared", "SKILL.md"))
}

func TestInit_DryRun_NoFiles(t *testing.T) {
	home := t.TempDir()
	code, _ := runInit(t, home, "--dry-run")
	assert.Equal(t, ExitOK, code)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
}

func TestInit_NoGlobal_WithProjectDir(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	code, _ := runInit(t, home, "--agent", "claude", "--project-dir", proj, "--no-global")
	assert.Equal(t, ExitOK, code)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
	assert.FileExists(t, filepath.Join(proj, ".claude", "skills", "mysql-query", "SKILL.md"))
}

func TestInit_UnknownAgent_ExitNonZero(t *testing.T) {
	home := t.TempDir()
	code, _ := runInit(t, home, "--agent", "nope")
	// unknown agent -> Install returns status "error" -> all failed -> ExitInitFailed
	assert.Equal(t, ExitInitFailed, code)
}

func TestInit_TextOutput(t *testing.T) {
	home := t.TempDir()
	code, out := runInit(t, home)
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, out, "mysql-cli skill init")
	assert.Contains(t, out, "claude")
}

func TestAllFailed(t *testing.T) {
	assert.False(t, allFailed(nil), "empty results should not be all-failed")
	assert.False(t, allFailed([]agents.InstallResult{
		{Agent: "claude", Status: "installed"},
		{Agent: "cursor", Status: "error"},
	}), "mixed results should not be all-failed")
	assert.False(t, allFailed([]agents.InstallResult{
		{Agent: "claude", Status: "skipped"},
	}), "skipped-only should not be all-failed")
	assert.True(t, allFailed([]agents.InstallResult{
		{Agent: "nope", Status: "error", Error: "unknown agent"},
	}), "all-error should be all-failed")
	assert.True(t, allFailed([]agents.InstallResult{
		{Agent: "a", Status: "error"},
		{Agent: "b", Status: "error"},
	}), "multiple all-error should be all-failed")
}

func TestEmitInitText_AllStatuses(t *testing.T) {
	var buf bytes.Buffer
	emitInitText(&buf, []agents.InstallResult{
		{Agent: "claude", Status: "installed", Paths: []string{"/a/SKILL.md"}},
		{Agent: "cursor", Status: "skipped", Error: "project-only"},
		{Agent: "nope", Status: "error", Error: "unknown agent"},
	})
	out := buf.String()
	assert.Contains(t, out, "mysql-cli skill init")
	assert.Contains(t, out, "claude")
	assert.Contains(t, out, "/a/SKILL.md")
	assert.Contains(t, out, "cursor")
	assert.Contains(t, out, "project-only")
	assert.Contains(t, out, "nope")
	assert.Contains(t, out, "unknown agent")
}

func TestEmitInitJSON_AllFailed(t *testing.T) {
	var buf bytes.Buffer
	results := []agents.InstallResult{
		{Agent: "nope", Status: "error", Error: "unknown agent"},
	}
	require.NoError(t, emitInitJSON(&buf, results))
	var env struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.False(t, env.Success)
	assert.Equal(t, "all agent skill installs failed", env.Error)
}

func TestEmitInitJSON_Success(t *testing.T) {
	var buf bytes.Buffer
	results := []agents.InstallResult{
		{Agent: "claude", Status: "installed", Paths: []string{"/a/SKILL.md"}},
	}
	require.NoError(t, emitInitJSON(&buf, results))
	var env struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.True(t, env.Success)
}

func TestInit_AllAgents_SomeSkipped(t *testing.T) {
	home := t.TempDir()
	// --agent all includes project-only agents (cursor, codex, etc.) which
	// return "skipped" without --project-dir; claude/aider still install.
	code, out := runInit(t, home, "--agent", "all", "-j")
	assert.Equal(t, ExitOK, code, "at least claude+aider install -> not all failed")
	var env struct {
		Data struct {
			Agents []struct {
				Agent  string `json:"agent"`
				Status string `json:"status"`
			} `json:"agents"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.GreaterOrEqual(t, len(env.Data.Agents), 1)
	// at least claude should be installed
	var hasInstalled bool
	for _, a := range env.Data.Agents {
		if a.Agent == "claude" && a.Status == "installed" {
			hasInstalled = true
		}
	}
	assert.True(t, hasInstalled, "claude should install under --agent all")
}

func TestInit_ErrorCodeMapping(t *testing.T) {
	// mapError -> ExitInitFailed (already exercised by UnknownAgent test,
	// but assert the mapping directly).
	assert.Equal(t, ExitInitFailed, mapError(ErrInitAllFailed))
	// errorCodeName covers the new INIT_FAILED case.
	assert.Equal(t, "INIT_FAILED", errorCodeName(ExitInitFailed))
	// formatErr renders the new code in both formats.
	jsonOut := formatErr(ErrInitAllFailed, "json")
	assert.Contains(t, jsonOut, `"code":"INIT_FAILED"`)
	assert.Contains(t, jsonOut, ErrInitAllFailed.Error())
	textOut := formatErr(ErrInitAllFailed, "table")
	assert.Contains(t, textOut, "Error [INIT_FAILED]")
	assert.Contains(t, textOut, ErrInitAllFailed.Error())
}

func TestInit_ProjectDirOnly_NoGlobal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// --no-global with --project-dir and --agent claude installs to proj only.
	code, out := runInit(t, home, "--agent", "claude", "--project-dir", proj, "--no-global", "-j")
	assert.Equal(t, ExitOK, code)
	var env struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
	assert.FileExists(t, filepath.Join(proj, ".claude", "skills", "mysql-shared", "SKILL.md"))
}

func TestInit_InvalidGlobalFormat(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	// override after newRootCmd so StringVarP default doesn't clobber it
	g.Format = "invalid"
	t.Setenv("HOME", home)
	root.SetArgs([]string{"init", "-j"})
	code := ExitOK
	if err := root.Execute(); err != nil {
		code = mapError(err)
	}
	// invalid format -> PersistentPreRunE error -> ExitConfigError
	assert.Equal(t, ExitConfigError, code)
}

func TestInit_InvalidTimeout(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	g.Timeout = "not-a-duration"
	t.Setenv("HOME", home)
	root.SetArgs([]string{"init", "-j"})
	code := ExitOK
	if err := root.Execute(); err != nil {
		code = mapError(err)
	}
	// invalid timeout -> PersistentPreRunE error -> ExitConfigError
	assert.Equal(t, ExitConfigError, code)
}

func TestInit_HomeFallback(t *testing.T) {
	// Empty HOME triggers os.UserHomeDir error -> fallback to os.Getwd().
	// Use --dry-run so no files are written to the cwd.
	t.Setenv("HOME", "")
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	root.SetArgs([]string{"init", "--dry-run", "-j"})
	code := ExitOK
	if err := root.Execute(); err != nil {
		code = mapError(err)
	}
	assert.Equal(t, ExitOK, code)
	var env struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.True(t, env.Success)
}
