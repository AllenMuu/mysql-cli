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
    const j = path.win32.join;
    return j(env.LOCALAPPDATA || j(home, 'AppData', 'Local'), 'mysql-cli');
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