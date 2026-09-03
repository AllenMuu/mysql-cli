# Changelog

## [Unreleased]

### Fixed
- Codex PermissionRequest hook 收紧自动放行范围：新增子命令白名单（`query`/`schema`/`sample`/`tables`/`databases`/`read`/`explore`/`analyze`/`version`），`mysql-cli agent`/`config`（写本地文件）、`txn`（CLI 层强制 `--write`）、裸 `mysql-cli`（交互 REPL）不再被静默放行；带值持久 flag（`-d`/`--format` 等）的取值不会被误判为子命令。文档补充绝对路径调用绕过 Rules 粗门的已知缺口，并记录已核验的 Codex hooks schema（codex-cli 0.153.0 官方文档）。

## [2.1.0] - 2026-08-12

### Added
- `mysql-cli agent init` 新增 Pi（pi.dev）支持:走 Pi 的 `tool_call` 事件扩展机制（in-process TypeScript，非 command-type hook，不 exec 子进程），matcher=`bash`（小写），决策对象 `{ block: true, reason? }`，人机确认靠扩展内 `ctx.ui.confirm()`。项目级写入 `.pi/extensions/mysql-write-guard.ts`（首次启动 pi 需 `/trust`），全局写入 `~/.pi/agent/extensions/mysql-write-guard.ts`（立即可用）；装好后在 pi 内 `/reload` 生效。**不复用 `mysql-write-guard.py`**——单独维护 `templates/pi-mysql-write-guard.ts`，把 shlex token + 正则回退检测逻辑以 TS 重写；非交互模式下 `ctx.ui` 不可用时默认 block（保守）。
- 退出码契约:新增 `11=internal`,连接/配置失败改用哨兵 error + `errors.Is` 精确映射;写路径与 schema 命令支持 `--timeout`。

### Fixed
- `install.sh`/`install.ps1`:改用 `checksums.txt` 校验下载完整性(原 per-asset `.sha256` 下载永远 404,校验从未生效)。
- SSH 隧道:拒绝权限过宽的私钥(须 0600 或更严);修复 long-running 下 proxy goroutine 泄漏(双向拷贝加 5s 宽限)。
- 写路径 `%w: %w` 双包装,DDL/destructive 的退出码不再被误判为 readonly(3)。

## [2.0.2] - 2026-08-06

### Added
- `mysql-cli agent init` 新增 TRAE 支持:PreToolUse hook（Claude Code 兼容，matcher=`RunCommand`，顶层 `version: 1`，hook 定义带 `timeout`）。项目级写入 `.trae/hooks.json` + `.trae/hooks/mysql-write-guard.py`，全局写入 `~/.trae-cn/hooks.json` + `~/.trae-cn/hooks/mysql-write-guard.py`（路径不对称是 TRAE 官方设计，国际版/中国版相同）。复用现有 `mysql-write-guard.py`，hook 命令路径用 `${TRAE_PROJECT_DIR:-${CLAUDE_PROJECT_DIR:-$PWD}}` 保持可移植。

### Changed
- README 重写:agent init 章节表述更清晰。

## [2.0.1] - 2026-07-30

### Added
- `mysql-cli agent init`:为 Claude Code / Cursor / opencode / GitHub Copilot / CodeBuddy 安装 per-agent 写操作确认配置(命中 `--write`/`--ddl`/`--yes` 即弹窗找人类确认)。交互式 + flag 兜底;配置模板内嵌,深合并现有 JSON(幂等,`.bak` 备份)。
- `install.sh` / `install.ps1`:一键安装(release 二进制 + skills + agent init),macOS/Linux/Windows。
- Untrusted project config 不再静默回落:命中时 stderr 告警(不含 trust 命令,防 AI 自动 trust);`--no-trust-warn` / `MYSQL_CLI_NO_TRUST_WARN=1` 静默;`--strict-trust` 升级为报错(exit 10)。
- `config trust` 加固:非交互模式需 `--yes`(TTY 交互 `y/N`),挡 AI 自动 trust。

### Changed
- Skill `--yes` 措辞收紧:从“确认破坏性操作”改为“标记破坏性操作、触发人类确认弹窗;是请求非豁免”。
- README 补 `Config resolution` 小节:文件优先级链、同名 datasource 整体替换、不同名并集、`default`/字段覆盖规则。

## [2.0.0] - 2026-07-27

### Breaking
- **SELECT 默认安全 cap 1000**:不带 LIMIT 的 SELECT 现在默认只返回 1000 行,`meta.truncated=true` 标记截断。需全表用 `--no-limit`;调默认值用 config `default_limit` 或 env `MYSQL_CLI_DEFAULT_LIMIT`;显式精确行数用 `--limit N`。动机:实测裸跑 4.4 万行表 = ~900 万 token(45 个 200K context 窗口),会当场撑爆 agent 会话。
- **SELECT 的 JSON 信封省略 `rows_affected`**(对 SELECT 恒为 0);改用 `meta.truncated`/`meta.limit`。DML/DDL 信封不变。
- **skill 安装迁移至 vercel-labs/skills 生态**:`mysql-cli init`、`mysql-cli skill install/list/version/check` 及 `scripts/install-skills.sh` 全部移除。改用 `npx skills add AllenMuu/mysql-cli` 安装 skill(支持 75+ agent,交互式选 agent/scope/install method)。无 Node 环境可手动复制仓库 `skills/` 目录。skill 版本真相源从二进制内嵌迁移至仓库 `skills/*/SKILL.md` frontmatter。

### Added
- `--format jsonl`:每行一个 JSON 对象,比 json 紧凑,适合 agent。
- `--no-limit` flag。
- config `default_limit` / env `MYSQL_CLI_DEFAULT_LIMIT`。
- Project-level config:`config init`/`trust`/`path`/`show` 子命令(trust store + 向上项目发现)。
- Grouped help output with agent-oriented notes.
- `.well-known/agent-skills/index.json`:vercel-labs/skills 首选发现机制,声明 3 个 skill。

### Changed
- Skill 内容统一为英文;3 个 skill 版本对齐到 2.0.0。
- 内部:移除 `internal/agents`、`internal/skillscheck`、`bundle.go`(`//go:embed skills`)—— skill 安装委托 vercel-labs/skills 生态,二进制不再内嵌 skill。
