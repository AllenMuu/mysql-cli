<div align="center">

# mysql-cli

**让任何能跑 shell 的 AI agent 直接查询 MySQL 的 Go CLI - 无需 MCP runtime。**

[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server) 的替代方案:
保留其全部读写能力,以普通子命令重新暴露。只要你的 agent 能跑 shell,就能查询 MySQL。

[English](./README.md) · [简体中文](./README-zh.md)

[![Go 版本](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![平台](https://img.shields.io/badge/平台-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#安装)
[![输出](https://img.shields.io/badge/输出-JSON%20%7C%20table%20%7C%20CSV%20%7C%20TSV-blue)](#输出)

</div>

---

## 为什么需要它

原 MCP server 很好用 —— 直到你想在不支持 MCP 的 agent 里调用它。`mysql-cli` 保留了同样的
安全模型和功能集,但作为一个**默认输出 JSON**、**退出码稳定**的单二进制分发,任何 agent
(Claude Code、Cursor、Codex、Aider ……)都能直接通过 shell 驱动它。

- **Agent 优先** - 稳定的 JSON 信封 + 数字退出码,设计目标是被解析,而非被阅读。
- **默认安全** - 开箱即只读;写 / DDL / 破坏性操作需要显式 flag。
- **零配置迁移** - 直接兼容 MCP server 的 `MYSQL_*` 环境变量。
- **多数据源** - TOML 命名 profile,可选 SSH 隧道。
- **单一二进制** - `go install` 即用。

## 安装

```bash
go install github.com/AllenMuu/mysql-cli/cmd/mysql-cli@latest
```

源码构建:

```bash
git clone https://github.com/AllenMuu/mysql-cli.git
cd mysql-cli
go build -o mysql-cli ./cmd/mysql-cli
```

> 需要 Go 1.22+。

## 快速开始

```bash
mysql-cli query "SELECT * FROM users LIMIT 10"        # 读(默认)
mysql-cli tables                                       # 列出表
mysql-cli schema users                                 # 表结构
mysql-cli                                              # 进入 REPL(人类调试)
```

## 配置

`~/.config/mysql-cli/config.toml`:

```toml
default = "dev"

[datasource.dev]
host = "127.0.0.1"
port = 3306
user = "root"
password = "${MYSQL_DEV_PASSWORD}"
database = "app"

[datasource.prod]
host = "db.prod.internal"
user = "ro_user"
password = "${MYSQL_PROD_PASSWORD}"
database = "main"
ssl_mode = "REQUIRED"
```

解析优先级:**CLI flag > 环境变量 > 配置文件 > 默认值**。密码支持 `${ENV}` 占位符。
原 MCP 的全部 `MYSQL_*` 环境变量同样支持,迁移零配置。

## 命令

| 命令 | 说明 |
| --- | --- |
| `query <sql>` | 执行 SQL(默认只读;DML 需 `--write`) |
| `txn <sql1> [sql2…]` | 在单个原子事务中执行多条语句 |
| `schema [table]` | 查看表结构;不指定表则查看整个数据库 |
| `sample <table>` | 采样行(`-n`,最多 20) |
| `tables [db]` | 列出表 |
| `databases` | 列出数据库 |
| `read <table>` | 前 100 行 |
| `explore` | 数据库 + 表概览 |
| `analyze <table>` | 一次返回 schema + sample |
| *(无)* | 进入交互式 REPL(人类调试) |

## Flags

| Flag | 说明 |
| --- | --- |
| `-d, --datasource <name>` | 配置中的命名数据源 |
| `-f, --format json\|table\|csv\|tsv` | 输出格式(默认 `json`) |
| `--write` | 允许 DML |
| `--ddl` | 允许 DDL(需 `--write`) |
| `--yes` | 确认破坏性操作 |
| `--limit N` | `SELECT` 行数上限 |
| `--timeout 30s` | 查询超时 |
| `--host/--port/--user/--password/--db` | 连接参数覆盖 |

## 输出

默认 JSON(agent 友好):

```json
{"success":true,"data":{"columns":["id"],"rows":[[1]]},"rows_affected":0,"meta":{}}
{"success":false,"error":{"code":"READONLY_VIOLATION","message":"UPDATE requires --write"}}
```

用 `-f table`、`-f csv` 或 `-f tsv` 切换为人类可读格式。

### 退出码

| 码 | 含义 |
| ---: | --- |
| `0` | 正常 |
| `2` | 连接错误 |
| `3` | 只读违规 |
| `4` | DDL 需 `--write` |
| `5` | 破坏性操作需 `--yes` |
| `6` | 标识符非法 |
| `7` | 拒绝多语句输入 |
| `8` | SQL 错误 |
| `9` | 超时 |
| `10` | 配置错误 |

## 安全

默认只读。写操作按层级放行:

- DML 需 `--write`
- DDL 需 `--write --ddl`
- `DROP`/`TRUNCATE` 以及无 `WHERE` 的 `UPDATE`/`DELETE` 需 `--yes`

标识符按严格白名单校验(`^[a-zA-Z0-9_$]+$`);多语句输入被拒绝(请用 `txn`)。
只读 / 多语句检查在**打开连接之前**执行,因此 agent 无需触碰数据库即可拿到正确退出码。

## SSH 隧道

数据源可以通过 SSH 堡垒机建立隧道,而非直连:

```toml
[datasource.prod]
host = "127.0.0.1"
port = 3330
user = "ro_user"
password = "${MYSQL_PROD_PASSWORD}"
database = "main"

[datasource.prod.ssh]
enable = true
host = "bastion.prod"
user = "deploy"
key_path = "~/.ssh/id_ed25519"
remote_host = "db.prod.internal"
remote_port = 3306
local_port = 3330
```

隧道在建立数据库连接前打开,并与之一起关闭。

## 架构

严格单向分层;`result` 是无依赖的中立契约,解耦生产者与消费者。

```
cmd/mysql-cli/main  ->  cli   (cobra 装配 + 退出码映射)
                          │
        config ─-> conn ─-> query ─-> result
          │        │       └─> safety   (纯逻辑,零依赖)
          │        └─> schema ─> result/safety
          └ env/file        repl  (聚合 query + schema + format)
                            format ← result
```

| 包 | 职责 |
| --- | --- |
| `result` | 共享 `Result{Columns, Rows, RowsAffected, LastInsertID}` - 中立契约 |
| `safety` | SQL 分类、只读闸门、标识符校验、多语句与破坏性操作识别(纯逻辑,完全可单测) |
| `config` | TOML 命名数据源 + `MYSQL_*` 环境变量兼容 |
| `conn` | DSN 渲染、连接池、SSH 隧道生命周期 |
| `query` | 读 / 写 / 事务执行,每条语句过 `safety` 闸门 |
| `schema` | 只读探索命令 |
| `format` | `result` -> json/table/csv/tsv |
| `cli` | cobra 子命令 + `mapError`(error -> 退出码) |
| `repl` | readline 交互壳,供人类调试 |

## 致谢

本项目灵感来源于并基于
[`designcomputer/mysql_mcp_server`](https://github.com/designcomputer/mysql_mcp_server)
构建。安全模型与功能集很大程度上承袭自该项目。

## 许可证

基于 [MIT 协议](./LICENSE) 发布。
