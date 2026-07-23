// Package agents installs bundled mysql-cli skills into AI agents in each
// agent's native format. It is the Go port of scripts/install-skills.sh and
// depends only on the standard library; skill content is injected via fs.FS.
package agents

import (
	"errors"
	"fmt"
	"strings"
)

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

// Selection constants for Run.
const (
	SelAuto = "auto"
	SelAll  = "all"
)

// ValidAgent reports whether name is a known agent.
func ValidAgent(name string) bool {
	for _, a := range AllAgents {
		if a == name {
			return true
		}
	}
	return false
}

// Install installs all skills for one agent per opts.
func Install(agent string, opts Options) InstallResult {
	r := InstallResult{Agent: agent}
	installers := map[string]func(Options) ([]string, error){
		Claude:   installClaude,
		Cursor:   installCursor,
		Codex:    installCodex,
		OpenCode: installOpenCode,
		Copilot:  installCopilot,
		Windsurf: installWindsurf,
		Aider:    installAider,
	}
	fn, ok := installers[agent]
	if !ok {
		r.Status = "error"
		r.Error = fmt.Sprintf("unknown agent %q", agent)
		return r
	}
	paths, err := fn(opts)
	r.Paths = paths
	switch {
	case err == nil && len(paths) > 0:
		r.Status = "installed"
	case err == nil && len(paths) == 0:
		r.Status = "skipped"
	case errors.Is(err, ErrProjectOnly):
		r.Status = "skipped"
		r.Error = err.Error()
	default:
		r.Status = "error"
		r.Error = err.Error()
	}
	return r
}

// parseList splits a comma-separated agent selection, trimming whitespace.
func parseList(sel string) []string {
	var out []string
	for _, p := range strings.Split(sel, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Run resolves the agent selection and installs skills for each. For SelAuto,
// agents are detected via Detect; if none detected, defaults to Claude
// (matching install-skills.sh). Detected reflects actual presence.
func Run(sel string, opts Options) []InstallResult {
	present := Detect(opts.Home, opts.ProjectDir)
	presentSet := map[string]bool{}
	for _, a := range present {
		presentSet[a] = true
	}
	var targets []string
	switch sel {
	case SelAll:
		targets = append([]string(nil), AllAgents...)
	case SelAuto:
		targets = present
		if len(targets) == 0 {
			targets = []string{Claude}
		}
	default:
		targets = parseList(sel)
	}
	results := make([]InstallResult, 0, len(targets))
	for _, a := range targets {
		r := Install(a, opts)
		r.Detected = presentSet[a]
		results = append(results, r)
	}
	return results
}
