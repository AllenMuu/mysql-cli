package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// helper: build a fake project tree under a temp home.
func makeProjectTree(t *testing.T, home string, relPath string) {
	t.Helper()
	p := filepath.Join(home, relPath)
	assert.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	assert.NoError(t, os.WriteFile(p, []byte("# stub"), 0o600))
}

func TestDiscoverProject_FoundAtCwd(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	makeProjectTree(t, root, ".config/mysql-cli/config.toml")
	gotRoot, gotPath, found := DiscoverProject(root, home)
	assert.True(t, found)
	assert.Equal(t, root, gotRoot)
	assert.Equal(t, filepath.Join(root, ".config/mysql-cli/config.toml"), gotPath)
}

func TestDiscoverProject_FoundInAncestor(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	makeProjectTree(t, root, ".config/mysql-cli/config.toml")
	// cwd is a subdir of root
	cwd := filepath.Join(root, "a", "b")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	gotRoot, _, found := DiscoverProject(cwd, home)
	assert.True(t, found)
	assert.Equal(t, root, gotRoot)
}

func TestDiscoverProject_StopsAtHome(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "proj", "sub")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found) // nothing above cwd until home (home itself is boundary, not searched as project)
}

func TestDiscoverProject_HomeGlobalConfigIsNotProject(t *testing.T) {
	home := t.TempDir()
	// global config lives at home (shared relative path) - must NOT be treated as a project
	makeProjectTree(t, home, relConfigPath)
	cwd := filepath.Join(home, "sub")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found, "home's global config must not be treated as a project root")
}

func TestDiscoverProject_NotFound(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "x")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found)
}

func TestMergeConfigs_NilHigh(t *testing.T) {
	low := &Config{DefaultDatasource: "g", Datasources: map[string]Datasource{"g": {Host: "h"}}}
	out := MergeConfigs(low, nil)
	assert.Same(t, low, out) // nil-safe: returns low directly
}

func TestMergeConfigs_SameNameReplaced(t *testing.T) {
	low := &Config{Datasources: map[string]Datasource{"prod": {Host: "global-prod", User: "guser"}}}
	high := &Config{Datasources: map[string]Datasource{"prod": {Host: "proj-prod"}}}
	out := MergeConfigs(low, high)
	// whole-replace: high.prod wins entirely, low.prod.User is gone
	assert.Equal(t, "proj-prod", out.Datasources["prod"].Host)
	assert.Equal(t, "", out.Datasources["prod"].User)
}

func TestMergeConfigs_UnionOfNames(t *testing.T) {
	low := &Config{Datasources: map[string]Datasource{"a": {Host: "ga"}}}
	high := &Config{Datasources: map[string]Datasource{"b": {Host: "pb"}}}
	out := MergeConfigs(low, high)
	assert.Len(t, out.Datasources, 2)
	assert.Equal(t, "ga", out.Datasources["a"].Host)
	assert.Equal(t, "pb", out.Datasources["b"].Host)
}

func TestMergeConfigs_SSHReplacedWholesale(t *testing.T) {
	low := &Config{Datasources: map[string]Datasource{"d": {SSH: &SSHConfig{Host: "gh"}}}}
	high := &Config{Datasources: map[string]Datasource{"d": {SSH: &SSHConfig{Host: "ph"}}}}
	out := MergeConfigs(low, high)
	assert.Equal(t, "ph", out.Datasources["d"].SSH.Host)
}

func TestMergeConfigs_DefaultOverride(t *testing.T) {
	low := &Config{DefaultDatasource: "g"}
	high := &Config{DefaultDatasource: "p"}
	assert.Equal(t, "p", MergeConfigs(low, high).DefaultDatasource)
	// high.Default empty -> keep low
	high2 := &Config{Datasources: map[string]Datasource{}}
	assert.Equal(t, "g", MergeConfigs(low, high2).DefaultDatasource)
}

func TestMergeConfigs_DefaultLimitZeroIsUnset(t *testing.T) {
	low := &Config{DefaultLimit: 2500}
	highZero := &Config{DefaultLimit: 0}
	assert.Equal(t, 2500, MergeConfigs(low, highZero).DefaultLimit) // 0 = unset -> keep low
	highSet := &Config{DefaultLimit: 500}
	assert.Equal(t, 500, MergeConfigs(low, highSet).DefaultLimit)
}

