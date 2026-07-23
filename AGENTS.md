This file provides guidance to AI agents when working with code in this repository.

## 项目定位

Go 实现的 MySQL CLI，目标是**替代 `designcomputer/mysql_mcp_server`**：把原 MCP 的全部只读/写能力下沉为命令行子命令，让任何能跑 shell 的 AI agent（Claude Code / Cursor / Codex / Aider）无需 MCP runtime 即可查询 MySQL。设计前提是 **agent 是首要调用方**（默认 JSON 输出、稳定退出码），REPL 仅供人类调试，不是主路径。

## 常用命令

```bash
go build ./...                              # 编译
go vet ./...                                # 静态检查
go test ./...                               # 单元测试（默认，136 用例，全部用 sqlmock，无需 DB）
go test ./internal/query/ -run TestApplyLimit -v   # 跑单个测试
go test -cover ./...                        # 覆盖率（项目目标 ≥80%，历史区间 81%~92%）
go test -coverprofile=cover.out ./... && go tool cover -func=cover.out   # 覆盖率明细
./scripts/skill-format-check.sh skills/     # 校验 SKILL.md frontmatter
./scripts/skill-format-check/test.sh        # skill-format-check 自测（good/bad 用例）
```

集成测试需要真实 MySQL，用 testcontainers-go 起 `mysql:8` 容器，**默认跳过**：

```bash
RUN_INTEGRATION=1 go test -tags=integration ./internal/integration/ -v
```

`internal/integration/integration_test.go` 带 `//go:build integration` 构建标签，且 `TestMain` 在 `RUN_INTEGRATION` 未设置时直接 `os.Exit(m.Run())` 跳过容器初始化。Docker 未运行时不要开此变量。

## 架构（分层与依赖方向）

包严格单向依赖，`result` 是无依赖底层，避免循环引用：

```
cmd/mysql-cli/main  ->  cli（cobra 装配 + 退出码映射 + skill 子命令）
                          ↓
        config ─-> conn ─-> query ─-> result
          │        │       └─-> safety（无依赖，纯逻辑）
          │        └─-> schema ─-> result/safety
          └─ env/file 解析   repl（聚合 query+schema+format）
                              format ← result
        cli（skill 子命令）─-> skillscheck ─-> bundle（根包，//go:embed skills/）
```

- **`result`** - 共享 `Result{Columns, Rows, RowsAffected, LastInsertID}`，是 query/schema（生产者）与 format/cli（消费者）之间的中立契约。
- **`safety`** - 纯逻辑、零依赖、完全可单测。SQL 分类（read/dml/ddl/unknown）、只读闸门、标识符校验、多语句检测、破坏性操作识别。**改动安全模型时只动这里。**
- **`config`** - TOML 命名数据源 + `MYSQL_*` 环境变量兼容（零配置迁移自原 MCP）。解析优先级：**CLI flag > env > file > default**（见 `Resolve`）。密码支持 `${ENV}` 占位符展开。
- **`conn`** - 由 `config.Datasource` 渲染 go-sql-driver DSN 并开连接池；SSH 隧道在 `Open` 前建立，DSN 指向本地转发端口。`Pool.Close` 先关隧道再关 `*sql.DB`（生命周期绑定，见下）。
- **`query`** - `Execute`（读，走 `QueryContext`）、`ExecuteWrite`（单条 DML/DDL，包在事务里提交）、`ExecuteTxn`（多条原子事务）。每条语句都过 safety 闸门 + 多语句检测。
- **`schema`** - 只读探索命令（`schema/sample/tables/databases/read/explore/analyze`），对应原 MCP 的 `get_schema_info`/`get_table_sample`/`list_resources`/`read_resource`。所有标识符在拼接 SQL 前经 `safety.Validate*` 校验。
- **`format`** - `result.Result` -> json/table/csv/tsv；JSON 严格信封 `{success,data,error:{code,message}}`。
- **`cli`** - cobra 子命令 + 全局 flag + `mapError` 把核心 error 翻译成退出码；含 `skill` 子命令（list/check/install/version）。
- **`repl`** - readline 交互壳，仅人类调试用，复用 query/schema/format。
- **`bundle`**（根包，`bundle.go`）- `//go:embed skills` 把 skill 定义嵌入二进制，是 `mysql-cli skill install` 零依赖安装的单一来源（与 `scripts/install-skills.sh` 共享 `skills/` 目录）。
- **`skillscheck`** - 对比已装 skill（如 `~/.claude/skills`）的 version frontmatter 与 bundle 内嵌版本，报 `ok/stale/missing/unknown`。仅 `mysql-cli skill check` 显式调用，**不走查询热路径**。

## 关键约定（改代码前必读）

