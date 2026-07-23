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
	home := t.TempDir()
	requireDir(t, home, ".claude") // Claude present -> Detected must be true
	res := Run(SelAll, baseOpts(home, ""))
	assert.Len(t, res, len(AllAgents))
	// copilot has no project dir -> skipped; rest installed (global)
	for _, r := range res {
		if r.Agent == Copilot {
			assert.Equal(t, "skipped", r.Status)
		} else {
			assert.Equal(t, "installed", r.Status, r.Agent)
		}
	}
	// Lock the Detected field in SelAll mode: presentSet is consulted for
	// every result, not just SelAuto. Claude is present (via ~/.claude);
	// Copilot is not (no .github anywhere). If r.Detected = presentSet[a]
	// were moved inside case SelAuto, claudeResult.Detected would be false.
	var claudeResult, copilotResult *InstallResult
	for i := range res {
		switch res[i].Agent {
		case Claude:
			claudeResult = &res[i]
		case Copilot:
			copilotResult = &res[i]
		}
	}
	assert.True(t, claudeResult.Detected, "Claude must be detected when ~/.claude exists")
	assert.False(t, copilotResult.Detected, "Copilot must not be detected without .github")
}

func TestRun_AutoDefaultsToClaudeWhenNoneDetected(t *testing.T) {
	res := Run(SelAuto, baseOpts(t.TempDir(), t.TempDir()))
	assert.Len(t, res, 1)
	assert.Equal(t, Claude, res[0].Agent)
	assert.False(t, res[0].Detected) // not actually present
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
	home := t.TempDir()
	requireDir(t, home, ".claude") // Claude present, Cursor not -> lock Detected in comma-list mode
	res := Run("claude,cursor", baseOpts(home, t.TempDir()))
	assert.Len(t, res, 2)
	assert.Equal(t, Claude, res[0].Agent)
	assert.Equal(t, Cursor, res[1].Agent)
	// Lock Detected in comma-list mode: presentSet is consulted per result
	// regardless of selection mode. If r.Detected = presentSet[a] were
	// moved inside case SelAuto, res[0].Detected would be false here.
	assert.True(t, res[0].Detected, "Claude must be detected when ~/.claude exists")
	assert.False(t, res[1].Detected, "Cursor must not be detected without ~/.cursor")
}

func TestParseList(t *testing.T) {
	assert.Equal(t, []string{Claude, Cursor}, parseList("claude, cursor "))
}
