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