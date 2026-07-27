# mysql-cli skill 安装接入 vercel-labs/skills 生态 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除 mysql-cli 自研 skill 安装代码(`init` 命令、`skill` 子命令组、`internal/agents`、`internal/skillscheck`、`bundle.go`、`scripts/install-skills.sh`),改由 `npx skills add AllenMuu/mysql-cli`(vercel-labs/skills 生态)安装,仓库侧新增 `.well-known/agent-skills/index.json`。

**Architecture:** mysql-cli 二进制与 skill 安装解耦。二进制只保留查询/事务/schema 核心;skill 成为仓库侧资产,由 vercel-labs/skills 生态(75+ agent、成熟交互式 TUI)管理。mysql-cli 不再内嵌 skill、不写任何 TUI 代码。

**Tech Stack:** Go 1.22、cobra、vercel-labs/skills(npx)、JSON。

## Global Constraints

- Go 1.22(`go.mod`);`go build ./...` / `go vet ./...` / `go test ./...` 必须全绿。
- 项目测试覆盖率目标 ≥80%(`go test -cover ./...`)。
- skill frontmatter 校验:`./scripts/skill-format-check.sh skills/` 必须通过。
- Conventional commits;commit 不带 attribution(全局 settings 已禁)。
- 远程是 GitHub(memory 记录:repo-is-github),`gh` 可用。
- 删除顺序遵循依赖:先删消费者(`init.go`/`skill.go`),再删被依赖包(`agents`/`skillscheck`/`bundle`),保证每个任务后 `go build ./...` 通过。
- `config init`(config 子命令)与本次删除的 `mysql-cli init`(skill 安装)是不同命令,前者保留不动。

## File Structure

**删除:**
- `internal/cli/init.go` -- `mysql-cli init` 命令(`agents` 包主消费者)
- `internal/cli/init_test.go` -- init 测试
- `internal/cli/skill.go` -- `mysql-cli skill` 子命令组(list/version/check/install)
- `internal/cli/skill_test.go` -- skill 测试
- `internal/agents/` -- 整个包(agents.go/detect.go/install.go/merge.go + tests)
- `internal/skillscheck/` -- 整个包(skillscheck.go + test)
- `bundle.go` -- 根包 `//go:embed skills` + `SkillNames/SkillFile/SkillsFS`
- `scripts/install-skills.sh` + `scripts/install-skills-test.sh`

**修改:**
- `internal/cli/root.go` -- 移除 `newSkillCmd()`/`newInitCmd()` 的 `AddCommand` 注册
- `internal/cli/version.go` -- 注释去掉 `skill version` 提及
- `README.md` / `README-zh.md` -- 安装说明改为 `npx skills add`
- `AGENTS.md` -- "Skill 体系"章节重写
- `CHANGELOG.md` -- `[Unreleased] > Breaking` 加条目

**新增:**
- `.well-known/agent-skills/index.json` -- vercel-labs/skills 首选发现机制,声明 3 skill

**保留不动:** `internal/{config,conn,query,result,safety,schema,format,repl}`、`internal/cli/version.go` 的 `version` 命令、`scripts/skill-format-check.sh` + `.github/workflows/skill-format-check.yml`、`skill-template/`、`skills/` 目录、`dist/npm/`。

---

### Task 1: 移除 `init` 与 `skill` 子命令注册及命令文件

**Files:**
- Modify: `internal/cli/root.go:102-116`(AddCommand 块)
- Delete: `internal/cli/init.go`, `internal/cli/init_test.go`, `internal/cli/skill.go`, `internal/cli/skill_test.go`

**Interfaces:**
- Consumes: 无(这是删除任务,移除的是命令注册与实现)
- Produces: `internal/cli` 包不再导出 `newInitCmd`/`newSkillCmd`;后续任务可安全删除 `agents`/`skillscheck`/`bundle`(它们的消费者本任务已移除)

- [ ] **Step 1: 移除 root.go 里的两个注册行**

