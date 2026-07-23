# npx 分发实现计划 (Plan 2 / 共 2 份)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 通过 npm 包 `@allenmuu/mysql-cli` 实现 `npx` 一键安装 mysql-cli 二进制(postinstall 从 GitHub Releases 下载预编译 Go 二进制),配合 GoReleaser CI 交叉编译与发布。不依赖 Plan 1 的 `init`(只打包 `cmd/mysql-cli`)。

**Architecture:** `dist/npm/` 下一个零依赖 Node 包装层:`install.js`(postinstall 下载器,纯函数可测)按平台下载 GoReleaser 产物并用系统 `tar` 解压;`bin/mysql-cli.js`(shim)把 `install` 子命令映射为「复制二进制到持久位」,其他参数透传 spawn Go 二进制。`.goreleaser.yml` 交叉编译 5 个目标发到 GitHub Releases;`.github/workflows/release.yml` 在 tag 触发发布 + 可选 npm publish。

**Tech Stack:** Node >= 18(内置 `node:test`,**零 npm 依赖**--解压用系统 `tar`)、GoReleaser v2、GitHub Actions。

## Global Constraints

- Go 1.22(`go.mod`);二进制入口 `./cmd/mysql-cli`(无 version 变量,**不加** `--version`,YAGNI)。
- npm 包名 `@allenmuu/mysql-cli`(spec D5);二进制托管 **GitHub Releases**(spec D6,仓库 `github.com/AllenMuu/mysql-cli`)。
- Node `engines >= 18`;测试用内置 `node:test`(**零测试依赖**);包装层**零 npm 运行时依赖**(解压 shell out 系统 `tar`)。
- 归档命名固定:`mysql-cli_<goos>_<goarch>.tar.gz`(windows 用 `.zip`);Node 映射 `process.platform`->goos(`darwin/linux/win32`->`darwin/linux/windows`)、`process.arch`->goarch(`x64`->`amd64`,`arm64`->`arm64`)。
- GoReleaser 输出目录设为 **`.goreleaser-dist/`**(非默认 `dist/`),避免 `--clean` 清掉已提交的 `dist/npm/`。**[对 spec 隐含默认的修正,已在计划标注]**
- 两步流(spec D2):`npx @allenmuu/mysql-cli install`(shim 复制二进制到 `~/.local/bin`)-> `mysql-cli init`。Go 二进制**不加** `install` 子命令(YAGNI)。
- postinstall 失败**不致命**:装提示桩 + 打印手动 URL,exit 0,不阻断 `npm i`。
- Windows arm64 **不在范围**(spec OUT);targets = darwin/amd64、darwin/arm64、linux/amd64、linux/arm64、windows/amd64。
- npm 发布为**可选/可门控**(spec:初期手动,稳定后自动化);用仓库变量 `NPM_PUBLISH=true` + `NPM_TOKEN` secret 启用。
- Conventional commits;attribution 已全局禁用。
- LICENSE 文件已在仓库根;package.json `license` 字段须与之一致(实现时读 `LICENSE` 确认,默认 MIT)。

## File Structure

**Create:**
- `dist/npm/package.json` - npm 包定义(name/bin/postinstall/engines/files)。
- `dist/npm/install.js` - postinstall 下载器 + 可测纯函数(`mapAsset`/`buildUrl`/`extractArchive`/`download`)。
- `dist/npm/bin/mysql-cli.js` - shim(`install`->复制持久位;其他->spawn Go 二进制)。
- `dist/npm/README.md` - npm 包页面说明。
- `dist/npm/test/install.test.js` - install.js 纯函数 + 解压 fixture 测试(node:test)。
- `dist/npm/test/shim.test.js` - shim 纯函数测试(node:test)。
- `.goreleaser.yml` - 交叉编译 + 归档 + checksum + release 配置。
- `.github/workflows/release.yml` - tag 触发 release + 可选 npm publish;PR 触发 check。
- `docs/superpowers/plans/2026-07-23-npx-init-plan-2-distro.md` - 本文件。

**Modify:**
- `.gitignore` - 加 npx 分发相关忽略项。
- `README.md` - 安装段加 npx 一键(推荐)。

---

### Task 1: postinstall 下载器 `install.js` + npm 包骨架

