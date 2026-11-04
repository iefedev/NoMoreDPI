# Changelog

## 1.1.0

- No profiles are included any more: a new install starts with an empty list and you add a
  profile for each site you need. Existing installs keep the profiles they have.

## 1.0.0

First public release.

- System-wide, per-domain DPI bypass through byedpi, one process per profile.
- Encrypted DNS (DNSCrypt, DoH) with an optional DNS lock that keeps apps and browsers on it.
- Auto-find picks a working byedpi setting for your connection.
- Tray icon and settings window, including service control: start, stop, restart and
  startup type.
- Installer and uninstaller; uninstalling restores DNS and browser settings.
- Machine-wide DNS settings (servers, DNS lock, fake IP range) need administrator rights to
  change; profile arguments are limited to a fixed list of byedpi options.
