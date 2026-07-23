package agents

import (
	"os"
	"path/filepath"
)

// Detect returns the agents present on the system, mirroring
// install-skills.sh detect_agents(). projectDir may be "".
func Detect(home, projectDir string) []string {
	var found []string
	any := func(paths ...string) bool {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	}
	if any(filepath.Join(home, ".claude"), filepath.Join(projectDir, ".claude")) {
		found = append(found, Claude)
	}
	if any(filepath.Join(home, ".cursor"), filepath.Join(projectDir, ".cursor")) {
		found = append(found, Cursor)
	}
	if any(filepath.Join(home, ".codex"), filepath.Join(projectDir, "AGENTS.md")) {
		found = append(found, Codex)
	}
	if any(filepath.Join(home, ".config", "opencode"), filepath.Join(projectDir, ".opencode")) {
		found = append(found, OpenCode)
	}
	if any(filepath.Join(projectDir, ".github")) {
		found = append(found, Copilot)
	}
	if any(filepath.Join(projectDir, ".windsurfrules"), filepath.Join(home, ".codeium"), filepath.Join(home, ".windsurf")) {
		found = append(found, Windsurf)
	}
	if any(filepath.Join(projectDir, ".aider.conf.yml"), filepath.Join(home, ".aider.conf.yml")) {
		found = append(found, Aider)
	}
	return found
}
