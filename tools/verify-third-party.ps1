# Checks that the vendored binaries are exactly the upstream release files: downloads each
# archive listed in third_party\upstream.json, verifies its SHA-256 against the pin, and
# compares the file inside it byte for byte with the copy in third_party\. Needs network.
# CI runs it on every build; run it by hand after updating a vendored binary.
param([string]$Root = (Split-Path $PSScriptRoot -Parent))
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
$pins = Get-Content (Join-Path $Root 'third_party\upstream.json') -Raw | ConvertFrom-Json
$work = Join-Path ([IO.Path]::GetTempPath()) ("nomoredpi-verify-" + [guid]::NewGuid().ToString('n'))
New-Item -ItemType Directory $work | Out-Null
try {
    foreach ($b in $pins.binaries) {
        $zip = Join-Path $work ([IO.Path]::GetFileName(([uri]$b.archive).AbsolutePath))
        Invoke-WebRequest -Uri $b.archive -OutFile $zip -UseBasicParsing
        $zipHash = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
        if ($zipHash -ne $b.archiveSha256) { throw "$($b.name): archive SHA-256 is $zipHash, expected $($b.archiveSha256)" }
        $archive = [IO.Compression.ZipFile]::OpenRead($zip)
        try {
            $entry = $archive.Entries | Where-Object { $_.FullName -eq $b.member } | Select-Object -First 1
            if (-not $entry) { throw "$($b.name): $($b.member) not found in $($b.archive)" }
            $out = Join-Path $work ([guid]::NewGuid().ToString('n'))
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $out)
        } finally { $archive.Dispose() }
        $upstream = (Get-FileHash $out -Algorithm SHA256).Hash.ToLower()
        $vendored = (Get-FileHash (Join-Path $Root "third_party\$($b.file)") -Algorithm SHA256).Hash.ToLower()
        if ($upstream -ne $vendored -or $vendored -ne $b.sha256) {
            throw "$($b.name): vendored $vendored, upstream $upstream, pinned $($b.sha256)"
        }
        Write-Host "OK  $($b.name)  $vendored  (= $($b.member) in $($b.archive))"
    }
} finally { Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue }
