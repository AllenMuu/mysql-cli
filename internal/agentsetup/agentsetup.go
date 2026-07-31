// Package agentsetup installs per-agent write-confirmation configs for
// mysql-cli. It is the engine behind `mysql-cli agent init`: each supported
// agent declares the files it needs (write-new, merge-into-JSON, or copy a
// hook script), and Install materializes them at the project or global scope.
package agentsetup

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed templates/mysql-write-guard.py
var hookScript []byte

//go:embed templates/cursor-rule.mdc
var cursorRule string

//go:embed templates/copilot-instructions.md
var copilotInstructions string

//go:embed templates/codebuddy.md
var codebuddyRule string

// Capability classifies how forcefully an agent guards writes.
type Capability int

const (
	CapEnforce Capability = iota // engine-level gate (hook/permission/regex)
	CapGuide                     // context instruction only
)

func (c Capability) String() string {
	if c == CapEnforce {
		return "enforce"
	}
	return "guide"
}

// Scope selects where configs are written.
type Scope int

const (
	ScopeProject Scope = iota
	ScopeGlobal
)

func (s Scope) String() string {
	if s == ScopeGlobal {
		return "global"
	}
	return "project"
}

// InstallOpts carries scope, target dirs, and behavior flags.
type InstallOpts struct {
	Scope      Scope
	Force      bool   // overwrite single-file configs that already exist
	DryRun     bool   // describe actions, write nothing
	ProjectDir string // cwd; used for ScopeProject
	Home       string // home dir; used for ScopeGlobal
}

// fileAction says how a step touches its target file.
type fileAction int

const (
	actionWriteFile  fileAction = iota // write content; skip if exists unless Force
	actionMergeJSON                    // deep-merge fragment into existing JSON (backup .bak)
	actionCopyScript                   // write executable script (overwrite)
)

// step is one file operation performed during Install.
type step struct {
	path     string
	action   fileAction
	content  []byte          // for writeFile / copyScript
	fragment map[string]any  // for mergeJSON
}

// Agent is one supported AI agent.
type Agent struct {
	Name  string
	Desc  string
	Cap   Capability
	steps func(InstallOpts) ([]step, error) // err = scope unsupported / unusable
}

// Agents is the ordered registry of supported agents.
var Agents = []Agent{claudeCode, cursor, opencode, copilot, codebuddy}

// Lookup returns the agent with the given name.
func Lookup(name string) (Agent, bool) {
	for _, a := range Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Names returns all agent names in registry order.
func Names() []string {
	out := make([]string, len(Agents))
	for i, a := range Agents {
		out[i] = a.Name
	}
	return out
}

// Install materializes the agent's steps. Returns the paths written (or that
// would be written under DryRun), in order.
func (a Agent) Install(opts InstallOpts) ([]string, error) {
	if a.steps == nil {
		return nil, fmt.Errorf("agent %q has no install steps", a.Name)
	}
	steps, err := a.steps(opts)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, s := range steps {
		w, err := execStep(s, opts)
		if err != nil {
			return written, err
		}
		if w != "" {
			written = append(written, w)
		}
	}
	return written, nil
}

func execStep(s step, opts InstallOpts) (string, error) {
	if opts.DryRun {
		verb := "write"
		if s.action == actionMergeJSON {
			verb = "merge"
		}
		return fmt.Sprintf("%s  %s", verb, s.path), nil
	}
	switch s.action {
	case actionWriteFile:
		if _, err := os.Stat(s.path); err == nil && !opts.Force {
			return "", nil // skip existing unless --force
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(s.path, s.content, 0o644); err != nil {
			return "", err
		}
		return s.path, nil
	case actionCopyScript:
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(s.path, s.content, 0o755); err != nil {
			return "", err
		}
		return s.path, nil
	case actionMergeJSON:
		return mergeJSONFile(s.path, s.fragment)
	}
	return "", fmt.Errorf("unknown action %d", s.action)
}

// mergeJSONFile reads path (if present), deep-merges fragment into it (or
// starts from fragment if absent), backs up the original to .bak, and writes
// the result. Returns the path written.
func mergeJSONFile(path string, fragment map[string]any) (string, error) {
	var existing map[string]any
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
		if err := os.WriteFile(path+".bak", data, 0o644); err != nil {
			return "", err
		}
	}
	dst := existing
	if dst == nil {
		dst = map[string]any{}
	}
	deepMerge(dst, fragment)
	out, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// deepMerge merges src into dst (mutating dst). Maps recurse; for slices, dst
// keeps its elements and appends src elements not already present (dedup by
// JSON serialization). Scalars in src overwrite dst.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok {
			dst[k] = sv
			continue
		}
		if sm, sok := sv.(map[string]any); sok {
			if dm, dok := dv.(map[string]any); dok {
				deepMerge(dm, sm)
				continue
			}
		}
		if ss, sok := sv.([]any); sok {
			if ds, dok := dv.([]any); dok {
				dst[k] = dedupAppend(ds, ss)
				continue
			}
		}
		dst[k] = sv
	}
}

