# mysql-cli 项目级 config 加载

- 日期: 2026-07-24
- 状态: 设计待审阅
- 关联分支: 建议从 `feat/default-limit` 切出 `feat/project-config`

## 背景

mysql-cli 当前只读 `~/.config/mysql-cli/config.toml`(`--config` flag 可覆盖为单文件),不支持环境变量指定路径,也没有项目级配置概念。这导致:

- 不同项目无法各自维护 datasource(只能挤在全局文件里,命名冲突)
- 无法像 MCP 的 `.mcp.json` 那样让仓库自带连接配置
- 没有 `MYSQL_CLI_CONFIG` 环境变量入口,部署/CI 场景不便

## 目标

1. **项目级 config**:从 cwd 向上查找 `.config/mysql-cli/config.toml`(与全局同构),与全局覆盖式合并
2. **环境变量指定路径**:`MYSQL_CLI_CONFIG` 指定 config 文件
3. **信任清单机制**:防恶意仓库(`.config/mysql-cli/config.toml` 指向攻击者 DB,或 `${ENV}` 套取本地环境变量密码)
4. **`config` 子命令族**:`path` / `show` / `trust` / `init`,支持 agent JSON 自省

## 非目标(YAGNI)

- 字段级深合并(整体替换已满足覆盖式需求)
- `config untrust`(手动编辑纯文本清单即可)
- 交互式信任向导(agent 是首要调用方,非交互为主)
- 配置热重载、schema 校验

## 决策汇总(已与用户确认)

| 决策点 | 选择 |
|---|---|
| 项目级发现方式 | 从 cwd 逐级向上找 `.config/mysql-cli/config.toml`,首个即停,到 home/fs root 为止 |
| 路径同构 | 项目级 `<root>/.config/mysql-cli/config.toml` 与全局 `~/.config/mysql-cli/config.toml` 相对路径一致 |
| 合并语义 | 覆盖式:同名 datasource 整体替换(含 SSH 子表),不同名取并集;`default`/`default_limit` 项目级覆盖全局 |
| `default_limit=0` | 视为未设置,沿用全局/内置默认 cap;无限制仍用 `--no-limit` flag |
| 显式路径语义 | `--config` flag / `MYSQL_CLI_CONFIG` env 指定 = 只读该文件,跳过自动发现;flag > env |
| 安全信任 | 信任清单机制(`~/.config/mysql-cli/trusted`,纯文本);未信任静默回退全局,exit 0 |
| `${ENV}` 展开边界 | 信任是项目根级 all-or-nothing;未信任 = 项目级整体不加载,`${ENV}` 永不被触碰 |
| 信任清单格式 | 纯文本,每行一个规范化绝对路径(`filepath.EvalSymlinks`),权限 0600 |
| 辅助命令 | `config` 子命令族:`path` / `show` / `trust` / `init` |
| 未信任行为 | 静默回退全局,不报错,exit 0(对 agent 友好) |

## 现状

- `internal/cli/commands.go:20` `defaultConfigPath()` 写死 `~/.config/mysql-cli/config.toml`
- `Globals.resolve()`(commands.go:28):文件存在 -> `config.LoadFile` 解析**单个**文件 -> `config.Resolve` 按 `flag > env > file > default`
- `Config{Datasources map[string]Datasource, Default, DefaultLimit}`
- 无环境变量路径入口,无项目级,无信任机制

## §1 架构与分层

新增 `internal/config/loader.go`,封装四件事:**路径发现 / 多层加载 / 覆盖式合并 / 信任清单**。cli 层只调一个入口。`config.go` 单文件解析逻辑不动,`Resolve` / `applyEnv` / `merge` 不动。

数据流:

1. `ResolvePathChain(opts)` 确定文件链
2. `MergeConfigs(chain)` 从低到高(全局 -> 项目级)覆盖式合成单个 `Config`
3. 现有 `Resolve` 做 datasource 字段解析(flag > `MYSQL_*` env > `Config` > default)

改动面:`loader.go`(新增)、`cli/commands.go` 的 `Globals.resolve()`(改调 loader)、`cli` 新增 `config` 子命令族。

## §2 发现链

