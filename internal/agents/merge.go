package agents

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const (
	beginMarker = "<!-- mysql-cli skill: begin (auto-generated) -->"
	endMarker   = "<!-- mysql-cli skill: end -->"
	updateNote  = "<!-- Re-run mysql-cli init to update. Do not edit between markers. -->"
)

// cursorDescriptions mirrors the make_mdc description args in install-skills.sh.
var cursorDescriptions = map[string]string{
	"mysql-shared": "mysql-cli shared rules: config, datasource, safety model, exit codes, error recovery, output formats",
	"mysql-query":  "Run SQL with mysql-cli: SELECT query, txn, DML (INSERT/UPDATE/DELETE), DDL",
	"mysql-schema": "Explore MySQL schema with mysql-cli: tables, databases, schema, sample, read, explore, analyze",
}

// isFrontmatterDelim reports whether line is a "---" frontmatter delimiter
// (optional trailing whitespace), matching install-skills.sh /^---[[:space:]]*$/.
func isFrontmatterDelim(line string) bool {
	return strings.TrimRight(line, " \t\r") == "---"
}

// SkillBody returns the body of a SKILL.md: content after the second "---"
// frontmatter delimiter. Mirrors install-skills.sh skill_body().
func SkillBody(content string) string {
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if isFrontmatterDelim(ln) {
			// find the next delim after i
			for j := i + 1; j < len(lines); j++ {
				if isFrontmatterDelim(lines[j]) {
					return strings.Join(lines[j+1:], "\n")
				}
			}
		}
	}
	return ""
}

// mergedBody concatenates all skill bodies in canonical (sorted) order,
// mirroring install-skills.sh skill_body_concat().
func mergedBody(fsys fs.FS, names []string) (string, error) {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var b strings.Builder
	for _, name := range sorted {
		data, err := fs.ReadFile(fsys, name+"/SKILL.md")
		if err != nil {
			return "", fmt.Errorf("read skill %s: %w", name, err)
		}
		b.WriteString("\n## mysql-cli skill: ")
		b.WriteString(name)
		b.WriteString("\n\n")
		b.WriteString(SkillBody(string(data)))
	}
	return b.String(), nil
}

// stripMarkedBlock removes the begin..end marker block (inclusive) from content.
func stripMarkedBlock(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	skip := false
	for _, ln := range lines {
		if ln == beginMarker {
			skip = true
			continue
		}
		if ln == endMarker && skip {
			skip = false
			continue
		}
		if !skip {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

// MergeInstructionFile returns instruction-file content after idempotently
// replacing the marked mysql-cli block with merged. Absent block => append.
// Properly idempotent (no whitespace accumulation; improves on the bash script).
func MergeInstructionFile(existing, merged string) string {
	base := stripMarkedBlock(existing)
	base = strings.TrimRight(base, "\n\r ")
	merged = strings.TrimRight(merged, "\n\r ")
	if base != "" {
		base += "\n\n"
	}
	return base + beginMarker + "\n" + updateNote + "\n" + merged + "\n" + endMarker + "\n"
}

// makeMDC renders a Cursor .mdc rule file from a skill name + SKILL.md body.
func makeMDC(skill, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: ")
	b.WriteString(cursorDescriptions[skill])
	b.WriteString("\n")
	b.WriteString("globs: *.sql\n")
	b.WriteString("alwaysApply: false\n")
	b.WriteString("---\n")
	b.WriteString(body)
	return b.String()
}
