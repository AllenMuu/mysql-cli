# mysql-cli 默认安全 LIMIT + 信封瘦身优化

- 日期: 2026-07-24
- 状态: 设计已批准,待实现计划
- 关联实测: `scripts/token-shootout.py`(mysql-cli vs mysql-mcp A/B)

## 背景

`scripts/token-shootout.py` 的 `--no-limit` 实测(`sd_cx_order`,44,516 行)揭示:

| 方法 | ≈tokens | 说明 |
|---|---:|---|
| MCP `execute_sql`(无 LIMIT) | 6,809,371 | mysql-mcp-server **无自动行数上限** |
| CLI `query`(无 `--limit`) | 9,085,333 | `applyLimit` 在 `--limit` 未设时是 no-op(`query.go:95`),同样全表扫 |
| CLI `query --limit 20` | 3,977 | 显式 flag 才截断 |

680~908 万 token = 34~45 个 200K context 窗口,一次忘加 LIMIT 的全表扫会当场撑爆任何 agent 会话。**两条路径裸跑都爆**,且 CLI 裸跑比 MCP 更费(+33%,JSON 信封更胖)。当前 CLI 的 `--limit` 是显式 opt-in,不是默认保护。

格式实测另显示:CLI `--format json` 比 MCP 胖 33%(36MB vs 27MB),只有 `csv` 能追平;`table` 格式是 token 杀手(+493%)。

## 目标

1. **默认安全 LIMIT**:SELECT 不带 LIMIT 时自动加 cap,消灭裸跑爆量灾难。对标 MCP 无保护的弱点,契合 README 的 "Safe by default" 定位--从"和 MCP 一样会爆"变成"默认就安全"。
2. **顺带信封瘦身**:缩小 CLI json 与 MCP 的 token 差距。

## 非目标

- 不改 DML/DDL 路径(cap 只作用于 read)
- 不引入 daemon / 连接池(短进程单二进制哲学不变;CLI 已比 MCP 快)
- 不保护 `SHOW`/`DESCRIBE`(语法不支持子查询 wrap,且结果集通常小;实测 `SHOW TABLES` 仅 15KB)
- 不优化 `table` 格式的 token 开销(它面向人类,非 agent 路径)

## 决策汇总(已与用户确认批准)

| 决策点 | 选择 |
|---|---|
| 北极星 | LIMIT 优先 + 顺带信封瘦身,一个 spec 覆盖 |
| 兼容策略 | 改默认开(breaking);`--no-limit` 关;`--limit` 显式 flag 保留;已带 LIMIT 的 SQL 不动 |
| 截断知情 | cap+1 探测 + `meta.truncated` 标记(零额外查询) |
| 信封瘦身 | 方案 A:SELECT 省 `rows_affected` + 新增 `--format jsonl` |
| cap 默认值 | 1000(`config.toml` / env 可配) |
| jsonl 截断传递 | stderr warning |

## §1 默认安全 LIMIT 机制

**改动位置**:`internal/query/query.go`(`applyLimit` 改造) + `internal/config`(加 `DefaultLimit`) + `internal/cli`(新增 `--no-limit` flag)

**cap 来源与优先级**:
`--limit N`(显式,精确 N 行,不探测截断) > `--no-limit`(完全禁用 cap) > config `default_limit` > env `MYSQL_CLI_DEFAULT_LIMIT` > 内置默认 **1000**

**数据流**(query.Execute 对 read 类 SQL):

1. `--no-limit` 设 -> 原样执行,不 cap(agent 自担风险,等价今天的行为)
2. `--limit N` 显式 -> wrap `LIMIT N`,精确返回 N 行,**不标 truncated**
3. 默认 cap 生效 -> 若 SQL 无 LIMIT(`hasLimit=false`)且是 SELECT/WITH -> wrap `SELECT * FROM (<sql>) AS _q LIMIT cap+1`;执行后若返回 cap+1 行 -> `truncated=true`,丢弃多余行只返 cap 行;否则 `truncated=false`
4. `truncated` + 实际 `limit` 传给 format 层写入 `meta`

**关键边界**:

- **已带 LIMIT 的 SQL 不动**(`hasLimit=true`,不二次 wrap)--保留原行为
- **只 wrap SELECT/WITH**:`selectRe` 只匹配 SELECT/WITH;`SHOW`/`DESCRIBE`/`EXPLAIN` 语法不支持子查询 wrap,不 cap
- **`--limit` 显式时不标 truncated**:用户明确要 N 行,截断语义无意义
- **非 read(DML/DDL)不涉及** cap

## §2 信封 / jsonl / 配置 / flag / 退出码 / skill / 测试

### §2.1 信封与 jsonl(format 层)

