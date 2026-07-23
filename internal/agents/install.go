package agents

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrProjectOnly indicates an agent is project-only and --project-dir was not set.
var ErrProjectOnly = errors.New("agent is project-only; pass --project-dir")

// Options controls install behavior.
type Options struct {
	Home       string   // user home dir
	ProjectDir string   // project root ("" = no project-level install)
	NoGlobal   bool     // skip global install
	DryRun     bool     // report paths without writing
	FS         fs.FS    // skills subtree (contains <name>/SKILL.md)
	Names      []string // skill names to install
}

// InstallResult is the outcome for one agent.
type InstallResult struct {
	Agent    string   `json:"agent"`
	Detected bool     `json:"detected"`
	Paths    []string `json:"paths"`
	Status   string   `json:"status"` // installed | skipped | error
	Error    string   `json:"error,omitempty"`
}

// writeIfNotDryRun writes content to path, creating parent dirs. No-op on DryRun.
func writeIfNotDryRun(opts Options, path, content string) error {
	if opts.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// copySkillTree copies the embedded <skill>/... tree into dstDir, replacing any
// existing copy (idempotent). Mirrors `rm -rf; cp -r` in install_claude.
func copySkillTree(opts Options, dstDir, skill string) error {
	if opts.DryRun {
		return nil
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dstDir, skill)); err != nil {
		return err
	}
	return fs.WalkDir(opts.FS, skill, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dstDir, p)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := fs.ReadFile(opts.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// installClaude copies skill trees to ~/.claude/skills and (if ProjectDir set)
// <proj>/.claude/skills. Mirrors install-skills.sh install_claude.
func installClaude(opts Options) ([]string, error) {
	var paths []string
	if opts.ProjectDir != "" {
		t := filepath.Join(opts.ProjectDir, ".claude", "skills")
		for _, s := range opts.Names {
			if err := copySkillTree(opts, t, s); err != nil {
				return paths, err
			}
			paths = append(paths, filepath.Join(t, s, "SKILL.md"))
		}
	}
	if !opts.NoGlobal {
		t := filepath.Join(opts.Home, ".claude", "skills")
		for _, s := range opts.Names {
			if err := copySkillTree(opts, t, s); err != nil {
				return paths, err
			}
			paths = append(paths, filepath.Join(t, s, "SKILL.md"))
		}
	}
	return paths, nil
}

// installCursor writes .mdc rule files to <proj>/.cursor/rules and (if
// ~/.cursor exists) ~/.cursor/rules. Mirrors install-skills.sh install_cursor.
func installCursor(opts Options) ([]string, error) {
	var paths []string
	writeMDC := func(dir string) error {
		for _, s := range opts.Names {
			data, err := fs.ReadFile(opts.FS, s+"/SKILL.md")
			if err != nil {
				return err
			}
			p := filepath.Join(dir, s+".mdc")
			if err := writeIfNotDryRun(opts, p, makeMDC(s, SkillBody(string(data)))); err != nil {
				return err
			}
			paths = append(paths, p)
		}
		return nil
	}
	if opts.ProjectDir != "" {
		if err := writeMDC(filepath.Join(opts.ProjectDir, ".cursor", "rules")); err != nil {
			return paths, err
		}
	}
	if !opts.NoGlobal {
		gdir := filepath.Join(opts.Home, ".cursor")
		if _, err := os.Stat(gdir); err == nil { // bash guard: only if ~/.cursor exists
			if err := writeMDC(filepath.Join(gdir, "rules")); err != nil {
				return paths, err
			}
		}
	}
	return paths, nil
}
