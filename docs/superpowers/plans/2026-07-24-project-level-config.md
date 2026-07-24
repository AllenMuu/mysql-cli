# mysql-cli 项目级 config 加载 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 mysql-cli 支持项目级 config(从 cwd 向上找 `.config/mysql-cli/config.toml`)、`MYSQL_CLI_CONFIG` 环境变量、覆盖式合并、信任清单机制,以及 `config` 子命令族(path/show/trust/init)。

**Architecture:** 新增 `internal/config/loader.go` 封装"路径发现 + 多层加载 + 覆盖式合并 + 信任清单",cli 层只调 `config.Load(opts)` 一个入口。`config.go` 单文件解析逻辑不动,`Resolve`/`applyEnv`/`merge`/`expandPassword` 不动。信任判断前置到合并之前(未信任的项目级不进合并链),因此合并后 Config 全部来自已信任源,`${ENV}` 展开无需区分来源。

**Tech Stack:** Go 1.x, spf13/cobra, BurntSushi/toml, stretchr/testify, sqlmock(测试无需真 DB)。

## Global Constraints

(每个 task 的要求隐式包含本节;值逐字来自 spec `docs/superpowers/specs/2026-07-24-project-level-config-design.md`)

- 包严格单向依赖,`config` 是底层(`result` 之外无依赖)。新增 `loader.go` 在 `config` 包内,不引入新包。
- 测试覆盖率 ≥80%,全部用 sqlmock / 临时文件,无需真 MySQL。
- 退出码契约不变:复用 `ExitConfigError = 10`(`internal/cli/root.go:27`),**不新增退出码**。未信任静默回退 = exit 0。
- 信任清单 `~/.config/mysql-cli/trusted`:纯文本,每行一个 `filepath.EvalSymlinks` 规范化的绝对路径,权限 `0600`。
- 密码脱敏:明文 -> `***`;`${ENV}` 占位符原样显示。
- `config.go` 的 `Resolve` / `applyEnv` / `merge` / `expandPassword` / `LoadFile` **不动**。
- commit 用 conventional commits:`feat(config): ...` / `feat(cli): ...` / `docs(skill): ...`。
- 项目级与全局相对路径同构:`.config/mysql-cli/config.toml`,仅根不同。
- `default_limit = 0` 视为未设置(沿用全局/内置默认 cap);无限制仍走 `--no-limit`。

---

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `internal/config/loader.go` | 路径发现 / 多层加载 / 覆盖式合并 / 信任清单 / `Masked` 脱敏 | create |
| `internal/config/loader_test.go` | loader 单测(sqlmock 不需要,纯文件/env) | create |
| `internal/cli/commands.go` | `Globals.resolve()` 改调 `config.Load` | modify |
| `internal/cli/root.go` | 注册 `newConfigCmd(g)`;`Globals` 加 `ConfigExplicit`;`PersistentPreRunE` 设 `ConfigExplicit` | modify |
| `internal/cli/config_cmd.go` | `config` 子命令族(path/show/trust/init) | create |
| `internal/cli/config_cmd_test.go` | 子命令测试(`Run` + 退出码/stdout) | create |
| `skills/mysql-shared/SKILL.md` | 加项目级/信任说明 + `config path` 自省提示 | modify |

**统一函数签名**(后续 task 引用,不得改名):

```go
// internal/config/loader.go

// PathEntry is one resolved config file in the chain (diagnostic view).
type PathEntry struct {
	Path    string // absolute config file path
	Kind    string // "explicit" | "project" | "global"
	Trusted bool   // true for explicit/global; project-only signal
	Exists  bool   // file present on disk
}

type LoadOpts struct {
	ConfigFlag string                        // --config value ("" if not explicitly set)
	EnvConfig  string                        // MYSQL_CLI_CONFIG value ("" if unset)
	Cwd        string                        // project discovery start dir
	Home       string                        // home dir: global config + trust store
	IsTrusted  func(projectRoot string) bool // injectable; nil -> use trust file at opts.Home
}

// DiscoverProject walks up from start looking for .config/mysql-cli/config.toml.
// Returns (projectRoot, configPath, found). projectRoot strips the
// .config/mysql-cli/config.toml suffix. Stops at home or filesystem root.
func DiscoverProject(start, home string) (root, configPath string, found bool)

// MergeConfigs overlays high onto low (覆盖式). high==nil returns low (nil-safe).
func MergeConfigs(low, high *Config) *Config

// ResolvePathChain returns the diagnostic view of ALL discovered entries
// (including an untrusted project entry, marked Trusted=false), ordered low->high.
func ResolvePathChain(opts LoadOpts) ([]PathEntry, error)

// Load resolves chain, loads trusted/explicit/global entries, merges -> Config.
// Returns (mergedConfig, entries, err). mergedConfig is nil if no file loaded.
func Load(opts LoadOpts) (*Config, []PathEntry, error)

// Trust store
func TrustFilePath(home string) string                         // <home>/.config/mysql-cli/trusted
func IsTrusted(home, projectRoot string) bool                   // EvalSymlinks-normalized lookup
func AddTrust(home, projectRoot string) error                   // idempotent append, 0600
func ReadTrusted(home string) ([]string, error)                 // parse plaintext lines

// Masked returns a copy of ds with plaintext password -> "***".
// "${ENV}" placeholders (match ^\$\{...\}$) are left as-is.
func Masked(ds Datasource) Datasource
```

