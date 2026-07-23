# larksuite/cli 调研报告

> 调研对象：飞书官方 CLI [`larksuite/cli`](https://github.com/larksuite/cli)（npm 包 `@larksuite/cli`，二进制名 `lark-cli`）。
> 替代之前误调的第三方 `yjwong/lark-cli`。本报告重点拆解其对各 AI Agent 的对接机制与 skill 治理体系，并给出 mysql-cli 的可执行复用清单。

---

## 1. 项目概况

| 维度 | 内容 |
|------|------|
| 仓库 | https://github.com/larksuite/cli |
| 维护方 | 飞书官方 larksuite 团队 |
| 语言 | Go（核心） + Node.js（npm 分发） |
| 许可证 | MIT |
| npm 包 | `@larksuite/cli` |
| 二进制名 | `lark-cli` |
| 命令规模 | 200+ 命令，覆盖 18 个业务域（Calendar / IM / Docs / Drive / Base / Sheets / Slides / Mail / Tasks / Wiki / Meetings / Approval / OKR 等） |
| Agent Skills | 26 个内置 Skill（位于 `skills/`，含 `lark-shared` 共享规则与 `lark-skill-maker` 元工具） |
| 定位 | "built for humans and AI Agents" —— 人机双目标 |
| 构建要求 | 源码构建需 Go ≥1.23 + Python 3；npm 安装仅需 Node.js |
| 安装 | `npx @larksuite/cli@latest install` |
| Skills 安装 | `npx skills add larksuite/cli -y -g`（依赖通用 `skills` 包管理器，非自研脚本） |

**核心理念**：Agent-Native Design。每条命令都经过真实 Agent 测试，强调精简参数、智能默认、结构化输出，最大化 Agent 调用成功率。

---

## 2. 对接各个 AI Agent 的核心机制

larksuite/cli 的 agent 友好性不是单点优化，而是从分发、格式、加载、版本同步到质量门禁的一整套机制。以下逐项展开。

### 2.1 标准 Skill 格式（YAML frontmatter + Markdown）

每个 skill 采用 Anthropic 开放 Skill 规范的 frontmatter 结构：

```yaml
---
name: lark-calendar
version: 1.0.0
description: "..."
metadata:
  requires:
    bins: ["lark-cli"]
  cliHelp: "lark-cli calendar --help"
---
```

- `name` / `version` / `description` 是 agent 识别与加载的标准字段；
- `metadata.requires.bins` 声明运行时依赖的二进制；
- `metadata.cliHelp` 指向该域的 `--help`，让 agent 自助补全。

这种格式不绑定任何特定 agent 平台，Cursor / Codex / Claude Code 等都能解析，是跨 agent 兼容的基础。

### 2.2 通用安装：`npx skills add` 而非自研脚本

```bash
npx skills add larksuite/cli -y -g
```

依赖通用的 `skills` 包管理器（业界共享，非飞书自研），避免每个 CLI 各写一套 `install-skills.sh`。这是与 mysql-cli 当前硬编码脚本的关键差异点。

### 2.3 shared skill 自动加载（`skills/lark-shared/SKILL.md`）

`lark-shared` 是所有 skill 的"公共契约"，承载：

- 配置初始化流程（`lark-cli config init`）
- 认证 split-flow（`--no-wait` 发起 + `--device-code` 完成）
- 身份切换（`--as user` / `--as bot`）与权限不足处理
- 业务域授权（`--domain`）
- `_notice` 抑制规则
- URL 转发与二维码生成的强约束

每个具体 skill 模板顶部强制声明：

> **CRITICAL - 开始前 MUST 先用 Read 工具读取 `../lark-shared/SKILL.md`**

这是"DRY + 单点真相"的范例：安全/认证/错误规则只维护一份，所有子 skill 通过引用复用，避免散落漂移。

### 2.4 Skill 模板化（`skill-template/skill-template.md`）

提供带 `{{变量}}` 占位的标准模板，新增 skill 时填充变量即可：

```markdown
---
name: lark-{{project}}
version: {{meta_version}}
description: "{{meta_description}}"
metadata:
  requires:
    bins: ["lark-cli"]
  cliHelp: "lark-cli {{service}} --help"
---

# {{service}} ({{version}})

**CRITICAL - 开始前 MUST 先用 Read 工具读取 [`../lark-shared/SKILL.md`](../lark-shared/SKILL.md)**
{{introduction}}
{{#shortcuts}} ... {{/shortcuts}}
{{#actions}} ... {{/actions}}
```

模板化保证所有 skill 的 frontmatter、章节结构、shared 引用一致。

### 2.5 skillscheck 同步检查（`internal/skillscheck/`）

CLI 内置的"已装 skill 版本是否匹配当前 CLI 版本"检查，由 5 个文件组成：

| 文件 | 职责 |
|------|------|
| `check.go` | `Init(currentVersion)` 在 CLI 启动前同步检查 |
| `sync.go` | 读取/写入本地同步状态 |
| `state.go` | 本地 state 文件管理 |
| `skip.go` | 跳过规则（CI 环境 / DEV 构建 / 非 release semver / `LARKSUITE_CLI_NO_SKILLS_NOTIFIER` opt-out） |
| `notice.go` | `StaleNotice` 生成与展示 |

关键设计（核实自 `check.go` 源码）：

- **零网络、零子进程**：仅读本地 state 文件，开销极小；
- 在 `cmd/root.go` 的 `rootCmd.Execute()` 前调用，每次 CLI 启动都跑，但通过 `shouldSkip` 在 CI/DEV 等场景跳过；
- 版本不匹配时生成 `StaleNotice`，通过 `_notice.skills` 字段提示 agent，不阻断命令执行。

### 2.6 质量门禁（`internal/qualitygate/skillscan/` + `rules/skillquality.go`）

`internal/qualitygate/` 是完整的质量门禁框架，其中针对 skill 的部分：

- `skillscan/harvest.go`：扫描 skill 内容；
- `skillscan/testdata/skills/lark-demo/SKILL.md`：测试用例；
- `rules/skillquality.go` + `skillquality_test.go`：skill 质量规则与单测。

质量门禁框架本身还包含 comment-audit、manifest-export、semantic-review、publiccontent/credential（凭据泄露扫描）等子能力，是工程化治理的体现。

### 2.7 CI 格式检查（`scripts/skill-format-check/` + workflow）

- 脚本：`scripts/skill-format-check/index.js` + `test.sh`；
- CI workflow：`.github/workflows/skill-format-check.yml`；
- 测试用例齐全：`tests/good-skill/`、`good-skill-minimal/`、`good-skill-complex/`、`bad-skill/`、`bad-skill-no-frontmatter/`、`bad-skill-unclosed-frontmatter/`。

PR 阶段校验 SKILL.md 的 frontmatter 合法性，从源头拒绝畸形 skill。

### 2.8 CLI 内置 skill 子命令（`cmd/skill/skill.go`）

```bash
lark-cli skills list                  # 列出所有 skill: name/description/version
lark-cli skills list lark-doc         # 列出某 skill 下一层（类 ls）
lark-cli skills read <name>           # 读取 SKILL.md 内容
```

关键设计（核实自 `cmd/skill/skill.go`）：skill 内容在**构建时内嵌进二进制**，与 CLI 版本严格绑定，避免外部文件漂移；`assets/`、`scripts/` 等机器资源不入二进制。

### 2.9 元工具：`lark-skill-maker`

一个"帮 agent 生成新 skill"的 skill，把 skill 创作本身也 agent 化。模板 + 生成器闭环。

### 2.10 三层命令架构

详见第 4 章。

### 2.11 Agent 专属设计

详见第 5 章。

### 2.12 extension/platform 可扩展治理（`extension/platform/`）

提供插件化治理框架，内置示例：

- `examples/audit-observer/`：审计观察者插件；
- `examples/readonly-policy/`：只读策略插件。

通过 `register.go` / `registrar.go` / `rule.go` / `selector.go` 等注册机制，允许第三方在不改 CLI 主干的前提下插入治理逻辑（如强制只读、审计日志）。

### 2.13 错误契约文档化（`errs/ERROR_CONTRACT.md`）

`errs/` 定义了一套 RFC 7807 对齐的类型化错误分类法，`ERROR_CONTRACT.md` 是单一真相源，服务三类受众：

1. AI agent / shell 脚本（解析 stderr JSON 信封）；
2. 协议适配器（映射到 MCP / OAuth 错误）；
3. 框架与业务代码（生产错误）。

核心不变量（核实自源码）：

- 每个错误归属唯一 **Category**（共 9 类，闭合集合）；
- 每个 typed error 有稳定 **Subtype**（小写下划线标识，未声明的 subtype 会让 CI 失败）；
- `Category + Subtype` 是 wire-stable，重命名即 breaking change；
- 错误信封输出到 **stderr**，每进程退出一个 JSON 对象；
- 退出码由 `Category` 经 `output.ExitCodeForCategory` 推导（如 `SecurityPolicyError` 经 `CategoryPolicy` 退出 6）。

---

## 3. Skill 治理三件套详解

larksuite/cli 把 skill 当代码治理，形成"模板 → 检查 → 门禁 → CI"闭环。

### 3.1 模板层（`skill-template/skill-template.md`）

- frontmatter 标准化（`name` / `version` / `description` / `metadata.requires.bins` / `metadata.cliHelp`）；
- 强制 shared 引用块；
- `{{变量}}` 占位覆盖 shortcuts / actions / 权限表 / resource sections。

### 3.2 同步检查层（`internal/skillscheck/`）

- 时机：CLI 启动时（`Init` 在 root 命令执行前）；
- 开销：零网络、零子进程，纯本地 state 比对；
- 跳过：CI / DEV / 非 release / opt-out 环境变量；
- 输出：`_notice.skills` 字段提示，不阻断；
- 状态文件：本地 state，记录上次同步的 skill 版本。

### 3.3 质量门禁层（`internal/qualitygate/skillscan/` + `rules/skillquality.go`）

- `skillscan/harvest.go` 扫描 skill 内容；
- `rules/skillquality.go` 定义命名、输出、dryrun 等规则；
- 配套单测 `skillquality_test.go` 保证规则本身可回归。

### 3.4 CI 层（`scripts/skill-format-check/` + `.github/workflows/skill-format-check.yml`）

- `index.js` 实现 frontmatter 解析与校验；
- `test.sh` 驱动测试用例；
- 测试矩阵覆盖 good/bad 两类，bad 用例细分"无 frontmatter""未闭合 frontmatter""格式错误"等具体场景。

### 3.5 二进制内嵌（`cmd/skill/skill.go` + `internal/skillcontent/`）

skill 内容构建时内嵌进二进制，`lark-cli skills list/read` 直接服务，保证"CLI 版本 ↔ skill 版本"严格一致，杜绝运行时读到旧文件。

---

## 4. 三层命令架构

CLI 提供三种粒度，从高频快捷到全量裸 API：

| 层级 | 前缀/形式 | 特点 | 示例 |
|------|-----------|------|------|
| **Shortcuts** | `+` 前缀 | 智能默认、dry-run 预览、表格输出，人机双友好 | `lark-cli calendar +agenda` |
| **API Commands** | `<service> <resource> <method>` | 与平台 API 同步，结构化参数 | `lark-cli im message create ...` |
| **Raw API** | 直接调底层 | 全覆盖兜底 | `lark-cli schema <service>.<resource>.<method>` 查参数结构后再调 |

Shortcuts 是 agent 首选：参数精简、输出友好、失败率低。Raw API 要求先跑 `schema` 查参数结构，禁止猜字段格式。

---

## 5. Agent 专属设计细节

larksuite/cli 在 README 中独立设置 **Quick Start (AI Agent)** 章节，与人类 Quick Start 并列。具体设计：

| 设计点 | 实现方式 |
|--------|----------|
| 独立 Agent 入口 | README 显式 "Quick Start (AI Agent)" 章节，并提示"如果是 AI Agent 帮用户安装，直接跳到该章节" |
| 异步认证 split-flow | `auth login --no-wait` 立即返回 `verification_url` + `device_code`；后续 `auth login --device-code <code>` 轮询完成 |
| 二维码生成 | `lark-cli auth qrcode <url>` 强制把授权 URL 转二维码，shared skill 中明文要求"必须生成二维码，不可跳过" |
| JSON 默认 | `--json` 全局可用，机器可读 |
| `_notice` 抑制 | 环境变量 `LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1` / `LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1` 抑制更新/技能版本通知，保证脚本拿到干净 JSON |
| URL opaque 规则 | 授权 URL 视为不可修改 opaque string，禁止编码/解码/重拼 query |
| 输入注入防护 | 防止恶意 prompt 注入 |
| 终端输出净化 | 防止恶意转义序列污染终端 |
| 凭证存储 | OS 原生 keychain（非明文文件） |
| Split-Flow 时序约束 | shared skill 明确：不要在同一轮展示 URL 后立刻 `--device-code` 阻塞轮询（harness 不透传中间输出时用户看不到 URL）；不缓存 `verification_url` / `device_code`，每次重新发起 |
| 身份切换 | `--as user` / `--as bot`，明确 bot 看不到用户资源、bot 无需 `auth login` 只需后台 scope |

---

## 6. 与 mysql-cli 现状对比

| 维度 | larksuite/cli | mysql-cli 现状 |
|------|---------------|----------------|
| Skill 格式 | 标准 YAML frontmatter + Markdown，跨 agent 通用 | 单文件 `skills/mysql/SKILL.md`（16.9K） |
| Skill 安装 | `npx skills add larksuite/cli -y -g`（通用包管理器） | `scripts/install-skills.sh` 硬编码 claude/cursor，其他 agent 靠 README 文字 |
| Shared skill 拆分 | `lark-shared/SKILL.md` 承载认证/权限/安全/错误，所有子 skill 强制引用 | 无，全部塞在单个 SKILL.md |
| Skill 模板 | `skill-template/skill-template.md` + `{{变量}}` | 无模板 |
| 版本同步检查 | `internal/skillscheck/`，每次启动零开销检查 + `_notice` 提示 | 无 |
| 质量门禁 | `internal/qualitygate/skillscan/` + `rules/skillquality.go` + 单测 | 无 |
| CI 格式校验 | `scripts/skill-format-check/` + workflow + good/bad 用例 | 无 |
| CLI 内置 skill 子命令 | `lark-cli skills list/read`，二进制内嵌 | 无 |
| 元工具 | `lark-skill-maker` 帮 agent 生成 skill | 无 |
| 错误契约 | `errs/ERROR_CONTRACT.md`，RFC 7807 aligned，9 Category + wire-stable Subtype，退出码由 Category 推导 | **已有 JSON 严格信封 + 稳定退出码契约（2~10）** —— 此点更简洁、更强 |
| Safety 可测性 | 散落在多处规则 | **safety 纯逻辑零依赖可测** —— 更优 |
| 三层命令 | Shortcuts / API / Raw API | 单层子命令（命令数量有限，无需分层） |
| 异步认证 | `--no-wait` / `--device-code` | 无认证流程（不适用） |
| 治理插件 | `extension/platform/`（audit-observer / readonly-policy） | 无（over-engineering 对当前规模） |
| 开发者文档 | `AGENTS.md` 类似的开发者指南 | 已有 `AGENTS.md`（面向开发该项目的 agent） |

**核心判断**：

- mysql-cli 的 **CLI 底层契约**（JSON 信封 + 稳定退出码 2~10 + safety 纯逻辑可测）已经比 larksuite/cli 更简洁、更强，应保持；
- 差距集中在 **skill 工程化治理层**：分发硬编码、无 shared 拆分、无模板、无版本检查、无 CI 校验、无内嵌子命令。

---

## 7. 对 mysql-cli 的复用建议（按优先级）

### 7.1 高优先级

**P1. 安装脚本去硬编码**

`scripts/install-skills.sh` 当前硬编码 claude/cursor 目录。两种改法择一：

- **轻量**：自动检测标准 skill 目录（`.claude/skills/`、`.cursor/rules/`、`.codex/`、`~/.claude/skills/` 等），按存在性分发；
- **彻底**：对接通用 `npx skills add`，让 mysql-cli 也成为 `skills` 包管理器的可装源，与 larksuite/cli 生态对齐。

**P2. Shared skill 拆分**

把当前 16.9K 单文件拆为：

```
skills/
├── mysql-shared/
│   └── SKILL.md          # 安全模型 + 退出码契约(2~10) + 配置 + JSON 信封 + 错误自修复
├── mysql-query/
│   └── SKILL.md          # 引用 ../mysql-shared/SKILL.md，专注查询/事务/写入
└── mysql-schema/
    └── SKILL.md          # 引用 ../mysql-shared/SKILL.md，专注库表探索/采样
```

子 skill 顶部强制 `MUST 先 Read ../mysql-shared/SKILL.md`。安全模型与退出码契约单点维护，避免漂移。

**P3. Skill 模板化**

建 `skill-template/skill-template.md`，frontmatter 加 `metadata.requires.bins: ["mysql-cli"]` / `cliHelp`，统一章节结构（触发条件 / 前置检查 / 命令参考 / 安全模型 / 典型工作流 / 错误自修复 / 输出处理）。

### 7.2 中优先级

**P4. skillscheck（显式子命令版）**

larksuite/cli 是每次启动跑（零开销 + skip 规则）。mysql-cli 每次新进程，**不宜自动跑**，改为显式子命令：

```bash
mysql-cli skill check      # 检查已装 skill 版本是否匹配 CLI
mysql-cli skill list       # 列出内置 skill
mysql-cli skill version    # 查看 skill 与 CLI 版本
```

避免给每次查询加额外开销，同时在用户主动检查时给出明确结论。

**P5. skill-format-check + CI workflow**

参照 `scripts/skill-format-check/`，加一个最小校验脚本 + GitHub/Gitee Actions workflow，PR 阶段校验 SKILL.md frontmatter 合法性（name/version/description 必填、frontmatter 闭合）。准备 good-skill / bad-skill 测试用例。

**P6. 内建 `mysql-cli skill` 子命令**

用 `cmd/skill/` 模式把 skill 内容二进制内嵌，提供 `list/read/check/version/install`，替换独立 shell 脚本，保证"CLI 版本 ↔ skill 版本"一致。

### 7.3 低优先级 / 不建议

| 项 | 结论 | 理由 |
|----|------|------|
| 三层 Shortcuts 架构 | **不建议** | mysql-cli 命令数量有限，分层收益小于复杂度成本 |
| `--no-wait` 异步认证 | **不适用** | mysql-cli 无 OAuth 认证流程 |
| `extension/platform` 治理插件 | **不建议** | 对当前规模 over-engineering |
| `lark-skill-maker` 元工具 | **暂缓** | skill 数量未到需要自动生成的体量 |

### 7.4 mysql-cli 应保持的优势

- **JSON 严格信封**（`{"success":..., "data":..., "error":...}`）—— 比 larksuite/cli 的 `{"ok":...}` 更结构化；
- **稳定退出码契约（2~10）**—— 每个码语义明确（READONLY_VIOLATION / DDL_NEEDS_WRITE / DESTRUCTIVE / MULTI_STATEMENT 等），agent 可直接分支处理；
- **safety 纯逻辑零依赖可测**—— 安全闸门（`--write` / `--ddl` / `--yes`）作为纯函数，单测覆盖，比 larksuite/cli 散落规则更可靠。

复用 larksuite/cli 的治理体系时，**不要替换**这三项，只在其之上补 skill 工程化层。

---

## 8. 结论与立即行动项

| 评估项 | 结论 |
|--------|------|
| 调研对象是否正确？ | 已修正为官方 `larksuite/cli`（前次误调 `yjwong/lark-cli`） |
| larksuite/cli 最值得借鉴的是什么？ | **Skill 工程化治理体系**：标准格式 + 通用安装 + shared 拆分 + 模板 + skillscheck + qualitygate + CI 校验 + 二进制内嵌子命令 |
| mysql-cli 底层是否需要改？ | **不需要**。JSON 信封 + 退出码 2~10 + safety 可测已优于 larksuite/cli，应保持 |
| 主要差距在哪？ | Skill 分发硬编码、无 shared 拆分、无模板、无版本检查、无 CI 校验、无内嵌子命令 |

**立即行动项（按顺序）**：

1. **P1 安装脚本去硬编码**：改 `scripts/install-skills.sh` 自动检测标准 skill 目录，或对接 `npx skills add`；
2. **P2 Shared skill 拆分**：抽出 `mysql-shared/SKILL.md`，承载安全模型 + 退出码契约 + 配置 + JSON 信封；`mysql-query` / `mysql-schema` 子 skill 强制引用；
3. **P3 Skill 模板化**：建 `skill-template/skill-template.md`，统一 frontmatter 与章节结构；
4. **P5 CI 格式校验**：加最小 `skill-format-check` 脚本 + workflow + good/bad 用例（投入小、收益快）；
5. **P4/P6 内建 skill 子命令**：后续随 CLI 演进，把 skill 内容二进制内嵌，提供 `list/read/check/version`。

---

## 附：核实来源

以下文件均通过 `gh api` 读取源码确认：

- `README.md`（项目概况、26 skills、三层架构、Quick Start AI Agent 章节）
- `skills/lark-shared/SKILL.md`（shared 自动加载、认证 split-flow、`_notice` 抑制环境变量、URL opaque 规则）
- `skill-template/skill-template.md`（frontmatter `metadata.requires.bins` / `cliHelp`、`{{变量}}` 占位、强制 Read shared）
- `internal/skillscheck/check.go`（`Init` 同步检查逻辑、零网络零子进程、skip 规则、`StaleNotice`）
- `cmd/skill/skill.go`（`lark-cli skills list/read` 子命令、二进制内嵌）
- `errs/ERROR_CONTRACT.md`（RFC 7807 错误契约、9 Category、wire-stable Subtype、退出码由 Category 推导）
- 目录树（`scripts/skill-format-check/` + good/bad 测试用例、`internal/qualitygate/skillscan/` + `rules/skillquality.go`、`extension/platform/examples/audit-observer/` + `readonly-policy/`）
