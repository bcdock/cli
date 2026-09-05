# @bcdock/cli

npm wrapper for the [bcdock CLI](https://github.com/bcdock/cli). The postinstall
script downloads the matching native binary from GitHub Releases (`bin/bcdock-native`
on Linux/macOS, `bin/bcdock.exe` on Windows). A small JS launcher shim at `bin/bcdock`
handles the cross-platform dispatch - Node.js starts, hands off to the native binary,
and exits. The native binary does the actual work.

## Install

```
npm install -g @bcdock/cli
bcdock --version
```

## Note on --ignore-scripts

If you run `npm install --ignore-scripts`, the postinstall script is skipped and
the binary will not be downloaded. Use the [install script](https://cli.bcdock.io/install.sh)
or a manual download from [releases](https://github.com/bcdock/cli/releases) instead.

## Source

Full source and docs: [github.com/bcdock/cli](https://github.com/bcdock/cli)
