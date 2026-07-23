// Package skillscheck compares installed mysql-cli skills (e.g. under
// ~/.claude/skills) against the versions bundled into the binary, reporting
// missing or stale skills. It mirrors the skillscheck concept from
// larksuite/cli, adapted to mysql-cli's per-process, connection-less model:
// the check runs only when explicitly invoked via `mysql-cli skill check`,
// never on the hot query path.
package skillscheck

import (
	"os"
	"path/filepath"
	"regexp"

	bundle "github.com/AllenMuu/mysql-cli"
)

// Status values reported per skill.
const (
	StatusOK      = "ok"      // installed and version matches
	StatusStale   = "stale"   // installed but version differs
	StatusMissing = "missing" // not installed
	StatusUnknown = "unknown" // installed but version unparseable
)

var versionRe = regexp.MustCompile(`(?m)^version:\s*"?([0-9]+\.[0-9]+\.[0-9]+)`)

// ParseVersion extracts a semver version from a SKILL.md frontmatter.
func ParseVersion(content string) string {
	m := versionRe.FindStringSubmatch(content)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// Result is the check outcome for one skill.
type Result struct {
	Skill        string `json:"skill"`
	Installed    bool   `json:"installed"`
	InstalledVer string `json:"installed_version,omitempty"`
	ExpectedVer  string `json:"expected_version"`
	Status       string `json:"status"`
	Path         string `json:"path"`
}

// Check scans targetDir for each bundled skill and compares its installed
// version frontmatter against the bundled version.
func Check(targetDir string) ([]Result, error) {
	names, err := bundle.SkillNames()
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(names))
	for _, name := range names {
		r := Result{Skill: name, ExpectedVer: expectedVersion(name)}
		r.Path = filepath.Join(targetDir, name, "SKILL.md")
		data, err := os.ReadFile(r.Path)
		if err != nil {
			r.Status = StatusMissing
			results = append(results, r)
			continue
		}
		r.Installed = true
		r.InstalledVer = ParseVersion(string(data))
		switch {
		case r.InstalledVer == "":
			r.Status = StatusUnknown
		case r.ExpectedVer != "" && r.InstalledVer != r.ExpectedVer:
			r.Status = StatusStale
		default:
			r.Status = StatusOK
		}
		results = append(results, r)
	}
	return results, nil
}

func expectedVersion(skill string) string {
	data, err := bundle.SkillFile(skill)
	if err != nil {
		return ""
	}
	return ParseVersion(string(data))
}