**Files:**
- Create: `dist/npm/package.json`、`dist/npm/install.js`、`dist/npm/README.md`、`dist/npm/test/install.test.js`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `mapAsset(platform, arch) -> {goos, goarch, ext, asset} | null`、`buildUrl(version, asset, mirror) -> string`、`extractArchive(archivePath, outDir) -> void`、`download(url) -> Promise<Buffer>`(均 `module.exports`)。
- Consumes: `require('./package.json').version`(由本任务创建的 package.json 提供)。

- [ ] **Step 1: 改 `.gitignore` 加忽略项**

在 `.gitignore` 末尾追加:
```
# npx distribution (Plan 2)
/dist/npm/bin/mysql-cli
/dist/npm/bin/mysql-cli.exe
/dist/npm/node_modules/
/dist/npm/package-lock.json
/.goreleaser-dist/
```

- [ ] **Step 2: 创建 `dist/npm/package.json`**

```json
{
  "name": "@allenmuu/mysql-cli",
  "version": "0.0.0",
  "description": "One-line install of mysql-cli (Go binary) for AI agents: npx @allenmuu/mysql-cli install",
  "bin": {
    "mysql-cli": "bin/mysql-cli.js"
  },
  "scripts": {
    "postinstall": "node install.js",
    "test": "node --test test/"
  },
  "engines": {
    "node": ">=18"
  },
  "files": [
    "install.js",
    "bin/mysql-cli.js",
    "README.md"
  ],
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "https://github.com/AllenMuu/mysql-cli.git"
  }
}
```

> 读仓库根 `LICENSE` 文件第一行确认许可证;若非 MIT,改 `license` 字段与之匹配。`version: "0.0.0"` 是开发占位;发布时 release workflow 用 `npm version` 从 tag 注入真实版本。

- [ ] **Step 3: 写失败测试 `dist/npm/test/install.test.js`**

```js
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');

const { mapAsset, buildUrl, extractArchive } = require('../install.js');

test('mapAsset maps supported platforms to GoReleaser assets', () => {
  assert.deepEqual(mapAsset('darwin', 'x64'), { goos: 'darwin', goarch: 'amd64', ext: 'tar.gz', asset: 'mysql-cli_darwin_amd64.tar.gz' });
  assert.deepEqual(mapAsset('darwin', 'arm64'), { goos: 'darwin', goarch: 'arm64', ext: 'tar.gz', asset: 'mysql-cli_darwin_arm64.tar.gz' });
  assert.deepEqual(mapAsset('linux', 'x64'), { goos: 'linux', goarch: 'amd64', ext: 'tar.gz', asset: 'mysql-cli_linux_amd64.tar.gz' });
  assert.deepEqual(mapAsset('linux', 'arm64'), { goos: 'linux', goarch: 'arm64', ext: 'tar.gz', asset: 'mysql-cli_linux_arm64.tar.gz' });
  assert.deepEqual(mapAsset('win32', 'x64'), { goos: 'windows', goarch: 'amd64', ext: 'zip', asset: 'mysql-cli_windows_amd64.zip' });
});

test('mapAsset returns null for unsupported platforms', () => {
  assert.equal(mapAsset('aix', 'x64'), null);
  assert.equal(mapAsset('linux', 'arm'), null);
});

test('buildUrl constructs release URL with and without mirror', () => {
  const asset = 'mysql-cli_darwin_amd64.tar.gz';
  assert.equal(
    buildUrl('1.2.3', asset, undefined),
    'https://github.com/AllenMuu/mysql-cli/releases/download/v1.2.3/mysql-cli_darwin_amd64.tar.gz'
  );
  assert.equal(
    buildUrl('1.2.3', asset, 'https://mirror.example.com/dl'),
    'https://mirror.example.com/dl/v1.2.3/mysql-cli_darwin_amd64.tar.gz'
  );
});

test('extractArchive extracts a tar.gz fixture into outDir', () => {
  const srcDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-src-'));
  const archiveDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-arc-'));
  const extractDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-ext-'));
  fs.writeFileSync(path.join(srcDir, 'mysql-cli'), '#!/bin/sh\necho fake\n');
  const archive = path.join(archiveDir, 'fixture.tar.gz');
  execFileSync('tar', ['-czf', archive, '-C', srcDir, 'mysql-cli']);
  extractArchive(archive, extractDir);
  assert.ok(fs.existsSync(path.join(extractDir, 'mysql-cli')), 'binary should be extracted');
});
```

