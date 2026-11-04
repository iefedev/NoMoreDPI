# Third-party notices

No More DPI ships or links the following software, each under its own license. The full
license texts are installed next to the program: `THIRD-PARTY-LICENSES.txt` (generated at
build time from the exact Go module and NuGet package versions used), `byedpi-LICENSE.txt`
and `wintun-LICENSE.txt`. Where the vendored binaries come from, and how that is checked,
is in [third_party/README.md](third_party/README.md).

## Bundled programs

| Component | Use | License |
|---|---|---|
| [byedpi](https://github.com/hufrea/byedpi) 0.17.3 (`ciadpi.exe`, official release binary of tag v0.17.3, commit `7efde1b`) by hufrea | the DPI bypass itself, one process per profile | MIT |
| [Wintun](https://www.wintun.net/) 0.14.1 (`wintun.dll`, amd64, signed) by WireGuard LLC | the virtual network adapter | Wintun Prebuilt Binaries License: redistributed unmodified, used only through its public API |

## Go modules linked into nomoredpi-svc.exe

| Module | License |
|---|---|
| Go runtime and standard library | BSD-3-Clause |
| github.com/xjasonlyu/tun2socks/v2 | MIT |
| gvisor.dev/gvisor | Apache-2.0 |
| golang.zx2c4.com/wireguard, golang.zx2c4.com/wireguard/windows, golang.zx2c4.com/wintun | MIT |
| github.com/miekg/dns | BSD-3-Clause |
| github.com/ameshkov/dnscrypt/v2, github.com/ameshkov/dnsstamps, github.com/AdguardTeam/golibs | Unlicense (public domain) |
| github.com/tailscale/wf, go4.org/netipx | BSD-3-Clause |
| github.com/Microsoft/go-winio | MIT |
| github.com/google/btree | Apache-2.0 |
| golang.org/x/crypto, x/exp, x/net, x/sys, x/time | BSD-3-Clause |

## .NET components in NoMoreDPI.exe

| Package | License |
|---|---|
| [WPF-UI](https://github.com/lepoco/wpfui) (WPF-UI, WPF-UI.Abstractions) | MIT |
| .NET runtime, WPF, Windows Forms, System.ServiceProcess.ServiceController | MIT |

WireGuard and Wintun are registered trademarks of Jason A. Donenfeld. No More DPI is not
affiliated with or endorsed by WireGuard LLC, the byedpi project or Microsoft.
