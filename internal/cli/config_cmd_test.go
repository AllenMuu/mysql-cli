package cli

import (
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
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot) // trust cwd's detected project root
	code := Run([]string{"config", "trust"})
	assert.Equal(t, ExitOK, code)
	// trust file now contains projRoot
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Contains(t, string(b), projRoot)
}

func TestConfigTrust_Idempotent(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot)
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"}))
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"})) // no duplicate
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Equal(t, 1, strings.Count(string(b), projRoot))
}

func TestConfigTrust_JSON(t *testing.T) {
	origCwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot)
	// capture stdout via a custom Run variant if available; else assert exit + file.
	assert.Equal(t, ExitOK, Run([]string{"config", "trust", "-j"}))
}