- [ ] **Step 4: 跑测试确认失败**

Run: `cd dist/npm && node --test test/install.test.js`
Expected: FAIL(`Cannot find module '../install.js'`)。

- [ ] **Step 5: 写实现 `dist/npm/install.js`**

```js
#!/usr/bin/env node
'use strict';

const https = require('node:https');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const pkg = require('./package.json');

// mapAsset maps Node's platform/arch to the GoReleaser archive asset.
// Returns { goos, goarch, ext, asset } or null if unsupported.
function mapAsset(platform, arch) {
  const goos = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[platform];
  const goarch = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!goos || !goarch) return null;
  const ext = goos === 'windows' ? 'zip' : 'tar.gz';
  const asset = `mysql-cli_${goos}_${goarch}.${ext}`;
  return { goos, goarch, ext, asset };
}

// buildUrl constructs the release download URL for an asset.
function buildUrl(version, asset, mirror) {
  const base = mirror || 'https://github.com/AllenMuu/mysql-cli/releases/download';
  return `${base}/v${version}/${asset}`;
}

// extractArchive extracts archivePath into outDir using the system `tar`
// (handles .tar.gz on unix; .zip on Windows 10+ bsdtar).
function extractArchive(archivePath, outDir) {
  execFileSync('tar', ['-xf', archivePath, '-C', outDir], { stdio: 'pipe' });
}

// download fetches a URL buffer, following up to 5 redirects (GitHub Releases
// redirects to S3). Not unit-tested (network); buildUrl/extractArchive are.
function download(url) {
  return new Promise((resolve, reject) => {
    const get = (u, redirs) => {
      if (redirs > 5) return reject(new Error('too many redirects'));
      https.get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          return get(res.headers.location, redirs + 1);
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`HTTP ${res.statusCode} for ${u}`));
        }
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => resolve(Buffer.concat(chunks)));
      }).on('error', reject);
    };
    get(url, 0);
  });
}

async function run() {
  const outDir = path.join(__dirname, 'bin');
  fs.mkdirSync(outDir, { recursive: true });
  const mapped = mapAsset(process.platform, process.arch);
  if (!mapped) {
    console.warn(`mysql-cli: unsupported platform ${process.platform}/${process.arch}; skipping binary download.`);
    return;
  }
  const exeName = mapped.goos === 'windows' ? 'mysql-cli.exe' : 'mysql-cli';
  const binPath = path.join(outDir, exeName);
  const url = buildUrl(pkg.version, mapped.asset, process.env.MYSQL_CLI_MIRROR);
  try {
    console.log(`mysql-cli: downloading ${mapped.asset}`);
    const buf = await download(url);
    const archivePath = path.join(outDir, mapped.asset);
    fs.writeFileSync(archivePath, buf);
    extractArchive(archivePath, outDir);
    fs.unlinkSync(archivePath);
    if (mapped.goos !== 'windows') fs.chmodSync(binPath, 0o755);
    if (!fs.existsSync(binPath)) throw new Error('binary not found after extraction');
    console.log(`mysql-cli: installed binary to ${binPath}`);
  } catch (err) {
    // Non-fatal: leave bin/ without the binary; the shim prints guidance.
    console.warn(`mysql-cli: could not install binary (${err.message}).`);
    console.warn(`Download manually: ${url}`);
  }
}

if (require.main === module) {
  run().catch((err) => {
    console.warn(`mysql-cli: postinstall error: ${err.message}`);
    // exit 0 - never fail npm install
  });
}

module.exports = { mapAsset, buildUrl, extractArchive, download };
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd dist/npm && node --test test/install.test.js`
Expected: PASS,4 个测试通过。

- [ ] **Step 7: 创建 `dist/npm/README.md`**

