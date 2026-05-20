'use strict';

const https   = require('node:https');
const fs      = require('node:fs');
const path    = require('node:path');
const crypto  = require('node:crypto');

const GITHUB_REPO = 'bcdock/cli';
const BINARY      = 'bcdock';
const PKG_ROOT    = __dirname;
const BIN_DIR     = path.join(PKG_ROOT, 'bin');
const VERSION_FILE = path.join(BIN_DIR, '.version');

const { version } = JSON.parse(fs.readFileSync(path.join(PKG_ROOT, 'package.json'), 'utf8'));

const BASE_URL = process.env.BCDOCK_RELEASE_BASE_URL ||
  `https://github.com/${GITHUB_REPO}/releases/download/v${version}`;

// Env-var overrides for test/CI: set BCDOCK_TEST_PLATFORM / BCDOCK_TEST_ARCH
// before requiring this module to exercise different platform paths.
function archiveInfo(platform, arch) {
  platform = platform || process.env.BCDOCK_TEST_PLATFORM || process.platform;
  arch     = arch     || process.env.BCDOCK_TEST_ARCH     || process.arch;

  const OS_MAP   = { linux: 'linux', darwin: 'macos', win32: 'windows' };
  const ARCH_MAP = { x64: 'x86_64', arm64: 'arm64' };

  const osName   = OS_MAP[platform];
  const archName = ARCH_MAP[arch];

  if (!osName || !archName) {
    throw new Error(
      `Unsupported platform ${platform}/${arch}. ` +
      `Install manually: https://github.com/${GITHUB_REPO}/releases`
    );
  }
  if (platform === 'win32' && arch === 'arm64') {
    throw new Error(
      `Windows arm64 is not supported. ` +
      `Install manually: https://github.com/${GITHUB_REPO}/releases`
    );
  }

  const fmt      = platform === 'win32' ? 'zip' : 'tar.gz';
  const binFile  = platform === 'win32' ? `${BINARY}.exe` : BINARY;
  // destFile is where we write on disk. On Windows same as binFile (.exe).
  // On Unix we use -native suffix so bin/bcdock can be the JS shim launcher.
  const destFile = platform === 'win32' ? `${BINARY}.exe` : `${BINARY}-native`;
  const archive  = `${BINARY}_${version}_${osName}_${archName}.${fmt}`;

  return { archive, binFile, fmt, destFile };
}

function fetch(url) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    const req = https.get(
      url,
      { headers: { 'User-Agent': `@bcdock/cli-installer/${version}` } },
      res => {
        if (res.statusCode === 301 || res.statusCode === 302) {
          return resolve(fetch(res.headers.location));
        }
        if (res.statusCode !== 200) {
          return reject(new Error(`HTTP ${res.statusCode} fetching ${url}`));
        }
        res.on('data', c => chunks.push(c));
        res.on('end', () => resolve(Buffer.concat(chunks)));
        res.on('error', reject);
      }
    );
    req.on('error', reject);
  });
}

function verifyChecksum(buf, filename, checksumsText) {
  const line = checksumsText
    .split('\n')
    .find(l => l.endsWith(`  ${filename}`) || l.endsWith(`\t${filename}`));
  if (!line) {
    throw new Error(`${filename} not found in checksums.txt`);
  }
  const expected = line.split(/\s+/)[0];
  const actual   = crypto.createHash('sha256').update(buf).digest('hex');
  if (actual !== expected) {
    throw new Error(`SHA-256 mismatch for ${filename}: expected ${expected}, got ${actual}`);
  }
}

async function extractBinary(archiveBuf, binFile, fmt, destPath) {
  const tmpArchive = destPath + '.tmp';
  fs.writeFileSync(tmpArchive, archiveBuf);
  try {
    if (fmt === 'tar.gz') {
      const tar = require('tar');
      // Try strip:1 first (goreleaser wraps archive in a directory).
      // strip:1 skips root-level entries (components <= strip), so if binary
      // sits at root we fall through to the strip:0 attempt.
      await tar.x({
        file: tmpArchive,
        cwd: BIN_DIR,
        strip: 1,
        filter: p => path.basename(p) === binFile,
      });
      if (!fs.existsSync(destPath)) {
        // Binary is at archive root - no wrapping directory.
        await tar.x({
          file: tmpArchive,
          cwd: BIN_DIR,
          strip: 0,
          filter: p => p === binFile,
        });
      }
    } else {
      // Windows zip
      const AdmZip = require('adm-zip');
      const zip    = new AdmZip(archiveBuf);
      const entry  = zip.getEntries().find(e => path.basename(e.entryName) === binFile);
      if (!entry) throw new Error(`${binFile} not found in zip`);
      fs.writeFileSync(destPath, zip.readFile(entry));
    }
  } finally {
    try { fs.unlinkSync(tmpArchive); } catch (_) {}
  }
  if (!fs.existsSync(destPath)) {
    throw new Error(`Extraction failed: ${binFile} not found after extract`);
  }
}

async function main() {
  const { archive, binFile, fmt, destFile } = archiveInfo();
  const destPath = path.join(BIN_DIR, destFile);

  // Fast path: already on this version.
  if (
    fs.existsSync(destPath) &&
    fs.existsSync(VERSION_FILE) &&
    fs.readFileSync(VERSION_FILE, 'utf8').trim() === version
  ) {
    process.stdout.write(`bcdock ${version} already installed.\n`);
    return;
  }

  process.stdout.write(`Installing bcdock ${version}...\n`);
  fs.mkdirSync(BIN_DIR, { recursive: true });

  const [archiveBuf, checksumsBuf] = await Promise.all([
    fetch(`${BASE_URL}/${archive}`),
    fetch(`${BASE_URL}/checksums.txt`),
  ]);

  verifyChecksum(archiveBuf, archive, checksumsBuf.toString('utf8'));
  await extractBinary(archiveBuf, binFile, fmt, destPath);

  if (process.platform !== 'win32') fs.chmodSync(destPath, 0o755);
  fs.writeFileSync(VERSION_FILE, version, 'utf8');
  process.stdout.write(`bcdock ${version} installed.\n`);
}

if (require.main === module) {
  main().catch(err => {
    const destFile = process.platform === 'win32' ? `${BINARY}.exe` : `${BINARY}-native`;
    const destPath = path.join(BIN_DIR, destFile);
    try { if (fs.existsSync(destPath)) fs.unlinkSync(destPath); } catch (_) {}
    try { if (fs.existsSync(VERSION_FILE)) fs.unlinkSync(VERSION_FILE); } catch (_) {}
    process.stderr.write(`bcdock install failed: ${err.message}\n`);
    process.exit(1);
  });
}

module.exports = { archiveInfo, verifyChecksum };
