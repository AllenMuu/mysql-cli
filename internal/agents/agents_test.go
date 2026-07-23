package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidAgent(t *testing.T) {
	assert.True(t, ValidAgent("claude"))
	assert.False(t, ValidAgent("nope"))
}

func TestInstall_UnknownAgent(t *testing.T) {
	r := Install("nope", baseOpts(t.TempDir(), ""))
	assert.Equal(t, "error", r.Status)
	assert.Contains(t, r.Error, "unknown agent")
}

func TestInstall_CopilotSkippedWithoutProjectDir(t *testing.T) {
	r := Install(Copilot, baseOpts(t.TempDir(), ""))
	assert.Equal(t, "skipped", r.Status)
}

func TestInstall_ClaudeSuccess(t *testing.T) {
	r := Install(Claude, baseOpts(t.TempDir(), ""))
	assert.Equal(t, "installed", r.Status)
	assert.NotEmpty(t, r.Paths)
}

func TestRun_All(t *testing.T) {
	res := Run(SelAll, baseOpts(t.TempDir(), ""))
	assert.Len(t, res, len(AllAgents))
	// copilot has no project dir -> skipped; rest installed (global)
	for _, r := range res {
		if r.Agent == Copilot {
			assert.Equal(t, "skipped", r.Status)
		} else {
			assert.Equal(t, "installed", r.Status, r.Agent)
		}
	}
}

func TestRun_AutoDefaultsToClaudeWhenNoneDetected(t *testing.T) {
	res := Run(SelAuto, baseOpts(t.TempDir(), t.TempDir()))
	assert.Len(t, res, 1)
	assert.Equal(t, Claude, res[0].Agent)
	assert.True(t, res[0].Detected == false) // not actually present
}

func TestRun_AutoDetectsPresent(t *testing.T) {
	home := t.TempDir()
	requireDir(t, home, ".claude")
	res := Run(SelAuto, baseOpts(home, t.TempDir()))
	assert.Len(t, res, 1)
	assert.Equal(t, Claude, res[0].Agent)
	assert.True(t, res[0].Detected)
}

func TestRun_CommaList(t *testing.T) {
	res := Run("claude,cursor", baseOpts(t.TempDir(), t.TempDir()))
	assert.Len(t, res, 2)
	assert.Equal(t, Claude, res[0].Agent)
	assert.Equal(t, Cursor, res[1].Agent)
}

func TestParseList(t *testing.T) {
	assert.Equal(t, []string{Claude, Cursor}, parseList("claude, cursor "))
}
