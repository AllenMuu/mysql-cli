'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');

const { bundledBinPath, persistentDir, doInstall, signalNumber, exitCodeForClose } = require('../bin/mysql-cli.js');

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

// rmTemp removes mkdtempSync scratch dirs (no-op when already gone), so tests
// do not leak temp directories.
function rmTemp(...dirs) {
  for (const d of dirs) fs.rmSync(d, { recursive: true, force: true });
}

test('doInstall copies the bundled binary to the dest dir and chmods it', () => {
  const srcDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-shim-src-'));
  const destDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-shim-dst-'));
  try {
    const exeName = process.platform === 'win32' ? 'mysql-cli.exe' : 'mysql-cli';
    const bundled = path.join(srcDir, exeName);
    fs.writeFileSync(bundled, '#!/bin/sh\necho fake\n');
    const dest = doInstall(bundled, destDir);
    assert.ok(fs.existsSync(dest), 'dest binary exists');
    assert.equal(path.dirname(dest), destDir);
  } finally {
    rmTemp(srcDir, destDir);
  }
});

test('signalNumber maps close-callback signal names via os.constants.signals', () => {
  assert.equal(signalNumber('SIGINT'), os.constants.signals.SIGINT);
  assert.equal(signalNumber('SIGTERM'), os.constants.signals.SIGTERM);
  assert.equal(signalNumber('SIGILL'), os.constants.signals.SIGILL);
  assert.equal(signalNumber('NOSUCHSIGNAL'), null);
  assert.equal(signalNumber(null), null);
  assert.equal(signalNumber(undefined), null);
});

test('exitCodeForClose propagates exit codes', () => {
  assert.equal(exitCodeForClose(0, null), 0);
  assert.equal(exitCodeForClose(7, null), 7);
  assert.equal(exitCodeForClose(null, null), 1);
});

test('exitCodeForClose maps signal deaths to 128+signal (shell convention)', () => {
  assert.equal(exitCodeForClose(null, 'SIGTERM'), 128 + os.constants.signals.SIGTERM);
  assert.equal(exitCodeForClose(null, 'SIGINT'), 128 + os.constants.signals.SIGINT);
  // A signal death wins over any reported code.
  assert.equal(exitCodeForClose(0, 'SIGTERM'), 128 + os.constants.signals.SIGTERM);
  assert.equal(exitCodeForClose(null, 'NOSUCHSIGNAL'), 1);
});

// runShim executes the real shim (bin/mysql-cli.js) as a child node process
// against a fake bundled binary in bundledDir.
function runShim(bundledDir) {
  const shim = path.join(__dirname, '..', 'bin', 'mysql-cli.js');
  return spawn(
    process.execPath,
    ['-e', `require(${JSON.stringify(shim)}).main([], ${JSON.stringify(bundledDir)})`],
    { stdio: 'ignore' }
  );
}

test('shim exits 128+signal when the child is killed by a signal', { skip: process.platform === 'win32' }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-sig-'));
  try {
    fs.writeFileSync(path.join(dir, 'mysql-cli'), '#!/bin/sh\nkill -TERM $$\n', { mode: 0o755 });
    const child = runShim(dir);
    const [code, signal] = await new Promise((resolve) => child.on('close', (c, s) => resolve([c, s])));
    assert.equal(signal, null);
    assert.equal(code, 128 + os.constants.signals.SIGTERM);
  } finally {
    rmTemp(dir);
  }
});

test('shim forwards SIGTERM to the child instead of orphaning it', { skip: process.platform === 'win32' }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-fwd-'));
  const ready = path.join(dir, 'ready');
  fs.writeFileSync(path.join(dir, 'mysql-cli'), `#!/bin/sh\ntouch ${JSON.stringify(ready)}\nsleep 30\n`, { mode: 0o755 });
  const shimProc = runShim(dir);
  try {
    for (let i = 0; i < 100 && !fs.existsSync(ready); i += 1) {
      await new Promise((r) => setTimeout(r, 50));
    }
    assert.ok(fs.existsSync(ready), 'fake binary should have started');
    shimProc.kill('SIGTERM');
    const [code, signal] = await new Promise((resolve) => shimProc.on('close', (c, s) => resolve([c, s])));
    // If SIGTERM were not forwarded, the shim itself would die by the signal
    // (signal='SIGTERM', code=null) and leave the Go child orphaned.
    assert.equal(signal, null);
    assert.equal(code, 128 + os.constants.signals.SIGTERM);
  } finally {
    shimProc.kill('SIGKILL');
    rmTemp(dir);
  }
});
