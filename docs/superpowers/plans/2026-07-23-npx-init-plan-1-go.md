# mysql-cli init + agents 实现计划 (Plan 1 / 共 2 份)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `mysql-cli init` 子命令,把 `scripts/install-skills.sh` 的 7-agent 安装逻辑移植为纯 Go `internal/agents` 包,从内嵌 bundle 装技能到各 agent,auto 检测已装 agent、幂等可重复。

**Architecture:** 新 `internal/agents` 包(stdlib-only,技能内容经 `fs.FS` 注入,便于用 `fstest.MapFS` 单测)提供 `Detect`/`Install`/`Run`;新 `internal/cli/init.go` cobra 子命令注入 `bundle.SkillsFS()`+`bundle.SkillNames()` 并打印 text/JSON 报告;新增退出码 `ExitInitFailed=11`(不占用既有 2-10 契约码);bash 脚本加废弃 banner。

**Tech Stack:** Go 1.22、cobra v1.8.1、标准库 `io/fs`+`testing/fstest`、testify v1.9.0。

## Global Constraints

- Go 1.22(`go.mod`);编译 `go build ./...`,静态 `go vet ./...`,测试 `go test ./...`。
- **退出码是契约**:既有 `0, 2-10`(`root.go` ExitOK..ExitConfigError)面向 agent、**不可改动**。仅新增 `ExitInitFailed = 11`。
- 技能集合固定:`mysql-shared` / `mysql-query` / `mysql-schema`(与 `scripts/install-skills.sh` 一致)。
- marker 字符串逐字固定:
  - begin = `<!-- mysql-cli skill: begin (auto-generated) -->`
  - end = `<!-- mysql-cli skill: end -->`
  - note = `<!-- Re-run mysql-cli init to update. Do not edit between markers. -->`
- agent 路径/格式表(对齐 bash 脚本;**修正 spec 4.1**:aider 有全局位):

  | agent | 全局位 | 项目位 | 格式 |
  |---|---|---|---|
  | claude | `~/.claude/skills/<name>/` | `<proj>/.claude/skills/<name>/` | 原生复制整树 |
  | cursor | `~/.cursor/rules/<name>.mdc`(仅当 `~/.cursor` 存在) | `<proj>/.cursor/rules/<name>.mdc` | 原生 .mdc |
  | codex | `~/.codex/instructions.md` | `<proj>/AGENTS.md` | 标记合并 |
  | opencode | `~/.config/opencode/instructions.md` | `<proj>/.opencode/instructions.md` | 标记合并 |
  | copilot | (无) | `<proj>/.github/copilot-instructions.md` | 标记合并,纯项目级 |
  | windsurf | `~/.windsurfrules` | `<proj>/.windsurfrules` | 标记合并 |
  | aider | `~/.aider.instructions.md` | `<proj>/.aider.instructions.md` | 标记合并 |

- **默认行为**(spec D7):`init` 默认只装**全局**;`--project-dir <path>` 追加项目级;`--no-global` 仅项目级。copilot 无全局,需 `--project-dir`。
- `internal/agents` 包**仅依赖标准库**(不 import bundle);`fs.FS`+技能名由 cli 层注入。这是对 spec「agents 依赖 bundle」的测试性改进(依赖注入),保持单向依赖。
- 新代码覆盖率 ≥80%(项目目标)。
- Conventional commits;attribution 已全局禁用。

## File Structure

**Create:**
- `internal/agents/merge.go` - marker 常量、`SkillBody`、`mergedBody`、`MergeInstructionFile`、`makeMDC`、cursor 描述表。
- `internal/agents/detect.go` - `Detect(home, projectDir)`。
- `internal/agents/install.go` - `Options`、`InstallResult`、`ErrProjectOnly`、`copySkillTree`、`writeIfNotDryRun`、7 个 `installXxx`。
- `internal/agents/agents.go` - agent 名常量、`AllAgents`、`ValidAgent`、`Install`、`Run`、`parseList`。
- `internal/agents/testutil_test.go` - `testFS()`、`testNames` 共享 fixture。
- `internal/agents/merge_test.go` / `detect_test.go` / `install_test.go` / `agents_test.go` - 表驱动测试。
- `internal/cli/init.go` - `newInitCmd`、`ErrInitAllFailed`、`emitInitText`、`emitInitJSON`、`allFailed`。
- `internal/cli/init_test.go` - flag/JSON/退出码测试。

**Modify:**
- `internal/cli/root.go` - const 块加 `ExitInitFailed = 11`;`AddCommand` 加 `newInitCmd()`。
- `internal/cli/errors.go` - `mapError` 加 `ErrInitAllFailed` 分支;`errorCodeName` 加 `ExitInitFailed`。
- `scripts/install-skills.sh` - 顶部加废弃 banner(stderr)。
- `README.md` - 安装段补 `mysql-cli init` 用法。

---

### Task 1: merge 原语(`internal/agents/merge.go`)

**Files:**
- Create: `internal/agents/merge.go`
- Test: `internal/agents/merge_test.go`、`internal/agents/testutil_test.go`