`ResolvePathChain(opts)` 确定文件链:

- `--config` flag 设 -> 链 = `[该文件]`(单文件,跳过发现,**向后兼容**)
- 否则 `MYSQL_CLI_CONFIG` env 设 -> 链 = `[该文件]`
- 否则 -> 链 = `[项目级, 全局]`(项目级优先):
  - **项目级**:从 cwd 逐级向上找 `.config/mysql-cli/config.toml`,首个即停,到 home 或 fs root 为止
  - **全局**:`~/.config/mysql-cli/config.toml`(现有)

两者相对路径同构(`.config/mysql-cli/config.toml`),仅根不同(home vs 项目根)。

## §3 合并语义(覆盖式)

`MergeConfigs(low, high *Config) *Config`(`low` = 全局,`high` = 项目级;`high` 为 nil 直接返回 `low`):

| 字段 | 合并规则 |
|---|---|
| `Datasources[name]` | 两边都有 -> 整体替换为 `high`(含 SSH 子表随之替换,不做字段级 merge);仅一边有 -> 取有的一边 |
| `Default` | `high.Default != ""` 覆盖,否则 `low.Default` |
| `DefaultLimit` | `high.DefaultLimit != 0` 覆盖,否则 `low.DefaultLimit`;0 视为未设置,沿用全局/内置默认 cap。无限制仍走 `--no-limit` flag |

**合并时机与信任**:信任判断在合并**之前**完成--未信任的项目级 config 不进入合并链。因此合并后的 `Config` 中所有 datasource 均来自已信任源(全局恒信任 + 项目级仅当已信任),`expandPassword`(`${ENV}` 展开)在选定 datasource 后做即可,无需区分来源,全部安全展开。合并的是带占位符的原始密码。

## §4 信任清单机制

**存储**:`~/.config/mysql-cli/trusted`,纯文本,每行一个**规范化的项目根绝对路径**(`filepath.EvalSymlinks` 后),防软链接欺骗。权限 `0600`。

**判断**:发现项目级 config(路径形如 `<root>/.config/mysql-cli/config.toml`)-> 项目根 = `<root>`(去掉 `.config/mysql-cli/config.toml` 后缀,**而非** config 文件的直接父目录 `.config/mysql-cli/`)-> 规范化后查清单:

- **命中** -> 加载项目级,参与覆盖式合并;其 datasource 的 `${ENV}` 正常展开
- **未命中** -> **整个项目级 config 不参与合并**,静默回退全局(**不报错、不阻塞 agent**);`config path` 标注未信任状态

**信任入口**:

1. `mysql-cli config trust [dir]`(`dir` 默认 = 自动检测到的项目根;cwd 不在任何项目根下时回退到 cwd):规范化绝对路径追加到清单,**幂等**(已存在不重复)。这是主要入口。
2. 交互终端(tty)且非 JSON 输出时,检测到未信任项目级 config 可提示 `[y/N]`--**可选增强**;agent 场景(非 tty / JSON 输出)一律跳过交互、静默回退,不破坏退出码契约。

**`${ENV}` 展开边界**:信任是**项目根级 all-or-nothing**。未信任 = 项目级整体不加载,自然无 `${ENV}` 可展开;已加载(已信任)的项目级 datasource 的 `${ENV}` 与全局一样正常展开。不做"半加载"。

## §5 config 子命令族

| 子命令 | 作用 | 输出 |
|---|---|---|
| `config path` `[-j]` | 显示生效文件链 + 信任状态 | 项目级路径(标注 `trusted` / `untrusted, skipped`)+ 全局路径 |
| `config show` `[-d name]` `[-j]` | 显示合并后最终 Config | 全部 datasource(密码脱敏)+ `default` + `default_limit`;`-d` 选单个 |
| `config trust [dir]` `[-j]` | 信任项目根(`dir` 默认检测到的项目根),追加清单 | 确认 + 规范化绝对路径;幂等 |
| `config init [--project\|--global] [--force]` | 生成模板 config.toml | `--project` 写项目根 `.config/mysql-cli/config.toml`,`--global` 写 `~/.config/mysql-cli/config.toml`;已存在则**不覆盖**,`--force` 覆盖 |

