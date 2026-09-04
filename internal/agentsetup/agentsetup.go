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
	"regexp"
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

//go:embed templates/pi-mysql-write-guard.ts
var piExtensionScript []byte

//go:embed templates/codex-mysql-write-guard.py
var codexHookScript []byte

//go:embed templates/codex-mysql-cli.rules
var codexRules string

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
	content  []byte         // for writeFile / copyScript
	fragment map[string]any // for mergeJSON
}

// Agent is one supported AI agent.
type Agent struct {
	Name string
	Desc string
	Cap  Capability
	// posixHook marks agents whose hook command depends on POSIX shell
	// expansion ($HOME, ${VAR:-default}) and a python3 executable; those do
	// not work on native Windows (see windowsIncompatWarning).
	posixHook bool
	steps     func(InstallOpts) ([]step, error) // err = scope unsupported / unusable
}

// Agents is the ordered registry of supported agents.
var Agents = []Agent{claudeCode, codex, cursor, opencode, copilot, codebuddy, trae, pi}

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
	if msg := windowsIncompatWarning(runtime.GOOS, a); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
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

// windowsIncompatWarning returns a stderr warning ("" when fine) for agents
// whose hook command relies on POSIX-only features: native Windows shells do
// not expand $HOME or ${VAR:-default}, and python3 is usually absent, so the
// hook would silently fail to run and the write guard would be inactive. We
// still install the files (the agent may run under WSL/Git-Bash where they
// work), but never silently.
func windowsIncompatWarning(goos string, a Agent) string {
	if goos != "windows" || !a.posixHook {
		return ""
	}
	return fmt.Sprintf(
		"warning: %s's hook command relies on POSIX shell expansion ($HOME, ${VAR:-...}) and python3; "+
			"on native Windows it will likely fail to run and the mysql-cli write guard will be inactive. "+
			"Prefer running this agent under WSL/Git-Bash.",
		a.Name,
	)
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
		if err := writeFileAtomic(s.path, s.content, 0o644); err != nil {
			return "", err
		}
		return s.path, nil
	case actionCopyScript:
		// 与 actionWriteFile 一致的 Force 语义：已存在且未 --force 则跳过，
		// 避免重跑 agent init 静默覆盖用户对 hook 脚本的修改。
		if _, err := os.Stat(s.path); err == nil && !opts.Force {
			return "", nil // skip existing unless --force
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return "", err
		}
		if err := writeFileAtomic(s.path, s.content, 0o755); err != nil {
			return "", err
		}
		return s.path, nil
	case actionMergeJSON:
		return mergeJSONFile(s.path, s.fragment)
	}
	return "", fmt.Errorf("unknown action %d", s.action)
}

// writeFileAtomic durably replaces path with data: it writes a temp file in
// the same directory, applies mode, then renames over the target. A crash
// mid-write can only lose the temp file, never truncate the target (agents
// parse these JSON files at startup; a half-written file would break them).
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentsetup-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()        // no-op when already closed
		os.Remove(tmpName) // no-op after a successful rename
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// mergeJSONFile reads path (if present), deep-merges fragment into it (or
// starts from fragment if absent), backs up the original to .bak, and writes
// the result. Returns the path written.
//
// 备份与合并后的目标文件都沿用原文件的 mode（如原文件是 0600，备份与结果也是
// 0600），避免硬编码 0644 把 opencode.json 等可能内嵌 API key 的文件变成同机
// 其他用户可读。.bak 只在不存在时写入：重复运行 agent init 不得把"已合并过"
// 的内容覆盖进唯一备份，否则用户最初的手工配置永久丢失。所有写入走
// writeFileAtomic（同目录临时文件 + rename），崩溃不会留下截断的 JSON。
func mergeJSONFile(path string, fragment map[string]any) (string, error) {
	var existing map[string]any
	targetMode := os.FileMode(0o644)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return "", fmt.Errorf("parse %s: %w", path, err)
		}
		if info, err := os.Stat(path); err == nil {
			targetMode = info.Mode().Perm()
		}
		// Only create the backup when none exists yet, so .bak always holds
		// the pristine pre-install original instead of an already-merged copy.
		if _, err := os.Stat(path + ".bak"); os.IsNotExist(err) {
			if err := writeFileAtomic(path+".bak", data, targetMode); err != nil {
				return "", err
			}
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
	if err := writeFileAtomic(path, append(out, '\n'), targetMode); err != nil {
		return "", err
	}
	return path, nil
}

// deepMerge merges src into dst (mutating dst). Maps recurse; for slices, dst
// keeps its elements and appends src elements not already present (dedup by
// JSON serialization, except hook-group arrays which use business-key
// replacement, see mergeLists). Scalars in src overwrite dst.
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
				dst[k] = mergeLists(ds, ss)
				continue
			}
		}
		dst[k] = sv
	}
}

// mergeLists merges src array items into dst. Hook-group arrays (our
// PreToolUse fragments) use business-key replacement so a re-install UPDATES
// the entry we previously installed instead of appending a near-duplicate
// after the user tweaked it (e.g. changed a timeout) -- exact-JSON dedup alone
// would treat the tweaked copy as foreign and stack a second entry, causing
// double confirmation prompts. Everything else dedups by JSON serialization
// via dedupAppend.
func mergeLists(dst, src []any) []any {
	if len(src) > 0 && allHookGroups(src) {
		return mergeHookGroups(dst, src)
	}
	return dedupAppend(dst, src)
}

// allHookGroups reports whether every item is a PreToolUse-style hook group
// (an object carrying both "matcher" and "hooks").
func allHookGroups(items []any) bool {
	for _, v := range items {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := m["matcher"]; !ok {
			return false
		}
		if _, ok := m["hooks"]; !ok {
			return false
		}
	}
	return true
}

// hookGuardMarkerRe matches the guard script paths this package installs,
// e.g. `python3 "$HOME/.claude/hooks/mysql-write-guard.py"`. The mandatory
// leading path separator plus the .py/.ts extension keep user-owned scripts
// whose names merely CONTAIN the marker (like `my-mysql-write-guard.py`)
// from being misclassified as ours.
var hookGuardMarkerRe = regexp.MustCompile(`[\\/]mysql-write-guard\.(?:py|ts)\b`)

// isOurHookEntry reports whether a single hook entry references the guard
// script we install (even after user tweaks to unrelated fields).
func isOurHookEntry(entry any) bool {
	hm, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := hm["command"].(string)
	return hookGuardMarkerRe.MatchString(cmd)
}

// carriesOurHook reports whether a hook group's command list references the
// guard script we install.
func carriesOurHook(group map[string]any) bool {
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		if isOurHookEntry(h) {
			return true
		}
	}
	return false
}

// mergeHookGroups merges our hook groups into dst: an existing group with the
// same matcher that carries our guard script is updated IN PLACE -- its hooks
// array is merged entry by entry (see mergeHookGroupHooks) so user-owned
// entries in the same group survive a re-install; anything else is appended
// unless JSON-identical.
func mergeHookGroups(dst, src []any) []any {
	out := make([]any, len(dst))
	copy(out, dst)
	for _, s := range src {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := sm["matcher"].(string)
		replaced := false
		for i, d := range out {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			dmMatcher, _ := dm["matcher"].(string)
			if dmMatcher == matcher && carriesOurHook(dm) {
				out[i] = mergeHookGroupHooks(dm, sm)
				replaced = true
				break
			}
		}
		if !replaced && !containsCanon(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// mergeHookGroupHooks merges the hooks array of an existing group (dst) with
// the incoming fragment's (src). Our own previous entries (matched by the
// guard marker, possibly user-tweaked) are replaced by the incoming versions
// in place; user-owned entries in the same group are preserved; incoming
// entries without an in-place slot are appended (deduped). All other
// group-level keys come from src, since the fragment is authoritative for
// them.
func mergeHookGroupHooks(dst, src map[string]any) map[string]any {
	merged := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		merged[k] = v
	}
	for k, v := range src {
		if k != "hooks" {
			merged[k] = v
		}
	}
	dh, _ := dst["hooks"].([]any)
	sh, _ := src["hooks"].([]any)
	hooks := make([]any, 0, len(dh)+len(sh))
	placed := 0 // number of incoming entries consumed by in-place replacement
	for _, d := range dh {
		if isOurHookEntry(d) && placed < len(sh) {
			hooks = append(hooks, sh[placed]) // our old entry -> fresh version
			placed++
			continue
		}
		hooks = append(hooks, d) // user-owned entry (or surplus ours) kept
	}
	for _, s := range sh[placed:] {
		if !containsCanon(hooks, s) {
			hooks = append(hooks, s)
		}
	}
	// Collapse exact duplicates (e.g. a user hand-duplicating our entry).
	merged["hooks"] = dedupAny(hooks)
	return merged
}

// dedupAny returns items with later JSON-identical duplicates removed, order
// preserved.
func dedupAny(items []any) []any {
	seen := make(map[string]bool, len(items))
	out := make([]any, 0, len(items))
	for _, v := range items {
		c := canon(v)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, v)
	}
	return out
}

func containsCanon(items []any, v any) bool {
	c := canon(v)
	for _, it := range items {
		if canon(it) == c {
			return true
		}
	}
	return false
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
	// hookCmd uses $HOME / ${CLAUDE_PROJECT_DIR:-$PWD} + python3 (POSIX only).
	posixHook: true,
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

var codex = Agent{
	Name: "codex",
	Desc: "Codex (Rules prompt + PermissionRequest hook -> human approval)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		// Codex has no Claude-style PreToolUse "ask": the value is parsed but
		// unsupported, and an unsupported decision marks the hook failed while
		// the tool call CONTINUES. So enforcement is a two-layer combo instead:
		//
		//   rules (coarse gate): every mysql-cli invocation (plus common
		//     wrapper prefixes) gets decision="prompt", routing it into the
		//     approval path. prefix_rule cannot express flags at arbitrary
		//     positions, so it must gate on the program, not the flags.
		//   hook (precise filter): the PermissionRequest hook auto-allows only
		//     PROVEN read-only calls; writes and anything uncertain emit no
		//     decision, keeping Codex's native human-approval prompt.
		//
		// Project-local hooks/rules load only after the user trusts the
		// project's .codex layer; Install never auto-trusts (that human trust
		// boundary is part of the security model).
		var base, hookCmd string
		if o.Scope == ScopeGlobal {
			base, hookCmd = o.Home, `python3 "$HOME/.codex/hooks/mysql-write-guard.py"`
		} else {
			base = o.ProjectDir
			// Fallback to $PWD for non-git projects: git rev-parse failing
			// would substitute an empty path and the hook would never run
			// (fail-safe direction, but every call would silently degrade to
			// the native approval prompt).
			hookCmd = `python3 "$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")/.codex/hooks/mysql-write-guard.py"`
		}
		frag := permissionRequestFragment("^Bash$", hookCmd)
		return []step{
			{path: filepath.Join(base, ".codex", "hooks", "mysql-write-guard.py"), action: actionCopyScript, content: codexHookScript},
			{path: filepath.Join(base, ".codex", "hooks.json"), action: actionMergeJSON, fragment: frag},
			{path: filepath.Join(base, ".codex", "rules", "mysql-cli-write-guard.rules"), action: actionWriteFile, content: []byte(codexRules)},
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
		// NOTE (C9): the broad allow rule and the ask rules coexist on purpose
		// and depend on opencode's rule-evaluation semantics: rules are matched
		// in object order and the LAST matching rule wins (verified against
		// opencode's permission docs, https://opencode.ai/docs/permissions,
		// 2026-09; the docs' recommended idiom is wildcard rules first,
		// specific rules after). Go's encoding/json serializes map keys in
		// sorted order, and "mysql-cli *" is a strict string prefix of the ask
		// patterns, so the allow rule always lands BEFORE the ask rules in the
		// written file and write/ddl/yes calls resolve to "ask". If opencode
		// ever switches to first-match-wins or unordered semantics, this
		// fragment must be re-verified.
		return []step{{path: path, action: actionMergeJSON, fragment: frag}}, nil
	},
}

// copilotAutoApprovePattern is the autoApprove regex Copilot consults before
// auto-running a terminal command: any command mentioning mysql-cli followed
// (anywhere after it, even across quoted SQL containing ;|&) by one of the
// write flags forces a human confirmation prompt (value false).
//
// Design notes (C8):
//   - Anchoring on `mysql-cli` avoids prompting for unrelated tools whose
//     flags merely contain write/ddl/yes (e.g. `other-tool --write-cache`).
//   - `.*?` deliberately crosses quotes and ;|& separators: SQL literals
//     routinely contain `;`/`|`/`&`, so a `[^|;&]*` gap would silently miss
//     plain writes like `mysql-cli query "UPDATE a; UPDATE b" --write` and
//     wrapped writes like `bash -c "echo hi" ; mysql-cli --write`. Erring
//     towards extra prompts is the safer failure mode.
//   - `(=|[^\\w-]|$)` accepts the pflag `--write=true` form while rejecting
//     longer flags such as `--write-cache` / `--yesman`.
//   - `.` does not match newlines (JS regex); terminal commands agents run
//     are single-line in practice.
const copilotAutoApprovePattern = `/mysql-cli.*?--(write|ddl|yes)(=|[^\w-]|$)/`

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
		frag := map[string]any{
			"chat.tools.terminal.autoApprove": map[string]any{
				copilotAutoApprovePattern: false,
			},
		}
		if o.Scope == ScopeGlobal {
			// C10: install into every VS Code edition present on the machine
			// (stable "Code" and Insiders "Code - Insiders"); the returned
			// paths (also under DryRun) tell the user which ones were hit.
			// When neither exists (fresh machine), stable is targeted.
			for _, p := range vscodeUserSettings(o.Home) {
				steps = append(steps, step{path: p, action: actionMergeJSON, fragment: frag})
			}
			return steps, nil
		}
		steps = append(steps, step{
			path:     filepath.Join(o.ProjectDir, ".vscode", "settings.json"),
			action:   actionMergeJSON,
			fragment: frag,
		})
		return steps, nil
	},
}

var codebuddy = Agent{
	Name: "codebuddy",
	Desc: "CodeBuddy (PreToolUse hook -> ask)",
	Cap:  CapEnforce,
	// hookCmd uses $HOME / ${CLAUDE_PROJECT_DIR:-$PWD} + python3 (POSIX only).
	posixHook: true,
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

var trae = Agent{
	Name: "trae",
	Desc: "TRAE (PreToolUse hook -> ask, Claude Code compatible)",
	Cap:  CapEnforce,
	// hookCmd uses $HOME / ${TRAE_PROJECT_DIR:-...} + python3 (POSIX only).
	posixHook: true,
	steps: func(o InstallOpts) ([]step, error) {
		// TRAE's path layout is intentionally asymmetric (per official docs):
		//   global  -> ~/.trae-cn/hooks.json         (note the "-cn" suffix)
		//   project -> $PROJECT_FOLDER/.trae/hooks.json
		// Both international and China editions use the same paths.
		var base, hookPath, hookCmd string
		if o.Scope == ScopeGlobal {
			base = filepath.Join(o.Home, ".trae-cn")
			hookPath = filepath.Join(base, "hooks", "mysql-write-guard.py")
			hookCmd = `python3 "$HOME/.trae-cn/hooks/mysql-write-guard.py"`
		} else {
			base = filepath.Join(o.ProjectDir, ".trae")
			hookPath = filepath.Join(base, "hooks", "mysql-write-guard.py")
			// TRAE injects TRAE_PROJECT_DIR (and CLAUDE_PROJECT_DIR for compat);
			// fall back to $PWD so the command stays portable.
			hookCmd = `python3 "${TRAE_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$PWD}}/.trae/hooks/mysql-write-guard.py"`
		}
		// TRAE's standardized tool name for terminal execution is "RunCommand"
		// (not Claude Code's "Bash").
		frag := traeHookFragment("RunCommand", hookCmd)
		return []step{
			{path: hookPath, action: actionCopyScript, content: hookScript},
			{path: filepath.Join(base, "hooks.json"), action: actionMergeJSON, fragment: frag},
		}, nil
	},
}

var pi = Agent{
	Name: "pi",
	Desc: "Pi Coding Agent (tool_call hook -> ctx.ui.confirm -> block)",
	Cap:  CapEnforce,
	steps: func(o InstallOpts) ([]step, error) {
		// Pi auto-discovers extensions from:
		//   global  -> ~/.pi/agent/extensions/*.ts        (immediate, no trust)
		//   project -> <project>/.pi/extensions/*.ts      (requires /trust on first run)
		// Auto-discovery loads files directly; no settings.json change needed.
		// Reload with `/reload` inside pi after install.
		var dir string
		if o.Scope == ScopeGlobal {
			dir = filepath.Join(o.Home, ".pi", "agent", "extensions")
		} else {
			dir = filepath.Join(o.ProjectDir, ".pi", "extensions")
		}
		return []step{
			{
				path:    filepath.Join(dir, "mysql-write-guard.ts"),
				action:  actionWriteFile, // 0o644; skip if exists unless --force
				content: piExtensionScript,
			},
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

// permissionRequestFragment builds a Codex hooks.json fragment. Codex's
// hooks.json schema mirrors Claude Code's (matcher/hooks/type/command) with
// two additions used here: timeout (seconds, per hook) and statusMessage
// (shown while the hook runs). The matcher is a regex applied to the
// canonical tool name; "^Bash$" is Codex's shell-command tool.
func permissionRequestFragment(matcher, hookCmd string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"PermissionRequest": []any{
				map[string]any{
					"matcher": matcher,
					"hooks": []any{
						map[string]any{
							"type":          "command",
							"command":       hookCmd,
							"timeout":       10,
							"statusMessage": "Checking mysql-cli operation",
						},
					},
				},
			},
		},
	}
}

// traeHookFragment builds a TRAE-style hooks.json fragment. TRAE's hooks.json
// schema differs from Claude Code's settings.json: it has a top-level
// "version: 1" and each hook definition carries a "timeout" (default 30). The
// matcher uses TRAE's standardized tool name "RunCommand" (not "Bash").
func traeHookFragment(matcher, hookCmd string) map[string]any {
	return map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": matcher,
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": hookCmd,
							"timeout": 30,
						},
					},
				},
			},
		},
	}
}

// vscodeUserSettings returns the VS Code User settings.json paths to install
// into: every edition directory that exists on the machine (stable "Code" and
// Insiders "Code - Insiders" -- C10: Insiders uses its own "Code - Insiders"
// directory, not the stable one). When neither exists (fresh machine), the
// stable path is returned so a first run still gets configured.
func vscodeUserSettings(home string) []string {
	var stable, insiders string
	switch runtime.GOOS {
	case "darwin":
		stable = filepath.Join(home, "Library", "Application Support", "Code", "User")
		insiders = filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User")
	case "windows":
		stable = filepath.Join(home, "AppData", "Roaming", "Code", "User")
		insiders = filepath.Join(home, "AppData", "Roaming", "Code - Insiders", "User")
	default: // linux & friends
		stable = filepath.Join(home, ".config", "Code", "User")
		insiders = filepath.Join(home, ".config", "Code - Insiders", "User")
	}
	var paths []string
	for _, dir := range []string{stable, insiders} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			paths = append(paths, filepath.Join(dir, "settings.json"))
		}
	}
	if len(paths) == 0 {
		paths = []string{filepath.Join(stable, "settings.json")}
	}
	return paths
}
