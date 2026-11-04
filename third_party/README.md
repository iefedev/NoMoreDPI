# Vendored third-party code

Nothing in this folder is modified. Each part can be checked against its upstream source.

## `byedpi-bin/ciadpi.exe`: byedpi 0.17.3, official release binary

- Upstream: <https://github.com/hufrea/byedpi>, release
  [v0.17.3](https://github.com/hufrea/byedpi/releases/tag/v0.17.3), tag commit
  `7efde1b1296eaaa187b70e951894dde17527489c`.
- Taken unchanged from the release asset `byedpi-17.3-x86_64-w64.zip`
  (SHA-256 `70d2c94147193cb915f9c6eb5144b8d404dacbcfa90bda2383b6b211afafa456`, the digest
  GitHub shows for that asset). The archive's `ciadpi.exe` and this file are the same bytes:
  SHA-256 `eb53ceeeb981cc6735ac24bb1e51e725280b86630e80fdf19ddc4ee4a5b54ef4`, 129,024 bytes,
  not code-signed (upstream does not sign it).
- It was built by the byedpi project, not by No More DPI. `ciadpi.exe --version` prints `17.3`.
- License: MIT, `byedpi-bin/LICENSE` (identical to `byedpi/LICENSE`).

## `byedpi/`: byedpi 0.17.3 source

- The files of tag `v0.17.3` (commit `7efde1b1296eaaa187b70e951894dde17527489c`), unchanged;
  upstream's `dist/` folder (Linux/BSD/Docker service files and Windows batch files) is left
  out. Every file's git blob hash equals the one in the upstream tree at that commit.
- It is here so that `ciadpi.exe` can be rebuilt from source:
  `./build.ps1 -CiadpiFromSource` compiles it with MinGW-w64 GCC using the flags of upstream's
  `Makefile` (`make windows`). A local build is not byte-identical to the release binary
  (compiler version and build environment differ); the default build ships the release binary.

## `wintun/wintun.dll`: Wintun 0.14.1, official signed DLL

- Upstream: <https://www.wintun.net/>, archive
  [wintun-0.14.1.zip](https://www.wintun.net/builds/wintun-0.14.1.zip)
  (SHA-256 `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`, as published on
  wintun.net), file `wintun/bin/amd64/wintun.dll`, unchanged.
- SHA-256 `e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce`, Authenticode
  signed by WireGuard LLC.
- License: Wintun Prebuilt Binaries License, `wintun/LICENSE.txt` (copied from the archive).

## How this is checked

- `third_party/upstream.json` pins the hashes above.
- `build.ps1` refuses to build if a vendored binary does not match its pin.
- `tools/verify-third-party.ps1` downloads both upstream archives, checks their SHA-256 and
  compares the files inside byte for byte with the vendored copies. CI runs it on every push,
  pull request and release.
- To check the source folder yourself:
  `git clone --branch v0.17.3 https://github.com/hufrea/byedpi` and compare with
  `git diff --no-index --ignore-cr-at-eol byedpi/ <clone>/` (only `dist/` and `.git/` differ).