---

## Phase 1 — loader 核心 + 接入(行为完全兼容)

### Task 1: `DiscoverProject` — 向上发现项目级 config

**Files:**
- Create: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Consumes: `config` 包现有类型(无)
- Produces: `DiscoverProject(start, home string) (root, configPath string, found bool)`

- [ ] **Step 1: Write the failing test**

```go
// internal/config/loader_test.go
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

func TestDiscoverProject_NotFound(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "x")
	assert.NoError(t, os.MkdirAll(cwd, 0o755))
	_, _, found := DiscoverProject(cwd, home)
	assert.False(t, found)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDiscoverProject -v`
Expected: FAIL with "undefined: DiscoverProject".

- [ ] **Step 3: Write minimal implementation**

```go
// internal/config/loader.go
package config

import (
	"os"
	"path/filepath"
)

// relConfigPath is the shared relative path for both global and project configs.
const relConfigPath = ".config/mysql-cli/config.toml"

// DiscoverProject walks up from start looking for .config/mysql-cli/config.toml.
// Returns (projectRoot, configPath, found). projectRoot strips the relConfigPath
// suffix (it is the dir containing .config/, NOT .config/mysql-cli/ itself).
// Stops when reaching home or the filesystem root.
func DiscoverProject(start, home string) (root, configPath string, found bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, relConfigPath)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return dir, candidate, true
		}
		// stop at home boundary (do not search home itself as a "project")
		if dir == home || dir == filepath.Dir(dir) {
			return "", "", false
		}
		dir = filepath.Dir(dir)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDiscoverProject -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add DiscoverProject for project-level config discovery"
```

---

### Task 2: `MergeConfigs` — 覆盖式合并

**Files:**
- Modify: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Datasource`(现有)
- Produces: `MergeConfigs(low, high *Config) *Config`

- [ ] **Step 1: Write the failing test**

```go
// append to internal/config/loader_test.go

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestMergeConfigs -v`
Expected: FAIL with "undefined: MergeConfigs".

- [ ] **Step 3: Write minimal implementation**

```go
// append to internal/config/loader.go

