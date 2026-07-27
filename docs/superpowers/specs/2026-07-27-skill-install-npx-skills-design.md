# mysql-cli skill 安装接入 vercel-labs/skills 生态

> 设计日期:2026-07-27
> 状态:待审

## 背景与动机

mysql-cli 现有三套自研 skill 安装路径:

- `mysql-cli init`(`internal/cli/init.go`)-- 检测 7 种 agent,用 `internal/agents.Run` 把内嵌 skill 装到各 agent 原生格式;支持 `--agent/--project-dir/--no-global/--dry-run/--json`。
- `mysql-cli skill install`(`internal/cli/skill.go`)-- 仅把 SKILL.md 复制到 `~/.claude/skills`(Claude 单格式)。
- `scripts/install-skills.sh` -- shell 版,等价于 `init`。

用户最初诉求:给 skill 安装加**交互式选择页面**(选 agent)+ **scope 确认弹窗**(user/project),参考 Claude 插件安装体验。

调研发现:飞书 `larksuite/cli` **不自研安装 TUI**,skill 安装完全委托通用包管理器 [`vercel-labs/skills`](https://github.com/vercel-labs/skills)(`npx skills add`)。该工具是业界主流的 open agent skills 生态,支持 75+ agent,有成熟的交互式 TUI(`@clack/prompts`)。

**决策**:mysql-cli 不再自研 skill 安装/TUI,接入 vercel-labs/skills 生态,委托 `npx skills add`。

## 目标

1. mysql-cli 仓库的 `skills/` 目录对齐 vercel-labs/skills 规范,用户用 `npx skills add AllenMuu/mysql-cli` 安装 skill。
2. 删除 mysql-cli 自研 skill 安装代码:`init` 命令、`skill` 子命令组、`internal/agents`、`internal/skillscheck`、`bundle.go`、`scripts/install-skills.sh`。
3. 获得 vercel-labs/skills 的 75+ agent 支持 + 成熟交互式 TUI(multiselect agent / select scope / select installMode / confirm),**mysql-cli 自己不写任何 TUI**。

## 非目标

- 不改 mysql-cli 核心(查询/事务/schema/safety/config/conn/format/repl)。
- 不改 skill 文件正文内容(只改其中"安装说明"段落)。
- 不内联或合并 `mysql-shared`(保留 DRY 拆分)。
- 不对接 `npx skills` 之外的通用包管理器。

## 关键调研结论(均核实自 vercel-labs/skills 源码)

1. **规范极简**:`skills/<name>/SKILL.md` + YAML frontmatter(`---\n...\n---`)。mysql-cli 现状(`skills/mysql-{shared,query,schema}/SKILL.md`)已完全符合,frontmatter 的 `metadata.binary/requires/cliHelp` 等自定义字段会被忽略,无害。
2. **首选发现机制**:仓库根 `.well-known/agent-skills/index.json`(`add.ts` line 556)。不加也能靠默认 `skills/*/SKILL.md` 扫描工作,但加了发现体验更好。
3. **不处理 skill 间引用**:全仓 grep 无"skill A 引用 B 则连带装 B"逻辑;vercel-labs/skills 自己的 `skills/` 只有一个独立 `find-skills`,无 shared 先例。**`mysql-shared` 模式有断裂风险**:用户若只装 `mysql-query`,`../mysql-shared/SKILL.md` 引用会断。
4. **交互栈**:`@clack/prompts`(轻量 prompt 库)+ `picocolors` + `@vercel/detect-agent`;仅"可搜索多选"(`src/prompts/search-multiselect.ts`)手写。这些都在 devDependencies,构建时打进二进制。
5. **scope 二选一**:project(默认,`./<agent>/skills/`)vs global(`-g`,`~/<agent>/skills/`)。与用户诉求一致。
6. **installMode**:symlink(推荐,单一真相源)/ copy。
7. **交互流程**:`intro -> multiselect skill -> multiselect agent -> select scope -> select installMode -> note 汇总 -> confirm -> 安装 -> note 结果 -> outro`,每步 `isCancel` 处理。

## 架构

```
用户/agent
   │
   ├─ 装skill: npx skills add AllenMuu/mysql-cli  ──→  vercel-labs/skills 交互式 TUI
   │                                                       ↓
   │                                  仓库 skills/mysql-{shared,query,schema}/SKILL.md (已就绪)
   │                                                       ↓
   │                                  装到 .agents/skills/ + 各 agent symlink (75+ agent)
   │
   └─ 跑查询: mysql-cli query/txn/schema/...  (Go 单二进制,无 Node 依赖)
```

mysql-cli 二进制与 skill 安装**完全解耦**:二进制只负责查询/事务/schema,skill 是仓库侧资产,由 vercel-labs/skills 生态管理。

## 决策汇总

| # | 决策点 | 选择 |
|---|--------|------|
| 1 | 接入方式 | 删除 `skill` 子命令组,委托 `npx skills add`(原"替换默认行为"在 A 方案下演变为删除) |
| 2 | agent 选择交互 | 由 vercel-labs/skills 的 `skills add` 提供(75+ agent),mysql-cli 不实现 |
| 3 | scope 选择交互 | 同上,project/global 二选一由 `skills add` 提供 |
| 4 | project-dir 来源 | vercel-labs/skills 的 project scope 即 `./<agent>/skills/`(当前目录),一致 |
| 5 | 改造范围 | 接入生态,委托 npx |
| 6 | 方案变体 | A 激进废弃 |
| 7 | mysql-shared 引用 | 文档引导全装 + `.well-known` 声明 3 skill(保留 DRY 拆分) |
| 8 | `skill install` 子命令 | 完全删除(敲 `mysql-cli skill install` 报 unknown command) |

> 决策 2/3/4(交互式 TUI 细节)在 A 方案下**由 vercel-labs/skills 提供**,mysql-cli 侧不实现 -- 这是 A 方案的核心红利:零 TUI 代码,直接复用生态成熟交互。

## 删除清单

### Go 代码

| 路径 | 说明 | 消费者核实 |
|------|------|-----------|
| `internal/cli/init.go` | `mysql-cli init` 命令,`agents` 包主消费者 | agents 唯一非测试消费者 |
| `internal/cli/init_test.go` | init 测试 | - |
| `internal/cli/skill.go` | `mysql-cli skill` 子命令组(list/version/check/install) | skillscheck/bundle 消费者 |
| `internal/cli/skill_test.go` | skill 测试 | - |
| `internal/agents/` | 整个包(agents.go/detect.go/install.go/merge.go + tests),7-agent 自研安装 | 删 init.go 后无消费者 |
| `internal/skillscheck/` | 整个包(skillscheck.go + test),版本同步检查 | 删 skill.go 后无消费者 |
| `bundle.go` | `//go:embed skills` + `SkillNames/SkillFile/SkillsFS`,根包 bundle | 删 init.go + skill.go 后无消费者 |

### 子命令注册

- `internal/cli/cli.go`(或注册处)移除 `newInitCmd()` 与 `newSkillCmd()` 的 `AddCommand` 调用。

### 脚本

| 路径 | 说明 |
|------|------|
| `scripts/install-skills.sh` | shell 版自研安装 |
| `scripts/install-skills-test.sh` | 其自测 |

### 保留(不动)

- `internal/cli/version.go` 的 `mysql-cli version` 命令(仅删注释里对 `skill version` 的提及)。
- `scripts/skill-format-check.sh` + `scripts/skill-format-check/` 测试目录 + `.github/workflows/skill-format-check.yml`(frontmatter 校验仍有用)。
- `skill-template/skill-template.md`。
- `skills/` 目录(仓库 skill 资产,接入生态的源)。
- `internal/{config,conn,query,result,safety,schema,format,repl}` 全部核心。
- `dist/npm/`(CLI 本身的 npm 分发,与 skill 安装无关)。

## 新增

### `.well-known/agent-skills/index.json`(仓库根)

声明 3 个 skill,供 vercel-labs/skills 首选发现机制读取。字段参考 vercel-labs/skills 规范(name/source/description),并在描述中提示"建议全装以保证 mysql-shared 引用不断"。具体 schema 在实现时参照 vercel-labs/skills 的 `add.ts` 对该文件的解析逻辑确定。

## 文档迁移

- **README.md / README-zh.md**:
  - 安装说明改为 `npx skills add AllenMuu/mysql-cli`(交互式)。
  - 非交互示例:`npx skills add AllenMuu/mysql-cli --skill '*' -a <agent> -g -y`(CI 友好)。
  - 说明 scope(project `./<agent>/skills/` vs global `~/<agent>/skills/`)+ installMode(symlink/copy)。
  - **强调"建议全装 3 skill 以保证 mysql-shared 引用不断"**。
  - 无 Node 用户 fallback:手动复制 `skills/` 目录到 `~/.claude/skills/` 等。
- **AGENTS.md**:"Skill 体系"章节重写,移除 `skill install`/`init`/`install-skills.sh`/`agents`/`skillscheck`/`bundle` 描述,改为生态接入说明。
- **skill 文件内的安装说明**(mysql-shared/mysql-query/mysql-schema 的 SKILL.md):改 `npx skills add`。
- **CHANGELOG.md**:记录本次 breaking change(`mysql-cli init` / `skill install` 移除,改用 `npx skills add`)。

## 测试策略

- 删 `internal/agents/*_test.go`、`internal/skillscheck/*_test.go`、`internal/cli/init_test.go`、`internal/cli/skill_test.go`。
- `internal/cli/cli_test.go`、`commands_test.go`、`errors_test.go` 中涉及 init/skill 子命令的用例同步移除。
- 保留 `scripts/skill-format-check/test.sh`(frontmatter 校验自测)。
- 生产代码与测试等量删除,覆盖率应维持(项目目标 ≥80%)。
- 新增验证(手动/CI):`npx skills add AllenMuu/mysql-cli --list` 能列出 3 skill;`--skill '*' -a claude-code -y --dry-run`(若有)能预演安装。

## 迁移影响与向后兼容

- **Breaking**:`mysql-cli init`、`mysql-cli skill install/list/version/check` 全部移除,敲这些命令报 unknown command。
- **运行时依赖**:装 skill 需 Node.js/npx。mysql-cli 二进制本身仍是 Go 单二进制,查询/事务/schema 不需 Node。
- **无 Node fallback**:手动复制仓库 `skills/` 目录到目标 agent 目录(README 说明)。
- **版本真相源**:从 CLI 二进制内嵌迁移到仓库 frontmatter(skill 版本 = GitHub 上 SKILL.md 的 version 字段)。

## 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| shared 引用断裂(用户只装单个 skill) | mysql-query/schema 顶部 `../mysql-shared/SKILL.md` 失效 | `.well-known` 声明 + README 强调全装 + agent 非交互装默认 `--skill '*'` |
| npx 依赖 | 无 Node 环境无法装 skill | README 提供 manual copy fallback |
| 向后不兼容 | 旧脚本/文档引用 `mysql-cli init`/`skill install` 报错 | CHANGELOG 标注 breaking;README 迁移指引 |
| vercel-labs/skills 上游变更 | 发现机制/规范漂移 | 锁定 skills 版本范围;关注上游 |

## 验证清单

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `go test ./...` 通过(136 用例删减后仍全绿)
- [ ] `go test -cover ./...` ≥80%
- [ ] `./scripts/skill-format-check.sh skills/` 通过
- [ ] `npx skills add AllenMuu/mysql-cli --list` 列出 3 skill
- [ ] `npx skills add AllenMuu/mysql-cli --skill '*' -a claude-code -y` 实测安装成功
- [ ] README/AGENTS.md 无残留旧安装说明
