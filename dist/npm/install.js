#!/usr/bin/env node
'use strict';

const https = require('node:https');
const http = require('node:http');
const tls = require('node:tls');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const pkg = require('./package.json');

// mapAsset maps Node's platform/arch to the GoReleaser archive asset.
// Returns { goos, goarch, ext, asset } or null if unsupported.
function mapAsset(platform, arch) {
  const goos = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[platform];
  let goarch = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!goos || !goarch) return null;
  // GoReleaser never builds windows/arm64 (.goreleaser.yml ignores it), so
  // that asset 404s. Windows 11 ARM64 runs the amd64 binary via x64
  // emulation, so fall back to the amd64 asset.
  if (goos === 'windows' && goarch === 'arm64') goarch = 'amd64';
  const ext = goos === 'windows' ? 'zip' : 'tar.gz';
  const asset = `mysql-cli_${goos}_${goarch}.${ext}`;
  return { goos, goarch, ext, asset };
}

// buildUrl constructs the release download URL for an asset. checksums.txt is
// just another release asset, so it shares this URL builder (same base URL,
// which matters for MYSQL_CLI_MIRROR integrity checking).
function buildUrl(version, asset, mirror) {
  const base = mirror || 'https://github.com/AllenMuu/mysql-cli/releases/download';
  return `${base}/v${version}/${asset}`;
}

// extractArchive extracts archivePath into outDir using the system `tar`
// (handles .tar.gz on unix; .zip on Windows 10+ bsdtar).
function extractArchive(archivePath, outDir) {
  execFileSync('tar', ['-xf', archivePath, '-C', outDir], { stdio: 'pipe' });
}

// proxyUrlFor returns the proxy URL to use for https requests, or null.
// Node's https module never reads HTTPS_PROXY/HTTP_PROXY on its own, so
// postinstall would fail behind corporate proxies without this.
function proxyUrlFor(env = process.env) {
  const raw = env.HTTPS_PROXY || env.https_proxy || env.HTTP_PROXY || env.http_proxy;
  if (!raw) return null;
  return raw.includes('://') ? raw : `http://${raw}`;
}

// connectViaProxy opens an HTTP CONNECT tunnel through an http:// proxy and
// resolves with the raw tunneled socket. https:// proxies are not supported
// (http:// covers the env-var proxy ecosystem; https:// fails loudly instead
// of silently misbehaving).
function connectViaProxy(proxyUrl, host, port, timeoutMs = 60000) {
  return new Promise((resolve, reject) => {
    const proxy = new URL(proxyUrl);
    if (proxy.protocol !== 'http:') {
      reject(new Error(`unsupported proxy protocol ${proxy.protocol} (only http:// proxies are supported)`));
      return;
    }
    const req = http.request({
      protocol: proxy.protocol,
      hostname: proxy.hostname,
      port: Number(proxy.port) || 80,
      method: 'CONNECT',
      path: `${host}:${port}`,
      headers: { Host: `${host}:${port}` },
    });
    req.setTimeout(timeoutMs, () => req.destroy(new Error(`proxy CONNECT timed out after ${Math.round(timeoutMs / 1000)}s`)));
    req.on('connect', (res, socket) => {
      if (res.statusCode !== 200) {
        socket.destroy();
        reject(new Error(`proxy CONNECT to ${host}:${port} failed: HTTP ${res.statusCode}`));
        return;
      }
      resolve(socket);
    });
    req.on('error', reject);
    req.end();
  });
}

// httpsGet issues one https GET and resolves with the response stream.
function httpsGet(url, opts = {}) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, opts, resolve);
    req.setTimeout(60000, () => req.destroy(new Error('download timed out after 60s')));
    req.on('error', reject);
  });
}

// fetchResponse GETs url directly, or through an HTTP CONNECT proxy when
// proxy env vars are set (Node never reads them by itself).
async function fetchResponse(url) {
  const proxyUrl = proxyUrlFor();
  if (!proxyUrl) return httpsGet(url);
  const target = new URL(url);
  const tunnel = await connectViaProxy(proxyUrl, target.hostname, Number(target.port) || 443);
  // TLS-wrap the tunnel and hand the socket to https via a one-shot agent.
  // servername keeps SNI + certificate verification against the target host.
  const agent = new https.Agent({ keepAlive: false });
  agent.createConnection = () => tls.connect({ socket: tunnel, servername: target.hostname });
  return httpsGet(url, { agent });
}

// download fetches a URL buffer, following up to 5 redirects (GitHub Releases
// redirects to S3). Not unit-tested (network); buildUrl/extractArchive are.
async function download(url) {
  let currentUrl = url;
  for (let redirects = 0; ; redirects += 1) {
    const res = await fetchResponse(currentUrl);
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
      if (redirects >= 5) {
        res.resume();
        throw new Error('too many redirects');
      }
      currentUrl = new URL(res.headers.location, currentUrl).href;
      res.resume();
      continue;
    }
    if (res.statusCode !== 200) {
      res.resume();
      throw new Error(`HTTP ${res.statusCode} for ${currentUrl}`);
    }
    return new Promise((resolve, reject) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
    });
  }
}

