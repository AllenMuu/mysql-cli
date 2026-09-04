package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	assert.Equal(t, false, aa[copilotAutoApprovePattern])
	_, err = os.Stat(filepath.Join(dir, ".github", "copilot-instructions.md"))
	assert.NoError(t, err, "instructions written at project scope")
}

func TestLookupAndNames(t *testing.T) {
	a, ok := Lookup("claude")
	require.True(t, ok)
	assert.Equal(t, CapEnforce, a.Cap)
	_, ok = Lookup("nope")
	assert.False(t, ok)
	assert.Len(t, Names(), 8)
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

func TestInstall_PiProject(t *testing.T) {
	dir := t.TempDir()
	written, err := pi.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	require.Len(t, written, 1)

	// Project scope: .pi/extensions/mysql-write-guard.ts (single file, no settings.json).
	extPath := filepath.Join(dir, ".pi", "extensions", "mysql-write-guard.ts")
	assert.Contains(t, written, extPath)

	info, err := os.Stat(extPath)
	require.NoError(t, err)
	// actionWriteFile uses 0o644, NOT executable (TS extension is loaded by pi runtime, not exec'd).
	assert.Zero(t, info.Mode()&0o100, "TS extension must not be executable")

	data, err := os.ReadFile(extPath)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `pi.on("tool_call"`)
	assert.Contains(t, body, `event.toolName !== "bash"`, "Pi tool name is lowercase 'bash'")
	assert.Contains(t, body, "ctx.ui.confirm")
	assert.Contains(t, body, "block: true")

	// No settings.json or hooks.json should be written (auto-discovery, single file).
	_, err = os.Stat(filepath.Join(dir, ".pi", "settings.json"))
	assert.True(t, os.IsNotExist(err), "Pi install must not write settings.json (auto-discovery)")
}

func TestInstall_PiGlobal(t *testing.T) {
	home := t.TempDir()
	written, err := pi.Install(InstallOpts{Scope: ScopeGlobal, Home: home})
	require.NoError(t, err)
	require.Len(t, written, 1)

	// Global scope: ~/.pi/agent/extensions/mysql-write-guard.ts.
	extPath := filepath.Join(home, ".pi", "agent", "extensions", "mysql-write-guard.ts")
	assert.Contains(t, written, extPath)

	data, err := os.ReadFile(extPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `pi.on("tool_call"`)
}

func TestInstall_PiDryRun(t *testing.T) {
	dir := t.TempDir()
	written, err := pi.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, DryRun: true})
	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.True(t, strings.HasPrefix(written[0], "write"), "dry-run emits 'write <path>'")

	// Dry-run must not touch the filesystem.
	_, err = os.Stat(filepath.Join(dir, ".pi", "extensions", "mysql-write-guard.ts"))
	assert.True(t, os.IsNotExist(err), "dry-run must not write")
}

func TestInstall_PiSkipIfExists(t *testing.T) {
	dir := t.TempDir()
	// First install writes the extension.
	written1, err := pi.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	require.Len(t, written1, 1)

	// Second install without --force must be a no-op (skip existing).
	written2, err := pi.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir})
	require.NoError(t, err)
	assert.Empty(t, written2, "second install without --force must skip the existing file")

	// --force overwrites.
	written3, err := pi.Install(InstallOpts{Scope: ScopeProject, ProjectDir: dir, Force: true})
	require.NoError(t, err)
	require.Len(t, written3, 1, "--force overwrites the existing extension")
}

// --- C4: .bak keeps the pristine original across repeated installs ---

func TestMergeJSONFile_BakPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := `{"hooks":{"PreToolUse":[]},"marker":"original"}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	frag := preToolUseFragment("Bash", `python3 "$HOME/.claude/hooks/mysql-write-guard.py"`)

	// First install creates the backup.
	_, err := mergeJSONFile(path, frag)
	require.NoError(t, err)

	// Simulate the user editing the merged file between installs.
	edited := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"python3 \"$HOME/.claude/hooks/mysql-write-guard.py\"","timeout":99}]}]},"marker":"original"}`
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o644))

	// Second install must NOT overwrite the backup with the edited content.
	_, err = mergeJSONFile(path, frag)
	require.NoError(t, err)

	bak, err := os.ReadFile(path + ".bak")
	require.NoError(t, err)
	assert.Equal(t, original, string(bak), ".bak must still hold the pristine pre-install original")
}

// --- C5: atomic write helper ---

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "file.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	require.NoError(t, writeFileAtomic(path, []byte("hello"), 0o640))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "mode applied to final file")

	// Overwrite via rename keeps content and mode.
	require.NoError(t, writeFileAtomic(path, []byte("world"), 0o640))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "world", string(data))

	// No temp files left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "temp file renamed away, exactly one file remains")
}

// --- C6: merged target keeps the original file mode ---

