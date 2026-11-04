# Architecture

Windows 10/11 x64. Two programs that talk over the named pipe `\\.\pipe\nomoredpi` (JSON
lines, `internal/ipc`):

- `nomoredpi-svc.exe` (Go, `cmd/nomoredpi`): the `NoMoreDPI` service (LocalSystem) and the
  admin commands that setup and the window call (`install`, `uninstall`, `prepare-upgrade`,
  `set-startup`, `set-dns`, `restore`, …).
- `NoMoreDPI.exe` (WPF + Wpf.Ui, `ui\`): tray icon and settings window, one per signed-in
  user. It reaches the engine over the pipe and the service itself through the service
  manager.

Setup (`installer\NoMoreDPI.iss`, Inno Setup 6) copies the files and calls
`nomoredpi-svc install --setup`; the uninstaller calls `uninstall --setup` before it deletes
the files. `nomoredpi-svc install` without `--setup` (UAC) does the same and also copies the
files to Program Files; that path is for development and creates no Apps & Features entry.

## Packages

| package | role |
|---|---|
| `internal/config` | `%ProgramData%\No More DPI\config.json`, presets, auto-find strategies, argument and fake-range checks, data folder ACL |
| `internal/fakeip` | stable domain ↔ fake address map (`100.79.0.0/16` by default), persisted |
| `internal/dnsserver` | resolver on 127.0.0.1/::1:53; DNSCrypt → TCP DNSCrypt → DoH upstream, cache |
| `internal/tunnel` | Wintun adapter + gVisor netstack (tun2socks core); TCP → profile ciadpi |
| `internal/byedpi` | one supervised ciadpi per profile, kill-on-close job object |
| `internal/sysdns` | point adapters with a resolver at us; originals saved before change |
| `internal/dnslock` | dynamic WFP session: block 53/853 + known DoH IPs, permit self + loopback |
| `internal/browserdns` | `DnsOverHttpsMode=off` (Chromium) / Firefox policy while active |
| `internal/autofind` | test strategies end to end over the current path |
| `internal/engine` | start/stop order, config changes, status, IPC handler |

## Security model

- The service runs as SYSTEM and trusts its data folder, so `config.SecureDir` gives
  `%ProgramData%\No More DPI` a protected DACL (SYSTEM/Administrators full, Users read),
  owner Administrators, and strips explicit entries from everything inside. Install and every
  service start apply it; by default ProgramData subfolders are user-writable.
- `browser-state.json` is filtered through the fixed policy list on load, so a tampered file
  cannot make the service write other HKLM values.
- The pipe admits SYSTEM, Administrators and Interactive Users. When a connection is
  accepted the service looks up the client process (`GetNamedPipeClientProcessId`) and
  whether its token is elevated.
  - Any signed-in user may pause and resume, edit profiles (names, domains, byedpi
    arguments) and run Auto-find.
  - The DNS servers, the DNS lock and the fake IP range change DNS for every user of the PC,
    so `setConfig` rejects changes to them unless the caller is elevated, and `setSystem`
    accepts only elevated callers. The window changes them through
    `nomoredpi-svc set-dns …` started with UAC.
- Profile arguments run in ciadpi as SYSTEM. `config.CheckArgs` accepts only an allowlist of
  long byedpi desync options by exact name (ciadpi's `getopt_long` also accepts any
  unambiguous prefix, so a denylist could be bypassed with `--fake-d` for `--fake-data`), no
  short options, no bare `--`, and for the options that would read a file (`--fake-data`,
  `--hosts`, `--ipset`) only inline `:data` values. The listener address and port are set
  by the service.
- The fake IP range must be a /16 to /24 block inside `100.64.0.0/10` or `198.18.0.0/15`, so
  it cannot capture ordinary destinations.
- The service DACL lets Interactive Users start, stop and query it, so the window does not
  need UAC for that; startup type and uninstall stay administrator-only (UAC).
- Each profile's ciadpi is a SOCKS5 proxy on `127.0.0.1` (ports 19090 and up) without
  authentication; any local process can use it, as with any local proxy.
- Developer-only pipe requests (`stacks`) exist only in builds made with `-tags debug`.

## Design notes

- **Fake IP addresses instead of routing real IPs.** Profile domains resolve to addresses
  from a private block that is routed into the Wintun adapter. Routing the real IPs of
  profile domains into the adapter would need per-IP routes that race with DNS answers,
  catch other sites on shared CDN addresses, and make ciadpi's own connections loop back
  unless they are bound to the egress interface, which Windows does not honour reliably for
  TCP (`IP_UNICAST_IF`) or when an adapter forwards traffic (weak host model). One on-link
  route for the fake block has none of these problems. Cost: an app that cached a real IP
  before the service started bypasses it until it resolves again; the Windows resolver
  cache is flushed on start.
- **Wintun, not a packet-diversion driver.** Wintun is the signed adapter driver that
  WireGuard uses; nothing is intercepted outside the fake block.
- **Fail-open.** The WFP session is dynamic, so its filters disappear with the process;
  adapters keep their original resolver as a secondary server; state files let the next
  start (or `nomoredpi-svc restore`, or uninstall) undo a crash.
- **Only adapters with a real resolver are taken over.** Changing DNS on adapters that have
  none (Hyper-V/WSL switches, the mobile hotspot, Teredo) makes Windows reconfigure ICS/HNS,
  which briefly disrupts networking. The IPv6 placeholders `fec0:0:0:ffff::/64` do not count
  as a resolver.
- **Browser DoH off through policy.** A browser in strict DoH mode would lose all name
  resolution under the DNS lock.
- **WFP filters at the transport layer too.** ALE filters are evaluated once per flow, and
  the DNS client service keeps flows that were authorized before the lock was enabled.
- **Teardown order.** The Wintun session is closed before `stack.Wait()`: the packet reader
  only returns once the session is closed, so the other order deadlocks.
- **ciadpi binary.** The official byedpi 0.17.3 release binary, byte-identical to the
  upstream archive (see `third_party/README.md`); `build.ps1 -CiadpiFromSource` builds it from
  the vendored source instead.
- **Tray/window.** WPF + Wpf.Ui 4.3 (.NET 10), self-contained single file (~70 MB; WPF cannot
  be trimmed). Config edits patch the `getConfig` JSON, never a typed model, so unknown
  fields (`probe`, `version`) survive a save.
- **Autostart is per user** (HKCU Run). Setup sets it with `NoMoreDPI.exe --autostart on`
  run as the signed-in user, not the elevated installer, so an install by another
  administrator account lands in the right profile.
- **Not code-signed.** SmartScreen warns on first run; the README says how to proceed.

## Testing

`go test ./cmd/nomoredpi/ ./internal/config/ ./internal/engine/ ./internal/ipc/` runs offline
(argument allowlist, fake-range checks, the elevation rule for DNS settings, pipe caller
identification, `set-dns` parsing). The other Go tests talk to real resolvers or need a
network where the probe hosts are DPI-blocked. The elevated end-to-end scripts in `tests\`
are described in the README's Development section.