**Interfaces:**
- Produces: `SkillBody(content string) string`、`mergedBody(fsys fs.FS, names []string) (string, error)`、`MergeInstructionFile(existing, merged string) string`、`makeMDC(skill, body string) string`、marker 常量 `beginMarker`/`endMarker`/`updateNote`、`cursorDescriptions` 表。
- Consumes: 标准库 `io/fs`、`sort`、`strings`、`fmt`。

- [ ] **Step 1: 写 testutil fixture**

`internal/agents/testutil_test.go`:
```go
package agents

import "testing/fstest"

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"mysql-shared/SKILL.md": {Data: []byte("---\nname: mysql-shared\nversion: 1.0.0\n---\n\nshared body\n")},
		"mysql-query/SKILL.md":  {Data: []byte("---\nname: mysql-query\nversion: 1.0.0\n---\n\nquery body\n")},
		"mysql-schema/SKILL.md": {Data: []byte("---\nname: mysql-schema\nversion: 1.0.0\n---\n\nschema body\n")},
	}
}

var testNames = []string{"mysql-shared", "mysql-query", "mysql-schema"}
```

- [ ] **Step 2: 写失败测试**

`internal/agents/merge_test.go`:
```go
package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillBody(t *testing.T) {
	in := "---\nname: x\nversion: 1.0.0\n---\n\nbody line 1\nbody line 2\n"
	assert.Equal(t, "\nbody line 1\nbody line 2\n", SkillBody(in))
}

func TestSkillBody_NoFrontmatter(t *testing.T) {
	assert.Equal(t, "", SkillBody("no delimiters here"))
}

func TestMergedBody(t *testing.T) {
	got, err := mergedBody(testFS(), testNames)
	require.NoError(t, err)
	// sorted order: mysql-query, mysql-schema, mysql-shared
	assert.Contains(t, got, "## mysql-cli skill: mysql-query")
	assert.Contains(t, got, "## mysql-cli skill: mysql-schema")
	assert.Contains(t, got, "## mysql-cli skill: mysql-shared")
	assert.Contains(t, got, "query body")
	assert.Less(t, strings.Index(got, "mysql-query"), strings.Index(got, "mysql-schema"))
}

func TestMergeInstructionFile_AppendWhenAbsent(t *testing.T) {
	got := MergeInstructionFile("", "MERGED")
	assert.Contains(t, got, beginMarker)
	assert.Contains(t, got, endMarker)
	assert.Contains(t, got, "MERGED")
	assert.Contains(t, got, updateNote)
}

func TestMergeInstructionFile_ReplacesExistingBlock(t *testing.T) {
	existing := "user notes\n\n" + beginMarker + "\nold\n" + endMarker + "\n"
	got := MergeInstructionFile(existing, "NEW")
	assert.Contains(t, got, "user notes")
	assert.Contains(t, got, "NEW")
	assert.NotContains(t, got, "old")
}

func TestMergeInstructionFile_Idempotent(t *testing.T) {
	merged, _ := mergedBody(testFS(), testNames)
	once := MergeInstructionFile("", merged)
	twice := MergeInstructionFile(once, merged)
	assert.Equal(t, once, twice, "re-running must not accumulate whitespace or duplicates")
}

func TestMakeMDC(t *testing.T) {
	got := makeMDC("mysql-query", "BODY")
	assert.Equal(t, "---\ndescription: Run SQL with mysql-cli: SELECT query, txn, DML (INSERT/UPDATE/DELETE), DDL\nglobs: *.sql\nalwaysApply: false\n---\nBODY", got)
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agents/ -run 'TestSkillBody|TestMergedBody|TestMergeInstructionFile|TestMakeMDC' -v`
Expected: FAIL(符号未定义 / 包无文件)。

- [ ] **Step 4: 写实现**

`internal/agents/merge.go`:
```go
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
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agents/ -run 'TestSkillBody|TestMergedBody|TestMergeInstructionFile|TestMakeMDC' -v`
Expected: PASS,全部通过。

- [ ] **Step 6: 提交**

```bash
git add internal/agents/merge.go internal/agents/merge_test.go internal/agents/testutil_test.go
git commit -m "feat(agents): add skill merge primitives (SkillBody/MergeInstructionFile/makeMDC)"
```

---

### Task 2: agent 检测(`internal/agents/detect.go`)

**Files:**
- Create: `internal/agents/detect.go`
- Test: `internal/agents/detect_test.go`

**Interfaces:**
- Produces: `Detect(home, projectDir string) []string`(返回已装 agent 名,顺序同 `AllAgents`)。
- Consumes: agent 名常量(在 Task 5 的 `agents.go` 定义;本任务先在 `detect.go` 顶部用占位常量?**否**——为避免循环,把 agent 名常量放在 `agents.go`,Task 2 依赖 Task 5 的常量。调整顺序:**先做 Task 5 的常量定义**,或把常量放进 `detect.go`。)