```markdown
# @allenmuu/mysql-cli

One-line install of the [mysql-cli](https://github.com/AllenMuu/mysql-cli) Go binary for AI agents.

## Install

```bash
npx @allenmuu/mysql-cli install      # installs the binary to ~/.local/bin
mysql-cli init                       # installs agent skills (auto-detected)
```

No Go toolchain required. The `install` command downloads the prebuilt binary for your platform from GitHub Releases.

## One-shot usage (no permanent install)

```bash
npx @allenmuu/mysql-cli init         # install skills into detected agents
npx @allenmuu/mysql-cli skill check  # check installed skill versions
npx @allenmuu/mysql-cli query "SELECT 1" -d mydb
```

## Mirror (GFW-friendly)

Set `MYSQL_CLI_MIRROR` to a mirror of the GitHub Releases download path:

```bash
MYSQL_CLI_MIRROR=https://ghproxy.com/https://github.com/AllenMuu/mysql-cli/releases/download npx @allenmuu/mysql-cli install
```

## License

MIT (matches the upstream repo).
```

- [ ] **Step 8: 验证 package.json 合法 + install.js 可被 require**

Run: `cd dist/npm && node -e "const p=require('./package.json'); console.log(p.name, p.version, p.bin)" && node -e "const m=require('./install.js'); console.log(typeof m.mapAsset, typeof m.buildUrl)"`
Expected: 打印 `@allenmuu/mysql-cli 0.0.0 { mysql-cli: 'bin/mysql-cli.js' }` 和 `function function`。

- [ ] **Step 9: 提交**

```bash
git add .gitignore dist/npm/package.json dist/npm/install.js dist/npm/README.md dist/npm/test/install.test.js
git commit -m "feat(npm): add @allenmuu/mysql-cli wrapper (postinstall downloader + tests)"
```

---

### Task 2: shim `bin/mysql-cli.js`

**Files:**
- Create: `dist/npm/bin/mysql-cli.js`、`dist/npm/test/shim.test.js`

**Interfaces:**
- Produces: `bundledBinPath(dir) -> string`、`persistentDir(platform, env, home) -> string`、`doInstall(bundled, dir) -> string`(返回 dest 路径),均 `module.exports`。
- Consumes: postinstall(Task 1)下载的 `bin/mysql-cli` 二进制(位于 `__dirname`)。

- [ ] **Step 1: 写失败测试 `dist/npm/test/shim.test.js`**

```js
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');

const { bundledBinPath, persistentDir, doInstall } = require('../bin/mysql-cli.js');

test('bundledBinPath returns platform-correct binary name', () => {
  // exeName is fixed at module load to the current platform; just assert shape.
  assert.ok(bundledBinPath('/x/y').startsWith('/x/y/mysql-cli'));
});

test('persistentDir returns ~/.local/bin on unix', () => {
  assert.equal(persistentDir('darwin', {}, '/home/u'), '/home/u/.local/bin');
  assert.equal(persistentDir('linux', {}, '/home/u'), '/home/u/.local/bin');
});

test('persistentDir returns AppData\\mysql-cli on windows', () => {
  assert.equal(persistentDir('win32', { LOCALAPPDATA: 'C:\\AppD' }, 'C:\\Users\\u'), 'C:\\AppD\\mysql-cli');
  assert.equal(persistentDir('win32', {}, 'C:\\Users\\u'), 'C:\\Users\\u\\AppData\\Local\\mysql-cli');
});

test('doInstall copies the bundled binary to the dest dir and chmods it', () => {
  const srcDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-shim-src-'));
  const destDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-shim-dst-'));
  const exeName = process.platform === 'win32' ? 'mysql-cli.exe' : 'mysql-cli';
  const bundled = path.join(srcDir, exeName);
  fs.writeFileSync(bundled, '#!/bin/sh\necho fake\n');
  const dest = doInstall(bundled, destDir);
  assert.ok(fs.existsSync(dest), 'dest binary exists');
  assert.equal(path.dirname(dest), destDir);
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd dist/npm && node --test test/shim.test.js`
Expected: FAIL(`Cannot find module '../bin/mysql-cli.js'`)。

- [ ] **Step 3: 写实现 `dist/npm/bin/mysql-cli.js`**

