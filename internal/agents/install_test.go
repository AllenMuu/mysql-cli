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
	assert.NoDirExists(t, filepath.Join(".", ".claude")) // no project install without --project-dir
	_ = paths
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
	o := baseOpts(home, "")
	o.DryRun = true
	_, err := installCursor(o)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(home, ".cursor"))
}
