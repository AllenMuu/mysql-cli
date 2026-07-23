# npx 一键安装 + `mysql-cli init` 多 Agent 安装 设计文档

> 日期：2026-07-23
> 状态：设计稿，待实现计划

## 1. 背景与目标

`mysql-cli` 已是可用 Go 二进制，但分发与技能安装存在两个缺口：

- **二进制分发**：当前仅 `go install ...@latest` 或源码 `go build`，**必须本机有 Go 1.22+**。无 npx / brew / curl 安装器，无预编译二进制。
- **技能装到各 agent**：`mysql-cli skill install [dir]` 只把技能复制到**单一目录**（默认 `~/.claude/skills`），**不自动检测、不处理 7 种 agent 的不同格式**。真正的「检测 7 种 agent + 按各自格式幂等安装」逻辑在 `scripts/install-skills.sh`（bash）里——**必须先 clone 仓库**才能跑。

参照 `larksuite/cli`（调研见 `docs/research-lark-cli.md`：Go 核心 + Node npm 包装，`npx @larksuite/cli@latest install` 一行装好），本设计把这两块补齐。

**成功标准**：
- 用户无需 Go 工具链，`npx @allenmuu/mysql-cli install` 一行永久装好二进制。
- `mysql-cli init` 一条命令自动检测已装 agent 并按各自格式安装全部内嵌技能，幂等可重复。
- 逻辑单一真相源在 Go（bash 脚本废弃），跨平台（含 Windows），可单测。
- 现有 `skill install/check/list/version` 行为不变；现有用户无破坏性迁移。

## 2. 设计决策总览

| # | 决策点 | 选定 | 备选（已否） |
|---|---|---|---|
| D1 | 二进制分发方式 | **npx Node 包装**（npm 包按平台下载预编译 Go 二进制） | curl\|sh、Homebrew tap、仅 go install |
| D2 | npx 调用模型 | **两步**：`npx ... install` 装二进制 → `mysql-cli init` 装技能 | 一步 `npx init`、两者都支持（注：shim 透传使 one-shot `npx init` 仍可用，但不作为主路径） |
| D3 | `init` 默认范围 | **auto 检测已装 agent**（`--agent all` 强制全 7 个） | 默认全 7 个、交互式多选 |
| D4 | init 实现方式 | **bash→Go 全移植**（新 `internal/agents` 包），废弃 bash 脚本 | init 转调内嵌 bash（Windows 脆）、bash 并行保留（双真相源漂移） |
| D5 | npm 包名 | **`@allenmuu/mysql-cli`**（scope，防占位） | 无 scope `mysql-cli`（可能被占） |
| D6 | 二进制托管 | **GitHub Releases**（go.mod 为 `github.com/AllenMuu/mysql-cli`，已有 `.github/workflows`，GoReleaser 默认契合） | Gitee Releases |
| D7 | init 默认装哪 | **默认装全局位**；`--project-dir <path>` 追加项目级；`--no-global` 仅项目级 | 默认全局+项目同时 |

## 3. 架构

沿用现有分层，不引入新依赖方向：

```
cmd/mysql-cli/main  ->  cli
                          ├── skill   (现有: list/check/install/version，不变)
                          ├── init    (新)  ──> internal/agents (新) ──> bundle (内嵌 skills/)
                          └── query/schema/conn/...  (不变)

dist/npm/                       (新, Node 包装层，独立于 Go 代码)
.goreleaser.yml                 (新, 交叉编译 + Release)
.github/workflows/release.yml   (新, tag 触发 GoReleaser + npm publish)
```

`internal/agents` 仅依赖根包 `bundle`（取技能内容）与标准库，与 `skillscheck` 平级，**不进查询热路径**。

## 4. 组件设计

### 4.1 `internal/agents` 包（Go，新）

**`agents.go` — agent 注册表**。每个 agent 是结构体，声明「如何检测 + 装到哪 + 什么格式」。路径与格式严格对齐现有 `install-skills.sh`：

