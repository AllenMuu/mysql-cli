---
name: mysql-{{domain}}
version: {{meta_version}}
description: "{{meta_description}}"
metadata:
  binary: mysql-cli
  requires:
    bins: ["mysql-cli"]
  cliHelp: "mysql-cli {{domain}} --help"
  config_file: ~/.config/mysql-cli/config.toml
  default_output: json
  license: MIT
---

# mysql-{{domain}} 技能 / mysql-{{domain}} Skill

**CRITICAL - 开始前 MUST 先用 Read 工具读取 [`../mysql-shared/SKILL.md`](../mysql-shared/SKILL.md)**,
其中包含配置与数据源、全局 flag、安全模型、稳定退出码、错误自修复与输出格式。
/ Contains config & datasource, global flags, safety model, exit codes, error recovery, output formats.

> Convention / 约定: assume `mysql-cli` is on `PATH`. / 假设 `mysql-cli` 已在 `PATH` 中。

{{introduction}}

---

## Trigger Conditions / 触发条件

Use this skill when the user asks about any of the following:
当用户提出以下需求时使用本技能:

{{trigger_conditions}}

---

## Commands / 命令

All commands share global flags (see `mysql-shared`): `-d/--datasource`,
`-f/--format` (default `json`), `--write`, `--ddl`, `--yes`, `--limit`,
`--timeout`, `--config`, and connection overrides.

{{commands_table}}

---

## Typical Workflow / 典型工作流

{{workflow}}

---

## Notes / 备注

- 错误修复、退出码、输出格式见 `mysql-shared`。/ For error recovery, exit codes, output formats, see `mysql-shared`.
- 用 `mysql-cli {{domain}} --help` 查看完整 flag。/ Run `mysql-cli {{domain}} --help` for full flags.