**密码脱敏规则**(安全关键):

- 明文密码 -> `***`
- `${ENV}` 占位符 -> **原样显示**(`${MYSQL_PASSWORD}`,不含明文,安全)

`config show` 复用 loader,走完整发现 + 信任判断 + 合并,展示的是真实生效配置(与实际查询时一致),不只读单文件。

## §6 错误处理与退出码

沿用现有契约(2/3/4/5/6/7/8/9/10),**不新增退出码**,复用 10(config)。

| 场景 | 行为 | 退出码 |
|---|---|---|
| 任一 config.toml TOML 语法错 | 报错并指明哪个文件 | 10 |
| 信任清单读取失败 | 视为"无已信任目录",静默回退 | 0 |
| `config trust` 路径无效 / 规范化失败 | 报错 | 10 |
| `config trust` 写清单失败 | 报错 | 10 |
| 未找到项目级 config | 无项目级,只用全局(正常路径) | 0 |
| `${ENV}` 引用未设置环境变量(已信任加载后) | 现有 `ErrPlaceholderUnset` | 10 |
| `--config` 指定文件不存在 | 现状:`cfg=nil` 继续,env/default 兜底 | 0 |
| 未信任目录 | **静默回退全局,不报错** | 0 |

## §7 完整优先级链

两层维度:

- **文件选择层**:`--config` flag > `MYSQL_CLI_CONFIG` env > 项目级(已信任) > 全局
- **字段层**(选定 datasource 后):flag overrides > `MYSQL_*` env > 文件 datasource > defaults

## §8 测试策略(≥80%,sqlmock 无需 DB)

`loader.go` 单测(核心):

- `ResolvePathChain`:`--config` flag / `MYSQL_CLI_CONFIG` env / 向上发现项目级 / 到 home 边界 / 未找到
- `MergeConfigs`:同名整体替换 / 不同名并集 / `Default` 覆盖 / `DefaultLimit=0` 视为未设置 / `high=nil` 直返 low / SSH 子表整体替换
- 信任判断:命中 / 未命中 / `EvalSymlinks` 规范化 / 软链接防欺骗
- `${ENV}` 展开:已信任正常展开 / 未信任不加载(占位符永不被触碰)
- `trusted` 文件:纯文本读写 / 幂等追加 / 权限 0600

cli 层:

- `config path/show/trust/init` 子命令测试(沿用 `commands_test.go` 的 `Run` + 检查 stdout/exit 模式)
- **向后兼容**:无项目级 + 无 env 时,`Globals.resolve()` 行为与现状完全一致(现有测试不破)
- 边界:cwd 在项目根上层 / 多层向上找到项目根 / 项目级与全局同名 / 项目级 `default` 指向其独有 datasource / 未信任 + `${ENV}`

无需集成测试(加载流程不依赖真 DB)。

**文档层**:skill 文档加一句"查询结果不符合预期时,先 `mysql-cli config path` 查信任状态",帮 agent 自省。

## §9 向后兼容

- 无项目级 + 无 env + 无 `--config`:行为与现状完全一致
- `--config` 单文件语义不变(向后兼容)
- 现有 `~/.config/mysql-cli/config.toml` 用户无需改动
- 新增能力对老用户透明(项目级需主动放置 + 信任)

## §10 安全考量

- 恶意仓库放 `.config/mysql-cli/config.toml` 指向攻击者 DB 或用 `${ENV}` 套取本地环境变量密码 -> 信任清单拦截,未信任不加载、不展开
- 信任清单按目录信任,`filepath.EvalSymlinks` 防软链接欺骗
- 清单文件权限 0600
- `config show` 密码脱敏,不泄露明文

## §11 实现阶段建议

可分阶段实现(降低单次 PR 风险):

1. **Phase 1**:`loader.go`(发现 + 合并 + 信任判断)+ `Globals.resolve()` 接入 + 单测。不含子命令,行为完全兼容。
2. **Phase 2**:`MYSQL_CLI_CONFIG` env + 信任清单读写 + `config trust` 子命令。
3. **Phase 3**:`config path` / `show` / `init` 子命令 + skill 文档更新。