对 `internal/cli/root.go` 做一处 Edit,把 AddCommand 块中的 `newSkillCmd()` 与 `newInitCmd()` 两行删掉,保留 `newConfigCmd(g)`:

old_string:
```
		newAnalyzeCmd(g),
		newSkillCmd(),
		newConfigCmd(g),
		newInitCmd(),
		newVersionCmd(),
```
new_string:
```
		newAnalyzeCmd(g),
		newConfigCmd(g),
		newVersionCmd(),
```

- [ ] **Step 2: 删除命令文件与测试**

Run:
```bash
git rm internal/cli/init.go internal/cli/init_test.go internal/cli/skill.go internal/cli/skill_test.go
```
Expected: 四个文件被删除,`git status` 显示 `deleted:`。

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 成功,无输出。`internal/agents`/`internal/skillscheck`/`bundle` 此时仍存在但已无消费者,自身可独立编译。

- [ ] **Step 4: 验证测试**

Run: `go test ./...`
Expected: 全绿。`config_cmd_test.go` 里的 `config init` 测试不受影响(config 子命令保留)。

- [ ] **Step 5: 验证静态检查**

Run: `go vet ./...`
Expected: 无告警。

- [ ] **Step 6: Commit**

```bash
git add internal/cli/root.go
git commit -m "refactor(cli): remove init and skill subcommands

Drops mysql-cli init (agents-based install) and the skill subcommand
group (list/version/check/install). Registration removed from root.go.
agents/skillscheck/bundle now have no consumers and will be deleted in
follow-up tasks. Skill install is delegated to npx skills add (see
spec 2026-07-27-skill-install-npx-skills-design)."
```

---

### Task 2: 删除 `internal/agents` 包

**Files:**
- Delete: `internal/agents/`(整个目录:agents.go, detect.go, install.go, merge.go, agents_test.go, detect_test.go, install_test.go, merge_test.go, testutil_test.go)

**Interfaces:**
- Consumes: 无(Task 1 已移除唯一消费者 `init.go`)
- Produces: 无(纯删除)

- [ ] **Step 1: 删除整个包**

Run:
```bash
git rm -r internal/agents
```
Expected: 目录及全部 9 个文件被删除。

- [ ] **Step 2: 验证无悬空引用**

Run: `grep -rn "internal/agents" --include="*.go" . | grep -v '.claude/worktrees'`
Expected: 无输出(无任何 Go 文件再 import `internal/agents`)。

- [ ] **Step 3: 验证编译与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(agents): delete self-built agent install package

internal/agents (7-agent detect/install/merge) is obsolete after
delegating skill install to npx skills add. No remaining consumers."
```

---

### Task 3: 删除 `internal/skillscheck` 包

**Files:**
- Delete: `internal/skillscheck/`(skillscheck.go, skillscheck_test.go)

**Interfaces:**
- Consumes: 无(Task 1 已移除唯一消费者 `skill.go`)
- Produces: 无

- [ ] **Step 1: 删除整个包**

Run:
```bash
git rm -r internal/skillscheck
```
Expected: 目录及 2 个文件被删除。

- [ ] **Step 2: 验证无悬空引用**

Run: `grep -rn "skillscheck" --include="*.go" . | grep -v '.claude/worktrees'`
Expected: 无输出。

- [ ] **Step 3: 验证编译与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(skillscheck): delete bundled-version sync check

skillscheck compared installed skills against the binary-embedded
version. With skills no longer embedded and install delegated to
npx skills (which provides its own update/list), this package is
obsolete."
```

---

### Task 4: 删除 `bundle.go`(根包内嵌)

**Files:**
- Delete: `bundle.go`

**Interfaces:**
- Consumes: 无(Task 1 已移除消费者 `init.go`/`skill.go`;Task 3 已移除 `skillscheck`)
- Produces: 根目录不再有 Go 文件;`go build ./...` 会跳过根目录(无 `.go` 文件),正常构建 `cmd/` 与 `internal/`

- [ ] **Step 1: 验证无悬空引用**

