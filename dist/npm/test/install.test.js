'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { execFileSync } = require('node:child_process');

const {
  mapAsset,
  buildUrl,
  extractArchive,
  proxyUrlFor,
  connectViaProxy,
  parseChecksums,
  sha256hex,
  verifyChecksum,
  installBinaryFromArchive,
} = require('../install.js');

test('mapAsset maps supported platforms to GoReleaser assets', () => {
  assert.deepEqual(mapAsset('darwin', 'x64'), { goos: 'darwin', goarch: 'amd64', ext: 'tar.gz', asset: 'mysql-cli_darwin_amd64.tar.gz' });
  assert.deepEqual(mapAsset('darwin', 'arm64'), { goos: 'darwin', goarch: 'arm64', ext: 'tar.gz', asset: 'mysql-cli_darwin_arm64.tar.gz' });
  assert.deepEqual(mapAsset('linux', 'x64'), { goos: 'linux', goarch: 'amd64', ext: 'tar.gz', asset: 'mysql-cli_linux_amd64.tar.gz' });
  assert.deepEqual(mapAsset('linux', 'arm64'), { goos: 'linux', goarch: 'arm64', ext: 'tar.gz', asset: 'mysql-cli_linux_arm64.tar.gz' });
  assert.deepEqual(mapAsset('win32', 'x64'), { goos: 'windows', goarch: 'amd64', ext: 'zip', asset: 'mysql-cli_windows_amd64.zip' });
});

