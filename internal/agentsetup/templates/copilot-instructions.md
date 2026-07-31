# mysql-cli 写操作守卫

`mysql-cli` 默认只读。命令含 `--write`/`--ddl`/`--yes` 任一即为写/破坏性操作。执行前必须向用户展示完整命令与影响范围并请求人工确认；`--yes` 是标记破坏性操作而非豁免，不得用它绕过确认。完整安全模型见 `skills/mysql-shared/SKILL.md` 的 Security Model 段。

> 强制拦截由 `.vscode/settings.json` 的 `chat.tools.terminal.autoApprove` 实现（命中写 flag 的正则即强制人工确认），本文件为补充引导。