Run: `grep -rn 'AllenMuu/mysql-cli"' --include="*.go" . | grep -v '.claude/worktrees' | grep -v _test.go`
Expected: 无输出(无生产代码再 import 根包 `bundle`)。

- [ ] **Step 2: 删除 bundle.go**

Run:
```bash
git rm bundle.go
```
Expected: 文件被删除。

- [ ] **Step 3: 验证编译与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。根目录无 `.go` 文件,`go build ./...` 跳过根目录,构建 `cmd/mysql-cli` 与 `internal/...`。

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor(bundle): delete embedded skills tree

//go:embed skills was the install source for mysql-cli skill install.
With install delegated to npx skills add (source = GitHub repo), the
embedded tree is dead weight. Skill version truth moves to repo
skills/*/SKILL.md frontmatter."
```

---

### Task 5: 删除 `scripts/install-skills.sh` 及其自测,修正 version.go 注释

**Files:**
- Delete: `scripts/install-skills.sh`, `scripts/install-skills-test.sh`
- Modify: `internal/cli/version.go:18-20`

**Interfaces:**
- Consumes: 无
- Produces: 无

- [ ] **Step 1: 删除安装脚本**

Run:
```bash
git rm scripts/install-skills.sh scripts/install-skills-test.sh
```
Expected: 两个文件被删除。

- [ ] **Step 2: 修正 version.go 注释**

对 `internal/cli/version.go` 做一处 Edit,去掉对已删除的 `skill version` 的提及:

old_string:
```
// newVersionCmd is the top-level `version` subcommand: it prints the binary
// version. This is distinct from `mysql-cli skill version`, which prints the
// versions of the bundled skills.
```
new_string:
```
// newVersionCmd is the top-level `version` subcommand: it prints the binary
// version (injected at release build time via GoReleaser ldflags).
```

- [ ] **Step 3: 验证编译与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。

- [ ] **Step 4: 验证 skill-format-check 仍可用**

Run: `./scripts/skill-format-check.sh skills/`
Expected: 通过(skill-format-check 脚本本身保留,不依赖被删的 install-skills.sh)。

- [ ] **Step 5: Commit**

```bash
git add internal/cli/version.go
git commit -m "chore(scripts): drop install-skills.sh + fix version.go comment

Removes the shell-based installer (replaced by npx skills add). Drops
the stale reference to mysql-cli skill version in version.go comment."
```

---

### Task 6: 新增 `.well-known/agent-skills/index.json`

**Files:**
- Create: `.well-known/agent-skills/index.json`

**Interfaces:**
- Consumes: vercel-labs/skills 的发现逻辑(`add.ts` 优先读此文件)
- Produces: `npx skills add AllenMuu/mysql-cli` 能列出/发现 3 个 skill

- [ ] **Step 1: 创建 index.json**

写入 `.well-known/agent-skills/index.json`:

```json
{
  "skills": [
    {
      "name": "mysql-shared",
      "description": "mysql-cli shared rules: config & datasource, global flags, output formats, error recovery, safety model, stable exit codes. Required by mysql-query and mysql-schema; install all three together.",
      "path": "skills/mysql-shared/SKILL.md"
    },
    {
      "name": "mysql-query",
      "description": "Run SQL with mysql-cli: SELECT, DML (INSERT/UPDATE/DELETE), DDL, multi-statement transactions. Read-only by default, JSON output, stable exit codes, tiered write gates.",
      "path": "skills/mysql-query/SKILL.md"
    },
    {
      "name": "mysql-schema",
      "description": "Explore MySQL schema with mysql-cli: tables, databases, sample, read, analyze. Read-only discovery.",
      "path": "skills/mysql-schema/SKILL.md"
    }
  ]
}
```

> 注:字段名基于 vercel-labs/skills 惯例推断。若 Step 2 的 `--list` 不能识别,查 `vercel-labs/skills` 的 `src/add.ts` 对 `.well-known/agent-skills/index.json` 的解析逻辑调整字段名。即使此文件不被识别,vercel-labs/skills 仍会回退到默认 `skills/*/SKILL.md` 扫描,3 个 skill 仍可安装。

- [ ] **Step 2: 验证 vercel-labs/skills 能发现 3 个 skill**

Run: `npx skills add AllenMuu/mysql-cli --list`
Expected: 列出 `mysql-shared`、`mysql-query`、`mysql-schema` 三个 skill(可能附带 description)。

若未列出:回退验证默认扫描是否工作 -- `npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -y --dry-run`(若有 dry-run)或观察 `--list` 是否走默认扫描。若默认扫描也不列出,检查仓库 `skills/*/SKILL.md` 结构是否完整(应已完整)。

- [ ] **Step 3: Commit**

```bash
git add .well-known/agent-skills/index.json
git commit -m "feat(skills): add .well-known/agent-skills index

Declares the 3 mysql-cli skills for vercel-labs/skills' preferred
discovery mechanism. Hints that all three should be installed together
(mysql-query/schema reference ../mysql-shared/SKILL.md)."
```

---

### Task 7: 重写 README.md 安装说明

**Files:**
- Modify: `README.md`(line 55-58, 120-140, 161, 310-325 等所有 `mysql-cli init`/`skill install`/`skill list`/`skill version`/`skill check`/`install-skills.sh` 引用)

**Interfaces:**
- Consumes: 无
- Produces: 用户文档对齐生态接入

- [ ] **Step 1: 替换安装说明段落**

定位 README.md 中 line 120-140 附近的安装说明段落(标题大致为 "Option 0 - `mysql-cli init` (recommended...)" 起到 `skill install`/`install-skills.sh` 示例结束),用以下内容整体替换:

```markdown
## Install Skills (AI Agents)

mysql-cli ships skills for AI agents (Claude Code, Cursor, Codex, and 70+ more)
via the [vercel-labs/skills](https://github.com/vercel-labs/skills) ecosystem.

```bash
npx skills add AllenMuu/mysql-cli
```

This opens an interactive picker: select agents, choose scope (project
`./<agent>/skills/` or global `~/<agent>/skills/`), choose install method
(symlink recommended), and confirm.

Non-interactive (CI / agents):

```bash
npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -g -y
```

> **Install all three skills** (`mysql-shared`, `mysql-query`, `mysql-schema`).
> `mysql-query` and `mysql-schema` reference `../mysql-shared/SKILL.md`; installing
> only one breaks the shared-rules reference.

**No Node.js?** Manually copy the `skills/` directory from this repo into your
agent's skill directory (e.g. `~/.claude/skills/`).
```

- [ ] **Step 2: 替换 Quick Start 里的 init 引用**

README.md line 55-58 附近,把 `mysql-cli init # installs agent skills...` 一行及"Then run `mysql-cli init` to install skills"一句,改为:

```markdown
npx skills add AllenMuu/mysql-cli    # installs agent skills (interactive)
```

并删去/改写"Then run `mysql-cli init` to install skills"为"Then run `npx skills add AllenMuu/mysql-cli` to install skills"。

- [ ] **Step 3: 替换 Agent 路径表与 skill 子命令表**

README.md line 310-316 的 agent 路径表里,把"Install"列的 `./scripts/install-skills.sh --agent <x>` 与 `mysql-cli skill install` 全部改为 `npx skills add AllenMuu/mysql-cli -a <x>`(对 Cursor/Codex/OpenCode/Copilot/Windsurf/Aider 同理,agent 名用 vercel-labs/skills 的命名,如 `claude-code`/`cursor`/`codex`/`opencode`/`github-copilot`/`windsurf`/`aider`)。

README.md line 322-325 的 skill 子命令表(`mysql-cli skill list/version/check/install`)整段删除(这些子命令已不存在)。

- [ ] **Step 4: 清理残留引用**

Run: `grep -n "install-skills\|mysql-cli init\|mysql-cli skill" README.md`
Expected: 无输出。若有残留,逐处替换为对应的 `npx skills add` 表述或删除。

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(readme): switch skill install docs to npx skills add

Replaces mysql-cli init / skill install / install-skills.sh references
with npx skills add AllenMuu/mysql-cli. Notes the all-three install
requirement and the no-Node manual-copy fallback."
```

---

### Task 8: 重写 README-zh.md 安装说明

**Files:**
- Modify: `README-zh.md`(line 110-118, 139, 282-297 等引用)

**Interfaces:**
- Consumes: 无
- Produces: 中文文档对齐

- [ ] **Step 1: 替换安装说明段落**

定位 README-zh.md line 110-118 附近的安装说明,用以下内容整体替换:

```markdown
## 安装 Skill(AI Agent)

mysql-cli 通过 [vercel-labs/skills](https://github.com/vercel-labs/skills) 生态为 AI agent(Claude Code、Cursor、Codex 等 70+ 种)提供 skill。

```bash
npx skills add AllenMuu/mysql-cli
```

会打开交互式选择:选 agent、选 scope(project `./<agent>/skills/` 或 global `~/<agent>/skills/`)、选安装方式(推荐 symlink)、确认。

非交互(CI / agent):

```bash
npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -g -y
```

> **务必安装全部 3 个 skill**(`mysql-shared`、`mysql-query`、`mysql-schema`)。
> `mysql-query` 与 `mysql-schema` 顶部引用 `../mysql-shared/SKILL.md`,只装单个会导致引用断裂。

**无 Node.js?** 手动把仓库 `skills/` 目录复制到 agent 的 skill 目录(如 `~/.claude/skills/`)。
```

- [ ] **Step 2: 替换 agent 路径表与 skill 子命令表**

README-zh.md line 282-288 的 agent 路径表"安装"列改为 `npx skills add AllenMuu/mysql-cli -a <x>`;line 294-297 的 skill 子命令表整段删除。

- [ ] **Step 3: 清理残留引用**

Run: `grep -n "install-skills\|mysql-cli init\|mysql-cli skill" README-zh.md`
Expected: 无输出。若有残留逐处替换。

- [ ] **Step 4: Commit**

```bash
git add README-zh.md
git commit -m "docs(readme-zh): 切换 skill 安装文档到 npx skills add"
```

---

### Task 9: 重写 AGENTS.md 的 Skill 体系章节

**Files:**
- Modify: `AGENTS.md`(line 52 的 `bundle` 条目 + "## Skill 体系"整节)

**Interfaces:**
- Consumes: 无
- Produces: 开发者文档对齐

- [ ] **Step 1: 移除架构图里的 bundle 条目**

AGENTS.md line 52 附近,删除 `bundle` 那一条:
```
- **`bundle`**（根包，`bundle.go`）- `//go:embed skills` 把 skill 定义嵌入二进制，是 `mysql-cli skill install` 零依赖安装的单一来源（与 `scripts/install-skills.sh` 共享 `skills/` 目录）。
```
同时移除架构图(line 33-41 附近)里 `cli（skill 子命令）─-> skillscheck ─-> bundle` 这一行依赖关系。

- [ ] **Step 2: 重写"## Skill 体系（对接 AI agent）"整节**

把该节(从 `## Skill 体系` 到文件末尾或下一 `##` 之前)替换为:

```markdown
## Skill 体系（对接 AI agent）

mysql-cli 的 skill 不再自研安装,而是接入 [vercel-labs/skills](https://github.com/vercel-labs/skills) 生态。skill 是仓库侧资产,由通用 `skills` 包管理器安装到 75+ agent。

- **skill 文件**:`skills/mysql-{shared,query,schema}/SKILL.md`。`mysql-shared` 承载配置/安全模型/退出码/错误自修复,被 `mysql-query`/`mysql-schema` 顶部 `MUST Read` 引用(auto-load,DRY)。
- **安装**:`npx skills add AllenMuu/mysql-cli`(交互式选 agent/scope/install method);非交互 `npx skills add AllenMuu/mysql-cli --skill '*' -a <agent> -g -y`。**务必全装 3 个 skill**,否则 `mysql-shared` 引用断裂。
- **发现机制**:仓库根 `.well-known/agent-skills/index.json` 声明 3 skill(vercel-labs/skills 首选);不加也可走默认 `skills/*/SKILL.md` 扫描。
- **格式校验**:`scripts/skill-format-check.sh` 校验 SKILL.md frontmatter(name/version/description/metadata + semver),CI `.github/workflows/skill-format-check.yml` PR 时强制。改 skill 后本地跑一遍。
- **版本真相源**:skill 版本 = 仓库 `skills/*/SKILL.md` frontmatter 的 `version` 字段(不再二进制内嵌)。
- **无 Node fallback**:手动复制仓库 `skills/` 目录到 agent skill 目录。
```

- [ ] **Step 3: 清理残留引用**

Run: `grep -n "install-skills\|mysql-cli init\|mysql-cli skill\|skillscheck\|bundle.go\|internal/agents" AGENTS.md`
Expected: 无输出(或仅剩新章节里无引用的说明文字)。逐处确认替换。

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md
git commit -m "docs(agents): rewrite skill section for npx skills ecosystem

Removes bundle/agents/skillscheck/install-skills.sh references. Documents
npx skills add as the install path, .well-known discovery, all-three
install requirement, and repo frontmatter as version truth."
```

---

### Task 10: CHANGELOG.md 加 breaking 条目

**Files:**
- Modify: `CHANGELOG.md`(`[Unreleased] > ### Breaking` 段)

**Interfaces:**
- Consumes: 无
- Produces: 无

- [ ] **Step 1: 在 Breaking 段追加条目**

在 `CHANGELOG.md` 的 `## [Unreleased]` > `### Breaking` 列表末尾追加:

```markdown
- **skill 安装迁移至 vercel-labs/skills 生态**:`mysql-cli init`、`mysql-cli skill install/list/version/check` 及 `scripts/install-skills.sh` 全部移除。改用 `npx skills add AllenMuu/mysql-cli` 安装 skill(支持 75+ agent,交互式选 agent/scope/install method)。无 Node 环境可手动复制仓库 `skills/` 目录。skill 版本真相源从二进制内嵌迁移至仓库 `skills/*/SKILL.md` frontmatter。
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): note skill install migration breaking change"
```