func TestMergeJSONFile_PreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"a":1}`), 0o600))

	_, err := mergeJSONFile(path, map[string]any{"b": 2})
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "merged target keeps original 0600 mode")
	binfo, err := os.Stat(path + ".bak")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), binfo.Mode().Perm(), "backup keeps original 0600 mode")
}

// --- C8: copilot autoApprove regex anchors on mysql-cli ---

func TestCopilotAutoApprovePattern(t *testing.T) {
	re, err := regexp.Compile(strings.Trim(copilotAutoApprovePattern, "/"))
	require.NoError(t, err)

	mustHit := []string{
		// plain write forms must keep prompting
		`mysql-cli query "UPDATE users SET name='a; b' WHERE id=1" --write`,
		`mysql-cli --write query "SELECT 1"`,
		`mysql-cli query "UPDATE t SET a=1" --write=true`,
		`mysql-cli txn "ALTER TABLE t ADD c INT" --ddl=1`,
		`mysql-cli query "DELETE FROM t WHERE 1=1" --write --yes`,
		`mysql-cli query "SELECT 1" --yes`,
		// separators and wrapped calls must not create gaps
		`bash -c "mysql-cli query 'UPDATE t; UPDATE u' --write"`,
		`bash -c "echo hi" ; mysql-cli --write`,
		`cd /app && mysql-cli query "x" --write`,
		// deliberate over-block (宁可多拦): a later bare --yes from another
		// tool after a read-only mysql-cli mention still prompts.
		`mysql-cli query "x" && npm install --yes`,
	}
	for _, cmd := range mustHit {
		assert.True(t, re.MatchString(cmd), "pattern must hit: %s", cmd)
	}

	mustMiss := []string{
		`mysql-cli query "SELECT 1"`,
		// unrelated tools' compound flags no longer prompt
		`other-tool --write-cache /tmp/x`,
		`other-tool --write-cache && mysql-cli query "SELECT 1"`,
		`other-tool --yesman`,
		`mysql-cli query "x" && other-tool --write-cache`,
	}
	for _, cmd := range mustMiss {
		assert.False(t, re.MatchString(cmd), "pattern must not hit: %s", cmd)
	}
}

// --- C10: VS Code Insiders gets its own settings dir ---

func TestVscodeUserSettingsInsiders(t *testing.T) {
	home := t.TempDir()
	var stableUser, insidersUser string
	switch runtime.GOOS {
	case "darwin":
		stableUser = filepath.Join(home, "Library", "Application Support", "Code", "User")
		insidersUser = filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User")
	case "windows":
		stableUser = filepath.Join(home, "AppData", "Roaming", "Code", "User")
		insidersUser = filepath.Join(home, "AppData", "Roaming", "Code - Insiders", "User")
	default:
		stableUser = filepath.Join(home, ".config", "Code", "User")
		insidersUser = filepath.Join(home, ".config", "Code - Insiders", "User")
	}

	// Neither edition installed: fall back to stable.
	paths := vscodeUserSettings(home)
	require.Len(t, paths, 1)
	assert.Equal(t, filepath.Join(stableUser, "settings.json"), paths[0])

	// Only Insiders installed: target Insiders only.
	require.NoError(t, os.MkdirAll(insidersUser, 0o755))
	paths = vscodeUserSettings(home)
	require.Len(t, paths, 1)
	assert.Equal(t, filepath.Join(insidersUser, "settings.json"), paths[0])

	// Both editions: install into both.
	require.NoError(t, os.MkdirAll(stableUser, 0o755))
	paths = vscodeUserSettings(home)
	require.Len(t, paths, 2)
	assert.Equal(t, filepath.Join(stableUser, "settings.json"), paths[0])
	assert.Equal(t, filepath.Join(insidersUser, "settings.json"), paths[1])
}

// --- C11: Windows warning for POSIX-only hooks ---

func TestWindowsIncompatWarning(t *testing.T) {
	assert.NotEmpty(t, windowsIncompatWarning("windows", trae), "POSIX-hook agents warn on Windows")
	assert.NotEmpty(t, windowsIncompatWarning("windows", claudeCode))
	assert.NotEmpty(t, windowsIncompatWarning("windows", codebuddy))
	assert.Empty(t, windowsIncompatWarning("windows", copilot), "non-POSIX-hook agents stay silent")
	assert.Empty(t, windowsIncompatWarning("windows", opencode))
	assert.Empty(t, windowsIncompatWarning("linux", trae), "no warning off Windows")
	assert.Empty(t, windowsIncompatWarning("darwin", claudeCode))
}

// --- C12: re-install replaces our (possibly tweaked) hook entry ---

func TestMergeJSONFile_ReplacesTweakedHookEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	frag := traeHookFragment("RunCommand", `python3 "${TRAE_PROJECT_DIR:-$PWD}/.trae/hooks/mysql-write-guard.py"`)

	// First install.
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1}`), 0o644))
	_, err := mergeJSONFile(path, frag)
	require.NoError(t, err)

	// User tweaks our entry afterwards (timeout 30 -> 99) and adds their own hook.
	tweaked := `{"version":1,"hooks":{"PreToolUse":[` +
		`{"matcher":"RunCommand","hooks":[{"type":"command","command":"python3 \"${TRAE_PROJECT_DIR:-$PWD}/.trae/hooks/mysql-write-guard.py\"","timeout":99}]},` +
		`{"matcher":"Bash","hooks":[{"type":"command","command":"echo mine"}]}]}}`
	require.NoError(t, os.WriteFile(path, []byte(tweaked), 0o644))

	// Re-install: our tweaked entry must be REPLACED in place, not appended.
	_, err = mergeJSONFile(path, frag)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	hooks, _ := got["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	require.Len(t, pre, 2, "our entry replaced in place; user hook kept; no duplicate")

	ours, _ := pre[0].(map[string]any)
	assert.Equal(t, "RunCommand", ours["matcher"])
	ourHooks, _ := ours["hooks"].([]any)
	require.Len(t, ourHooks, 1)
	h, _ := ourHooks[0].(map[string]any)
	assert.Equal(t, float64(30), h["timeout"], "re-install resets the tweaked timeout via replacement")
	assert.Contains(t, h["command"], "mysql-write-guard")
}