// dedupAppend appends src items to dst, skipping any whose JSON serialization
// already appears in dst. Order: dst preserved, new src items appended.
func dedupAppend(dst, src []any) []any {
	seen := make(map[string]bool, len(dst)+len(src))
	for _, v := range dst {
		seen[canon(v)] = true
	}
	for _, v := range src {
		c := canon(v)
		if !seen[c] {
			seen[c] = true
			dst = append(dst, v)
		}
	}
	return dst
}

// canon returns a stable JSON string for dedup comparison.
func canon(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// --- agent definitions ---

var claudeCode = Agent{
	Name: "claude",
	Desc: "Claude Code (PreToolUse hook -> ask)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		var base, hookCmd string
		if o.Scope == ScopeGlobal {
			base, hookCmd = o.Home, `python3 "$HOME/.claude/hooks/mysql-write-guard.py"`
		} else {
			base, hookCmd = o.ProjectDir, `python3 "${CLAUDE_PROJECT_DIR:-$PWD}/.claude/hooks/mysql-write-guard.py"`
		}
		frag := preToolUseFragment("Bash", hookCmd)
		return []step{
			{path: filepath.Join(base, ".claude", "hooks", "mysql-write-guard.py"), action: actionCopyScript, content: hookScript},
			{path: filepath.Join(base, ".claude", "settings.json"), action: actionMergeJSON, fragment: frag},
		}, nil
	},
}

var cursor = Agent{
	Name: "cursor",
	Desc: "Cursor (.cursor/rules, guide only)",
	Cap:  CapGuide,
	steps: func(o InstallOpts) ([]step, error) {
		if o.Scope == ScopeGlobal {
			return nil, errors.New("cursor: global scope not supported (Cursor user rules live in IDE settings); use --project")
		}
		return []step{
			{path: filepath.Join(o.ProjectDir, ".cursor", "rules", "mysql-cli-write-guard.mdc"),
				action: actionWriteFile, content: []byte(cursorRule)},
		}, nil
	},
}

var opencode = Agent{
	Name: "opencode",
	Desc: "opencode (permission.bash glob -> ask)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		var path string
		if o.Scope == ScopeGlobal {
			path = filepath.Join(o.Home, ".config", "opencode", "opencode.json")
		} else {
			path = filepath.Join(o.ProjectDir, "opencode.json")
		}
		frag := map[string]any{
			"permission": map[string]any{
				"bash": map[string]any{
					"mysql-cli *":         "allow",
					"mysql-cli *--write*": "ask",
					"mysql-cli *--ddl*":   "ask",
					"mysql-cli *--yes*":   "ask",
				},
			},
		}
		return []step{{path: path, action: actionMergeJSON, fragment: frag}}, nil
	},
}

var copilot = Agent{
	Name: "copilot",
	Desc: "GitHub Copilot (autoApprove regex -> false)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		var steps []step
		if o.Scope == ScopeProject {
			steps = append(steps, step{
				path:   filepath.Join(o.ProjectDir, ".github", "copilot-instructions.md"),
				action: actionWriteFile, content: []byte(copilotInstructions),
			})
		}
		var settingsPath string
		if o.Scope == ScopeGlobal {
			p, err := vscodeUserSettings(o.Home)
			if err != nil {
				return nil, err
			}
			settingsPath = p
		} else {
			settingsPath = filepath.Join(o.ProjectDir, ".vscode", "settings.json")
		}
		frag := map[string]any{
			"chat.tools.terminal.autoApprove": map[string]any{
				"/--(write|ddl|yes)(\\b|=)/": false,
			},
		}
		steps = append(steps, step{path: settingsPath, action: actionMergeJSON, fragment: frag})
		return steps, nil
	},
}

var codebuddy = Agent{
	Name: "codebuddy",
	Desc: "CodeBuddy (PreToolUse hook -> ask)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		var base, hookCmd string
		if o.Scope == ScopeGlobal {
			base, hookCmd = o.Home, `python3 "$HOME/.codebuddy/hooks/mysql-write-guard.py"`
		} else {
			base, hookCmd = o.ProjectDir, `python3 "${CLAUDE_PROJECT_DIR:-$PWD}/.codebuddy/hooks/mysql-write-guard.py"`
		}
		frag := preToolUseFragment("Bash", hookCmd)
		return []step{
			{path: filepath.Join(base, ".codebuddy", "hooks", "mysql-write-guard.py"), action: actionCopyScript, content: hookScript},
			{path: filepath.Join(base, ".codebuddy", "settings.json"), action: actionMergeJSON, fragment: frag},
		}, nil
	},
}

// preToolUseFragment builds a hooks.PreToolUse fragment for matcher/bash-hook.
func preToolUseFragment(matcher, hookCmd string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": matcher,
					"hooks": []any{
						map[string]any{"type": "command", "command": hookCmd},
					},
				},
			},
		},
	}
}

// vscodeUserSettings returns the VS Code User settings.json path for the OS.
func vscodeUserSettings(home string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json"), nil
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", "settings.json"), nil
	default: // linux & friends
		return filepath.Join(home, ".config", "Code", "User", "settings.json"), nil
	}
}
