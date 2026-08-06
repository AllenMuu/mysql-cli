package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeepMerge(t *testing.T) {
	dst := map[string]any{
		"a": float64(1),
		"b": map[string]any{"x": float64(1)},
		"c": []any{float64(1), float64(2)},
		"s": "old",
	}
	src := map[string]any{
		"b": map[string]any{"y": float64(2)},
		"c": []any{float64(2), float64(3)},
		"s": "new",
		"d": float64(4),
	}
	deepMerge(dst, src)
	assert.Equal(t, float64(1), dst["a"], "existing scalar kept")
	assert.Equal(t, map[string]any{"x": float64(1), "y": float64(2)}, dst["b"], "maps recurse")
	assert.Equal(t, []any{float64(1), float64(2), float64(3)}, dst["c"], "slice dedup + append")
	assert.Equal(t, "new", dst["s"], "src scalar overwrites")
	assert.Equal(t, float64(4), dst["d"], "new key added")
}

func TestMergeJSONFile_New(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	frag := map[string]any{"permission": map[string]any{"bash": map[string]any{"mysql-cli *--write*": "ask"}}}
	written, err := mergeJSONFile(path, frag)
	require.NoError(t, err)
	assert.Equal(t, path, written)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	bash, _ := got["permission"].(map[string]any)["bash"].(map[string]any)
	assert.Equal(t, "ask", bash["mysql-cli *--write*"])
}

func TestMergeJSONFile_ExistingPreToolUseDedup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"rtk hook claude"}]}]},"other":"keep"}`
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o644))
	frag := preToolUseFragment("Bash", `python3 "$HOME/.claude/hooks/mysql-write-guard.py"`)
	_, err := mergeJSONFile(path, frag)
	require.NoError(t, err)

	// backup written
	_, err = os.Stat(path + ".bak")
	assert.NoError(t, err, ".bak backup created")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "keep", got["other"], "unrelated existing key kept")
	pre, _ := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	assert.Len(t, pre, 2, "rtk + our hook")

	// idempotent: second install must not duplicate our hook
	_, err = mergeJSONFile(path, frag)
	require.NoError(t, err)
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &got)
	pre, _ = got["hooks"].(map[string]any)["PreToolUse"].([]any)
	assert.Len(t, pre, 2, "second install does not duplicate our hook")
}

func TestInstall_ClaudeProject(t *testing.T) {
	dir := t.TempDir()
	written, err := claudeCode.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	assert.Len(t, written, 2)
	hookPath := filepath.Join(dir, ".claude", "hooks", "mysql-write-guard.py")
	assert.Contains(t, written, hookPath)
	info, err := os.Stat(hookPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "hook script is executable")
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "mysql-write-guard.py")
	assert.Contains(t, string(data), "CLAUDE_PROJECT_DIR")
}

func TestInstall_CursorGlobalUnsupported(t *testing.T) {
	_, err := cursor.Install(InstallOpts{Scope: ScopeGlobal, Home: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global scope not supported")
}

func TestInstall_CursorProjectWritesRule(t *testing.T) {
	dir := t.TempDir()
	written, err := cursor.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	require.Len(t, written, 1)
	data, err := os.ReadFile(filepath.Join(dir, ".cursor", "rules", "mysql-cli-write-guard.mdc"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "alwaysApply: true")
}

func TestInstall_DryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	written, err := opencode.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, DryRun: true})
	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.True(t, strings.HasPrefix(written[0], "merge"))
	_, err = os.Stat(filepath.Join(dir, "opencode.json"))
	assert.True(t, os.IsNotExist(err), "dry-run must not write")
}

func TestInstall_OpencodeProjectMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"$schema":"https://opencode.ai/config.json","permission":{"bash":{"ls":"allow"}}}`), 0o644))
	_, err := opencode.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "https://opencode.ai/config.json", got["$schema"], "existing top-level kept")
	bash, _ := got["permission"].(map[string]any)["bash"].(map[string]any)
	assert.Equal(t, "allow", bash["ls"], "existing bash rule kept")
	assert.Equal(t, "ask", bash["mysql-cli *--write*"], "our rule merged")
}

func TestInstall_CopilotProjectFlatKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vscode", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"editor.tabSize":2}`), 0o644))
	_, err := copilot.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, float64(2), got["editor.tabSize"], "existing setting kept")
	aa, ok := got["chat.tools.terminal.autoApprove"].(map[string]any)
	require.True(t, ok, "flat VS Code key merged as one key")
	assert.Equal(t, false, aa["/--(write|ddl|yes)(\\b|=)/"])
	_, err = os.Stat(filepath.Join(dir, ".github", "copilot-instructions.md"))
	assert.NoError(t, err, "instructions written at project scope")
}

