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

func TestLookup_Codex(t *testing.T) {
	a, ok := Lookup("codex")
	require.True(t, ok)
	assert.Equal(t, CapEnforce, a.Cap, "codex uses engine-level rules + hooks")
	assert.Equal(t, "codex", a.Name)
}

func TestNames_ContainsCodex(t *testing.T) {
	names := Names()
	require.Len(t, names, 8)
	assert.Equal(t, "codex", names[1], "codex registered right after claude")
}

func TestInstall_CodexProject(t *testing.T) {
	dir := t.TempDir()
	written, err := codex.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	require.Len(t, written, 3)

	hookPath := filepath.Join(dir, ".codex", "hooks", "mysql-write-guard.py")
	hooksJSONPath := filepath.Join(dir, ".codex", "hooks.json")
	rulesPath := filepath.Join(dir, ".codex", "rules", "mysql-cli-write-guard.rules")
	assert.Contains(t, written, hookPath)
	assert.Contains(t, written, hooksJSONPath)
	assert.Contains(t, written, rulesPath)

	// Hook script is executable.
	info, err := os.Stat(hookPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "hook script is executable")

	// hooks.json: PermissionRequest on Bash with the git-rooted command.
	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	hooks, _ := got["hooks"].(map[string]any)
	perm, _ := hooks["PermissionRequest"].([]any)
	require.Len(t, perm, 1)
	group, _ := perm[0].(map[string]any)
	assert.Equal(t, "^Bash$", group["matcher"], "matcher filters by canonical tool name")
	hookList, _ := group["hooks"].([]any)
	require.Len(t, hookList, 1)
	h, _ := hookList[0].(map[string]any)
	assert.Equal(t, "command", h["type"])
	assert.Equal(t, float64(10), h["timeout"])
	cmd, _ := h["command"].(string)
	assert.Contains(t, cmd, "git rev-parse --show-toplevel")
	assert.Contains(t, cmd, ".codex/hooks/mysql-write-guard.py")
	// Codex does not support permissionDecision=ask; hook must never emit it.
	body, err := os.ReadFile(hookPath)
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"permissionDecision"`)

	// Rules file: coarse prompt gate for mysql-cli.
	rulesData, err := os.ReadFile(rulesPath)
	require.NoError(t, err)
	assert.Contains(t, string(rulesData), `pattern = ["mysql-cli"]`)
	assert.Contains(t, string(rulesData), `decision = "prompt"`)
}

func TestInstall_CodexGlobal(t *testing.T) {
	home := t.TempDir()
	written, err := codex.Install(InstallOpts{Scope: ScopeGlobal, Home: home})
	require.NoError(t, err)
	require.Len(t, written, 3)

	hookPath := filepath.Join(home, ".codex", "hooks", "mysql-write-guard.py")
	hooksJSONPath := filepath.Join(home, ".codex", "hooks.json")
	rulesPath := filepath.Join(home, ".codex", "rules", "mysql-cli-write-guard.rules")
	assert.Contains(t, written, hookPath)
	assert.Contains(t, written, hooksJSONPath)
	assert.Contains(t, written, rulesPath)

	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `$HOME/.codex/hooks/mysql-write-guard.py`)
	assert.Contains(t, string(data), `"PermissionRequest"`)
	assert.Contains(t, string(data), `"^Bash$"`)
}

func TestInstall_CodexMergesIntoExisting(t *testing.T) {
	dir := t.TempDir()
	hooksJSONPath := filepath.Join(dir, ".codex", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hooksJSONPath), 0o755))
	// Simulate an existing Codex hooks.json with an unrelated PostToolUse hook.
	existing := `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"echo done"}]}]}}`
	require.NoError(t, os.WriteFile(hooksJSONPath, []byte(existing), 0o644))

	_, err := codex.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)

	data, err := os.ReadFile(hooksJSONPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	hooks, _ := got["hooks"].(map[string]any)
	_, ok := hooks["PostToolUse"]
	assert.True(t, ok, "existing PostToolUse hook preserved")
	perm, _ := hooks["PermissionRequest"].([]any)
	require.Len(t, perm, 1, "PermissionRequest added")

	// .bak backup created.
	_, err = os.Stat(hooksJSONPath + ".bak")
	assert.NoError(t, err, ".bak backup created for hooks.json")
}

func TestInstall_CodexIdempotent(t *testing.T) {
	dir := t.TempDir()
	opts := InstallOpts{Scope: ScopeProject, ProjectDir: dir}
	_, err := codex.Install(opts)
	require.NoError(t, err)

	// Second install: single-file artifacts skip; hooks.json merge dedups our
	// hook entry, so the PermissionRequest list stays at length 1.
	_, err = codex.Install(opts)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, ".codex", "hooks.json"))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	hooks, _ := got["hooks"].(map[string]any)
	perm, _ := hooks["PermissionRequest"].([]any)
	assert.Len(t, perm, 1, "second install does not duplicate our hook")
}

func TestInstall_CodexDryRun(t *testing.T) {
	dir := t.TempDir()
	written, err := codex.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, DryRun: true})
	require.NoError(t, err)
	require.Len(t, written, 3)

	verbs := map[string]bool{}
	for _, w := range written {
		switch {
		case strings.HasPrefix(w, "write"):
			verbs["write"] = true
		case strings.HasPrefix(w, "merge"):
			verbs["merge"] = true
		}
	}
	assert.True(t, verbs["write"], "dry-run describes the script + rules writes")
	assert.True(t, verbs["merge"], "dry-run describes the hooks.json merge")

	for _, p := range []string{
		filepath.Join(dir, ".codex", "hooks.json"),
		filepath.Join(dir, ".codex", "hooks", "mysql-write-guard.py"),
		filepath.Join(dir, ".codex", "rules", "mysql-cli-write-guard.rules"),
	} {
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "dry-run must not write %s", p)
	}
}

func TestInstall_CodexForce(t *testing.T) {
	dir := t.TempDir()
	opts := InstallOpts{Scope: ScopeProject, ProjectDir: dir}

	_, err := codex.Install(opts)
	require.NoError(t, err)
	// Tamper with the artifacts to detect later overwrite.
	rulesPath := filepath.Join(dir, ".codex", "rules", "mysql-cli-write-guard.rules")
	require.NoError(t, os.WriteFile(rulesPath, []byte("# user edited"), 0o644))
	hookPath := filepath.Join(dir, ".codex", "hooks", "mysql-write-guard.py")
	require.NoError(t, os.WriteFile(hookPath, []byte("# user edited"), 0o755))

	// Without --force: single-file artifacts are skipped (user edits kept).
	written, err := codex.Install(opts)
	require.NoError(t, err)
	data, _ := os.ReadFile(rulesPath)
	assert.Contains(t, string(data), "# user edited", "rules file not overwritten without --force")

	// With --force: overwritten.
	written, err = codex.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, Force: true})
	require.NoError(t, err)
	assert.Contains(t, written, rulesPath, "--force overwrites the rules file")
	data, _ = os.ReadFile(rulesPath)
	assert.Contains(t, string(data), `pattern = ["mysql-cli"]`)
	data, _ = os.ReadFile(hookPath)
	assert.Contains(t, string(data), "PermissionRequest")
}
