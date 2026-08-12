# pi.dev 调研报告

> 调研对象：[pi.dev](https://pi.dev) 官方编码 Agent CLI（npm 包 `@earendil-works/pi-coding-agent`，二进制名 `pi`；GitHub 仓库 `earendil-works/pi`）。
> 调研目的：判断 Pi 是否能纳入 mysql-cli 的 `agent init` 命令，用于写操作（`--write` / `--ddl` / `--yes`）人类确认拦截。

---

## 0. 结论速览

| 评估项 | 结论 |
|--------|------|
| **Hook 支持判定** | **SUPPORTED** —— Pi 有完整、文档化的 `tool_call` 事件钩子，可拦截、可改写、可阻断工具调用 |
| **Tool name matcher** | `bash`（**小写**；与 Claude Code 的 `Bash`、TRAE 的 `RunCommand` 都不同） |
| **Hook 类型** | **TypeScript 扩展**（不是 command-type hook，不 exec 子进程；扩展是 in-process Node.js 模块） |
| **Tool input 可见性** | ✅ `event.input.command` 直接拿到命令字符串，可改写（mutation） |
| **Decision 语义** | `{ block: true, reason?: string }` 阻断；mutate `event.input` 改写；return undefined 放行。**无原生 `ask` 决策**（不像 Claude Code 的 `permissionDecision: "ask"`），但 `ctx.ui.confirm()` 在交互模式下提供等价人机确认 UX |
| **Config 位置（项目）** | `.pi/extensions/*.ts`（自动发现，但需先 `/trust` 当前项目） + `.pi/settings.json` |
| **Config 位置（全局）** | `~/.pi/agent/extensions/*.ts`（自动发现，无需 trust） + `~/.pi/agent/settings.json` |
| **能否复用现有 `mysql-write-guard.py`** | ❌ 不能直接复用（Pi 扩展是 TS，in-process；现有 Python 脚本走 stdin/stdout 协议）。需新增一份 TS 扩展模板，把检测逻辑（shlex token + regex fallback）以 TS 重写 |
| **能否纳入 `agent init`** | ✅ 可以。新建 `pi` Agent，`Cap: CapEnforce`，使用 `actionWriteFile` 原语写入 TS 扩展文件即可 |
| **是否值得纳入** | ✅ 值得。Pi 是近一年增长最快的开源 Agent CLI（GitHub 77k+ stars），契合 mysql-cli "agent 是首要调用方" 定位，TS 原生扩展不需要 Python 依赖 |

---

## 1. pi.dev 是什么

**Pi**（产品名 "Pi Coding Agent"，仓库 `earendil-works/pi`，官网 [pi.dev](https://pi.dev)）是 [Mario Zechner](https://github.com/badlogic)（libGDX 作者）发起、Earendil Inc. 维护的极简终端 AI 编码 Agent 框架（agent harness）。MIT 协议，TypeScript 实现，monorepo 结构（`pi-ai` / `pi-agent-core` / `pi-coding-agent` / `pi-tui` 四个核心包）。CLI 二进制名为 `pi`，npm 包名 `@earendil-works/pi-coding-agent`。

核心理念："**Adapt Pi to your workflows, not the other way around**"——Pi 故意把 Plan Mode / Sub-Agents / MCP / 权限弹窗 / 沙箱等"全家桶功能"留作扩展，核心仅 4 个内置工具（`read` / `write` / `edit` / `bash`）+ 20+ 生命周期事件订阅，通过 TypeScript 扩展、Skills、Prompt 模板、Themes 四个维度定制。

---

## 2. Hook 支持（核心结论：SUPPORTED）

### 2.1 机制：`tool_call` 事件 + bail 派发策略

Pi 的扩展 API（[官方文档](https://pi.dev/docs/latest/extensions)）暴露了完整的生命周期事件流。其中**工具调用前**的事件叫 `tool_call`，触发于 `tool_execution_start` 之后、工具真正执行之前，可拦截 / 改写 / 阻断。

官方文档原文（[extensions.md](https://pi.dev/docs/latest/extensions)，"Tool Events"章节）：

> Fired after `tool_execution_start`, before the tool executes. **Can block.**
>
> Behavior guarantees:
> - Mutations to `event.input` affect the actual tool execution
> - Later `tool_call` handlers see mutations made by earlier handlers
> - No re-validation is performed after your mutation
> - Return values from `tool_call` only control blocking via `{ block: true, reason?: string }`

派发策略（社区对官方文档的二次整理，[腾讯云开发者文章](https://cloud.tencent.com.cn/developer/article/2670659) 有完整对比）：

| 事件名 | 触发时机 | 派发策略 | 能干什么 |
|--------|----------|----------|----------|
| `tool_call` | LLM 给出 toolCall，执行前 | **bail**（任一插件返回 `{block:true}` 立即短路，拒绝执行） | 改 `event.input`；返回 `{block:true}` 拒绝 |
| `tool_result` | 结果回灌 LLM 前 | waterfall（链式改写） | 改 `content` / `details` / `isError` |
| `tool_execution_start` / `tool_execution_update` / `tool_execution_end` | 工具执行生命周期 | fire-and-forget | 埋点、UI |
| `before_agent_start` | LLM 调用前 | — | 改 system prompt / 注入消息 |
| `session_start` / `session_shutdown` | 会话生命周期 | — | 状态加载、清理 |

### 2.2 官方文档示例（完整代码，从 [pi.dev/docs/latest/extensions](https://pi.dev/docs/latest/extensions) 直接引用）

```typescript
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

export default function (pi: ExtensionAPI) {
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName === "bash" && event.input.command?.includes("rm -rf")) {
      const ok = await ctx.ui.confirm("Dangerous!", "Allow rm -rf?");
      if (!ok) return { block: true, reason: "Blocked by user" };
    }
  });

  // 也可注册自定义工具、命令、快捷键、CLI flag
  pi.registerTool({ /* ... */ });
  pi.registerCommand("hello", { /* ... */ });
}
```

### 2.3 关键差异：Pi 扩展是 in-process TypeScript，不是 command-type hook

| 维度 | Claude Code / CodeBuddy / TRAE | **Pi** |
|------|--------------------------------|--------|
| Hook 实现 | `type: "command"` + `command: "python3 ..."` 走子进程 | TS 模块，in-process（jiti 即时编译，无需 build） |
| Hook 协议 | stdin 接收 JSON 事件，stdout 返回 JSON 决策 | 直接 `pi.on("tool_call", handler)`，事件对象作参数传入 |
| 决策语义 | `permissionDecision: "allow" / "ask" / "deny"` | `{ block: true, reason? }` 或返回 undefined 放行；"ask" 需扩展自己调 `ctx.ui.confirm()` |
| 工具名 | `Bash`（Claude）、`RunCommand`（TRAE） | **`bash`（小写）** |
| 配置位置 | `~/.claude/settings.json`（hooks 数组） | `~/.pi/agent/extensions/*.ts` 或 `.pi/extensions/*.ts`（自动发现，**无需改 settings.json**） |
| Python 依赖 | 需 `python3` 解释器 | 仅需 Node.js（Pi 运行时本身就在 Node.js 内） |

**含义**：Pi 不能直接复用 `internal/agentsetup/templates/mysql-write-guard.py`，需要新增一份 `.ts` 扩展模板，把检测逻辑以 TS 重写。但好处是：mysql-cli 不再需要 Python 解释器，更纯净。

### 2.4 Pi 官方对"权限弹窗"的立场

Pi 官方在 [pi.dev](https://pi.dev) landing page 与 [README](https://github.com/earendil-works/pi) 明确表态：

> **No permission popups** — Run in a container, or build your own confirmation flow with extensions inline with your environment and security requirements.

这并非"不支持"，而是"故意不在核心内置"，把权限治理完全交给扩展层。社区已有多份成熟实现可作旁证：

- [`pi-perm`](https://www.npmjs.com/package/pi-perm) — 完整权限控制（allow / confirm / block / audit），可包裹 Anthropic Sandbox Runtime
- [`pi-permissions`](https://www.npmjs.com/package/pi-permissions) — allow/deny glob 规则，"deny always wins"
- [`pi-permission-gate`](https://www.npmjs.com/package/pi-permission-gate) — deny-by-default + 路径规范化 + secret 脱敏
- [`@diegopetrucci/pi-permission-gate`](https://www.npmjs.com/package/@diegopetrucci/pi-permission-gate) — 简化的 `rm -rf` / `sudo` / `chmod 777` 拦截

`pi-permission-gate` 的 README 直接证实 `tool_call` 事件的可用性：

> The extension hooks into three Pi events: `session_start` — loads and merges policy files; `before_agent_start` — hides tools blocked at session level via `setActiveTools()`; **`tool_call` — enforces granular rules before each tool executes**.

---

## 3. 配置文件与自动发现（项目 vs 全局）

### 3.1 路径（官方 [settings.md](https://pi.dev/docs/latest/settings) 与 [extensions.md](https://pi.dev/docs/latest/extensions) 直接引用）

| 资源 | 全局（所有项目） | 项目（当前目录） |
|------|------------------|------------------|
| **设置文件** | `~/.pi/agent/settings.json` | `.pi/settings.json` |
| **扩展（自动发现）** | `~/.pi/agent/extensions/*.ts`<br>`~/.pi/agent/extensions/*/index.ts` | `.pi/extensions/*.ts`<br>`*.pi/extensions/*/index.ts` |
| **扩展（settings.json 显式）** | `~/.pi/agent/settings.json` 的 `extensions` 数组 | `.pi/settings.json` 的 `extensions` 数组 |

`settings.json` 里的 `extensions` 字段长这样（[settings.md](https://pi.dev/docs/latest/settings)）：

```json
{
  "extensions": [
    "/path/to/local/extension.ts",
    "/path/to/local/extension/dir"
  ],
  "packages": [
    "npm:@foo/bar@1.0.0",
    "git:github.com/user/repo@v1"
  ]
}
```

### 3.2 重要陷阱：项目级扩展需要"Trust"才能加载

[extensions.md](https://pi.dev/docs/latest/extensions) 安全提示原文：

> Extensions are auto-discovered from trusted locations. **Project-local `.pi/extensions` entries load only after the project is trusted.**

[settings.md](https://pi.dev/docs/latest/settings) 进一步说明 trust 流程：

> On interactive startup, pi asks before trusting a project folder that contains project-local settings, resources, or project `.agents/skills`... Trusting a project allows pi to load `.pi/settings.json` and `.pi` resources, install missing project packages, and execute project extensions.

非交互模式（`pi -p`、`--mode json`、`--mode rpc`）不会弹 trust 提示，回退到全局设置 `defaultProjectTrust`（默认 `"ask"`），可用 `--approve` / `--no-approve` 在单次运行中覆盖。

**对 mysql-cli `agent init` 的影响**：
- `--global` scope：直接写入 `~/.pi/agent/extensions/mysql-write-guard.ts`，**立即可用**，无 trust 步骤；
- `--project` scope：写入 `<project>/.pi/extensions/mysql-write-guard.ts`，**用户首次启动 pi 时会弹 trust 提示**，确认后才生效。
- 这与 Cursor 全局 scope 不支持（用户规则在 IDE 设置里）的"非对称路径"是同类约束，agentsetup 已有先例（见 `trae` Agent 的 `.trae-cn` 后缀特例）。

### 3.3 热重载

`/reload` 命令可热加载扩展（[extensions.md](https://pi.dev/docs/latest/extensions)）：

> Put extensions in `~/.pi/agent/extensions/` (global) or `.pi/extensions/` (project-local) for auto-discovery... Extensions in auto-discovered locations can be hot-reloaded with `/reload`.

装好后无需重启 pi，让用户 `/reload` 即可。

---

## 4. 替代 gating 机制（如不走 hook 路线）

| 机制 | 适用场景 | 备注 |
|------|----------|------|
| **`tool_call` 事件 hook**（推荐） | 精确拦截、改写、人机确认 | 引擎级强约束，CapEnforce |
| `setActiveTools()`（`before_agent_start` 事件） | 会话级整工具屏蔽 | 例如禁止 `bash` 工具暴露给 LLM，过粗 |
| `permissions.json`（社区包 `pi-permissions` / `pi-permission-gate`） | 规则驱动 allow/deny glob | 本质还是 `tool_call` 事件包装，可作引用 |
| 容器化（OpenShell / Gondolin / Plain Docker） | OS 级沙箱边界 | [containerization.md](https://pi.dev/docs/latest/containerization)，与 mysql-cli 无关 |
| `AGENTS.md` 上下文指令 | 引导模型自己避免写操作 | 仅 CapGuide，模型可被 prompt injection 绕过 |

Pi 没有"内置 allowlist / regex deny 配置项"，所有权限治理都依赖扩展层。这意味着：**走 `tool_call` hook 是 Pi 上做写操作 gating 的唯一干净路径**。

---

## 5. mysql-cli `agent init` 集成评估

### 5.1 能否纳入现有 `Agent` 模型？✅ 完全可以

现有 `agentsetup.go` 的三个原语：

| 原语 | 用法 | Pi 集成方式 |
|------|------|-------------|
| `actionWriteFile` | 写新文件，存在则跳过（除非 `--force`） | **用此**：写 `mysql-write-guard.ts` 到扩展目录 |
| `actionMergeJSON` | 深合并 JSON（备份 `.bak`） | 可选：合并 `settings.json` 的 `extensions` 数组（但自动发现模式下不需要） |
| `actionCopyScript` | 写可执行脚本（覆盖） | 不适用：Pi 扩展是 `.ts`，不是可执行文件，权限 `0o644` 即可 |

`ScopeProject` 与 `ScopeGlobal` 都支持（详见 5.2 的具体路径）。

### 5.2 `Capability` 判定：**CapEnforce**

理由：
- `tool_call` 事件是引擎级 hook，**在工具真正执行前**阻断，模型无法绕过；
- Pi 官方文档明确"Can block"；
- `pi-permission-gate` 等社区包证实 `tool_call` 事件 + `{ block: true }` 是生产级强约束。

### 5.3 具体安装步骤（草拟）

| Scope | 文件路径 | 动作 |
|-------|----------|------|
| **Global** | `~/.pi/agent/extensions/mysql-write-guard.ts` | `actionWriteFile` 写入 TS 扩展 |
| **Project** | `<project>/.pi/extensions/mysql-write-guard.ts` | `actionWriteFile` 写入 TS 扩展（用户首次运行 pi 时需 trust） |

**不需要**改 `settings.json`——自动发现机制直接加载 `extensions/*.ts`。

### 5.4 TS 扩展模板的检测逻辑

把现有 `mysql-write-guard.py` 的两段检测（shlex token + regex fallback）以 TS 重写：

```typescript
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const WRITE_FLAGS = ["--write", "--ddl", "--yes"];
const PREFIXES = ["rtk", "sudo", "env", "nohup", "command"];
const RTK_SUB = new Set(["proxy"]);

function isMysqlCliWrite(command: string): boolean {
  // shlex 等价：用简易 token 切分（保留引号语义）
  const tokens = splitShellTokens(command);
  if (tokens) {
    const cmd = commandWord(tokens);
    const isMysql = cmd === "mysql-cli" || cmd.endsWith("/mysql-cli");
    if (isMysql && WRITE_FLAGS.some(f => tokens.includes(f))) return true;
  }
  // Fallback: 正则边界匹配（与 Python 版一致）
  if (/\bmysql-cli\b/.test(command)) {
    for (const flag of WRITE_FLAGS) {
      const re = new RegExp(`(?:^|\\s)${escapeRegex(flag)}(?=[\\s"'\\';|&]|$)`);
      if (re.test(command)) return true;
    }
  }
  return false;
}

// ...（splitShellTokens / commandWord / escapeRegex 实现，~30 行）

export default function (pi: ExtensionAPI) {
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "bash") return;
    const command: string | undefined = event.input?.command;
    if (!command || !isMysqlCliWrite(command)) return;
    const ok = await ctx.ui.confirm(
      "mysql-cli write operation",
      "This mysql-cli call carries --write / --ddl / --yes. Approve?"
    );
    if (!ok) {
      return { block: true, reason: "mysql-cli write requires human approval" };
    }
  });
}
```

注意点：
- Pi 的 `ctx.ui.confirm()` 在**非交互模式**（`pi -p` / `--mode json` / `--mode rpc`）下行为需测试；社区包 `@diegopetrucci/pi-permission-gate` 给出经验："If pi is running without an interactive UI, it blocks matching commands by default"。可加 `ctx.hasUI` 检查，无 UI 时直接 `block: true`。
- 与 Python hook 的"返回 `permissionDecision: 'ask'` 让 agent UI 弹窗"不同，Pi 扩展自己用 `ctx.ui.confirm()` 弹窗，UX 等价但实现路径不同。

### 5.5 阻断点 / 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| TS 扩展不能复用 `mysql-write-guard.py` | 需新增一份模板 + 单独维护检测逻辑 | 检测逻辑就 ~30 行，可接受；或抽出共享 JSON 规则（如 flags / prefixes 列表）由两份模板各自嵌入 |
| Pi 扩展系统较新（v0.74+ 迁移到 earendil-works 组织后稳定） | API 可能后续调整 | 锁定 ExtensionAPI 当前签名；监控 pi changelog |
| 项目级扩展需 trust 才生效 | `--project` 装好后用户首次启动 pi 会弹 trust 提示 | 安装时 stderr 提示用户："运行 pi 后请确认 trust 当前项目" |
| `ctx.ui.confirm()` 在非交互模式下的行为文档未明示 | 可能默认放行或默认 block | 在扩展内显式判 `ctx.hasUI`，无 UI 时回退到 block（与 `@diegopetrucci/pi-permission-gate` 一致） |
| Pi 的内置工具名是 `bash`（小写），与 Claude/TRAE 不同 | 写扩展时硬编码 `event.toolName === "bash"` | 与 trae 用 `RunCommand` 一样，在 Agent struct 内固定即可 |

---

## 6. 推荐：✅ 应该加入

理由：
1. Pi 是 2025–2026 年增长最快的开源 Agent CLI（GitHub 77k+ stars，[bestofjs](https://bestofjs.org/projects/pi) 月增 ~500 stars/day），与 mysql-cli "agent 是首要调用方" 定位高度契合；
2. `tool_call` 事件机制成熟、文档详尽、社区生态有多份权限扩展背书；
3. TS 原生扩展不需要 Python 解释器，反而比 Claude/CodeBuddy/TRAE 的 hook 路径更纯净；
4. agentsetup 已有非对称路径先例（trae 的 `.trae-cn`），加 Pi 的 `~/.pi/agent/extensions/` 不破坏既有模型。

### 6.1 `Agent{}` struct 草拟

```go
//go:embed templates/pi-mysql-write-guard.ts
var piExtensionScript []byte

var pi = Agent{
    Name: "pi",
    Desc: "Pi Coding Agent (tool_call hook -> ctx.ui.confirm -> block)",
    Cap:  CapEnforce,
    steps: func(o InstallOpts) ([]step, error) {
        // Pi 扩展自动发现路径：
        //   global  -> ~/.pi/agent/extensions/*.ts
        //   project -> <project>/.pi/extensions/*.ts
        // 无需改 settings.json（自动发现直接加载）。
        var dir string
        if o.Scope == ScopeGlobal {
            dir = filepath.Join(o.Home, ".pi", "agent", "extensions")
        } else {
            dir = filepath.Join(o.ProjectDir, ".pi", "extensions")
        }
        return []step{
            {
                path:   filepath.Join(dir, "mysql-write-guard.ts"),
                action: actionWriteFile,  // 0o644，存在则跳过（除非 --force）
                content: piExtensionScript,
            },
        }, nil
    },
}
```

更新 `Agents` 注册表（[agentsetup.go:94](file:///Users/allenj/work/AllenMuu/mysql-cli/internal/agentsetup/agentsetup.go#L94)）：

```go
var Agents = []Agent{claudeCode, cursor, opencode, copilot, codebuddy, trae, pi}
```

新增模板文件：`internal/agentsetup/templates/pi-mysql-write-guard.ts`，内容为 §5.4 的 TS 扩展实现。

### 6.2 单测要点

参照 `agentsetup_test.go` 现有套路：
- `TestPi_Installs_ProjectScope`：断言 `<tmp>/.pi/extensions/mysql-write-guard.ts` 被写入；
- `TestPi_Installs_GlobalScope`：断言 `<home>/.pi/agent/extensions/mysql-write-guard.ts` 被写入；
- `TestPi_DryRun`：断言输出 `write <path>` 描述；
- `TestPi_SkipIfExists`：预置文件后断言不覆盖（除非 `Force`）。

无需测 TS 扩展本身的运行时行为（那需要拉起 Node.js + pi runtime，超出单元测试范围，与现有 Python hook 未做端到端测试一致）。

---

## 7. Sources

**Pi 官方**
- [pi.dev landing page](https://pi.dev) — 产品定位、设计哲学、安装入口
- [Pi Documentation Latest](https://pi.dev/docs/latest) — 文档总目录
- [Extensions docs](https://pi.dev/docs/latest/extensions) — `tool_call` 事件、ExtensionAPI、自动发现路径、`{ block: true }` 决策语义（**核心证据**）
- [Settings docs](https://pi.dev/docs/latest/settings) — `settings.json` 路径、project trust 流程、`defaultProjectTrust`
- [Containerization docs](https://pi.dev/docs/latest/containerization) — 沙箱与权限边界备选方案
- [earendil-works/pi GitHub repo](https://github.com/earendil-works/pi) — 源码、README、AGENTS.md
- [bestofjs.org/projects/pi](https://bestofjs.org/projects/pi) — npm 月下载量、Stars 增长数据

**Pi 社区扩展（旁证 `tool_call` 事件可用性）**
- [`pi-perm`](https://www.npmjs.com/package/pi-perm) — allow/confirm/block/audit + Sandbox Runtime 包裹
- [`pi-permissions`](https://www.npmjs.com/package/pi-permissions) — allow/deny glob，"deny always wins"
- [`pi-permission-gate`](https://www.npmjs.com/package/pi-permission-gate) — deny-by-default，三事件 hook（session_start / before_agent_start / tool_call）
- [`@diegopetrucci/pi-permission-gate`](https://www.npmjs.com/package/@diegopetrucci/pi-permission-gate) — `tool_call` hook + `ctx.ui.confirm()` 模式范例
- [`pi-edit-hooks`](https://www.npmjs.com/package/pi-edit-hooks) — `onEdit` / `onStop` 钩子（与 `tool_call` 不同，关注文件编辑而非命令执行）

**社区整理**
- [A field guide to extending pi](https://curtisalexander.github.io/agent-stuff/) — 扩展机制全景、extension vs skill vs prompt 决策表
- [Agent ToolCall 循环怎么定制？PI Extension 与 DeepAgents Middleware 两条岔路深度对比](https://cloud.tencent.com.cn/developer/article/2670659) — bail / waterfall / fire-and-forget 派发策略对比
- [Pi Code Agent 教學手冊（Enterprise Edition）](https://chihhung.github.io/Blog/posts/%E6%95%99%E5%AD%B8/ai%E9%96%8B%E7%99%BC/pi-code-agent-%E6%95%99%E5%AD%B8%E6%89%8B%E5%86%8A/) — Extension vs Skill vs Prompt Template 选择指南
- [Pi.dev深度解析](https://yunpan.plus/t/25368-1-1) — 中文社区对 Pi 哲学的拆解