```js
#!/usr/bin/env node
'use strict';

const { spawn } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');

const exeName = process.platform === 'win32' ? 'mysql-cli.exe' : 'mysql-cli';

// bundledBinPath returns the path to the binary downloaded by postinstall.
function bundledBinPath(dir) {
  return path.join(dir, exeName);
}

// persistentDir returns the permanent install location for the binary.
function persistentDir(platform, env, home) {
  if (platform === 'win32') {
    return path.join(env.LOCALAPPDATA || path.join(home, 'AppData', 'Local'), 'mysql-cli');
  }
  return path.join(home, '.local', 'bin');
}

// doInstall copies the bundled binary into dir, chmods it, returns dest path.
function doInstall(bundled, dir) {
  fs.mkdirSync(dir, { recursive: true });
  const dest = path.join(dir, exeName);
  fs.copyFileSync(bundled, dest);
  if (process.platform !== 'win32') fs.chmodSync(dest, 0o755);
  return dest;
}

// main: `install` -> copy to persistent dir; anything else -> spawn Go binary.
function main(argv, bundledDir) {
  bundledDir = bundledDir || __dirname;
  const bundled = bundledBinPath(bundledDir);
  if (argv[0] === 'install') {
    if (!fs.existsSync(bundled)) {
      console.error('mysql-cli: binary not bundled; re-run `npm install` or download manually:');
      console.error('  https://github.com/AllenMuu/mysql-cli/releases');
      return 1;
    }
    const dir = persistentDir(process.platform, process.env, os.homedir());
    const dest = doInstall(bundled, dir);
    const onPath = (process.env.PATH || '').split(path.delimiter).indexOf(dir) !== -1;
    console.log(`Installed mysql-cli to ${dest}`);
    if (!onPath) {
      console.log(`Add ${dir} to your PATH, then run: mysql-cli init`);
    } else {
      console.log('Next: run `mysql-cli init` to install agent skills.');
    }
    return 0;
  }
  if (!fs.existsSync(bundled)) {
    console.error(`mysql-cli binary not found at ${bundled}.`);
    console.error('Re-run `npm install`, or download manually from:');
    console.error('  https://github.com/AllenMuu/mysql-cli/releases');
    return 1;
  }
  const child = spawn(bundled, argv, { stdio: 'inherit' });
  child.on('error', (err) => {
    console.error(`mysql-cli: failed to spawn binary: ${err.message}`);
    process.exit(1);
  });
  child.on('close', (code) => process.exit(code == null ? 1 : code));
  return undefined; // exit handled by child 'close' handler
}

if (require.main === module) {
  const code = main(process.argv.slice(2));
  if (code !== undefined) process.exit(code);
}

module.exports = { bundledBinPath, persistentDir, doInstall, main };
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd dist/npm && node --test test/shim.test.js`
Expected: PASS,4 个测试通过。

- [ ] **Step 5: 跑全部 npm 测试**

Run: `cd dist/npm && node --test test/`
Expected: PASS(install + shim 共 8 个测试)。

- [ ] **Step 6: 提交**

```bash
git add dist/npm/bin/mysql-cli.js dist/npm/test/shim.test.js
git commit -m "feat(npm): add bin/mysql-cli.js shim (install command + binary exec)"
```

---

### Task 3: `.goreleaser.yml` 交叉编译配置

**Files:**
- Create: `.goreleaser.yml`

**Interfaces:**
- Produces:GitHub Release 产物 `mysql-cli_<os>_<arch>.tar.gz`(.zip for windows)、`checksums.txt`。
- Consumes:`./cmd/mysql-cli` 入口、仓库根 `LICENSE`。

- [ ] **Step 1: 创建 `.goreleaser.yml`**

```yaml
version: 2

project_name: mysql-cli

# Output to a separate dir so `--clean` never clobsters the committed
# dist/npm/ package source.
dist: .goreleaser-dist

builds:
  - id: mysql-cli
    main: ./cmd/mysql-cli
    binary: mysql-cli
    env:
      - CGO_ENABLED=0
    goos:
      - darwin
      - linux
      - windows
    goarch:
      - amd64
      - arm64
    ignore:
      - goos: windows
        goarch: arm64

archives:
  - id: default
    name: mysql-cli_{{.Os}}_{{.Arch}}
    format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
    files:
      - LICENSE*

checksum:
  name_template: 'checksums.txt'

changelog:
  use: github

release:
  github:
    owner: AllenMuu
    name: mysql-cli
```

- [ ] **Step 2: 安装 GoReleaser(若未装)**

Run: `which goreleaser || go install github.com/goreleaser/goreleaser/v2@latest`
Expected: `which goreleaser` 打印路径,或 `go install` 成功(二进制在 `$(go env GOPATH)/bin`)。若 `go install` 后 `which goreleaser` 仍空,把 `$(go env GOPATH)/bin` 加到 PATH。

