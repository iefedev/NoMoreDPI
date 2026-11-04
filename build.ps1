# Builds everything into dist\: nomoredpi-svc.exe (service + admin commands, GUI subsystem),
# nomoredpi-cli.exe (the same as a console program, for `run` and status in a terminal),
# NoMoreDPI.exe (tray + settings window, self-contained .NET), ciadpi.exe (byedpi 0.17.3),
# wintun.dll and THIRD-PARTY-LICENSES.txt. With -Installer it also compiles the setup with
# Inno Setup 6 and writes its .sha256 next to it.
#
#   -Version 1.2.3        version stamped into every binary and the setup (default 1.0.0)
#   -Installer            also build dist\NoMoreDPI-<version>-setup.exe
#   -ProjectUrl <url>     project page linked from the app and the setup (CI passes the repo URL)
#   -CiadpiFromSource     compile ciadpi.exe from third_party\byedpi instead of using the
#                         official release binary; -CC picks the compiler (default gcc, MinGW-w64)
#   -Dev                  developer build: Go `-tags debug` (extra pipe commands such as
#                         `stacks`) and a Debug UI (`NoMoreDPI.exe --demo`). Never release it.
param(
    [string]$Version = '1.0.0',
    [switch]$Installer,
    [string]$ProjectUrl = '',
    [switch]$CiadpiFromSource,
    [string]$CC = 'gcc',
    [switch]$Dev
)
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null
Remove-Item (Join-Path $dist '*-setup.exe*') -Force -ErrorAction SilentlyContinue
# Pick up tools installed since this shell started (Go, .NET, Inno Setup). Appended, so
# toolchains already on the process PATH (setup-go / setup-dotnet in CI) take precedence.
$env:Path = $env:Path + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
# 1.2.3-beta.1 -> 1.2.3 for the numeric file version fields.
$numeric = ($Version -split '[-+]')[0]
if ($numeric -notmatch '^\d+\.\d+\.\d+$') { throw "Version must look like 1.2.3 or 1.2.3-beta.1, not '$Version'" }
if ($ProjectUrl -and $ProjectUrl -notmatch '^https://[^\s"]+$') { throw "ProjectUrl must be an https:// URL" }

# The vendored binaries must be the pinned upstream files (third_party\README.md).
$pins = Get-Content (Join-Path $root 'third_party\upstream.json') -Raw | ConvertFrom-Json
foreach ($b in $pins.binaries) {
    $h = (Get-FileHash (Join-Path $root "third_party\$($b.file)") -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($h -ne $b.sha256) { throw "third_party\$($b.file) does not match its pin in third_party\upstream.json ($h)" }
}

Push-Location $root
try {
    $ld = "-s -w -X nomoredpi/internal/engine.AppVersion=$Version"
    $tags = if ($Dev) { @('-tags', 'debug') } else { @() }
    go build -trimpath @tags -ldflags "$ld -H windowsgui" -o (Join-Path $dist 'nomoredpi-svc.exe') ./cmd/nomoredpi
    if ($LASTEXITCODE) { throw 'go build (svc) failed' }
    go build -trimpath @tags -ldflags $ld -o (Join-Path $dist 'nomoredpi-cli.exe') ./cmd/nomoredpi
    if ($LASTEXITCODE) { throw 'go build (cli) failed' }
    # Single file with WPF's native DLLs inside: setup copies only the exe.
    $configuration = if ($Dev) { 'Debug' } else { 'Release' }
    dotnet publish (Join-Path $root 'ui\NoMoreDPI.csproj') -c $configuration -nologo -v q -o (Join-Path $root 'build\ui') `
        "-p:Version=$numeric" "-p:InformationalVersion=$Version" "-p:ProjectUrl=$ProjectUrl"
    if ($LASTEXITCODE) { throw 'dotnet publish (ui) failed' }
    Copy-Item (Join-Path $root 'build\ui\NoMoreDPI.exe') (Join-Path $dist 'NoMoreDPI.exe') -Force
} finally { Pop-Location }

$ciadpi = Join-Path $dist 'ciadpi.exe'
if (-not $CiadpiFromSource) {
    Copy-Item (Join-Path $root 'third_party\byedpi-bin\ciadpi.exe') $ciadpi -Force
} else {
    & (Join-Path $root 'tools\build-ciadpi.ps1') -Out $ciadpi -CC $CC -Root $root
}

Copy-Item (Join-Path $root 'third_party\wintun\wintun.dll') (Join-Path $dist 'wintun.dll') -Force
& (Join-Path $root 'tools\collect-licenses.ps1') -Out (Join-Path $dist 'THIRD-PARTY-LICENSES.txt') -Root $root

if ($Installer) {
    $iscc = (Get-Command iscc.exe -ErrorAction SilentlyContinue).Source
    if (-not $iscc) {
        $iscc = @("${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe", "$env:ProgramFiles\Inno Setup 6\ISCC.exe", "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe") |
            Where-Object { Test-Path $_ } | Select-Object -First 1
    }
    if (-not $iscc) { throw 'Inno Setup 6 (ISCC.exe) not found; install it or build without -Installer' }
    $defines = @("/DAppVersion=$Version", "/DNumericVersion=$numeric")
    if ($ProjectUrl) { $defines += "/DAppUrl=$ProjectUrl" }
    & $iscc /Qp @defines "/O$dist" (Join-Path $root 'installer\NoMoreDPI.iss')
    if ($LASTEXITCODE) { throw 'Inno Setup failed' }
    Get-ChildItem $dist -Filter '*-setup.exe' | ForEach-Object {
        $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "$hash  $($_.Name)" | Set-Content -Encoding ascii "$($_.FullName).sha256"
    }
}
Get-ChildItem $dist | Format-Table Name, Length -AutoSize