> **顺序调整**:agent 名常量(`Claude` 等)放 `agents.go`。为让 Task 2 编译,先在 Task 2 创建一个仅含常量与 `AllAgents` 的最小 `agents.go`,Task 5 再往里加 `Install`/`Run`。故 Task 2 同时创建 `agents.go`(常量部分)。

- [ ] **Step 1: 写最小 `agents.go`(常量)**

`internal/agents/agents.go`(本步只写常量,`Install`/`Run` 留到 Task 5):
```go
// Package agents installs bundled mysql-cli skills into AI agents in each
// agent's native format. It is the Go port of scripts/install-skills.sh and
// depends only on the standard library; skill content is injected via fs.FS.
package agents

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
```

- [ ] **Step 2: 写失败测试**

`internal/agents/detect_test.go`:
```go
package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetect_None(t *testing.T) {
	tmp := t.TempDir()
	assert.Empty(t, Detect(tmp, t.TempDir()))
}

func TestDetect_ClaudeGlobal(t *testing.T) {
	home := t.TempDir()
	requireDir(t, home, ".claude")
	assert.Equal(t, []string{Claude}, Detect(home, t.TempDir()))
}

func TestDetect_CopilotProjectOnly(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	requireDir(t, proj, ".github")
	got := Detect(home, proj)
	assert.Contains(t, got, Copilot)
	assert.NotContains(t, got, Claude)
}

func TestDetect_AiderGlobal(t *testing.T) {
	home := t.TempDir()
	requireFile(t, home, ".aider.conf.yml", "read: [.aider.instructions.md]\n")
	got := Detect(home, t.TempDir())
	assert.Contains(t, got, Aider)
}

func TestDetect_AllPresent(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	requireDir(t, home, ".claude")
	requireDir(t, home, ".cursor")
	requireDir(t, home, ".codex")
	requireDir(t, home, ".config/opencode")
	requireDir(t, proj, ".github")
	requireFile(t, proj, ".windsurfrules", "")
	requireFile(t, home, ".aider.conf.yml", "")
	got := Detect(home, proj)
	assert.Equal(t, AllAgents, got)
}

func requireDir(t *testing.T, parts ...string) {
	t.Helper()
	requireNoErr(t, os.MkdirAll(filepath.Join(parts...), 0o755))
}
func requireFile(t *testing.T, parts ...string) {
	t.Helper()
	requireNoErr(t, os.WriteFile(filepath.Join(parts...), []byte(""), 0o644))
}
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agents/ -run TestDetect -v`
Expected: FAIL(`Detect` 未定义)。

- [ ] **Step 4: 写实现**

`internal/agents/detect.go`:
```go
package agents

import (
	"os"
	"path/filepath"
)

// Detect returns the agents present on the system, mirroring
// install-skills.sh detect_agents(). projectDir may be "".
func Detect(home, projectDir string) []string {
	var found []string
	any := func(paths ...string) bool {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	}
	if any(filepath.Join(home, ".claude"), filepath.Join(projectDir, ".claude")) {
		found = append(found, Claude)
	}
	if any(filepath.Join(home, ".cursor"), filepath.Join(projectDir, ".cursor")) {
		found = append(found, Cursor)
	}
	if any(filepath.Join(home, ".codex"), filepath.Join(projectDir, "AGENTS.md")) {
		found = append(found, Codex)
	}
	if any(filepath.Join(home, ".config", "opencode"), filepath.Join(projectDir, ".opencode")) {
		found = append(found, OpenCode)
	}
	if any(filepath.Join(projectDir, ".github")) {
		found = append(found, Copilot)
	}
	if any(filepath.Join(projectDir, ".windsurfrules"), filepath.Join(home, ".codeium"), filepath.Join(home, ".windsurf")) {
		found = append(found, Windsurf)
	}
	if any(filepath.Join(projectDir, ".aider.conf.yml"), filepath.Join(home, ".aider.conf.yml")) {
		found = append(found, Aider)
	}
	return found
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agents/ -run TestDetect -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/agents/agents.go internal/agents/detect.go internal/agents/detect_test.go
git commit -m "feat(agents): add agent detection (Detect) mirroring install-skills.sh"
```

---

### Task 3: 原生格式 installer(claude / cursor)

**Files:**
- Create: `internal/agents/install.go`(本任务先写 helper + claude + cursor;Task 4 追加其余 5 个)
- Test: `internal/agents/install_test.go`

**Interfaces:**
- Produces: `Options`、`InstallResult`、`ErrProjectOnly`、`copySkillTree(opts, dstDir, skill) error`、`writeIfNotDryRun(opts, path, content) error`、`installClaude(opts) ([]string, error)`、`installCursor(opts) ([]string, error)`。
- Consumes: `makeMDC`、`SkillBody`(Task 1);agent 常量(Task 2)。

- [ ] **Step 1: 写失败测试**