- [ ] **Step 3: 校验配置**

Run: `goreleaser check`
Expected: 输出 `configuration is valid` 或同等成功信息,exit 0。

- [ ] **Step 4: snapshot 干跑(完整交叉编译验证)**

Run: `goreleaser snapshot --clean`
Expected: 成功构建 5 个目标(darwin_amd64/darwin_arm64/linux_amd64/linux_arm64/windows_amd64),`.goreleaser-dist/` 下生成归档与 checksums,exit 0。此步较慢(交叉编译),耐心等待。

- [ ] **Step 5: 确认归档命名与 install.js 期望一致**

Run: `ls .goreleaser-dist/ | grep -E 'mysql-cli_(darwin|linux|windows)_(amd64|arm64)'`
Expected: 列出 `mysql-cli_darwin_amd64.tar.gz`、`mysql-cli_darwin_arm64.tar.gz`、`mysql-cli_linux_amd64.tar.gz`、`mysql-cli_linux_arm64.tar.gz`、`mysql-cli_windows_amd64.zip`(命名与 Task 1 `mapAsset` 输出逐字一致)。

- [ ] **Step 6: 清理 snapshot 产物**

Run: `rm -rf .goreleaser-dist/`
Expected: 目录删除(`.goreleaser-dist/` 已在 `.gitignore`,不会被提交)。

- [ ] **Step 7: 提交**

```bash
git add .goreleaser.yml
git commit -m "ci: add goreleaser config for cross-platform binary releases"
```

---

### Task 4: `.github/workflows/release.yml` 发布工作流

**Files:**
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Produces:tag `v*` 触发 -> GoReleaser 发 GitHub Release;PR 触发 -> `goreleaser check` + `node --test`。
- Consumes:`.goreleaser.yml`(Task 3)、`dist/npm/`(Task 1/2)、secrets `NPM_TOKEN`、repo 变量 `NPM_PUBLISH`。

- [ ] **Step 1: 创建 `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags:
      - 'v*'
  pull_request:
    paths:
      - 'dist/npm/**'
      - '.goreleaser.yml'
      - '.github/workflows/release.yml'

permissions:
  contents: write

jobs:
  check:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - uses: actions/setup-node@v4
        with:
          node-version: '18'
      - name: GoReleaser config check
        uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: check
      - name: npm wrapper tests
        working-directory: dist/npm
        run: node --test test/

  release:
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  publish-npm:
    if: startsWith(github.ref, 'refs/tags/v') && vars.NPM_PUBLISH == 'true'
    needs: release
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '18'
          registry-url: 'https://registry.npmjs.org'
      - name: Set version from tag and publish
        working-directory: dist/npm
        run: |
          npm version "$(echo "${{ github.ref_name }}" | sed 's/^v//')" --no-git-tag-version
          npm publish --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

- [ ] **Step 2: 校验 YAML 合法**

Run: `node -e "const fs=require('fs');const s=require('yaml');const d=s.parse(fs.readFileSync('.github/workflows/release.yml','utf8'));console.log('jobs:',Object.keys(d.jobs).join(','))" 2>/dev/null || python3 -c "import yaml,sys; d=yaml.safe_load(open('.github/workflows/release.yml')); print('jobs:',','.join(d['jobs']))"`
Expected: 打印 `jobs: check,release,publish-npm`(若 `yaml` 模块缺失,用 python3;两者都没有则目测缩进)。

- [ ] **Step 3: 提交**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add release workflow (goreleaser on tag, npm publish gated by NPM_PUBLISH)"
```

---

### Task 5: README 加 npx 一键安装说明

**Files:**
- Modify: `README.md`(安装段)

**Interfaces:** 无;用户文档。

- [ ] **Step 1: 在 README 安装段加 npx 选项**

在 `README.md` 的「#### Install」节(现有 `Option 1 - go install (recommended):` 之前)插入一个新的推荐选项,并把原 `Option 1` 的 `(recommended)` 去掉、改为 `Option 2`。新内容:

```markdown
**Option 1 - `npx` (recommended, no Go toolchain needed):**

```bash
npx @allenmuu/mysql-cli install      # installs the prebuilt binary to ~/.local/bin
mysql-cli init                       # installs agent skills into detected agents
```

The `npx` command downloads the prebuilt binary for your platform from GitHub Releases. Set `MYSQL_CLI_MIRROR` to use a download mirror. Then run `mysql-cli init` to install skills.
```