**读/写路由**：`cli.newQueryCmd` 和 `repl.runSQL` 按 `safety.Classify` 分流--read/unknown 走 `Execute`（`QueryContext` 拿 rows），dml/ddl 走 `ExecuteWrite`（事务内 `Exec` 拿 rows affected）。**不要把写语句塞进 `QueryContext`**，go-sql-driver 会拒绝。

**SQL 读值必须转字符串**：驱动对文本列返回 `[]byte`，`query.go` 和 `schema.go` 的扫描循环都把 `[]byte` 转成 `string`，否则 JSON 输出会变成 base64。新增扫描路径时照搬此转换。

**退出码是契约**：`cli` 层的 `Exit*` 常量（2=conn, 3=readonly, 4=ddl-needs-write, 5=destructive-needs-yes, 6=identifier, 7=multi-statement, 8=sql, 9=timeout, 10=config）面向 agent，不可随意改动。`mapError` 按 `errors.Is` 识别 `safety.*` / `query.*` 哨兵 error，连接/配置失败靠 `err.Error()` 字符串匹配兜底。新增 error 类型时挂到对应哨兵（`fmt.Errorf("%w: %v", ErrGuard, err)`）。

**SSH 隧道生命周期**：`conn.openWithTunnelHook` 建隧道后把 `closer` 存进 `Pool`，`Pool.Close` 必须先关隧道再关 DB。`tunnelHook` 是可测试的注入点（`ssh_test.go` 替换它，避免真连 SSH）。

**多语句拒绝**：`safety.HasMultiStatement` 容忍单个结尾分号，但拒绝中间分号；多语句必须走 `txn` 子命令。闸门检查在 `query` 里、也在 `cli` 层连接前预检（让 readonly/multi-statement 在不连库时就返回正确退出码）。

**标识符只走白名单**：`safety.ValidateIdentifier`（`^[a-zA-Z0-9_$]+$`）和 `ValidateQualifiedTable`（`db.table`）。schema 命令拼 SQL 前必校验，防注入。表/库名用反引号包裹，`information_schema` 查询用单引号字面量。

**`analyze` 的 padRow 保留原值**：不要用 `fmt.Sprintf` 把单元格字符串化--保留 `nil`/`int`/`string` 原值，format 层才能正确渲染 NULL/数字（最近一次 fix 即为此）。

## 配置与调用

配置文件：`~/.config/mysql-cli/config.toml`（`--config` 可覆盖）。数据源用 `[datasource.<name>]`，顶层 `default` 指定默认；SSH 隧道用 `[datasource.<name>.ssh]` 子表。完整示例见 `README.md`。

子命令与 flag 语义见 `README.md`（`query/txn/schema/sample/tables/databases/read/explore/analyze`，及 `--write/--ddl/--yes/--limit/--timeout/-f`）。默认只读；DML 需 `--write`，DDL 需 `--write --ddl`，`DROP/TRUNCATE` 及无 `WHERE` 的 `UPDATE/DELETE` 需 `--yes`。

## Skill 体系（对接 AI agent）

skill 让 agent 零配置发现并正确调用 mysql-cli，设计参照 `larksuite/cli`（调研见 `docs/research-lark-cli.md`）。

- **skill 文件**：`skills/mysql-{shared,query,schema}/SKILL.md`。`mysql-shared` 承载配置/安全模型/退出码/错误自修复，被 `mysql-query`/`mysql-schema` 顶部 `MUST Read` 引用（auto-load，DRY）。新增 skill 建 `skills/mysql-<name>/SKILL.md`，参考 `skill-template/skill-template.md`。
- **安装**（二选一）：
  - `./scripts/install-skills.sh` -- auto 检测 `~/.claude`/`~/.cursor`，claude 复制 3 个 skill 目录，cursor 生成适配 `.mdc`；支持 `--agent claude|cursor|all`、`--project-dir`、`--no-global`。
  - `mysql-cli skill install [target-dir]` -- 从二进制内嵌的 bundle 安装，零外部依赖（默认 `~/.claude/skills`）。
- **版本同步检查**：`mysql-cli skill check [target-dir] [-j]` 对比已装 skill version 与内嵌版本，状态 `ok/stale/missing/unknown`，始终 exit 0（agent 解析 JSON `status` 字段）。
- **格式校验**：`scripts/skill-format-check.sh` 校验 SKILL.md frontmatter（name/version/description/metadata + name 匹配目录 + semver），CI `.github/workflows/skill-format-check.yml` 在 PR 时强制。改 skill 后本地跑一遍。
- **改动 skill 后**：skill 文件是 `bundle` 的 embed 源，改完 `go build` 重新嵌入；`scripts/install-skills.sh` 与 bundle 共享同一份 `skills/`，无需同步两份。