| agent | 全局位 | 项目位 | 格式 |
|---|---|---|---|
| claude | `~/.claude/skills/<name>/SKILL.md` | `.claude/skills/<name>/SKILL.md` | 原生复制 |
| cursor | `~/.cursor/rules/<name>.mdc` | `.cursor/rules/<name>.mdc` | 原生复制（.mdc） |
| codex | `~/.codex/instructions.md` | `AGENTS.md` | 标记间追加合并 |
| opencode | `~/.config/opencode/instructions.md` | `.opencode/instructions.md` | 标记间追加合并 |
| copilot | — | `.github/copilot-instructions.md` | 标记间追加合并 |
| windsurf | `~/.windsurfrules` | `.windsurfrules` | 标记间追加合并 |
| aider | — | `.aider.instructions.md` | 标记间追加合并 |

技能集合固定三个：`mysql-shared` / `mysql-query` / `mysql-schema`（与脚本一致）。

- **`detect.go`**：逐 agent 查其全局配置目录/已知文件是否存在（`~/.claude`、`~/.cursor`、`~/.codex` 等），返回已装列表。auto 模式只往检测到的装。
- **`install.go`**：按 agent 选「原生复制」或「标记合并」写入对应路径。复用 `bundle.SkillsFS()` 取技能内容。
- **`merge.go`**：幂等合并，marker 复用脚本同款以保证与已装内容兼容：
  ```
  <!-- mysql-cli skill: begin (auto-generated) -->
  ... 合并后的技能体 ...
  <!-- mysql-cli skill: end -->
  ```
  存在则整体替换、不存在则追加；marker 块损坏则 warn 后重写。

### 4.2 `mysql-cli init` 命令（`internal/cli/init.go`，新 cobra 子命令）

```
mysql-cli init [--agent auto|claude|cursor|codex|opencode|copilot|windsurf|aider|all] 
               [--project-dir <path>] [--no-global] [--dry-run] [-j|--json]
```

- `--agent`（默认 `auto`）：检测已装 agent 只往它们装；`all` 强制全 7；逗号分隔指定子集。
  - **auto 与纯项目级 agent**：copilot / aider 无全局标记可检测，auto 模式下**仅当给定 `--project-dir` 时**才装到该项目；未给 `--project-dir` 则跳过它们。其余 5 个 agent（claude/cursor/codex/opencode/windsurf）有全局配置目录，auto 可检测。
- 默认装**全局位**；`--project-dir <path>` 同时追加项目级；`--no-global` 仅项目级。
- `--dry-run`：只打印将写哪些文件，不落盘。
- `-j`：JSON 信封 `{success, data:{agents:[{name, detected, paths[], status}]}, error}`，agent 可解析。
- 退出码：至少成功装一个 / 无可装 → exit 0；全部因错误写失败 → 非零（复用 `cli` 层 `mapError`，新增 init 专属码，取现有 2-10 契约码之后的下一个空闲值，**不占用既有 agent 契约码**）。

**`init` 数据流**：
1. `agents.Detect()` 扫描 7 agent 全局标记 → 已装列表。
2. 按 `--agent` 过滤（auto = 已装列表）。
3. 对每个保留 agent × 每个 skill 调 `agents.Install()`：原生 agent `WriteFile` 到 `<dir>/<name>/SKILL.md`（或 `.mdc`）；追加 agent 读目标 → 替换/插入 marker 块 → 写回。
4. 汇总报告（text / JSON）。

### 4.3 npx 包装层（`dist/npm/`，独立于 Go）

三个文件：

**`package.json`**
- `name: "@allenmuu/mysql-cli"`、`bin: { "mysql-cli": "bin/mysql-cli.js" }`、`scripts.postinstall: "node install.js"`、`engines.node >= 18`。
- 版本号与 Go release tag 同步（`v1.2.3` → `1.2.3`）。

**`install.js`**（postinstall 下载器）
- 按 `process.platform`×`process.arch` 映射 GoReleaser 产物 `mysql-cli_<os>_<arch>.tar.gz`（windows 用 `.zip`）。
- 从 GitHub Releases 下载 → 解压二进制到包目录 `./bin/mysql-cli` → chmod 0o755。
- 支持 `MYSQL_CLI_MIRROR` 环境变量换镜像（GFW 友好）。
- **失败不致命**：下不到（网络 / 无对应 arch）时装「提示桩」打印手动下载 URL，postinstall 仍 exit 0，不阻断 `npm i`。