func writeCfgAt(t *testing.T, path, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLoad_ConfigFlagSingleFile(t *testing.T) {
	home := t.TempDir()
	explicit := filepath.Join(home, "x.toml")
	writeCfgAt(t, explicit, `default = "a"
[datasource.a]
host = "ha"
`)
	cfg, entries, err := Load(LoadOpts{ConfigFlag: explicit, Home: home, Cwd: home})
	assert.NoError(t, err)
	assert.Equal(t, "ha", cfg.Datasources["a"].Host)
	assert.Len(t, entries, 1)
	assert.Equal(t, "explicit", entries[0].Kind)
	assert.True(t, entries[0].Trusted)
}

func TestLoad_EnvConfigSingleFile(t *testing.T) {
	home := t.TempDir()
	env := filepath.Join(home, "e.toml")
	writeCfgAt(t, env, `[datasource.b]
host = "hb"
`)
	cfg, entries, err := Load(LoadOpts{EnvConfig: env, Home: home, Cwd: home})
	assert.NoError(t, err)
	assert.Equal(t, "hb", cfg.Datasources["b"].Host)
	assert.Equal(t, "explicit", entries[0].Kind) // env path treated as explicit single-file
}

func TestLoad_ProjectTrustedMergedOverGlobal(t *testing.T) {
	home := t.TempDir()
	globalPath := filepath.Join(home, relConfigPath)
	writeCfgAt(t, globalPath, `default = "g"
[datasource.g]
host = "gh"
[datasource.shared]
host = "sh"
`)
	projRoot := filepath.Join(home, "proj")
	projPath := filepath.Join(projRoot, relConfigPath)
	writeCfgAt(t, projPath, `default = "p"
[datasource.p]
host = "ph"
[datasource.shared]
host = "projsh"
`)
	cfg, entries, err := Load(LoadOpts{
		Cwd: projRoot, Home: home,
		IsTrusted: func(string) bool { return true }, // trusted
	})
	assert.NoError(t, err)
	// union: g (global-only) + p (project-only) + shared (project wins)
	assert.Equal(t, "gh", cfg.Datasources["g"].Host)
	assert.Equal(t, "ph", cfg.Datasources["p"].Host)
	assert.Equal(t, "projsh", cfg.Datasources["shared"].Host)
	assert.Equal(t, "p", cfg.DefaultDatasource)
	// entries: project + global, both trusted
	assert.Len(t, entries, 2)
}

func TestLoad_ProjectUntrustedFallsBackToGlobal(t *testing.T) {
	home := t.TempDir()
	globalPath := filepath.Join(home, relConfigPath)
	writeCfgAt(t, globalPath, `[datasource.g]
host = "gh"
`)
	projRoot := filepath.Join(home, "proj")
	writeCfgAt(t, filepath.Join(projRoot, relConfigPath), `[datasource.p]
host = "ph"
`)
	cfg, entries, err := Load(LoadOpts{
		Cwd: projRoot, Home: home,
		IsTrusted: func(string) bool { return false }, // untrusted
	})
	assert.NoError(t, err) // silent fallback, no error
	assert.Equal(t, "gh", cfg.Datasources["g"].Host)
	assert.NotContains(t, cfg.Datasources, "p") // project NOT loaded
	// entries still show project entry (diagnostic), marked untrusted
	var projEntry *PathEntry
	for i := range entries {
		if entries[i].Kind == "project" {
			projEntry = &entries[i]
		}
	}
	if assert.NotNil(t, projEntry) {
		assert.False(t, projEntry.Trusted)
	}
}

func TestLoad_NoConfigReturnsNil(t *testing.T) {
	home := t.TempDir()
	cfg, _, err := Load(LoadOpts{Cwd: home, Home: home})
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoad_TomlSyntaxError(t *testing.T) {
	home := t.TempDir()
	bad := filepath.Join(home, "bad.toml")
	writeCfgAt(t, bad, `default = "unclosed`)
	_, _, err := Load(LoadOpts{ConfigFlag: bad, Home: home, Cwd: home})
	assert.Error(t, err)
}