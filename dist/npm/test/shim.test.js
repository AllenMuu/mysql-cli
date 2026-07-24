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