**`bin/mysql-cli.js`**（shim）
- `npx @allenmuu/mysql-cli install`：把包内二进制复制到持久位 `~/.local/bin/mysql-cli`（Windows 用 `%LOCALAPPDATA%\mysql-cli\mysql-cli.exe`），chmod，打印 PATH 提示。**不自动改 shell rc**（脆弱且侵入）。
- 其他任何参数：`spawn` 包内（或持久位）Go 二进制并透传，继承 stdio、透传退出码。→ `npx @allenmuu/mysql-cli init` / `skill check` / `query ...` 均 one-shot 可跑。

> `install` 是 **npx 期命令**（仅永久装二进制时用）；Go 二进制**不加** `install` 子命令（YAGNI）。两步即 `npx @allenmuu/mysql-cli install` → `mysql-cli init`。

### 4.4 CI：GoReleaser + 发布

- **`.goreleaser.yml`**：`CGO_ENABLED=0`；targets = darwin/amd64、darwin/arm64、linux/amd64、linux/arm64、windows/amd64；archive `mysql-cli_{{.Os}}_{{.Arch}}`；生成 checksum；发到 GitHub Releases。
- **`.github/workflows/release.yml`**：push tag `v*` → GoReleaser 构建 + 发 Release →（可选）同 job 用 `NPM_TOKEN` `npm publish dist/npm`。初期可手动发 npm，稳定后自动化。
- CI 自检：`goreleaser check` + `--snapshot` dry-run。

## 5. 现有命令与脚本处置

- `mysql-cli skill install [dir]`：**保留**，作为单目录底层原语（power user / CI 场景）。语义不变。
- `mysql-cli skill list/check/version`：不变。
- `scripts/install-skills.sh`：加**废弃 banner**（stderr 提示「请用 `mysql-cli init`」），逻辑保留一个版本，下个版本移除。不立刻删，避免破坏现有用户/文档。

## 6. 错误处理

- postinstall 下载失败 → 提示桩 + 手动 URL（4.3）。
- agent 检测拿不到 home → 回退 cwd + warn。
- 单个 agent 写失败 → 记进报告、继续其他 agent；**全部失败才非零退出**。
- marker 块损坏 → warn 后重写。
- TTY / 非 TTY：auto 检测两种都行；`-j` 给 agent 结构化输出。

## 7. 测试

- `internal/agents`：`detect`/`install` 表驱动测试（原生复制正确性、合并幂等=跑两次内容不变、marker 替换、损坏重写）；移植 `scripts/install-skills-test.sh` 的 good/bad 场景。
- `internal/cli/init_test.go`：flag 解析、JSON 信封形状、退出码、`--dry-run` 不落盘。
- npm `install.js`：本地 fixture tarball mock 下载 + 平台/arch 映射矩阵；用内置 `node:test`，零新依赖。
- GoReleaser：`goreleaser check` + snapshot。
- 现有 `skill install/check` 测试不变。
- 覆盖率沿用项目目标 ≥80%（针对 `internal/agents` 与 `internal/cli` 新代码）。

## 8. 范围

**IN**：`internal/agents` 包、`mysql-cli init` 命令、`dist/npm/` 三文件、`.goreleaser.yml`、`.github/workflows/release.yml`、npm 发布、bash 脚本废弃 banner、README 安装段重写（npx + init）、上述测试。

**OUT（YAGNI）**：交互式多选 init、brew/curl 安装器、自动改 PATH/rc、npm 源码构建兜底、代码签名/公证、Windows arm64（后续可加）、Go `install` 子命令。

## 9. 迁移与未来工作

- **迁移**：现有 `install-skills.sh` 用户接到 banner 后改用 `mysql-cli init`；技能 marker 复用旧款，已装内容无需重装即可被 `init` 识别/更新。`mysql-cli skill check` 仍用于版本同步校验。
- **未来**：Windows arm64 target、代码签名/公证、`init` 交互式多选（若用户反馈需要）、npm 发布全自动化、`goreleaser` homebrew tap（若要补 brew 分发）。