// --- F3: re-install keeps user hooks appended to OUR matcher group ---

// TestMergeJSONFile_PreservesUserHooksInSameGroup（F3）：用户把自定义 hook
// 追加进与我们相同 matcher 的 group 后重装，自定义条目必须保留、我们的
// 条目更新为新版（不得整组替换静默删掉用户条目）。
func TestMergeJSONFile_PreservesUserHooksInSameGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	ourCmd := `python3 "$HOME/.claude/hooks/mysql-write-guard.py"`
	frag := preToolUseFragment("Bash", ourCmd)

	// First install.
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o644))
	_, err := mergeJSONFile(path, frag)
	require.NoError(t, err)

	// User tweaks our entry (timeout 99) AND appends their own hook into the
	// SAME matcher group's hooks array.
	edited := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": ourCmd, "timeout": 99},
						map[string]any{"type": "command", "command": "echo user-own"},
					},
				},
			},
		},
	}
	b, err := json.Marshal(edited)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	// Re-install.
	_, err = mergeJSONFile(path, frag)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	pre, _ := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	require.Len(t, pre, 1, "still a single group; no duplicate group appended")

	group, _ := pre[0].(map[string]any)
	assert.Equal(t, "Bash", group["matcher"])
	entries, _ := group["hooks"].([]any)
	require.Len(t, entries, 2, "our entry updated in place; user entry kept")

	// Our entry was replaced by the fresh fragment (user's timeout tweak reset).
	h0, _ := entries[0].(map[string]any)
	assert.Equal(t, ourCmd, h0["command"])
	_, hasTweak := h0["timeout"]
	assert.False(t, hasTweak, "re-install resets our tweaked entry via replacement")

	// The user's own entry survives the re-install.
	h1, _ := entries[1].(map[string]any)
	assert.Equal(t, "echo user-own", h1["command"])
}

// --- F4: guard-marker matching requires a path-anchored script name ---

// TestCarriesOurHookMarker（F4）：特征匹配要求「路径分隔符 + 完整文件名
// mysql-write-guard.py/.ts」，用户自建的 my-mysql-write-guard.py 不再被
// 误判为我们的 hook。
func TestCarriesOurHookMarker(t *testing.T) {
	group := func(cmd any) map[string]any {
		return map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": cmd}}}
	}
	// 我们安装的命令形态：路径 + mysql-write-guard.py（.ts）。
	assert.True(t, carriesOurHook(group(`python3 "$HOME/.claude/hooks/mysql-write-guard.py"`)))
	assert.True(t, carriesOurHook(group(`python3 "${CLAUDE_PROJECT_DIR:-$PWD}/.codebuddy/hooks/mysql-write-guard.py"`)))
	assert.True(t, carriesOurHook(group(`python3 "$HOME/.trae-cn/hooks/mysql-write-guard.py"`)))
	assert.True(t, carriesOurHook(group(`node /opt/tools/mysql-write-guard.ts`)))
	// 用户自有脚本：文件名仅包含 mysql-write-guard 子串，前无路径分隔符。
	assert.False(t, carriesOurHook(group(`python3 /hooks/my-mysql-write-guard.py`)), "my-mysql-write-guard.py is NOT ours")
	assert.False(t, carriesOurHook(group(`python3 /hooks/mysql-write-guard.pyx`)))
	assert.False(t, carriesOurHook(group(`python3 /hooks/mysql-write-guard-py`)))
	assert.False(t, carriesOurHook(group(`python3 mysql-write-guard.py`)), "bare name without a path is not an install of ours")
	// 无 command 字段 / 非 map 条目。
	assert.False(t, carriesOurHook(map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command"}}}))
	assert.False(t, carriesOurHook(map[string]any{"matcher": "Bash", "hooks": []any{"not-a-map"}}))
}
