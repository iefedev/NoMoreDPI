# Full cycle through the real installer: setup (install or upgrade), graceful service stop,
# e2e, start again. Run elevated after `build.ps1 -Installer`, on a test machine: it
# installs No More DPI and changes system DNS while it runs.
# Results: tests\cycle-result.txt (+ tests\e2e-result.txt).
#Requires -RunAsAdministrator
$root = Split-Path $PSScriptRoot -Parent
$out = Join-Path $PSScriptRoot 'cycle-result.txt'
$data = Join-Path $env:ProgramData 'No More DPI'
$lines = [System.Collections.Generic.List[string]]::new()
function Check($name, [bool]$ok, $detail = '') { $lines.Add(("[{0}] {1} {2}" -f ($(if ($ok) { 'PASS' } else { 'FAIL' })), $name, $detail)) }
function BravePolicy { (Get-ItemProperty HKLM:\SOFTWARE\Policies\BraveSoftware\Brave -Name DnsOverHttpsMode -ErrorAction SilentlyContinue).DnsOverHttpsMode }
. (Join-Path $PSScriptRoot 'test-profiles.ps1')
$ownConfig = $false

try {
    $setup = Get-ChildItem (Join-Path $root 'dist') -Filter '*-setup.exe' | Sort-Object LastWriteTime | Select-Object -Last 1
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $p = Start-Process $setup.FullName -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART' -PassThru -Wait
    Check 'setup (install/upgrade)' ($p.ExitCode -eq 0 -and (Get-Service NoMoreDPI).Status -eq 'Running') ("{0} in {1:n1}s" -f $setup.Name, $sw.Elapsed.TotalSeconds)
    Start-Sleep -Seconds 3
    # No profiles ship with the app: load the test ones (the user's config is put back at the end).
    Stop-Service NoMoreDPI
    $ownConfig = Use-TestProfiles
    Start-Service NoMoreDPI
    Start-Sleep -Seconds 3
    $svcExe = (Get-CimInstance Win32_Service -Filter "Name='NoMoreDPI'").PathName
    Check 'service runs installed binary' ($svcExe -like "*$env:ProgramFiles\No More DPI\nomoredpi-svc.exe*") $svcExe
    Check 'interactive users may start/stop' ((sc.exe sdshow NoMoreDPI) -match '\(A;;CCLCSWRPWPDTLOCRRC;;;IU\)')
    Check 'data folder locked down' ((Get-Acl $data).Sddl -match 'D:PAI?\(A;OICI;FA;;;SY\)\(A;OICI;FA;;;BA\)\(A;OICI;0x1200a9;;;BU\)$') (Get-Acl $data).Sddl
    Check 'brave DoH policy off while active' ((BravePolicy) -eq 'off') "value=$(BravePolicy)"
    $c = curl.exe -s -o NUL -m 15 -w '%{http_code}' https://discord.com/api/v9/gateway
    Check 'discord via installed service' ($c -eq '200') "http=$c"

    $sw = [Diagnostics.Stopwatch]::StartNew()
    Stop-Service NoMoreDPI -ErrorAction SilentlyContinue
    $st = (Get-Service NoMoreDPI).Status
    Check 'graceful service stop' ($st -eq 'Stopped' -and $sw.Elapsed.TotalSeconds -lt 15) ("{0} in {1:n1}s" -f $st, $sw.Elapsed.TotalSeconds)
    $tail = Get-Content (Join-Path $data 'service.log') -Encoding utf8 -Tail 6
    Check 'stop was clean (no timeout)' (-not ($tail -match 'timed out')) ($tail[-1])
    Check 'adapter removed on stop' (-not (Get-NetAdapter -Name 'No More DPI' -ErrorAction SilentlyContinue))
    Check 'brave policy restored on stop' (-not (BravePolicy)) "value=$(BravePolicy)"
    Check 'no state files left' (-not (Test-Path (Join-Path $data 'dns-state.json')) -and -not (Test-Path (Join-Path $data 'browser-state.json')))

    & (Join-Path $PSScriptRoot 'e2e.ps1')

    Start-Service NoMoreDPI
    Start-Sleep -Seconds 3
    Check 'service started again' ((Get-Service NoMoreDPI).Status -eq 'Running')
    $c = curl.exe -s -o NUL -m 15 -w '%{http_code}' https://discord.com/api/v9/gateway
    Check 'discord after restart' ($c -eq '200') "http=$c"
    Check 'brave DoH policy off again' ((BravePolicy) -eq 'off')
} catch {
    $lines.Add("ERROR: $_")
} finally {
    if ($ownConfig) {
        Stop-Service NoMoreDPI -ErrorAction SilentlyContinue
        Restore-Config
        Start-Service NoMoreDPI -ErrorAction SilentlyContinue
    }
    $lines | Set-Content $out -Encoding utf8
}