`internal/agents/install_test.go`:
```go
package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseOpts(home, proj string) Options {
	return Options{Home: home, ProjectDir: proj, FS: testFS(), Names: testNames}
}

func TestInstallClaude_GlobalAndProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	paths, err := installClaude(baseOpts(home, proj))
	require.NoError(t, err)
	for _, s := range testNames {
		assert.FileExists(t, filepath.Join(home, ".claude", "skills", s, "SKILL.md"))
		assert.FileExists(t, filepath.Join(proj, ".claude", "skills", s, "SKILL.md"))
	}
	assert.NotEmpty(t, paths)
}

func TestInstallClaude_GlobalOnlyByDefault(t *testing.T) {
	home := t.TempDir()
	paths, err := installClaude(baseOpts(home, ""))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(home, ".claude", "skills", "mysql-query", "SKILL.md"))
	assert.NoDirExists(t, filepath.Join(".", ".claude")) // no project install without --project-dir
	_ = paths
}

func TestInstallClaude_NoGlobal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	o := baseOpts(home, proj)
	o.NoGlobal = true
	_, err := installClaude(o)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
	assert.FileExists(t, filepath.Join(proj, ".claude", "skills", "mysql-shared", "SKILL.md"))
}

func TestInstallCursor_MDCFiles(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cursor"), 0o755))
	_, err := installCursor(baseOpts(home, proj))
	require.NoError(t, err)
	body, _ := os.ReadFile(filepath.Join(proj, ".cursor", "rules", "mysql-query.mdc"))
	assert.Contains(t, string(body), "description: Run SQL with mysql-cli")
	assert.Contains(t, string(body), "globs: *.sql")
	assert.FileExists(t, filepath.Join(home, ".cursor", "rules", "mysql-shared.mdc"))
}

func TestInstallCursor_DryRun(t *testing.T) {
	home := t.TempDir()
	o := baseOpts(home, "")
	o.DryRun = true
	_, err := installCursor(o)
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(home, ".cursor"))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agents/ -run 'TestInstallClaude|TestInstallCursor' -v`
Expected: FAIL(`Options`/`installClaude` 未定义)。

- [ ] **Step 3: 写实现**

`internal/agents/install.go`:
```go
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
	_ = os.RemoveAll(filepath.Join(dstDir, skill))
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agents/ -run 'TestInstallClaude|TestInstallCursor' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/agents/install.go internal/agents/install_test.go
git commit -m "feat(agents): add native installers for claude and cursor"
```

---

### Task 4: 标记合并 installer(codex / opencode / copilot / windsurf / aider)

**Files:**
- Modify: `internal/agents/install.go`(追加 5 个 installer)
- Test: `internal/agents/install_test.go`(追加用例)

**Interfaces:**
- Produces: `installCodex`、`installOpenCode`、`installCopilot`、`installWindsurf`、`installAider`(签名均为 `func(Options) ([]string, error)`)。
- Consumes: `MergeInstructionFile`、`mergedBody`(Task 1);`ErrProjectOnly`(Task 3)。

- [ ] **Step 1: 写失败测试**

追加到 `internal/agents/install_test.go`:
```go
func TestInstallCodex_MergesIdempotently(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	o := baseOpts(home, proj)
	_, err := installCodex(o)
	require.NoError(t, err)
	p := filepath.Join(proj, "AGENTS.md")
	first, _ := os.ReadFile(p)
	// re-run: content stable
	_, err = installCodex(o)
	require.NoError(t, err)
	second, _ := os.ReadFile(p)
	assert.Equal(t, string(first), string(second))
	assert.Contains(t, string(first), beginMarker)
	assert.Contains(t, string(first), "## mysql-cli skill: mysql-query")
	assert.FileExists(t, filepath.Join(home, ".codex", "instructions.md"))
}

func TestInstallCopilot_ProjectOnly_NeedsProjectDir(t *testing.T) {
	home := t.TempDir()
	_, err := installCopilot(baseOpts(home, ""))
	assert.ErrorIs(t, err, ErrProjectOnly)
}

func TestInstallCopilot_ProjectInstall(t *testing.T) {
	proj := t.TempDir()
	_, err := installCopilot(baseOpts(t.TempDir(), proj))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(proj, ".github", "copilot-instructions.md"))
}

func TestInstallWindsurf_GlobalAndProject(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	_, err := installWindsurf(baseOpts(home, proj))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(home, ".windsurfrules"))
	assert.FileExists(t, filepath.Join(proj, ".windsurfrules"))
}

func TestInstallAider_GlobalWithoutProjectDir(t *testing.T) {
	home := t.TempDir()
	_, err := installAider(baseOpts(home, ""))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(home, ".aider.instructions.md"))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agents/ -run 'TestInstallCodex|TestInstallCopilot|TestInstallWindsurf|TestInstallAider' -v`
Expected: FAIL(函数未定义)。

- [ ] **Step 3: 写实现**

