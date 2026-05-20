'use strict';

const fs   = require('node:fs');
const path = require('node:path');

const BIN_DIR = path.join(__dirname, 'bin');

// Defensive cleanup on npm uninstall. npm removes node_modules anyway, but
// this ensures the binary and version marker are gone if someone uninstalls
// while the package lives outside node_modules (e.g. global install).
for (const name of ['bcdock-native', 'bcdock.exe', '.version']) {
  const target = path.join(BIN_DIR, name);
  try {
    if (fs.existsSync(target)) fs.unlinkSync(target);
  } catch (_) {}
}
