package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseOpts(home, proj string) Options {
	return Options{Home: home, ProjectDir: proj, FS: testFS(), Names: testNames}
}

func TestInstallClaude_GlobalAndProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	paths, err := installClaude(baseOpts(home, proj))
	require.NoError(t, err)
	for _, s := range testNames {
		assert.FileExists(t, filepath.Join(home, ".claude", "skills", s, "SKILL.md"))
		assert.FileExists(t, filepath.Join(proj, ".claude", "skills", s, "SKILL.md"))
	}
	assert.NotEmpty(t, paths)
}

func TestInstallClaude_GlobalOnlyByDefault(t *testing.T) {
	home := t.TempDir()
	paths, err := installClaude(baseOpts(home, ""))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(home, ".claude", "skills", "mysql-query", "SKILL.md"))
	assert.NotEmpty(t, paths) // ProjectDir=="" short-circuits project install; no cwd check needed
}

func TestInstallClaude_NoGlobal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	o := baseOpts(home, proj)
	o.NoGlobal = true
	_, err := installClaude(o)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
	assert.FileExists(t, filepath.Join(proj, ".claude", "skills", "mysql-shared", "SKILL.md"))
}

func TestInstallClaude_DryRun(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	o := baseOpts(home, proj)
	o.DryRun = true
	paths, err := installClaude(o)
	require.NoError(t, err)
	// DryRun must report paths but write nothing.
	assert.NotEmpty(t, paths)
	assert.NoDirExists(t, filepath.Join(proj, ".claude"))
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
}

func TestInstallCursor_MDCFiles(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cursor"), 0o755))
	_, err := installCursor(baseOpts(home, proj))
	require.NoError(t, err)
	body, _ := os.ReadFile(filepath.Join(proj, ".cursor", "rules", "mysql-query.mdc"))
	assert.Contains(t, string(body), "description: Run SQL with mysql-cli")
	assert.Contains(t, string(body), "globs: *.sql")
	assert.FileExists(t, filepath.Join(home, ".cursor", "rules", "mysql-shared.mdc"))
}

func TestInstallCursor_DryRun(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// Create ~/.cursor so the global branch is entered (otherwise the
	// existence guard would hide DryRun's effect on the global branch).
	requireDir(t, home, ".cursor")
	o := baseOpts(home, proj)
	o.DryRun = true
	paths, err := installCursor(o)
	require.NoError(t, err)
	// DryRun must report paths but write no .mdc files anywhere.
	assert.NotEmpty(t, paths)
	assert.NoDirExists(t, filepath.Join(proj, ".cursor", "rules"))
	assert.NoDirExists(t, filepath.Join(home, ".cursor", "rules"))
}

func TestInstallCursor_SkipsGlobalWhenCursorMissing(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// Deliberately do NOT create ~/.cursor; NoGlobal=false.
	o := baseOpts(home, proj)
	paths, err := installCursor(o)
	require.NoError(t, err)
	// Project .mdc files must exist; global ~/.cursor/rules must not.
	for _, s := range testNames {
		assert.FileExists(t, filepath.Join(proj, ".cursor", "rules", s+".mdc"))
	}
	assert.NoDirExists(t, filepath.Join(home, ".cursor", "rules"))
	assert.NotEmpty(t, paths)
}

func TestInstallCursor_NoGlobal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	// ~/.cursor exists but NoGlobal=true must still skip the global branch.
	requireDir(t, home, ".cursor")
	o := baseOpts(home, proj)
	o.NoGlobal = true
	paths, err := installCursor(o)
	require.NoError(t, err)
	for _, s := range testNames {
		assert.FileExists(t, filepath.Join(proj, ".cursor", "rules", s+".mdc"))
	}
	assert.NoDirExists(t, filepath.Join(home, ".cursor", "rules"))
	assert.NotEmpty(t, paths)
}