追加到 `internal/agents/install.go`:
```go
// writeMerged reads existing file at path (if any), replaces/appends the marked
// block with the merged skill bodies, and writes it back. No-op body on DryRun
// still records the path. Returns the path written.
func writeMerged(opts Options, path string) (string, error) {
	merged, err := mergedBody(opts.FS, opts.Names)
	if err != nil {
		return path, err
	}
	existing, _ := os.ReadFile(path) // absent is OK
	content := MergeInstructionFile(string(existing), merged)
	if err := writeIfNotDryRun(opts, path, content); err != nil {
		return path, err
	}
	return path, nil
}

// installCodex writes the merged block to <proj>/AGENTS.md and ~/.codex/instructions.md.
func installCodex(opts Options) ([]string, error) {
	var paths []string
	if opts.ProjectDir != "" {
		p, err := writeMerged(opts, filepath.Join(opts.ProjectDir, "AGENTS.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	if !opts.NoGlobal {
		p, err := writeMerged(opts, filepath.Join(opts.Home, ".codex", "instructions.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// installOpenCode writes the merged block to <proj>/.opencode/instructions.md
// and ~/.config/opencode/instructions.md.
func installOpenCode(opts Options) ([]string, error) {
	var paths []string
	if opts.ProjectDir != "" {
		p, err := writeMerged(opts, filepath.Join(opts.ProjectDir, ".opencode", "instructions.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	if !opts.NoGlobal {
		p, err := writeMerged(opts, filepath.Join(opts.Home, ".config", "opencode", "instructions.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// installCopilot is project-only: writes <proj>/.github/copilot-instructions.md.
// Returns ErrProjectOnly if ProjectDir is empty.
func installCopilot(opts Options) ([]string, error) {
	if opts.ProjectDir == "" {
		return nil, ErrProjectOnly
	}
	p, err := writeMerged(opts, filepath.Join(opts.ProjectDir, ".github", "copilot-instructions.md"))
	if err != nil {
		return []string{p}, err
	}
	return []string{p}, nil
}

// installWindsurf writes the merged block to <proj>/.windsurfrules and ~/.windsurfrules.
func installWindsurf(opts Options) ([]string, error) {
	var paths []string
	if opts.ProjectDir != "" {
		p, err := writeMerged(opts, filepath.Join(opts.ProjectDir, ".windsurfrules"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	if !opts.NoGlobal {
		p, err := writeMerged(opts, filepath.Join(opts.Home, ".windsurfrules"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// installAider writes the merged block to <proj>/.aider.instructions.md and
// ~/.aider.instructions.md. (aider has a global install path, unlike copilot.)
func installAider(opts Options) ([]string, error) {
	var paths []string
	if opts.ProjectDir != "" {
		p, err := writeMerged(opts, filepath.Join(opts.ProjectDir, ".aider.instructions.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	if !opts.NoGlobal {
		p, err := writeMerged(opts, filepath.Join(opts.Home, ".aider.instructions.md"))
		paths = append(paths, p)
		if err != nil {
			return paths, err
		}
	}
	return paths, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agents/ -run 'TestInstallCodex|TestInstallCopilot|TestInstallWindsurf|TestInstallAider' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/agents/install.go internal/agents/install_test.go
git commit -m "feat(agents): add merge installers for codex/opencode/copilot/windsurf/aider"
```

---

### Task 5: `Install` 调度器 + `Run` 选择器(`internal/agents/agents.go`)

**Files:**
- Modify: `internal/agents/agents.go`(在 Task 2 常量基础上追加)
- Test: `internal/agents/agents_test.go`

**Interfaces:**
- Produces: `ValidAgent(name string) bool`、`Install(agent string, opts Options) InstallResult`、`Run(sel string, opts Options) []InstallResult`、`parseList(sel string) []string`、选择常量 `SelAuto="auto"`、`SelAll="all"`。
- Consumes: 7 个 `installXxx`(Task 3/4)、`Detect`(Task 2)、`AllAgents`(Task 2)、`ErrProjectOnly`(Task 3)。

- [ ] **Step 1: 写失败测试**