- SELECT 走新 `format.ReadJSON(r, truncated, limit)`:省 `rows_affected`(对 SELECT 恒 0),`meta:{truncated, limit}`;DML/DDL 仍用现有 `SuccessJSON`(保留 `rows_affected`)
- 新增 `--format jsonl`:每行一个 JSON 对象 `{col:val,...}`,NULL 渲染为原生 `null`
- jsonl 的 truncated 传递:**stderr 输出一行 `# truncated:true limit:1000`**(jsonl 是纯行流,stdout 不混 meta;agent 可选读 stderr)。不采用末行 `{"_truncated":true}` 方案,因为会污染行流

SELECT 默认 json 输出示例:

```json
{"success":true,"data":{"columns":["id","name"],"rows":[[1,"a"]]},"meta":{"truncated":false,"limit":1000}}
```

jsonl 示例:

```
{"id":1,"name":"a"}
{"id":2,"name":"b"}
```

### §2.2 配置(config 层)

- config.toml 顶层 `default_limit = 1000`(不新建子表,KISS)
- env `MYSQL_CLI_DEFAULT_LIMIT`
- Resolve 优先级:`--limit` > `--no-limit` > config > env > 默认 1000

### §2.3 flag / 退出码(cli 层)

- 新增全局 `--no-limit`(bool);`--limit` 语义不变
- **不新增退出码**:truncated 在 meta,success 仍 exit 0(截断不是错误,是安全保护)
- breaking:CHANGELOG 明示"SELECT 默认 cap 1000"

### §2.4 skill 更新(skills/)

- `mysql-shared/SKILL.md`:加默认 cap 行为 + `truncated` 含义 + `--no-limit` + `jsonl`
- `mysql-query/SKILL.md`:教 agent 见 `truncated:true` -> 主动 `--no-limit` 或 `COUNT(*)` 看全量;省 token 选 `jsonl`
- 两个 skill `version` frontmatter bump(skillscheck 对比)

### §2.5 测试

- **单测**(sqlmock,无需 DB):
  - `applyLimit` 默认 cap(无 `--limit` 无 `--no-limit`)-> wrap `LIMIT cap+1`
  - `hasLimit=true` 不 wrap
  - `--no-limit` 不 wrap
  - `--limit N` 显式 wrap `LIMIT N`(不加 +1,不标 truncated)
  - cap+1 探测两分支:返回 cap+1 行 -> `truncated=true` + 丢多余行;返回 ≤cap -> `truncated=false`
  - `SHOW`/`DESCRIBE` 不 wrap(`selectRe` 不匹配)
  - `ReadJSON`:省 `rows_affected` + `meta.truncated/limit`
  - `SuccessJSON`(write):保留 `rows_affected`
  - jsonl 输出格式 + NULL 渲染为 `null`
  - config `default_limit` 优先级(flag > no-limit > config > env > 默认)
- **集成**(testcontainers-go,`mysql:8`):插 >1000 行,验证截断 + `truncated` 标记 + `--no-limit` 全返回
- **shootout 复测**:加 cap 后 `--no-limit` 档的"默认 cap"分支应被截到 1000 行(≈token 从 908 万降到 ~20 万),作回归证据

## 架构改动位置一览

| 包/文件 | 改动 |
|---|---|
| `internal/query/query.go` | `applyLimit` 改造:默认 cap + cap+1 探测;`Execute` 扫描循环计数 + truncated 判定 |
| `internal/config` | 加 `DefaultLimit` 字段;`Resolve` 纳入 `default_limit` / `MYSQL_CLI_DEFAULT_LIMIT` |
| `internal/cli` | 新增全局 `--no-limit` flag;query 子命令把 cap/no-limit 透传 query 层 |
| `internal/format/format.go` | 新增 `ReadJSON(r, truncated, limit)`;新增 jsonl 分支;`SuccessJSON` 不变 |
| `skills/mysql-shared/SKILL.md` | 默认 cap + truncated + `--no-limit` + jsonl 说明;version bump |
| `skills/mysql-query/SKILL.md` | agent 适配指引;version bump |
| `CHANGELOG.md` | breaking:SELECT 默认 cap 1000 |

## Breaking Change 与迁移

- **SELECT 默认 cap 1000**:现有依赖全表返回的 agent 需显式加 `--no-limit`(或调大 `default_limit` / `--limit N`)。这类用法本就危险,迁移成本低。
- **SELECT 的 json 信封省 `rows_affected`**:该字段对 SELECT 恒为 0,现有解析它的 agent 拿到的是缺失而非错误,影响小。
- **CHANGELOG 明示**;skill 更新教 agent 识别 `truncated` 并主动看全量。
- 建议在下一个版本号体现 breaking(按 semver)。

## 开放问题

无。所有关键决策已在 brainstorming 阶段与用户确认(jsonl 截断走 stderr、cap=1000、改默认开等)。
