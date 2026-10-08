'use strict';

// Unit tests for install.js helpers. No network calls.
// Run with: node test.js

const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const { archiveInfo, verifyChecksum } = require('./install.js');

let passed = 0;
let failed = 0;

function test(name, fn) {
  try {
    fn();
    process.stdout.write(`  ok: ${name}\n`);
    passed++;
  } catch (err) {
    process.stderr.write(`  FAIL: ${name}\n    ${err.message}\n`);
    failed++;
  }
}

// Archive name generation
test('linux/x64 -> bcdock_VERSION_linux_x86_64.tar.gz', () => {
  const { archive, binFile, fmt } = archiveInfo('linux', 'x64');
  assert.match(archive, /^bcdock_.+_linux_x86_64\.tar\.gz$/);
  assert.equal(binFile, 'bcdock');
  assert.equal(fmt, 'tar.gz');
});

test('linux/arm64 -> bcdock_VERSION_linux_arm64.tar.gz', () => {
  const { archive } = archiveInfo('linux', 'arm64');
  assert.match(archive, /^bcdock_.+_linux_arm64\.tar\.gz$/);
});

test('darwin/x64 -> bcdock_VERSION_macos_x86_64.tar.gz', () => {
  const { archive } = archiveInfo('darwin', 'x64');
  assert.match(archive, /^bcdock_.+_macos_x86_64\.tar\.gz$/);
});

test('darwin/arm64 -> bcdock_VERSION_macos_arm64.tar.gz', () => {
  const { archive } = archiveInfo('darwin', 'arm64');
  assert.match(archive, /^bcdock_.+_macos_arm64\.tar\.gz$/);
});

test('win32/x64 -> bcdock_VERSION_windows_x86_64.zip + bcdock.exe', () => {
  const { archive, binFile, fmt, destFile } = archiveInfo('win32', 'x64');
  assert.match(archive, /^bcdock_.+_windows_x86_64\.zip$/);
  assert.equal(binFile, 'bcdock.exe');
  assert.equal(fmt, 'zip');
  assert.equal(destFile, 'bcdock.exe');
});

test('linux/x64 destFile is bcdock-native (keeps bin/bcdock free for JS shim)', () => {
  const { destFile } = archiveInfo('linux', 'x64');
  assert.equal(destFile, 'bcdock-native');
});

test('darwin/arm64 destFile is bcdock-native', () => {
  const { destFile } = archiveInfo('darwin', 'arm64');
  assert.equal(destFile, 'bcdock-native');
});

test('unsupported platform throws', () => {
  assert.throws(() => archiveInfo('freebsd', 'x64'), /Unsupported/);
});

test('unsupported arch throws', () => {
  assert.throws(() => archiveInfo('linux', 'ia32'), /Unsupported/);
});

test('win32/arm64 throws (not built by goreleaser)', () => {
  assert.throws(() => archiveInfo('win32', 'arm64'), /not supported/);
});

// Checksum verification (no network, no file I/O)
test('verifyChecksum passes on correct hash', () => {
  const buf  = Buffer.from('fake binary content for testing');
  const hash = crypto.createHash('sha256').update(buf).digest('hex');
  verifyChecksum(buf, 'bcdock_0.0.1_linux_x86_64.tar.gz',
    `${hash}  bcdock_0.0.1_linux_x86_64.tar.gz\n`);
});

test('verifyChecksum throws on hash mismatch', () => {
  const buf = Buffer.from('fake binary content for testing');
  const bad = 'a'.repeat(64);
  assert.throws(
    () => verifyChecksum(buf, 'bcdock_0.0.1_linux_x86_64.tar.gz',
      `${bad}  bcdock_0.0.1_linux_x86_64.tar.gz\n`),
    /mismatch/
  );
});

test('verifyChecksum throws when filename missing from checksums', () => {
  const buf = Buffer.from('x');
  assert.throws(
    () => verifyChecksum(buf, 'bcdock_0.0.1_linux_x86_64.tar.gz', 'nothing here\n'),
    /not found in checksums/
  );
});

// Summary
process.stdout.write(`\n${passed + failed} tests: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