---

### Task 11: 最终验证

**Files:**
- 无修改,仅验证

**Interfaces:**
- Consumes: Task 1-10 全部完成
- Produces: 改造完成的可发布状态

- [ ] **Step 1: 全量编译/静态检查/测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿。

- [ ] **Step 2: 覆盖率**

Run: `go test -cover ./...`
Expected: 总覆盖率 ≥80%(删除的测试与生产代码等量,覆盖率应维持)。

- [ ] **Step 3: skill frontmatter 校验**

Run: `./scripts/skill-format-check.sh skills/`
Expected: 通过。

- [ ] **Step 4: 端到端安装实测**

Run: `npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -y`
Expected: 安装成功,3 个 skill 落到 `~/.claude/skills/`(或 `.agents/skills/` + symlink)。验证 `~/.claude/skills/mysql-shared/SKILL.md`、`mysql-query/SKILL.md`、`mysql-schema/SKILL.md` 均存在。

- [ ] **Step 5: 旧命令确认已移除**

Run: `./mysql-cli skill install 2>&1; ./mysql-cli init 2>&1`
Expected: 报 `unknown command "skill"`/`unknown command "init"`(或 cobra 的等效错误)。

- [ ] **Step 6: 残留引用全局扫描**

Run: `grep -rn "install-skills\|mysql-cli init\|mysql-cli skill\|internal/agents\|skillscheck\|bundle.go\|SkillNames\|SkillFile\|SkillsFS" --include="*.go" --include="*.md" --include="*.sh" . | grep -v '.claude/worktrees' | grep -v 'docs/superpowers/'`
Expected: 无输出(`docs/superpowers/` 下的 spec/plan 自身引用不算)。

- [ ] **Step 7: 若全部通过,无需额外 commit**(本任务纯验证)。若 Step 6 发现残留,回到对应任务修复后再 commit。