`internal/agents/agents_test.go`:
```go
package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidAgent(t *testing.T) {
	assert.True(t, ValidAgent("claude"))
	assert.False(t, ValidAgent("nope"))
}

func TestInstall_UnknownAgent(t *testing.T) {
	r := Install("nope", baseOpts(t.TempDir(), ""))
	assert.Equal(t, "error", r.Status)
	assert.Contains(t, r.Error, "unknown agent")
}

func TestInstall_CopilotSkippedWithoutProjectDir(t *testing.T) {
	r := Install(Copilot, baseOpts(t.TempDir(), ""))
	assert.Equal(t, "skipped", r.Status)
}

func TestInstall_ClaudeSuccess(t *testing.T) {
	r := Install(Claude, baseOpts(t.TempDir(), ""))
	assert.Equal(t, "installed", r.Status)
	assert.NotEmpty(t, r.Paths)
}

func TestRun_All(t *testing.T) {
	res := Run(SelAll, baseOpts(t.TempDir(), ""))
	assert.Len(t, res, len(AllAgents))
	// copilot has no project dir -> skipped; rest installed (global)
	for _, r := range res {
		if r.Agent == Copilot {
			assert.Equal(t, "skipped", r.Status)
		} else {
			assert.Equal(t, "installed", r.Status, r.Agent)
		}
	}
}

func TestRun_AutoDefaultsToClaudeWhenNoneDetected(t *testing.T) {
	res := Run(SelAuto, baseOpts(t.TempDir(), t.TempDir()))
	assert.Len(t, res, 1)
	assert.Equal(t, Claude, res[0].Agent)
	assert.True(t, res[0].Detected == false) // not actually present
}

func TestRun_AutoDetectsPresent(t *testing.T) {
	home := t.TempDir()
	requireDir(t, home, ".claude")
	res := Run(SelAuto, baseOpts(home, t.TempDir()))
	assert.Len(t, res, 1)
	assert.Equal(t, Claude, res[0].Agent)
	assert.True(t, res[0].Detected)
}

func TestRun_CommaList(t *testing.T) {
	res := Run("claude,cursor", baseOpts(t.TempDir(), t.TempDir()))
	assert.Len(t, res, 2)
	assert.Equal(t, Claude, res[0].Agent)
	assert.Equal(t, Cursor, res[1].Agent)
}

func TestParseList(t *testing.T) {
	assert.Equal(t, []string{Claude, Cursor}, parseList("claude, cursor "))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agents/ -run 'TestValidAgent|TestInstall|TestRun|TestParseList' -v`
Expected: FAIL(`Install`/`Run`/`parseList` 未定义)。

- [ ] **Step 3: 写实现**

追加到 `internal/agents/agents.go`(常量块下方):
```go
import (
	"errors"
	"fmt"
	"strings"
)

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
	case err == nil:
		r.Status = "installed"
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
```

> 注:`agents.go` 顶部 `package agents` 已存在(Task 2);本步在文件末尾追加 import 块与函数。若 import 与 Task 2 冲突,合并到顶部 import。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agents/ -run 'TestValidAgent|TestInstall|TestRun|TestParseList' -v`
Expected: PASS。

- [ ] **Step 5: 全包测试 + 覆盖率**

Run: `go test -cover ./internal/agents/`
Expected: PASS,覆盖率 ≥80%(若不足,补 `DryRun`/`NoGlobal` 边界用例)。

- [ ] **Step 6: 提交**

```bash
git add internal/agents/agents.go internal/agents/agents_test.go
git commit -m "feat(agents): add Install dispatcher and Run selection (auto/all/list)"
```

---

### Task 6: `mysql-cli init` cobra 命令 + 退出码

**Files:**
- Create: `internal/cli/init.go`、`internal/cli/init_test.go`
- Modify: `internal/cli/root.go`(const 加 `ExitInitFailed = 11`;`AddCommand` 加 `newInitCmd()`)、`internal/cli/errors.go`(`mapError` + `errorCodeName`)

**Interfaces:**
- Produces: `newInitCmd() *cobra.Command`、`ErrInitAllFailed`。
- Consumes: `agents.Run`/`agents.Options`/`agents.InstallResult`(Task 5)、`bundle.SkillsFS`/`bundle.SkillNames`(根包)、`mapError`/退出码(`cli` 包)。

- [ ] **Step 1: 改 `root.go` 加退出码与命令注册**

`internal/cli/root.go` const 块(在 `ExitConfigError = 10` 后追加一行):
```go
	ExitConfigError            = 10
	ExitInitFailed             = 11
```
`newRootCmd` 的 `root.AddCommand(...)`(在 `newSkillCmd(),` 后追加):
```go
		newSkillCmd(),
		newInitCmd(),
```

- [ ] **Step 2: 改 `errors.go` 加映射**

`internal/cli/errors.go` `mapError` switch 顶部(`case errors.Is(err, safety.ErrReadonlyViolation):` 之前)插入:
```go
	case errors.Is(err, ErrInitAllFailed):
		return ExitInitFailed
```
`errorCodeName` switch(在 `case ExitConfigError:` 块后)插入:
```go
	case ExitInitFailed:
		return "INIT_FAILED"
```
(`errors` 包已在 errors.go import。)

- [ ] **Step 3: 写失败测试**

`internal/cli/init_test.go`:
```go
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runInit(t *testing.T, home string, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	g := &Globals{Format: "json", out: &buf}
	root := newRootCmd(g)
	// isolate HOME so detection is deterministic
	t.Setenv("HOME", home)
	root.SetArgs(append([]string{"init"}, args...))
	code := ExitOK
	if err := root.Execute(); err != nil {
		code = mapError(err)
	}
	return code, buf.String()
}

