// Package agents installs bundled mysql-cli skills into AI agents in each
// agent's native format. It is the Go port of scripts/install-skills.sh and
// depends only on the standard library; skill content is injected via fs.FS.
package agents

// Agent name constants.
const (
	Claude   = "claude"
	Cursor   = "cursor"
	Codex    = "codex"
	OpenCode = "opencode"
	Copilot  = "copilot"
	Windsurf = "windsurf"
	Aider    = "aider"
)

// AllAgents is the canonical ordered list, matching install-skills.sh.
var AllAgents = []string{Claude, Cursor, Codex, OpenCode, Copilot, Windsurf, Aider}
