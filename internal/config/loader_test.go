package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// helper: build a fake project tree under a temp home.
func makeProjectTree(t *testing.T, home string, relPath string) {
	t.Helper()
	p := filepath.Join(home, relPath)
	assert.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	assert.NoError(t, os.WriteFile(p, []byte("# stub"), 0o600))
}

func TestDiscoverProject_FoundAtCwd(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	makeProjectTree(t, root, ".config/mysql-cli/config.toml")
	gotRoot, gotPath, found := DiscoverProject(root, home)
	assert.True(t, found)
	assert.Equal(t, root, gotRoot)
	assert.Equal(t, filepath.Join(root, ".config/mysql-cli/config.toml"), gotPath)
}

func TestDiscoverProject_FoundInAncestor(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	makeProjectTree(t, root, ".config/mysql-cli/config.toml")
	// cwd is a subdir of root
	cwd := filepath.Join(root, "a", "b")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	gotRoot, _, found := DiscoverProject(cwd, home)
	assert.True(t, found)
	assert.Equal(t, root, gotRoot)
}

func TestDiscoverProject_StopsAtHome(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "proj", "sub")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found) // nothing above cwd until home (home itself is boundary, not searched as project)
}

func TestDiscoverProject_HomeGlobalConfigIsNotProject(t *testing.T) {
	home := t.TempDir()
	// global config lives at home (shared relative path) - must NOT be treated as a project
	makeProjectTree(t, home, relConfigPath)
	cwd := filepath.Join(home, "sub")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found, "home's global config must not be treated as a project root")
}

func TestDiscoverProject_NotFound(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "x")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found)
}