test('mapAsset falls back to the amd64 asset on windows/arm64', () => {
  // GoReleaser never builds windows/arm64 (ignored in .goreleaser.yml), so it
  // must map to the amd64 asset (Windows 11 ARM64 runs it via x64 emulation).
  assert.deepEqual(mapAsset('win32', 'arm64'), { goos: 'windows', goarch: 'amd64', ext: 'zip', asset: 'mysql-cli_windows_amd64.zip' });
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

test('buildUrl constructs the checksums.txt URL from the same base as the asset', () => {
  // checksums.txt must come from the same base URL (mirror included) for the
  // integrity check to cover mirrored downloads.
  assert.equal(
    buildUrl('1.2.3', 'checksums.txt', 'https://mirror.example.com/dl'),
    'https://mirror.example.com/dl/v1.2.3/checksums.txt'
  );
});

test('parseChecksums extracts the hash for the matching asset', () => {
  const content = [
    '1111111111111111111111111111111111111111111111111111111111111111  mysql-cli_darwin_amd64.tar.gz',
    '2222222222222222222222222222222222222222222222222222222222222222  mysql-cli_windows_amd64.zip',
    'garbage line without columns',
  ].join('\n');
  assert.equal(parseChecksums(content, 'mysql-cli_windows_amd64.zip'), '2222222222222222222222222222222222222222222222222222222222222222');
  assert.equal(parseChecksums(content, 'mysql-cli_linux_amd64.tar.gz'), null);
  assert.equal(parseChecksums('', 'mysql-cli_windows_amd64.zip'), null);
});

test('sha256hex computes the sha256 hex digest', () => {
  // Known vector: sha256("hello\n").
  assert.equal(sha256hex(Buffer.from('hello\n')), '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03');
});

test('verifyChecksum passes on match and throws a checksumMismatch error otherwise', () => {
  const buf = Buffer.from('hello\n');
  const good = '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03';
  verifyChecksum(buf, good, 'mysql-cli_darwin_amd64.tar.gz'); // must not throw
  assert.throws(
    () => verifyChecksum(buf, 'deadbeef', 'mysql-cli_darwin_amd64.tar.gz'),
    (err) => {
      assert.match(err.message, /checksum mismatch for mysql-cli_darwin_amd64\.tar\.gz/);
      assert.equal(err.checksumMismatch, true);
      return true;
    }
  );
});

test('proxyUrlFor reads the four proxy env vars and defaults the scheme', () => {
  assert.equal(proxyUrlFor({}), null);
  assert.equal(proxyUrlFor({ HTTPS_PROXY: 'http://p:8080' }), 'http://p:8080');
  assert.equal(proxyUrlFor({ https_proxy: 'http://p:8080' }), 'http://p:8080');
  assert.equal(proxyUrlFor({ HTTP_PROXY: 'http://p:8080' }), 'http://p:8080');
  assert.equal(proxyUrlFor({ http_proxy: 'http://p:8080' }), 'http://p:8080');
  // HTTPS_PROXY wins over HTTP_PROXY.
  assert.equal(proxyUrlFor({ HTTPS_PROXY: 'http://a:1', HTTP_PROXY: 'http://b:2' }), 'http://a:1');
  // Scheme-less values are treated as http:// (curl-style).
  assert.equal(proxyUrlFor({ HTTP_PROXY: 'p:8080' }), 'http://p:8080');
});

// startProxy spins up a local HTTP proxy stub and invokes onConnect for each
// CONNECT request.
function startProxy(onConnect) {
  const server = http.createServer();
  server.on('connect', onConnect);
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

function closeServer(server) {
  return new Promise((resolve) => {
    server.closeAllConnections?.();
    server.close(resolve);
  });
}

test('connectViaProxy establishes a CONNECT tunnel through an HTTP proxy', async () => {
  let sawTarget = null;
  const server = await startProxy((req, socket) => {
    sawTarget = req.url;
    // Write the CONNECT response, then half-close the server side so the
    // stub proxy shuts down deterministically once the test is done.
    socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
    socket.end();
  });
  try {
    const socket = await connectViaProxy(`http://127.0.0.1:${server.address().port}`, 'example.com', 443);
    assert.ok(socket, 'tunnel socket returned');
    assert.equal(sawTarget, 'example.com:443');
    socket.destroy();
  } finally {
    await closeServer(server);
  }
});

test('connectViaProxy rejects when the proxy denies CONNECT', async () => {
  const server = await startProxy((req, socket) => {
    socket.write('HTTP/1.1 403 Forbidden\r\n\r\n');
    socket.end();
  });
  try {
    await assert.rejects(
      connectViaProxy(`http://127.0.0.1:${server.address().port}`, 'example.com', 443, 2000),
      /403/
    );
  } finally {
    await closeServer(server);
  }
});

// rmTemp removes mkdtempSync scratch dirs (no-op when already gone), so tests
// do not leak temp directories.
function rmTemp(...dirs) {
  for (const d of dirs) fs.rmSync(d, { recursive: true, force: true });
}

test('extractArchive extracts a tar.gz fixture into outDir', () => {
  const srcDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-src-'));
  const archiveDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-arc-'));
  const extractDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-ext-'));
  try {
    fs.writeFileSync(path.join(srcDir, 'mysql-cli'), '#!/bin/sh\necho fake\n');
    const archive = path.join(archiveDir, 'fixture.tar.gz');
    execFileSync('tar', ['-czf', archive, '-C', srcDir, 'mysql-cli']);
    extractArchive(archive, extractDir);
    assert.ok(fs.existsSync(path.join(extractDir, 'mysql-cli')), 'binary should be extracted');
  } finally {
    rmTemp(srcDir, archiveDir, extractDir);
  }
});

test('installBinaryFromArchive extracts only the binary into outDir', () => {
  const workDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-d9-work-'));
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mc-d9-out-'));
  try {
    const srcDir = path.join(workDir, 'src');
    fs.mkdirSync(srcDir);
    fs.writeFileSync(path.join(srcDir, 'mysql-cli'), '#!/bin/sh\necho fake\n');
    fs.writeFileSync(path.join(srcDir, 'LICENSE'), 'MIT\n');
    const archive = path.join(workDir, 'fixture.tar.gz');
    execFileSync('tar', ['-czf', archive, '-C', srcDir, 'mysql-cli', 'LICENSE']);
    const mapped = { goos: 'linux', goarch: 'amd64', ext: 'tar.gz', asset: 'mysql-cli_linux_amd64.tar.gz' };
    const binPath = installBinaryFromArchive(fs.readFileSync(archive), mapped, outDir);
    assert.equal(binPath, path.join(outDir, 'mysql-cli'));
    assert.ok(fs.existsSync(binPath), 'binary should be installed');
    // Only the binary lands in outDir: no LICENSE leak, no leftover temp dir.
    assert.deepEqual(fs.readdirSync(outDir).sort(), ['mysql-cli']);
    if (process.platform !== 'win32') {
      assert.ok(fs.statSync(binPath).mode & 0o111, 'binary should be executable');
    }
  } finally {
    rmTemp(workDir, outDir);
  }
});
