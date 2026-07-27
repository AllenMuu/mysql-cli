# Changelog

## [Unreleased]

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