func TestLookupAndNames(t *testing.T) {
	a, ok := Lookup("claude")
	require.True(t, ok)
	assert.Equal(t, CapEnforce, a.Cap)
	_, ok = Lookup("nope")
	assert.False(t, ok)
	assert.Len(t, Names(), 6)
}

func TestInstall_TraeProject(t *testing.T) {
	dir := t.TempDir()
	written, err := trae.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	require.Len(t, written, 2)

	// Project scope uses .trae (without -cn suffix).
	hookPath := filepath.Join(dir, ".trae", "hooks", "mysql-write-guard.py")
	hooksJSONPath := filepath.Join(dir, ".trae", "hooks.json")
	assert.Contains(t, written, hookPath)
	assert.Contains(t, written, hooksJSONPath)

	// Hook script is executable.
	info, err := os.Stat(hookPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "hook script is executable")

	// hooks.json content: version=1, matcher=RunCommand (not Bash), timeout=30,
	// and the command path uses TRAE_PROJECT_DIR for portability.
	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, float64(1), got["version"], "TRAE hooks.json has top-level version=1")
	hooks, _ := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	require.Len(t, pre, 1)
	group, _ := pre[0].(map[string]any)
	assert.Equal(t, "RunCommand", group["matcher"], "TRAE matcher uses standardized RunCommand")
	hookList, _ := group["hooks"].([]any)
	require.Len(t, hookList, 1)
	h, _ := hookList[0].(map[string]any)
	assert.Equal(t, "command", h["type"])
	assert.Equal(t, float64(30), h["timeout"])
	cmd, _ := h["command"].(string)
	assert.Contains(t, cmd, "TRAE_PROJECT_DIR")
	assert.Contains(t, cmd, ".trae/hooks/mysql-write-guard.py")
}

func TestInstall_TraeGlobal(t *testing.T) {
	home := t.TempDir()
	written, err := trae.Install(InstallOpts{Scope: ScopeGlobal, Home: home})
	require.NoError(t, err)
	require.Len(t, written, 2)

	// Global scope uses .trae-cn (with -cn suffix) per TRAE's asymmetric design.
	hookPath := filepath.Join(home, ".trae-cn", "hooks", "mysql-write-guard.py")
	hooksJSONPath := filepath.Join(home, ".trae-cn", "hooks.json")
	assert.Contains(t, written, hookPath)
	assert.Contains(t, written, hooksJSONPath)

	info, err := os.Stat(hookPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "hook script is executable")

	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version": 1`)
	assert.Contains(t, string(data), `"RunCommand"`)
	assert.Contains(t, string(data), `$HOME/.trae-cn/hooks/mysql-write-guard.py`)
}

func TestInstall_TraeDryRun(t *testing.T) {
	dir := t.TempDir()
	written, err := trae.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, DryRun: true})
	require.NoError(t, err)
	require.Len(t, written, 2)

	// Dry-run emits one "write" + one "merge" description; writes nothing.
	verbs := map[string]bool{}
	for _, w := range written {
		if strings.HasPrefix(w, "write") {
			verbs["write"] = true
		}
		if strings.HasPrefix(w, "merge") {
			verbs["merge"] = true
		}
	}
	assert.True(t, verbs["write"], "dry-run describes the hook script write")
	assert.True(t, verbs["merge"], "dry-run describes the hooks.json merge")

	_, err = os.Stat(filepath.Join(dir, ".trae", "hooks.json"))
	assert.True(t, os.IsNotExist(err), "dry-run must not write")
	_, err = os.Stat(filepath.Join(dir, ".trae", "hooks", "mysql-write-guard.py"))
	assert.True(t, os.IsNotExist(err), "dry-run must not write hook script")
}

func TestInstall_TraeMergesIntoExisting(t *testing.T) {
	dir := t.TempDir()
	hooksJSONPath := filepath.Join(dir, ".trae", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hooksJSONPath), 0o755))
	// Simulate an existing TRAE hooks.json with an unrelated Stop hook.
	existing := `{"version":1,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo done"}]}]}}`
	require.NoError(t, os.WriteFile(hooksJSONPath, []byte(existing), 0o644))

	_, err := trae.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)

	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, float64(1), got["version"], "version preserved")
	hooks, _ := got["hooks"].(map[string]any)
	// Stop hook must be preserved.
	_, ok := hooks["Stop"]
	assert.True(t, ok, "existing Stop hook preserved")
	// PreToolUse hook added.
	pre, _ := hooks["PreToolUse"].([]any)
	require.Len(t, pre, 1)

	// .bak backup created.
	_, err = os.Stat(hooksJSONPath + ".bak")
	assert.NoError(t, err, ".bak backup created for hooks.json")
}
