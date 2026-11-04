# Test profiles for the end-to-end scripts. No profiles ship with No More DPI, so the scripts
# put these into config.json for the run and put the user's own config back afterwards.
# Dot-source this file; call Use-TestProfiles and Restore-Config with the service stopped.
# Use-TestProfiles returns $true only when it saved the user's config, so when cycle.ps1 runs
# e2e.ps1 inside its own test setup, only cycle.ps1 restores.

$script:ConfigPath = Join-Path $env:ProgramData 'No More DPI\config.json'
$script:ConfigBackup = "$script:ConfigPath.e2e-backup"

$script:TestProfiles = @(
    [ordered]@{ id = 'discord'; name = 'Discord'; enabled = $true; args = '--tlsrec 1+s'; port = 19091; probe = 'discord.com'
        domains = @('discord.com', 'discord.gg', 'discord.media', 'discordapp.com', 'discordapp.net', 'discordcdn.com') }
    [ordered]@{ id = 'roblox'; name = 'Roblox'; enabled = $true; args = '--tlsrec 1+s'; port = 19092; probe = 'www.roblox.com'
        domains = @('roblox.com', 'rbxcdn.com', 'rbxinfra.com') }
)

function Use-TestProfiles {
    if (-not (Test-Path $script:ConfigPath)) { throw "No config.json at $script:ConfigPath; start the service once first." }
    $saved = $false
    if (-not (Test-Path $script:ConfigBackup)) { Copy-Item $script:ConfigPath $script:ConfigBackup; $saved = $true }
    $c = Get-Content $script:ConfigPath -Raw | ConvertFrom-Json
    $c.profiles = $script:TestProfiles
    # Go's JSON reader rejects a byte-order mark, so write UTF-8 without one.
    [IO.File]::WriteAllText($script:ConfigPath, ($c | ConvertTo-Json -Depth 10), [Text.UTF8Encoding]::new($false))
    return $saved
}

function Restore-Config {
    if (Test-Path $script:ConfigBackup) { Move-Item $script:ConfigBackup $script:ConfigPath -Force }
}