(原 `Option 1 - go install (recommended)` 改为 `Option 2 - go install:`;其余内容不变。)

- [ ] **Step 2: 验证 README 渲染**

Run: 目测「#### Install」节,确认 Option 1(npx)/ Option 2(go install)顺序正确、代码围栏成对、markdown 合法。

- [ ] **Step 3: 提交**

```bash
git add README.md
git commit -m "docs(readme): add npx one-line install as recommended option"
```

---

## Self-Review

**1. Spec coverage:**
- spec 4.3 `package.json`(name/bin/postinstall/engines/files)-> Task 1 ✅
- spec 4.3 `install.js`(platform 映射、GitHub Releases 下载、解压到 ./bin、chmod、MYSQL_CLI_MIRROR、失败不致命)-> Task 1 ✅
- spec 4.3 `bin/mysql-cli.js`(install->复制持久位+PATH 提示、其他->spawn 透传、不改 rc)-> Task 2 ✅
- spec 4.3 「Go 二进制不加 install 子命令(YAGNI)」-> 未加(Plan 1/现有代码无 install 子命令)✅
- spec 4.4 `.goreleaser.yml`(CGO_ENABLED=0、5 targets、archive 命名、checksum、GitHub Releases)-> Task 3 ✅(windows/arm64 按 OUT 排除)
- spec 4.4 `.github/workflows/release.yml`(tag->GoReleaser、可选 npm publish with NPM_TOKEN)-> Task 4 ✅(用 NPM_PUBLISH 变量门控,匹配 spec「初期手动,稳定后自动化」)
- spec 4.4 「CI 自检 goreleaser check + snapshot」-> Task 3 Step 3/4(snapshot)+ Task 4 check job(check)✅
- spec 7 npm `install.js` 测试(fixture tarball + 平台/arch 映射矩阵 + node:test 零依赖)-> Task 1 Step 3 ✅
- spec 8 范围 IN:`dist/npm/` 三文件(+README)、`.goreleaser.yml`、release.yml、npm 发布、README 安装段-> 全覆盖 ✅
- spec 8 OUT(不加 Go install 子命令、不加 Windows arm64、不自动改 rc、不源码构建兜底)-> 均遵守 ✅
- 设计修正:GoReleaser `dist: .goreleaser-dist`(避开 `dist/npm/` 冲突)-> Global Constraints + Task 3 注释 ✅

**2. Placeholder scan:** 无 TBD/TODO;每步含完整代码与命令。`download`(网络)明确标注不单测,buildUrl/extractArchive 覆盖可测部分。✅

**3. Type/签名一致性:**
- `mapAsset`/`buildUrl`/`extractArchive`/`download` 在 Task 1 定义并 `module.exports`,Task 1 测试 `require('../install.js')` 取用一致。✅
- `bundledBinPath`/`persistentDir`/`doInstall`/`main` 在 Task 2 定义并导出,Task 2 测试取用一致;`main(argv, bundledDir)` 签名在实现与(隐式)调用一致。✅
- 归档命名 `mysql-cli_<goos>_<goarch>.{tar.gz|zip}`:Task 1 `mapAsset` 产出、Task 3 GoReleaser `archives.name: mysql-cli_{{.Os}}_{{.Arch}}`、Task 3 Step 5 校验--三者逐字一致(`{{.Os}}`=goos、`{{.Arch}}`=goarch)。✅
- `exeName`(`mysql-cli`/`mysql-cli.exe`):Task 1 install.js、Task 2 shim 一致。✅
- npm version 同步:Task 1 package.json `version: "0.0.0"`(占位)、Task 4 `npm version` 从 tag 注入、install.js `pkg.version` 读取--链路一致。✅

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-23-npx-init-plan-2-distro.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?

> 前置条件(执行前/首次发布前需用户配置):
> - npm 发布:在 npm 创建/拥有 `@allenmuu` scope(组织或账号),GitHub repo 加 `NPM_TOKEN` secret,并把 repo 变量 `NPM_PUBLISH` 设为 `true` 启用自动发布。否则首次发布只产出 GitHub Release,手动 `npm publish`。
> - 首次发布:打 tag `git tag v0.1.0 && git push origin v0.1.0` 触发 release.yml。
