package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/AllenMuu/mysql-cli/internal/skillscheck"
	"github.com/stretchr/testify/assert"
)

func TestPrintSkillVersions(t *testing.T) {
	var buf bytes.Buffer
	assert.NoError(t, printSkillVersions(&buf))
	out := buf.String()
	for _, s := range []string{"mysql-shared", "mysql-query", "mysql-schema", "1.0.0"} {
		assert.Contains(t, out, s)
	}
}

func TestInstallSkillsWritesFiles(t *testing.T) {
	dir := t.TempDir()
	n, err := installSkills(dir)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, n, 3)
	for _, s := range []string{"mysql-shared", "mysql-query", "mysql-schema"} {
		_, err := os.Stat(filepath.Join(dir, s, "SKILL.md"))
		assert.NoError(t, err, "missing %s/SKILL.md", s)
	}
}

func TestInstallThenCheckAllOK(t *testing.T) {
	dir := t.TempDir()
	_, err := installSkills(dir)
	assert.NoError(t, err)
	results, err := skillscheck.Check(dir)
	assert.NoError(t, err)
	for _, r := range results {
		assert.Equal(t, skillscheck.StatusOK, r.Status, "%s: %s", r.Skill, r.Status)
	}
}

func TestSkillListRunExitZero(t *testing.T) {
	assert.Equal(t, ExitOK, Run([]string{"skill", "list"}))
}

func TestSkillInstallRunExitZero(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, ExitOK, Run([]string{"skill", "install", dir}))
	_, err := os.Stat(filepath.Join(dir, "mysql-shared", "SKILL.md"))
	assert.NoError(t, err)
}

func TestSkillCheckRunExitZero(t *testing.T) {
	dir := t.TempDir()
	// Empty dir: every skill is missing, but check still exits 0.
	assert.Equal(t, ExitOK, Run([]string{"skill", "check", dir}))
}