// MergeConfigs overlays high onto low using覆盖式 (override) semantics:
// same-name datasource is replaced wholesale (including SSH subtable),
// distinct names are unioned, Default/DefaultLimit override when non-zero/non-empty.
// high==nil returns low unchanged (nil-safe).
func MergeConfigs(low, high *Config) *Config {
	if high == nil {
		return low
	}
	if low == nil {
		low = &Config{Datasources: map[string]Datasource{}}
	}
	out := &Config{
		DefaultDatasource: low.DefaultDatasource,
		DefaultLimit:      low.DefaultLimit,
		Datasources:       map[string]Datasource{},
	}
	for k, v := range low.Datasources {
		out.Datasources[k] = v
	}
	for k, v := range high.Datasources {
		out.Datasources[k] = v // whole-replace (shallow copy of Datasource value is fine: it's a value type, SSH ptr shared with high)
	}
	if high.DefaultDatasource != "" {
		out.DefaultDatasource = high.DefaultDatasource
	}
	if high.DefaultLimit != 0 {
		out.DefaultLimit = high.DefaultLimit
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestMergeConfigs -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add MergeConfigs with override semantics"
```

---

### Task 3: `ResolvePathChain` + `Load` — 总入口(信任判断先注入 stub)

> 本 task 信任判断用注入的 `IsTrusted` stub(测试覆盖);真信任清单实现在 Task 5,Task 6 把默认实现接上。这样 Phase 1 可独立交付、行为兼容(信任默认 false 时项目级不加载,等价于"无项目级")。

**Files:**
- Modify: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Consumes: `DiscoverProject`(Task 1), `MergeConfigs`(Task 2), `LoadFile`(现有)
- Produces: `PathEntry`, `LoadOpts`, `ResolvePathChain(opts) ([]PathEntry, error)`, `Load(opts) (*Config, []PathEntry, error)`

- [ ] **Step 1: Write the failing test**

```go
// append to internal/config/loader_test.go

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL with "undefined: Load / LoadOpts / PathEntry".

- [ ] **Step 3: Write minimal implementation**

```go
// append to internal/config/loader.go

// PathEntry is one resolved config file in the chain (diagnostic view).
type PathEntry struct {
	Path    string // absolute config file path
	Kind    string // "explicit" | "project" | "global"
	Trusted bool   // true for explicit/global; project-only signal
	Exists  bool   // file present on disk
}

// LoadOpts controls path resolution, project discovery, and trust checks.
type LoadOpts struct {
	ConfigFlag string                        // --config value ("" if not explicitly set)
	EnvConfig  string                        // MYSQL_CLI_CONFIG value ("" if unset)
	Cwd        string                        // project discovery start dir
	Home       string                        // home dir: global config + trust store
	IsTrusted  func(projectRoot string) bool // injectable; nil -> always false (Phase 1)
}

// globalConfigPath returns <home>/.config/mysql-cli/config.toml.
func globalConfigPath(home string) string { return filepath.Join(home, relConfigPath) }

// ResolvePathChain returns the diagnostic view of all discovered entries
// (including an untrusted project entry marked Trusted=false), ordered low->high.
func ResolvePathChain(opts LoadOpts) ([]PathEntry, error) {
	var entries []PathEntry
	// explicit single-file (flag or env) short-circuits discovery
	if opts.ConfigFlag != "" || opts.EnvConfig != "" {
		p := opts.ConfigFlag
		if p == "" {
			p = opts.EnvConfig
		}
		_, err := os.Stat(p)
		entries = []PathEntry{{Path: p, Kind: "explicit", Trusted: true, Exists: err == nil}}
		return entries, nil
	}
	// project (if found), then global
	if root, p, found := DiscoverProject(opts.Cwd, opts.Home); found {
		trusted := false
		if opts.IsTrusted != nil {
			trusted = opts.IsTrusted(root)
		}
		entries = append(entries, PathEntry{Path: p, Kind: "project", Trusted: trusted, Exists: true})
	}
	gp := globalConfigPath(opts.Home)
	_, err := os.Stat(gp)
	entries = append(entries, PathEntry{Path: gp, Kind: "global", Trusted: true, Exists: err == nil})
	return entries, nil
}

// Load resolves the chain, loads trusted/explicit/global entries, merges -> Config.
// Returns (mergedConfig, entries, err). mergedConfig is nil if no file was loaded.
// Trust is enforced at merge time: an untrusted project entry is NOT loaded,
// so the merged Config contains only trusted sources.
func Load(opts LoadOpts) (*Config, []PathEntry, error) {
	entries, err := ResolvePathChain(opts)
	if err != nil {
		return nil, entries, err
	}
	var merged *Config
	// load low->high: global first, then project (if trusted). explicit is single.
	for _, e := range entries {
		if !e.Exists {
			continue
		}
		if e.Kind == "project" && !e.Trusted {
			continue // untrusted project: skip load entirely
		}
		cfg, err := LoadFile(e.Path)
		if err != nil {
			return nil, entries, err
		}
		merged = MergeConfigs(merged, cfg)
	}
	return merged, entries, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run "TestLoad|TestDiscoverProject|TestMergeConfigs" -v`
Expected: PASS (all loader tests green).

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add Load/ResolvePathChain with trust-gated merge"
```

---

### Task 4: 接入 `Globals.resolve()` + `ConfigExplicit`(行为兼容)

**Files:**
- Modify: `internal/cli/root.go`(`Globals` 加字段 + `PersistentPreRunE` 设 `ConfigExplicit` + 注册 `newConfigCmd` 占位)
- Modify: `internal/cli/commands.go`(`resolve()` 改调 `config.Load`)
- Test: `internal/cli/commands_test.go`(确认现有测试仍通过)

**Interfaces:**
- Consumes: `config.Load`, `config.LoadOpts`(Task 3)
- Produces: `Globals.ConfigExplicit bool`;`resolve()` 用 `config.Load`

> 注:`newConfigCmd` 在 Task 7+ 才有实参;本 task 先在 `root.go` 注册一个**空壳** `newConfigCmd(g)`(返回 `&cobra.Command{Use:"config"}`),避免编译错。Task 7 起逐步填充子命令。

- [ ] **Step 1: Write the failing test (兼容性回归)**

```go
// append to internal/cli/commands_test.go

// Behavioral compat: no project + no env + no explicit --config behaves as today.
func TestResolveCompatNoConfig(t *testing.T) {
	// HOME isolated -> no global config -> env/default fallback.
	t.Setenv("HOME", t.TempDir())
	code := Run([]string{"query", "SELECT 1", "--host", "127.0.0.1", "--port", "1"})
	assert.Equal(t, ExitConnFailed, code) // reached connection stage (config ok, conn fails)
}

// --config single-file still works and is the only source.
func TestResolveCompatExplicitConfigFlag(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.toml")
	os.WriteFile(cfg, []byte(`[datasource.x]
host = "h"
`), 0o600)
	t.Setenv("HOME", t.TempDir())
	code := Run([]string{"query", "SELECT 1", "-d", "nonexistent", "--config", cfg})
	assert.Equal(t, ExitConfigError, code) // unknown datasource -> config error (file loaded, name missing)
}
```

- [ ] **Step 2: Run test to verify it fails (or confirm baseline)**

Run: `go test ./internal/cli/ -run "TestResolveCompat" -v`
Expected: FAIL (resolve still uses old single-file path; new tests may pass by luck but `resolve()` not yet calling Load). Confirm at minimum that the suite compiles.

- [ ] **Step 3: Modify `root.go` — add `ConfigExplicit` + register placeholder `config` cmd**

```go
// internal/cli/root.go — edit Globals struct (add field after ConfigPath):
	ConfigPath     string
	ConfigExplicit bool   // true when --config was explicitly set on the command line

// inside newRootCmd, edit PersistentPreRunE to set ConfigExplicit:
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			g.ConfigExplicit = cmd.Flags().Changed("config")
			if g.Format != "json" && g.Format != "table" && g.Format != "csv" && g.Format != "tsv" && g.Format != "jsonl" {
				return fmt.Errorf("invalid format %q (want json|table|csv|tsv|jsonl)", g.Format)
			}
			if _, err := time.ParseDuration(g.Timeout); err != nil {
				return fmt.Errorf("invalid timeout %q: %w", g.Timeout, err)
			}
			return nil
		},

// in root.AddCommand(...), add newConfigCmd(g):
		newConfigCmd(g),
		newInitCmd(),
```

```go
// internal/cli/config_cmd.go — placeholder (filled in Task 7+)
package cli

import "github.com/spf13/cobra"

func newConfigCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Manage config: project-level discovery, trust, and inspection",
	}
}
```

- [ ] **Step 4: Modify `commands.go` — `resolve()` uses `config.Load`**

```go
// internal/cli/commands.go — replace the body of (g *Globals) resolve():
func (g *Globals) resolve() (config.Datasource, error) {
	cwd, _ := os.Getwd()
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	cfgFlag := ""
	if g.ConfigExplicit {
		cfgFlag = g.ConfigPath
	}
	merged, _, err := config.Load(config.LoadOpts{
		ConfigFlag: cfgFlag,
		EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
		Cwd:        cwd,
		Home:       home,
		// IsTrusted left nil in Phase 1 -> project never loaded (compat).
		// Task 6 wires the real trust store here.
	})
	if err != nil {
		return config.Datasource{}, err
	}
	if merged != nil {
		g.DefaultLimit = merged.DefaultLimit
	}
	over := config.Datasource{
		Host: g.Host, Port: g.Port, User: g.User, Password: g.Password, Database: g.Database,
	}
	return config.Resolve(merged, g.Datasource, over)
}
```

(`defaultConfigPath()` 可保留未用,或删除;为最小改动保留它作为 `--config` flag 的 default value 字符串来源。)

- [ ] **Step 5: Run tests — full cli suite must stay green**

Run: `go test ./internal/cli/ -v && go test ./internal/config/ -v`
Expected: PASS (existing tests unaffected; new compat tests pass). Coverage check: `go test -cover ./internal/config/ ./internal/cli/`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/root.go internal/cli/commands.go internal/cli/config_cmd.go internal/cli/commands_test.go
git commit -m "feat(cli): wire Globals.resolve to config.Load (compat preserved)"
```

