package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigTrust_DefaultCwd(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a SUBDIR of projRoot (not projRoot itself) so DiscoverProject
	// must walk up to find projRoot/.config/mysql-cli/config.toml. If discovery
	// were broken (found=false), the fallback root would be this subdir, and
	// AddTrust would record the subdir -- not projRoot -- making the assertion
	// below genuinely distinguish the discovery path from the fallback.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)
	code := Run([]string{"config", "trust"})
	assert.Equal(t, ExitOK, code)
	// Exact equality on the trimmed trust-file content: a BROKEN DiscoverProject
	// (found=false -> fallback root=dir=projRoot/sub) would record projRoot/sub,
	// and projRoot is a SUBSTRING of projRoot/sub (Contains would still pass).
	// EvalSymlinks normalizes macOS /var -> /private/var so the assertion is stable.
	b, err := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.NoError(t, err)
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, strings.TrimSpace(string(b)))
}

func TestConfigTrust_Idempotent(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a subdir so DiscoverProject must walk up; if discovery were
	// broken the fallback would record the subdir, failing the assertion.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"}))
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"})) // no duplicate
	// Exact equality proves idempotency: two trust calls must still produce a
	// single trimmed line == want. Substring Count would still pass for a
	// broken DiscoverProject (projRoot is a substring of projRoot/sub).
	b, err := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.NoError(t, err)
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, strings.TrimSpace(string(b))) // single line == want => idempotent
}

func TestConfigTrust_JSON(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	cfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("# stub"), 0o600)
	// chdir into a subdir so DiscoverProject must walk up; if discovery were
	// broken the fallback would record the subdir, failing the assertion below.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Capture os.Stdout (config trust writes via cmd.OutOrStdout() -> os.Stdout).
	// Package tests are serial (no t.Parallel) so mutating global os.Stdout is
	// safe; restore via t.Cleanup registered BEFORE mutating os.Stdout so a
	// panic between the assignment and a later Cleanup registration cannot
	// leak the pipe-writer as os.Stdout.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "trust", "-j"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// MarshalIndent produces `"key": value` (colon+space); parse the envelope
	// to make the assertion robust against formatting drift.
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Trusted string `json:"trusted"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(out, &env))
	assert.True(t, env.Success)
	assert.NotEmpty(t, env.Data.Trusted)
	// Also confirm the trusted path is projRoot (EvalSymlinks-normalized for macOS /var -> /private/var).
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.Equal(t, want, env.Data.Trusted)
}

// TestConfigPath_ShowsProjectAndGlobal trusts projRoot first, then runs
// `config path` from a SUBDIR of projRoot so DiscoverProject must walk up to
// find projRoot/.config/mysql-cli/config.toml. It captures stdout and asserts
// content (not just exit code) so a broken discovery (no project line) or a
// broken format string (no "project:"/"global:" tags) fails the test.
func TestConfigPath_ShowsProjectAndGlobal(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	projCfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(projCfgDir, 0o755)
	os.WriteFile(filepath.Join(projCfgDir, "config.toml"), []byte("# p"), 0o600)
	// global config at home
	os.MkdirAll(filepath.Join(home, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "mysql-cli", "config.toml"), []byte("# g"), 0o600)
	// chdir into a SUBDIR of projRoot so DiscoverProject must walk up. If
	// discovery were broken (found=false), no "project:" line would appear and
	// the tag assertions below would fail.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Trust projRoot first so the project entry is [trusted], not [untrusted, skipped].
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"}))

	// Capture os.Stdout (config path writes via cmd.OutOrStdout() -> os.Stdout).
	// Pre-register t.Cleanup BEFORE mutating os.Stdout so a panic between the
	// assignment and a later Cleanup registration cannot leak the pipe-writer.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "path"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// Tag substrings are fixed status tokens (not paths), so substring search
	// is safe here. They genuinely distinguish: (a) discovery worked (project:
	// present), (b) trust was recorded ([trusted] vs [untrusted, skipped]),
	// (c) global chain still listed (global:).
	assert.Contains(t, string(out), "project:")
	assert.Contains(t, string(out), "[trusted]")
	assert.Contains(t, string(out), "global:")
	// Belt-and-suspenders: the project path printed must reference projRoot
	// (EvalSymlinks-normalized for macOS /var -> /private/var).
	want, _ := filepath.EvalSymlinks(projRoot)
	assert.True(t, strings.Contains(string(out), want),
		"expected stdout to reference projRoot %q, got:\n%s", want, string(out))
}

// TestConfigPath_UntrustedProjectSkipped runs `config path` WITHOUT trusting
// projRoot, from a SUBDIR of projRoot. It captures stdout and asserts the
// project line is present but marked [untrusted, skipped] (not just exit code)
// so a broken trust-check or broken format string fails the test.
func TestConfigPath_UntrustedProjectSkipped(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	projCfgDir := filepath.Join(projRoot, ".config", "mysql-cli")
	os.MkdirAll(projCfgDir, 0o755)
	os.WriteFile(filepath.Join(projCfgDir, "config.toml"), []byte("# p"), 0o600)
	// global config at home (so global: line is present)
	os.MkdirAll(filepath.Join(home, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "mysql-cli", "config.toml"), []byte("# g"), 0o600)
	// chdir into a SUBDIR of projRoot so DiscoverProject must walk up; if
	// discovery were broken no "project:" line would appear, failing the
	// [untrusted, skipped] assertion below.
	sub := filepath.Join(projRoot, "sub")
	os.MkdirAll(sub, 0o755)
	os.Chdir(sub)

	// Capture os.Stdout with pre-registered t.Cleanup (panic-safe).
	orig := os.Stdout
	r, w, _ := os.Pipe()
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
	os.Stdout = w
	code := Run([]string{"config", "path"})
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)

	assert.Equal(t, ExitOK, code)
	// Tag assertions distinguish: discovery worked (project: present), trust
	// was NOT recorded ([untrusted, skipped] vs [trusted]), global still listed.
	assert.Contains(t, string(out), "project:")
	assert.Contains(t, string(out), "[untrusted, skipped]")
	assert.Contains(t, string(out), "global:")
}
