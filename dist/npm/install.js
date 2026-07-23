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