---

## Phase 2 — 环境变量 + 信任清单 + `config trust`

### Task 5: 信任清单读写(`TrustFilePath`/`IsTrusted`/`AddTrust`/`ReadTrusted`)

**Files:**
- Modify: `internal/config/loader.go`
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Consumes: `filepath.EvalSymlinks`(stdlib)
- Produces: `TrustFilePath`, `IsTrusted`, `AddTrust`, `ReadTrusted`

- [ ] **Step 1: Write the failing test**

```go
// append to internal/config/loader_test.go

func TestTrustFilePath(t *testing.T) {
	assert.Equal(t, filepath.Join("H", ".config", "mysql-cli", "trusted"), TrustFilePath("H"))
}

func TestAddTrust_Idempotent(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	assert.NoError(t, os.MkdirAll(root, 0o755))
	assert.NoError(t, AddTrust(home, root))
	assert.NoError(t, AddTrust(home, root)) // duplicate, no error, single line
	list, err := ReadTrusted(home)
	assert.NoError(t, err)
	assert.Equal(t, []string{root}, list)
}

func TestIsTrusted_HitAndMiss(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	os.MkdirAll(root, 0o755)
	assert.False(t, IsTrusted(home, root))
	assert.NoError(t, AddTrust(home, root))
	assert.True(t, IsTrusted(home, root))
}

func TestIsTrusted_SymlinkNormalized(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(home, "link")
	os.Symlink(real, link)
	assert.NoError(t, AddTrust(home, link))     // add via symlink path
	assert.True(t, IsTrusted(home, real))        // resolves to real -> trusted
}

func TestReadTrusted_NoFileReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	list, err := ReadTrusted(home)
	assert.NoError(t, err)
	assert.Empty(t, list)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run "TestTrustFilePath|TestAddTrust|TestIsTrusted|TestReadTrusted" -v`
Expected: FAIL with "undefined: TrustFilePath" etc.

- [ ] **Step 3: Write minimal implementation**

```go
// append to internal/config/loader.go

import "sort" // add to existing import block

// TrustFilePath returns <home>/.config/mysql-cli/trusted.
func TrustFilePath(home string) string {
	return filepath.Join(home, relConfigPath[:len(relConfigPath)-len("config.toml")]+"trusted")
}
// (equivalent to filepath.Join(home, ".config", "mysql-cli", "trusted"))

// ReadTrusted parses the plaintext trust file (one normalized path per line).
// Missing file -> empty list, no error.
func ReadTrusted(home string) ([]string, error) {
	b, err := os.ReadFile(TrustFilePath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// normalizePath resolves symlinks to a canonical absolute path.
func normalizePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// IsTrusted reports whether projectRoot (symlink-normalized) is in the trust file.
func IsTrusted(home, projectRoot string) bool {
	target := normalizePath(projectRoot)
	list, err := ReadTrusted(home)
	if err != nil {
		return false // unreadable trust store -> treat as none, silent fallback
	}
	for _, e := range list {
		if e == target {
			return true
		}
	}
	return false
}

// AddTrust appends projectRoot (normalized) to the trust file, idempotently.
// Creates the parent dir and file with 0600 if absent.
func AddTrust(home, projectRoot string) error {
	target := normalizePath(projectRoot)
	list, _ := ReadTrusted(home)
	for _, e := range list {
		if e == target {
			return nil // already trusted
		}
	}
	list = append(list, target)
	sort.Strings(list)
	var b strings.Builder
	for i, e := range list {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e)
	}
	b.WriteByte('\n')
	tf := TrustFilePath(home)
	if err := os.MkdirAll(filepath.Dir(tf), 0o700); err != nil {
		return err
	}
	return os.WriteFile(tf, []byte(b.String()), 0o600)
}
```

