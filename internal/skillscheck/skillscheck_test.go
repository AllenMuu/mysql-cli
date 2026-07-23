package skillscheck

import (
	"os"
	"path/filepath"
	"testing"

	bundle "github.com/AllenMuu/mysql-cli"
)

func TestParseVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"---\nversion: 1.2.3\n---\n", "1.2.3"},
		{"---\nversion: \"0.9.0\"\n---\n", "0.9.0"},
		{"---\nname: x\nversion: 10.20.30\n---\n", "10.20.30"},
		{"---\nname: x\n---\n", ""},
		{"no frontmatter at all", ""},
	}
	for _, c := range cases {
		if got := ParseVersion(c.in); got != c.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	names, err := bundle.SkillNames()
	if err != nil {
		t.Fatalf("SkillNames: %v", err)
	}
	if len(names) < 3 {
		t.Fatalf("need >=3 bundled skills, got %d", len(names))
	}

	dir := t.TempDir()
	// names[0]: installed at expected version -> ok
	// names[1]: installed at a bogus version -> stale
	// names[2]: not installed -> missing
	data0, err := bundle.SkillFile(names[0])
	if err != nil {
		t.Fatalf("SkillFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, names[0]), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, names[0], "SKILL.md"), data0, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, names[1]), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "---\nname: " + names[1] + "\nversion: 0.0.1\ndescription: stale\nmetadata:\n  binary: mysql-cli\n---\n# stale\n"
	if err := os.WriteFile(filepath.Join(dir, names[1], "SKILL.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Check(dir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	byName := map[string]Result{}
	for _, r := range results {
		byName[r.Skill] = r
	}
	if got := byName[names[0]].Status; got != StatusOK {
		t.Errorf("names[0] status = %q, want %q", got, StatusOK)
	}
	if got := byName[names[1]].Status; got != StatusStale {
		t.Errorf("names[1] status = %q, want %q", got, StatusStale)
	}
	if got := byName[names[2]].Status; got != StatusMissing {
		t.Errorf("names[2] status = %q, want %q", got, StatusMissing)
	}
}
