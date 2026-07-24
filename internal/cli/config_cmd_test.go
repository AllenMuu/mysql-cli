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
	os.Chdir(projRoot) // trust cwd's detected project root
	code := Run([]string{"config", "trust"})
	assert.Equal(t, ExitOK, code)
	// trust file now contains projRoot. projRoot lives under t.TempDir() so
	// filepath.EvalSymlinks may resolve /var/... to /private/var/... on macOS;
	// normalize via EvalSymlinks so the substring check is stable.
	want, _ := filepath.EvalSymlinks(projRoot)
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Contains(t, string(b), want)
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
	os.Chdir(projRoot)
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"}))
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"})) // no duplicate
	want, _ := filepath.EvalSymlinks(projRoot)
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Equal(t, 1, strings.Count(string(b), want))
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
	os.Chdir(projRoot)

	// Capture os.Stdout (config trust writes via cmd.OutOrStdout() -> os.Stdout).
	// Package tests are serial (no t.Parallel) so mutating global os.Stdout is
	// safe; restore via t.Cleanup.
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := Run([]string{"config", "trust", "-j"})
	w.Close()
	os.Stdout = orig
	t.Cleanup(func() { os.Stdout = orig; r.Close() })
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