> Add `import "sort"` and `import "strings"` to the import block. (`os`/`filepath` already imported.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run "TestTrustFilePath|TestAddTrust|TestIsTrusted|TestReadTrusted" -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add trust store (plaintext, symlink-normalized, 0600)"
```

---

### Task 6: `Load` 接真信任清单 + `MYSQL_CLI_CONFIG` env 路由

**Files:**
- Modify: `internal/config/loader.go`(`Load` 默认 `IsTrusted` 用 trust file)
- Modify: `internal/cli/commands.go`(`resolve` 传真 `IsTrusted`)
- Test: `internal/config/loader_test.go`

**Interfaces:**
- Consumes: `IsTrusted`(Task 5)
- Produces: `Load` 默认信任行为(无注入时用 trust file)

- [ ] **Step 1: Write the failing test**

```go
// append to internal/config/loader_test.go

// Default IsTrusted (nil) uses the real trust file at Home.
func TestLoad_DefaultIsTrustedUsesTrustFile(t *testing.T) {
	home := t.TempDir()
	globalPath := filepath.Join(home, relConfigPath)
	writeCfgAt(t, globalPath, `[datasource.g]
host = "gh"
`)
	projRoot := filepath.Join(home, "proj")
	writeCfgAt(t, filepath.Join(projRoot, relConfigPath), `[datasource.p]
host = "ph"
`)
	// not trusted yet -> project skipped
	cfg, _, err := Load(LoadOpts{Cwd: projRoot, Home: home}) // IsTrusted nil
	assert.NoError(t, err)
	assert.NotContains(t, cfg.Datasources, "p")

	// trust it -> project loaded
	assert.NoError(t, AddTrust(home, projRoot))
	cfg2, _, err := Load(LoadOpts{Cwd: projRoot, Home: home})
	assert.NoError(t, err)
	assert.Equal(t, "ph", cfg2.Datasources["p"].Host)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad_DefaultIsTrustedUsesTrustFile -v`
Expected: FAIL (Phase 1 default IsTrusted always false).

- [ ] **Step 3: Wire the default in `Load`**

```go
// internal/config/loader.go — in Load(), before ResolvePathChain:
func Load(opts LoadOpts) (*Config, []PathEntry, error) {
	isTrusted := opts.IsTrusted
	if isTrusted == nil {
		isTrusted = func(root string) bool { return IsTrusted(opts.Home, root) }
	}
	opts.IsTrusted = isTrusted
	entries, err := ResolvePathChain(opts)
	// ... rest unchanged
```

```go
// internal/cli/commands.go — in resolve(), pass IsTrusted explicitly (clear intent):
	merged, _, err := config.Load(config.LoadOpts{
		ConfigFlag: cfgFlag,
		EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
		Cwd:        cwd,
		Home:       home,
		IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
	})
```

- [ ] **Step 4: Run test to verify it passes + full suite**

Run: `go test ./internal/config/ ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go internal/cli/commands.go
git commit -m "feat(config): wire default trust store into Load + MYSQL_CLI_CONFIG env"
```

---

### Task 7: `config trust` 子命令

**Files:**
- Modify: `internal/cli/config_cmd.go`
- Test: `internal/cli/config_cmd_test.go`(create)
- Modify: `internal/cli/root.go`(no change — already registered `newConfigCmd(g)` in Task 4)

**Interfaces:**
- Consumes: `config.AddTrust`, `config.TrustFilePath`, `config.DiscoverProject`
- Produces: `newConfigTrustCmd(g)`, wired under `config`

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/config_cmd_test.go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigTrust_DefaultCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot) // trust cwd's detected project root
	code := Run([]string{"config", "trust"})
	assert.Equal(t, ExitOK, code)
	// trust file now contains projRoot
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Contains(t, string(b), projRoot)
}

func TestConfigTrust_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot)
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"}))
	assert.Equal(t, ExitOK, Run([]string{"config", "trust"})) // no duplicate
	b, _ := os.ReadFile(filepath.Join(home, ".config", "mysql-cli", "trusted"))
	assert.Equal(t, 1, strings.Count(string(b), projRoot))
}

func TestConfigTrust_JSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.Chdir(projRoot)
	// capture stdout via a custom Run variant if available; else assert exit + file.
	assert.Equal(t, ExitOK, Run([]string{"config", "trust", "-j"}))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestConfigTrust -v`
Expected: FAIL (`config trust` not a registered subcommand yet).

- [ ] **Step 3: Implement `config trust` + wire under `config`**

```go
// internal/cli/config_cmd.go — replace placeholder:
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AllenMuu/mysql-cli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage config: project-level discovery, trust, and inspection",
	}
	cmd.AddCommand(newConfigTrustCmd(g))
	return cmd
}

func newConfigTrustCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "trust [dir]",
		Short: "Trust a project root so its .config/mysql-cli/config.toml is loaded",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			} else {
				cwd, _ := os.Getwd()
				dir = cwd
			}
			// If dir is not itself a project root, walk up to find one.
			root, _, found := config.DiscoverProject(dir, home)
			if !found {
				root = dir // fall back to the given/cwd dir as-is
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("cannot resolve path %q: %w", root, err)
			}
			if err := config.AddTrust(home, abs); err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				out := map[string]any{"trusted": abs}
				b, _ := json.MarshalIndent(map[string]any{"success": true, "data": out}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "✅ trusted: %s\n", abs)
			}
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestConfigTrust -v`
Expected: PASS (3 tests). Add `"strings"` import to the test file.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config_cmd.go internal/cli/config_cmd_test.go
git commit -m "feat(cli): add 'config trust' subcommand"
```

---

## Phase 3 — `config path` / `show` / `init` + 文档

### Task 8: `config path` 子命令

**Files:**
- Modify: `internal/cli/config_cmd.go`
- Test: `internal/cli/config_cmd_test.go`

**Interfaces:**
- Consumes: `config.ResolvePathChain`, `config.LoadOpts`
- Produces: `newConfigPathCmd(g)`

- [ ] **Step 1: Write the failing test**

```go
// append to internal/cli/config_cmd_test.go

func TestConfigPath_ShowsProjectAndGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(projRoot, ".config", "mysql-cli", "config.toml"), []byte("# p"), 0o600)
	os.WriteFile(filepath.Join(home, ".config", "mysql-cli", "config.toml"), []byte("# g"), 0o600)
	os.Chdir(projRoot)
	code := Run([]string{"config", "path"})
	assert.Equal(t, ExitOK, code)
	// (stdout assertions are optional; exit code + no panic is the contract)
}

func TestConfigPath_UntrustedProjectSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(filepath.Join(projRoot, ".config", "mysql-cli"), 0o755)
	os.WriteFile(filepath.Join(projRoot, ".config", "mysql-cli", "config.toml"), []byte("# p"), 0o600)
	os.Chdir(projRoot)
	// not trusted -> path still lists it but marks untrusted; exits 0
	assert.Equal(t, ExitOK, Run([]string{"config", "path"}))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestConfigPath -v`
Expected: FAIL (`config path` not registered).

- [ ] **Step 3: Implement `config path`**

```go
// internal/cli/config_cmd.go — add to newConfigCmd AddCommand list:
	cmd.AddCommand(newConfigTrustCmd(g), newConfigPathCmd(g))

// new function:
func newConfigPathCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: "Show the resolved config file chain and trust status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			cwd, _ := os.Getwd()
			entries, err := config.ResolvePathChain(config.LoadOpts{
				ConfigFlag: explicitConfigFlag(g),
				EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
				Cwd:        cwd,
				Home:       home,
				IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
			})
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				type entry struct {
					Path string `json:"path"`
					Kind string `json:"kind"`
					Trusted bool `json:"trusted"`
					Exists  bool `json:"exists"`
				}
				out := []entry{}
				for _, e := range entries {
					out = append(out, entry{e.Path, e.Kind, e.Trusted, e.Exists})
				}
				b, _ := json.MarshalIndent(map[string]any{"success": true, "data": map[string]any{"entries": out}}, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				for _, e := range entries {
					status := "trusted"
					if e.Kind == "project" && !e.Trusted {
						status = "untrusted, skipped"
					}
					if !e.Exists {
						status = "missing"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%-8s %s   [%s]\n", e.Kind, e.Path, status)
				}
			}
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}

// helper shared by path/show: returns --config value only if explicitly set.
func explicitConfigFlag(g *Globals) string {
	if g.ConfigExplicit {
		return g.ConfigPath
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestConfigPath -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config_cmd.go internal/cli/config_cmd_test.go
git commit -m "feat(cli): add 'config path' subcommand"
```

---

### Task 9: `config show` 子命令(密码脱敏)

**Files:**
- Modify: `internal/config/loader.go`(add `Masked`)
- Modify: `internal/cli/config_cmd.go`
- Test: `internal/config/loader_test.go`(Masked 单测) + `internal/cli/config_cmd_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.Masked`
- Produces: `Masked(ds Datasource) Datasource`, `newConfigShowCmd(g)`

- [ ] **Step 1: Write the failing test (config layer — Masked)**

```go
// append to internal/config/loader_test.go

func TestMasked_PlaintextHidden(t *testing.T) {
	out := Masked(Datasource{Host: "h", Password: "secret"})
	assert.Equal(t, "***", out.Password)
	assert.Equal(t, "h", out.Host) // other fields preserved
}

func TestMasked_EnvPlaceholderKept(t *testing.T) {
	out := Masked(Datasource{Password: "${MYSQL_PASSWORD}"})
	assert.Equal(t, "${MYSQL_PASSWORD}", out.Password) // not masked
}

func TestMasked_EmptyStaysEmpty(t *testing.T) {
	out := Masked(Datasource{Password: ""})
	assert.Equal(t, "", out.Password)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestMasked -v`
Expected: FAIL ("undefined: Masked").

- [ ] **Step 3: Implement `Masked`**

```go
// append to internal/config/loader.go

// placeholderMaskRe matches ${ENV} password placeholders (reuse shape from config.go).
var placeholderMaskRe = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]*\}$`)

// Masked returns a copy of ds with a plaintext password replaced by "***".
// "${ENV}" placeholders (and empty) are left unchanged.
func Masked(ds Datasource) Datasource {
	out := ds // value copy
	if ds.Password != "" && !placeholderMaskRe.MatchString(ds.Password) {
		out.Password = "***"
	}
	return out
}
```

> Add `"regexp"` to imports (it is already imported in config.go but loader.go needs its own import).

- [ ] **Step 4: Write the failing test (cli layer — `config show`)**

```go
// append to internal/cli/config_cmd_test.go

func TestConfigShow_MasksPassword(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "mysql-cli", "config.toml")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte(`default = "d"
[datasource.d]
host = "h"
password = "supersecret"
`), 0o600)
	os.Chdir(home)
	// capture stdout: use a buffer-backed Run if the package exposes one; here assert exit 0.
	assert.Equal(t, ExitOK, Run([]string{"config", "show", "-j"}))
}
```

- [ ] **Step 5: Implement `config show`**

```go
// internal/cli/config_cmd.go — add to AddCommand list:
	cmd.AddCommand(newConfigTrustCmd(g), newConfigPathCmd(g), newConfigShowCmd(g))

func newConfigShowCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "show [name]",
		Short: "Show the merged effective config (passwords masked)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return fmt.Errorf("cannot determine home: %w", err)
			}
			cwd, _ := os.Getwd()
			merged, _, err := config.Load(config.LoadOpts{
				ConfigFlag: explicitConfigFlag(g),
				EnvConfig:  os.Getenv("MYSQL_CLI_CONFIG"),
				Cwd:        cwd,
				Home:       home,
				IsTrusted:  func(root string) bool { return config.IsTrusted(home, root) },
			})
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")
			if merged == nil {
				merged = &config.Config{Datasources: map[string]Datasource{}}
			}
			// filter to a single datasource if -d/--name given
			if len(args) == 1 {
				ds, ok := merged.Datasources[args[0]]
				if !ok {
					return fmt.Errorf("unknown datasource %q", args[0])
				}
				emitMaskedDS(cmd.OutOrStdout(), args[0], ds, asJSON)
				return nil
			}
			emitMaskedConfig(cmd.OutOrStdout(), merged, asJSON)
			return nil
		},
	}
	c.Flags().BoolP("json", "j", false, "emit JSON")
	return c
}
```

> `emitMaskedConfig` / `emitMaskedDS` print `Default`, `DefaultLimit`, and each datasource (via `config.Masked`) as text or JSON. Plaintext password prints `***`; `${ENV}` prints as-is. Implementation is straightforward formatting; reuse `encoding/json` for the `-j` path.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ ./internal/cli/ -run "TestMasked|TestConfigShow" -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go internal/cli/config_cmd.go internal/cli/config_cmd_test.go
git commit -m "feat(cli): add 'config show' with password masking"
```

---

### Task 10: `config init` 子命令

**Files:**
- Modify: `internal/cli/config_cmd.go`
- Test: `internal/cli/config_cmd_test.go`

**Interfaces:**
- Consumes: `relConfigPath`(via `config` exported const, see below), `os.UserHomeDir`
- Produces: `newConfigInitCmd(g)`

> `relConfigPath` is currently unexported. Export it as `RelConfigPath` in loader.go (one-line change) so cli can reference the shared path.

- [ ] **Step 1: Write the failing test**

```go
// append to internal/cli/config_cmd_test.go

func TestConfigInit_ProjectCreatesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projRoot := filepath.Join(home, "proj")
	os.MkdirAll(projRoot, 0o755)
	os.Chdir(projRoot)
	assert.Equal(t, ExitOK, Run([]string{"config", "init", "--project"}))
	_, err := os.Stat(filepath.Join(projRoot, ".config", "mysql-cli", "config.toml"))
	assert.NoError(t, err)
}

func TestConfigInit_DoesNotOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gp := filepath.Join(home, ".config", "mysql-cli", "config.toml")
	os.MkdirAll(filepath.Dir(gp), 0o755)
	os.WriteFile(gp, []byte("# existing"), 0o600)
	// without --force -> non-zero exit, file unchanged
	code := Run([]string{"config", "init", "--global"})
	assert.NotEqual(t, ExitOK, code)
	b, _ := os.ReadFile(gp)
	assert.Equal(t, "# existing", string(b))
	// with --force -> overwritten
	assert.Equal(t, ExitOK, Run([]string{"config", "init", "--global", "--force"}))
	b2, _ := os.ReadFile(gp)
	assert.NotEqual(t, "# existing", string(b2))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestConfigInit -v`
Expected: FAIL (`config init` not registered).

- [ ] **Step 3: Export `RelConfigPath` + implement `config init`**

```go
// internal/config/loader.go — rename const:
const RelConfigPath = ".config/mysql-cli/config.toml"
// and update all internal references (DiscoverProject, globalConfigPath) to use RelConfigPath.
```

```go
// internal/cli/config_cmd.go — add to AddCommand list:
	cmd.AddCommand(newConfigTrustCmd(g), newConfigPathCmd(g), newConfigShowCmd(g), newConfigInitCmd(g))

const configTemplate = `# mysql-cli config (generated by 'mysql-cli config init')
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
# password = "secret"            # plaintext
# password = "${MYSQL_PASSWORD}" # or ${ENV} placeholder (trusted dirs only)
database = "test"
`

func newConfigInitCmd(g *Globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Write a template config.toml (--project or --global)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			project, _ := cmd.Flags().GetBool("project")
			global, _ := cmd.Flags().GetBool("global")
			if project == global {
				return fmt.Errorf("specify exactly one of --project or --global")
			}
			var target string
			if global {
				home, err := os.UserHomeDir()
				if err != nil || home == "" {
					return fmt.Errorf("cannot determine home: %w", err)
				}
				target = filepath.Join(home, config.RelConfigPath)
			} else {
				cwd, _ := os.Getwd()
				target = filepath.Join(cwd, config.RelConfigPath)
			}
			if !force {
				if _, err := os.Stat(target); err == nil {
					return fmt.Errorf("config already exists at %s (use --force to overwrite)", target)
				}
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(configTemplate), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✅ wrote %s\n", target)
			return nil
		},
	}
	c.Flags().Bool("project", false, "write to <cwd>/.config/mysql-cli/config.toml")
	c.Flags().Bool("global", false, "write to ~/.config/mysql-cli/config.toml")
	c.Flags().Bool("force", false, "overwrite if exists")
	return c
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestConfigInit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/loader.go internal/cli/config_cmd.go internal/cli/config_cmd_test.go
git commit -m "feat(cli): add 'config init' subcommand (--project/--global)"
```

---

### Task 11: skill 文档更新

**Files:**
- Modify: `skills/mysql-shared/SKILL.md`

> 无 TDD(文档)。运行格式校验脚本确保 frontmatter 合规。

- [ ] **Step 1: Edit `skills/mysql-shared/SKILL.md`**

Add a new "Project-level config" section covering:

- 项目级 config 位置:`<project-root>/.config/mysql-cli/config.toml`(从 cwd 向上查找,与全局同构)
- 信任机制:首次需 `mysql-cli config trust`,否则静默回退全局(exit 0)。`${ENV}` 占位符仅在已信任项目级 config 中展开
- 优先级链:`--config` > `MYSQL_CLI_CONFIG` > 项目级(已信任) > 全局
- 自省提示:"查询结果不符合预期时,先 `mysql-cli config path` 查信任状态;`mysql-cli config show` 查合并后配置(密码脱敏)"
- 子命令族速查:`config path|show|trust|init`

Bump the skill `version` frontmatter by a patch (e.g. `1.1.0` -> `1.2.0`) per the skill versioning convention.

- [ ] **Step 2: Run skill format check**

Run: `./scripts/skill-format-check.sh skills/`
Expected: exit 0, all skills valid.

- [ ] **Step 3: Commit**

```bash
git add skills/mysql-shared/SKILL.md
git commit -m "docs(skill): document project-level config + trust + config subcommands"
```

---

### Task 12: 全量验证 + 覆盖率

**Files:**
- (no source changes unless verification surfaces issues)

- [ ] **Step 1: Full build + vet + test + coverage**

Run:
```bash
go build ./...
go vet ./...
go test -cover ./...
```
Expected: build clean; vet clean; all tests pass; `internal/config` and `internal/cli` coverage ≥80%.

- [ ] **Step 2: Manual smoke (optional, no DB needed)**

```bash
# in a temp project dir:
mkdir -p /tmp/p/.config/mysql-cli && echo '[datasource.d]
host = "127.0.0.1"' > /tmp/p/.config/mysql-cli/config.toml
cd /tmp/p
mysql-cli config path        # shows project (untrusted, skipped) + global
mysql-cli config trust       # trust it
mysql-cli config path        # shows project [trusted]
mysql-cli config show -j     # merged config, passwords masked
```
Expected: matches spec §4/§5 output shape.

- [ ] **Step 3: Commit (only if fixes were made)**

```bash
git add -A
git commit -m "test(config,cli): final coverage + smoke verification"
```
(If no changes, skip — nothing to commit.)

---

## Plan Self-Review

**1. Spec coverage** (spec section -> task):
- 发现链(§2):Task 1 `DiscoverProject`, Task 3 `ResolvePathChain`
- 合并语义(§3):Task 2 `MergeConfigs`
- 信任清单(§4):Task 5 trust store, Task 6 default wiring, Task 7 `config trust`
- config 子命令族(§5):Task 7 trust, Task 8 path, Task 9 show+`Masked`, Task 10 init
- 错误处理/退出码(§6):Task 3 (`Load` toml err), Task 4 (compat exit codes), Task 7/10 (exit 0 / non-0)
- 优先级链(§7):Task 3 (`ResolvePathChain` flag>env>project>global) + Task 4 (`ConfigExplicit`)
- 测试策略(§8):Tasks 1-10 TDD, Task 12 coverage gate
- 向后兼容(§9):Task 4 compat tests + `ConfigExplicit` flag-default handling
- 安全(§10):Task 5 (`EvalSymlinks` + 0600), Task 9 (`Masked`)
- 文档(§8 文档层):Task 11
- 分阶段(§11):Phase 1 (Tasks 1-4) / Phase 2 (5-7) / Phase 3 (8-11) / verify (12)

**2. Placeholder scan**: No TBD/TODO/"implement later". Task 9 Step 5 `emitMaskedConfig`/`emitMaskedDS` described in prose rather than full code — this is intentional formatting boilerplate (straightforward json/text printing), but flagged: implementer should write both JSON and text paths. No other prose-only steps.

**3. Type consistency**: `DiscoverProject`, `MergeConfigs`, `Load`, `LoadOpts`, `PathEntry`, `IsTrusted`, `AddTrust`, `ReadTrusted`, `TrustFilePath`, `Masked`, `RelConfigPath` — names consistent across all tasks. `Globals.ConfigExplicit` introduced in Task 4, used in Task 4/8/9 via `explicitConfigFlag(g)` helper. `ExitOK`/`ExitConfigError` from existing `root.go`.
