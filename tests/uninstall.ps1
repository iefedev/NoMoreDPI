# Uninstall through the real uninstaller and verify everything is back. Run elevated, after
# an install made with setup into the default folder. Results: tests\uninstall-result.txt
#Requires -RunAsAdministrator
$out = Join-Path $PSScriptRoot 'uninstall-result.txt'
$app = Join-Path $env:ProgramFiles 'No More DPI'
$data = Join-Path $env:ProgramData 'No More DPI'
$lines = @()
function Check($name, [bool]$ok, $detail = '') { $script:lines += ("[{0}] {1} {2}" -f ($(if ($ok) { 'PASS' } else { 'FAIL' })), $name, $detail) }
$p = Start-Process (Join-Path $app 'unins000.exe') -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART' -PassThru -Wait
Check 'uninstaller exit code' ($p.ExitCode -eq 0) $p.ExitCode
Start-Sleep -Seconds 4
Check 'service removed' (-not (Get-Service NoMoreDPI -ErrorAction SilentlyContinue))
Check 'adapter removed' (-not (Get-NetAdapter -Name 'No More DPI' -ErrorAction SilentlyContinue))
Check 'install dir removed' (-not (Test-Path $app))
Check 'brave policy gone' (-not (Get-ItemProperty HKLM:\SOFTWARE\Policies\BraveSoftware\Brave -Name DnsOverHttpsMode -ErrorAction SilentlyContinue))
$dns = Get-DnsClientServerAddress -AddressFamily IPv4 | Where-Object { $_.ServerAddresses -contains '127.0.0.1' }
Check 'no adapter left on 127.0.0.1' (-not $dns) (($dns | ForEach-Object InterfaceAlias) -join ',')
Check 'tray not running' (-not (Get-Process NoMoreDPI -ErrorAction SilentlyContinue))
Check 'run entry removed' (-not (Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'No More DPI' -ErrorAction SilentlyContinue))
Check 'settings kept (silent uninstall answers No)' (Test-Path (Join-Path $data 'config.json'))
$c = curl.exe -s -o NUL -m 10 -w '%{http_code}' https://example.com/
Check 'internet works after uninstall' ($c -eq '200') "http=$c"
$lines | Set-Content $out -Encoding utf8