func TestInit_AutoDefaultClaude_JSON(t *testing.T) {
	home := t.TempDir()
	code, out := runInit(t, home, "-j")
	require.Equal(t, ExitOK, code)
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			Agents []struct {
				Agent  string `json:"agent"`
				Status string `json:"status"`
			} `json:"agents"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &env))
	assert.True(t, env.Success)
	require.Len(t, env.Data.Agents, 1)
	assert.Equal(t, "claude", env.Data.Agents[0].Agent)
	assert.Equal(t, "installed", env.Data.Agents[0].Status)
	assert.FileExists(t, filepath.Join(home, ".claude", "skills", "mysql-shared", "SKILL.md"))
}

func TestInit_DryRun_NoFiles(t *testing.T) {
	home := t.TempDir()
	code, _ := runInit(t, home, "--dry-run")
	assert.Equal(t, ExitOK, code)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
}

func TestInit_NoGlobal_WithProjectDir(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	code, _ := runInit(t, home, "--agent", "claude", "--project-dir", proj, "--no-global")
	assert.Equal(t, ExitOK, code)
	assert.NoDirExists(t, filepath.Join(home, ".claude"))
	assert.FileExists(t, filepath.Join(proj, ".claude", "skills", "mysql-query", "SKILL.md"))
}

func TestInit_UnknownAgent_ExitNonZero(t *testing.T) {
	home := t.TempDir()
	code, _ := runInit(t, home, "--agent", "nope")
	// unknown agent -> Install returns status "error" -> all failed -> ExitInitFailed
	assert.Equal(t, ExitInitFailed, code)
}

func TestInit_TextOutput(t *testing.T) {
	home := t.TempDir()
	code, out := runInit(t, home)
	assert.Equal(t, ExitOK, code)
	assert.Contains(t, out, "mysql-cli skill init")
	assert.Contains(t, out, "claude")
}

// ensure HOME restore doesn't leak
var _ = os.Setenv
```

- [ ] **Step 4: 跑测试确认失败**

Run: `go test ./internal/cli/ -run TestInit -v`
Expected: FAIL(`newInitCmd`/`ErrInitAllFailed` 未定义;`ExitInitFailed` 已定义于 root.go 但命令未注册)。

- [ ] **Step 5: 写实现**

`internal/cli/init.go`:
```go
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	bundle "github.com/AllenMuu/mysql-cli"
	"github.com/AllenMuu/mysql-cli/internal/agents"
	"github.com/spf13/cobra"
)

// ErrInitAllFailed is returned when every selected agent install failed.
var ErrInitAllFailed = errors.New("all agent skill installs failed")

func newInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "Install bundled skills into detected AI agents",
		Long: "Detect installed AI agents (Claude Code, Cursor, Codex, OpenCode, " +
			"Copilot, Windsurf, Aider) and install the bundled mysql-cli skills " +
			"into each in the agent's native format. Idempotent and re-runnable.",
		Args: cobra.NoArgs,
	}
	c.Flags().String("agent", "auto", "agent selection: auto|all|comma list (claude,cursor,...)")
	c.Flags().String("project-dir", "", "project root for project-level install")
	c.Flags().Bool("no-global", false, "skip global install")
	c.Flags().Bool("dry-run", false, "report without writing files")
	c.Flags().BoolP("json", "j", false, "emit JSON instead of text")

	c.RunE = func(cmd *cobra.Command, args []string) error {
		agentSel, _ := cmd.Flags().GetString("agent")
		projectDir, _ := cmd.Flags().GetString("project-dir")
		noGlobal, _ := cmd.Flags().GetBool("no-global")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		asJSON, _ := cmd.Flags().GetBool("json")

		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home, _ = os.Getwd()
		}
		fsys, err := bundle.SkillsFS()
		if err != nil {
			return err
		}
		names, err := bundle.SkillNames()
		if err != nil {
			return err
		}
		opts := agents.Options{
			Home:       home,
			ProjectDir: projectDir,
			NoGlobal:   noGlobal,
			DryRun:     dryRun,
			FS:         fsys,
			Names:      names,
		}
		results := agents.Run(agentSel, opts)

		var emitErr error
		if asJSON {
			emitErr = emitInitJSON(cmd.OutOrStdout(), results)
		} else {
			emitInitText(cmd.OutOrStdout(), results)
		}
		if emitErr != nil {
			return emitErr
		}
		if allFailed(results) {
			return ErrInitAllFailed
		}
		return nil
	}
	return c
}

func allFailed(results []agents.InstallResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Status != "error" {
			return false
		}
	}
	return true
}

func emitInitText(w io.Writer, results []agents.InstallResult) {
	fmt.Fprintln(w, "🔧 mysql-cli skill init")
	for _, r := range results {
		switch r.Status {
		case "installed":
			fmt.Fprintf(w, "   ✅ %-10s %s\n", r.Agent, strings.Join(r.Paths, ", "))
		case "skipped":
			fmt.Fprintf(w, "   ⏭️  %-10s %s\n", r.Agent, r.Error)
		case "error":
			fmt.Fprintf(w, "   ❌ %-10s %s\n", r.Agent, r.Error)
		}
	}
}

func emitInitJSON(w io.Writer, results []agents.InstallResult) error {
	type envelope struct {
		Success bool                   `json:"success"`
		Data    map[string]any         `json:"data"`
		Error   string                 `json:"error"`
	}
	env := envelope{
		Success: !allFailed(results),
		Data:    map[string]any{"agents": results},
	}
	if !env.Success {
		env.Error = "all agent skill installs failed"
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(out))
	return nil
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/cli/ -run TestInit -v`
Expected: PASS。

- [ ] **Step 7: 全量编译 + 测试 + 覆盖率**

Run: `go build ./... && go vet ./... && go test -cover ./internal/cli/ ./internal/agents/`
Expected: 全 PASS;`internal/agents`、`internal/cli` 覆盖率 ≥80%。

- [ ] **Step 8: 手工烟测**

Run: `go run ./cmd/mysql-cli init --dry-run -j` 与 `go run ./cmd/mysql-cli init --help`
Expected: dry-run 输出 JSON 且不落盘;`--help` 列出全部 flag。

- [ ] **Step 9: 提交**

```bash
git add internal/cli/init.go internal/cli/init_test.go internal/cli/root.go internal/cli/errors.go
git commit -m "feat(cli): add `mysql-cli init` command to install skills into detected agents"
```

---

### Task 7: 废弃 bash 脚本 + README 更新

**Files:**
- Modify: `scripts/install-skills.sh`(顶部加 banner)
- Modify: `README.md`(安装段补 `mysql-cli init`)

**Interfaces:** 无代码接口;文档与脚本行为。

- [ ] **Step 1: 给 bash 脚本加废弃 banner**

在 `scripts/install-skills.sh` 的 `set -euo pipefail`(第 30 行)之后插入:
```bash
echo "⚠️  install-skills.sh is deprecated; use \`mysql-cli init\` instead." >&2
echo "    This script will be removed in a future release." >&2
```

- [ ] **Step 2: 验证脚本仍可运行**

Run: `bash -n scripts/install-skills.sh && ./scripts/install-skills.sh --help`
Expected: `bash -n` 无语法错误;`--help` 正常输出用法(banner 也打印到 stderr)。

- [ ] **Step 3: 更新 README 安装段**

`README.md`「Step 2 - Install Agent Skills」处,在现有两个 Option 之前/之后增加推荐项:
```markdown
**Option 0 - `mysql-cli init` (recommended, no repo clone needed):**

```bash
mysql-cli init                       # auto-detect installed agents, install to global
mysql-cli init --agent all           # install for all 7 agents
mysql-cli init --project-dir ~/my-project --no-global  # project-level only
mysql-cli init -j                    # JSON output for agents
```
```
(保留现有 Option A/B 不删,仅在上方标注 `mysql-cli init` 为推荐。)

- [ ] **Step 4: 验证 README 渲染**

Run: 确认 README 无断链 / markdown 语法正常(目测)。

- [ ] **Step 5: 提交**

```bash
git add scripts/install-skills.sh README.md
git commit -m "docs: deprecate install-skills.sh in favor of `mysql-cli init`"
```

---

## Self-Review

**1. Spec coverage:**
- D1 npx 分发 → **Plan 2**(本计划不涉及,已声明拆分)。
- D2 两步流 → install 属 Plan 2;`init` 属本计划 Task 6。✅
- D3 auto 检测 → Task 2 Detect + Task 5 Run(SelAuto 默认 claude)。✅
- D4 bash->Go 移植 → Task 1-5 完整移植 7 agent。✅
- D7 默认全局、`--project-dir` 追加项目、`--no-global` → Task 3/4 installer + Task 6 flag。✅
- 退出码新增不占既有 → Task 6 `ExitInitFailed=11` + mapError。✅
- copilot 纯项目级 → Task 4 `installCopilot` + `ErrProjectOnly` → Task 5 status "skipped"。✅
- aider 全局修正 → Task 4 `installAider` 全局位 + Task 2 检测 `~/.aider.conf.yml`。✅
- bash 脚本废弃 banner → Task 7。✅
- 测试 ≥80% → Task 5 Step 5、Task 6 Step 7。✅

**2. Placeholder scan:** 无 TBD/TODO;每步含完整代码与命令。✅

**3. Type consistency:**
- `Options` 字段(`Home/ProjectDir/NoGlobal/DryRun/FS/Names`)Task 3 定义 → Task 4/5/6 一致使用。✅
- `InstallResult{Agent,Detected,Paths,Status,Error}` Task 3 定义 → Task 5 `Install`/`Run` 填充 → Task 6 `emitInitJSON`/`emitInitText` 读取一致。✅
- `Install(agent, opts) InstallResult` 与 `Run(sel, opts) []InstallResult` 签名跨任务一致。✅
- `ErrProjectOnly` Task 3 定义 → Task 4 返回 → Task 5 `errors.Is` 识别。✅
- `ExitInitFailed=11` Task 6 root.go 定义 → errors.go mapError → init_test.go 断言。✅
- installer 签名统一 `func(Options) ([]string, error)`(Task 3/4 七个)→ Task 5 `installers` map 注册。✅

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-23-npx-init-plan-1-go.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?

> Plan 2(分发侧:`dist/npm/` + GoReleaser + release workflow)在本计划交付后另起一份。
