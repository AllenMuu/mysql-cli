// Package bundle embeds the mysql-cli skill definitions (skills/*) into the
// binary so that `mysql-cli skill install` can install them with no external
// dependencies - no repo checkout, no package manager. The embedded tree is
// the single source of truth shared with scripts/install-skills.sh.
package bundle

import (
	"embed"
	"io/fs"
	"sort"
)

// Skills is the embedded skills/ directory tree.
//
//go:embed skills
var Skills embed.FS

// SkillNames returns the sorted names of the embedded skill directories.
func SkillNames() ([]string, error) {
	entries, err := Skills.ReadDir("skills")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// SkillFile returns the bytes of <skill>/SKILL.md.
func SkillFile(skill string) ([]byte, error) {
	return Skills.ReadFile("skills/" + skill + "/SKILL.md")
}

// SkillsFS returns the "skills" subtree, for walking during install.
func SkillsFS() (fs.FS, error) {
	return fs.Sub(Skills, "skills")
}
