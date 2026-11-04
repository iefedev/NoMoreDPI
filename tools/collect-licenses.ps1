# Writes THIRD-PARTY-LICENSES.txt: the full license texts of everything linked into the
# shipped binaries, taken from the exact module and package versions the build used.
#   - Go: every module linked into nomoredpi-svc.exe (go list -deps), plus the Go runtime
#   - .NET: the runtime packs that the self-contained NoMoreDPI.exe carries
#     (Microsoft.NETCore.App.Runtime and Microsoft.WindowsDesktop.App.Runtime, with their
#     LICENSE and THIRD-PARTY-NOTICES) and every NuGet package in project.assets.json
# Fails if a module has no license file, so a new dependency cannot ship without one.
# build.ps1 runs it after the builds; run it by hand with -Out <file>.
param(
    [Parameter(Mandatory)][string]$Out,
    [string]$Root = (Split-Path $PSScriptRoot -Parent)
)
$ErrorActionPreference = 'Stop'
$licenseName = '^(licen[cs]e|copying|unlicense|notice|patents|third-party-notices|thirdpartynotices)(\.(txt|md|rst))?$'
$sb = [System.Text.StringBuilder]::new()
function Section($title, $files) {
    [void]$sb.AppendLine('=' * 78)
    [void]$sb.AppendLine($title)
    [void]$sb.AppendLine('=' * 78)
    foreach ($f in $files) {
        [void]$sb.AppendLine("--- $($f.Name)")
        [void]$sb.AppendLine((Get-Content $f.FullName -Raw -Encoding UTF8).TrimEnd())
        [void]$sb.AppendLine()
    }
}
function LicenseFiles($dir) {
    # CultureInvariant: under some cultures (Turkish dotted/dotless i) "LICENSE" -match
    # "license" is false.
    Get-ChildItem -LiteralPath $dir -File |
        Where-Object { [regex]::IsMatch($_.Name, $licenseName, 'IgnoreCase, CultureInvariant') } | Sort-Object Name
}

[void]$sb.AppendLine('Third-party licenses for No More DPI')
[void]$sb.AppendLine('See THIRD-PARTY-NOTICES.md for what each component is used for.')
[void]$sb.AppendLine()

Push-Location $Root
try {
    # Go modules linked into nomoredpi-svc.exe.
    $mods = go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}' ./cmd/nomoredpi |
        Where-Object { $_ } | Sort-Object -Unique
    if ($LASTEXITCODE) { throw 'go list failed' }
    $goroot = (go env GOROOT).Trim()
    Section "Go runtime and standard library $((go env GOVERSION).Trim())" (LicenseFiles $goroot)
    foreach ($m in $mods) {
        $path, $ver, $dir = $m -split '\|', 3
        $files = @(LicenseFiles $dir)
        if (-not $files) { throw "no license file found for Go module $path@$ver in $dir" }
        Section "$path $ver" $files
    }

    # .NET: the runtime packs bundled into the self-contained exe, then the NuGet packages.
    # The runtime packs are the ones restore resolved for publish (downloadDependencies);
    # the dotnet install root's own LICENSE.txt covers the SDK install, not these packs.
    $assets = Join-Path $Root 'ui\obj\project.assets.json'
    if (-not (Test-Path $assets)) { throw "$assets not found: build the UI first" }
    $json = Get-Content $assets -Raw -Encoding UTF8 | ConvertFrom-Json
    $pkgRoot = @($json.packageFolders.PSObject.Properties.Name)[0]
    $dotnetRoot = Split-Path (Get-Command dotnet).Source -Parent
    $packs = foreach ($fw in $json.project.frameworks.PSObject.Properties) { $fw.Value.downloadDependencies }
    foreach ($name in 'Microsoft.NETCore.App.Runtime.win-x64', 'Microsoft.WindowsDesktop.App.Runtime.win-x64') {
        $pack = $packs | Where-Object { $_.name -eq $name } | Select-Object -First 1
        if (-not $pack) { throw "runtime pack $name not in $assets (is the UI published self-contained for win-x64?)" }
        $ver = ($pack.version -replace '[\[\]\s]', '' -split ',')[0]
        $dir = @((Join-Path $dotnetRoot "packs\$name\$ver"), (Join-Path $pkgRoot "$($name.ToLowerInvariant())\$ver")) |
            Where-Object { Test-Path $_ } | Select-Object -First 1
        if (-not $dir) { throw "runtime pack $name $ver not found under $dotnetRoot\packs or $pkgRoot" }
        $files = @(LicenseFiles $dir)
        if (-not $files) { throw "no license file in runtime pack $dir" }
        Section "$name $ver (bundled into NoMoreDPI.exe)" $files
    }
    foreach ($lib in $json.libraries.PSObject.Properties) {
        if ($lib.Value.type -ne 'package' -or $lib.Name -like 'Microsoft.NET.ILLink.Tasks/*') { continue } # build-time only
        $dir = Join-Path $pkgRoot $lib.Value.path
        $nuspec = Get-ChildItem $dir -Filter '*.nuspec' | Select-Object -First 1
        $expr = if ($nuspec) { ([xml](Get-Content $nuspec.FullName -Raw -Encoding UTF8)).package.metadata.license.'#text' } else { $null }
        $files = @(LicenseFiles $dir)
        if (-not $files -and $expr -ne 'MIT') { throw "no license file for NuGet package $($lib.Name)" }
        $title = $lib.Name -replace '/', ' '
        if ($expr) { $title += " (license: $expr)" }
        if (-not $files) { $title += ' - MIT, same text as Microsoft.NETCore.App.Runtime above' }
        Section $title $files
    }
} finally { Pop-Location }

[IO.File]::WriteAllText($Out, $sb.ToString(), [Text.UTF8Encoding]::new($false))
Write-Host "wrote $Out"