// parseChecksums returns the expected sha256 hex digest for asset from
// goreleaser checksums.txt content ("<hash>  <asset>" per line), or null.
function parseChecksums(content, asset) {
  for (const line of content.split('\n')) {
    const parts = line.trim().split(/\s+/);
    if (parts.length >= 2 && parts[1] === asset) return parts[0];
  }
  return null;
}

// sha256hex returns the sha256 hex digest of buf.
function sha256hex(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

// verifyChecksum throws (with .checksumMismatch = true) when buf does not
// match the expected sha256, so callers treat it as a hard integrity failure
// rather than a best-effort download problem.
function verifyChecksum(buf, expected, asset) {
  const actual = sha256hex(buf);
  if (actual !== expected) {
    const err = new Error(`checksum mismatch for ${asset}: expected ${expected}, got ${actual} (download may be corrupted or tampered with)`);
    err.checksumMismatch = true;
    throw err;
  }
}

// installBinaryFromArchive extracts the downloaded archive inside a temp
// dir under outDir and copies ONLY the binary into outDir. Other archive
// members (LICENSE) are discarded with the temp dir, keeping bin/ clean for
// git (previously LICENSE leaked into bin/ as untracked files).
function installBinaryFromArchive(buf, mapped, outDir) {
  const exeName = mapped.goos === 'windows' ? 'mysql-cli.exe' : 'mysql-cli';
  const binPath = path.join(outDir, exeName);
  const workDir = fs.mkdtempSync(path.join(outDir, 'postinstall-'));
  try {
    const archivePath = path.join(workDir, mapped.asset);
    fs.writeFileSync(archivePath, buf);
    extractArchive(archivePath, workDir);
    const extracted = path.join(workDir, exeName);
    if (!fs.existsSync(extracted)) throw new Error('binary not found after extraction');
    fs.copyFileSync(extracted, binPath);
    if (mapped.goos !== 'windows') fs.chmodSync(binPath, 0o755);
  } finally {
    fs.rmSync(workDir, { recursive: true, force: true });
  }
  return binPath;
}

async function run() {
  const outDir = path.join(__dirname, 'bin');
  fs.mkdirSync(outDir, { recursive: true });
  const mapped = mapAsset(process.platform, process.arch);
  if (!mapped) {
    console.warn(`mysql-cli: unsupported platform ${process.platform}/${process.arch}; skipping binary download.`);
    return;
  }
  const url = buildUrl(pkg.version, mapped.asset, process.env.MYSQL_CLI_MIRROR);
  try {
    console.log(`mysql-cli: downloading ${mapped.asset}`);
    const buf = await download(url);
    // Integrity check against goreleaser's checksums.txt from the same base
    // URL (this closes the supply-chain gap for MYSQL_CLI_MIRROR). If
    // checksums.txt cannot be fetched (e.g. a mirror that has not synced it),
    // warn and continue, for backward compatibility.
    let expected = null;
    try {
      const sums = await download(buildUrl(pkg.version, 'checksums.txt', process.env.MYSQL_CLI_MIRROR));
      expected = parseChecksums(sums.toString('utf8'), mapped.asset);
      if (!expected) console.warn(`mysql-cli: no checksums.txt entry for ${mapped.asset}; skipping integrity check`);
    } catch (err) {
      console.warn(`mysql-cli: checksums.txt unavailable (${err.message}); skipping integrity check`);
    }
    if (expected) {
      verifyChecksum(buf, expected, mapped.asset);
      console.log('mysql-cli: checksum verified');
    }
    const binPath = installBinaryFromArchive(buf, mapped, outDir);
    console.log(`mysql-cli: installed binary to ${binPath}`);
  } catch (err) {
    if (err && err.checksumMismatch) throw err; // integrity failure: abort install
    // Non-fatal: leave bin/ without the binary; the shim prints guidance.
    console.warn(`mysql-cli: could not install binary (${err.message}).`);
    console.warn(`Download manually: ${url}`);
    const proxyUrl = proxyUrlFor();
    if (proxyUrl) {
      console.warn(`mysql-cli: a proxy is in effect (${proxyUrl}); check it is reachable and allows CONNECT, or unset HTTPS_PROXY/HTTP_PROXY to download directly.`);
    }
  }
}

if (require.main === module) {
  run().catch((err) => {
    if (err && err.checksumMismatch) {
      // Tampered/corrupted download: fail the install loudly.
      console.error(`mysql-cli: postinstall failed: ${err.message}`);
      process.exit(1);
    }
    console.warn(`mysql-cli: postinstall error: ${err.message}`);
    // exit 0 - never fail npm install
  });
}

module.exports = {
  mapAsset,
  buildUrl,
  extractArchive,
  download,
  proxyUrlFor,
  connectViaProxy,
  parseChecksums,
  sha256hex,
  verifyChecksum,
  installBinaryFromArchive,
};
