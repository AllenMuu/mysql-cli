package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetect_None(t *testing.T) {
	tmp := t.TempDir()
	assert.Empty(t, Detect(tmp, t.TempDir()))
}

func TestDetect_ClaudeGlobal(t *testing.T) {
	home := t.TempDir()
	requireDir(t, home, ".claude")
	assert.Equal(t, []string{Claude}, Detect(home, t.TempDir()))
}

func TestDetect_CopilotProjectOnly(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	requireDir(t, proj, ".github")
	got := Detect(home, proj)
	assert.Contains(t, got, Copilot)
	assert.NotContains(t, got, Claude)
}

func TestDetect_AiderGlobal(t *testing.T) {
	home := t.TempDir()
	requireFile(t, home, ".aider.conf.yml", "read: [.aider.instructions.md]\n")
	got := Detect(home, t.TempDir())
	assert.Contains(t, got, Aider)
}

func TestDetect_AllPresent(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	requireDir(t, home, ".claude")
	requireDir(t, home, ".cursor")
	requireDir(t, home, ".codex")
	requireDir(t, home, ".config/opencode")
	requireDir(t, proj, ".github")
	requireFile(t, proj, ".windsurfrules", "")
	requireFile(t, home, ".aider.conf.yml", "")
	got := Detect(home, proj)
	assert.Equal(t, AllAgents, got)
}

func requireDir(t *testing.T, parts ...string) {
	t.Helper()
	requireNoErr(t, os.MkdirAll(filepath.Join(parts...), 0o755))
}
func requireFile(t *testing.T, parts ...string) {
	t.Helper()
	// last element is content, preceding elements are path components
	path := filepath.Join(parts[:len(parts)-1]...)
	content := parts[len(parts)-1]
	requireNoErr(t, os.WriteFile(path, []byte(content), 0o644))
}
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
