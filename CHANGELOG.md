# Changelog

## [Unreleased]

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
