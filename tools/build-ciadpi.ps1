# Compiles ciadpi.exe from the vendored byedpi source (third_party\byedpi) with upstream's
# own recipe: the flags and files of its Makefile's `windows` target, as one compiler call.
# -CC is the compiler command, MinGW-w64 GCC by default; anything that accepts GCC flags
# works, e.g. -CC 'zig cc -target x86_64-windows-gnu'. build.ps1 -CiadpiFromSource calls it;
# CI runs it to check that the vendored source builds.
param(
    [Parameter(Mandatory)][string]$Out,
    [string]$CC = 'gcc',
    [string]$Root = (Split-Path $PSScriptRoot -Parent)
)
$ErrorActionPreference = 'Stop'
$Out = [IO.Path]::GetFullPath($Out)
$ccExe, $ccArgs = $CC -split '\s+'
Push-Location (Join-Path $Root 'third_party\byedpi')
try {
    & $ccExe @ccArgs -D_DEFAULT_SOURCE -I. -std=c99 -O2 -Wall -Wno-unused -Wextra -Wno-unused-parameter -pedantic `
        packets.c main.c conev.c proxy.c desync.c mpool.c extend.c win_service.c -o $Out -lws2_32 -lmswsock
    if ($LASTEXITCODE) { throw "ciadpi build failed ($CC)" }
} finally { Pop-Location }
$v = & $Out --version
if ($LASTEXITCODE -or "$v".Trim() -ne '17.3') { throw "built ciadpi.exe reports version '$v', expected 17.3" }
Write-Host "built $Out (ciadpi $v)"
