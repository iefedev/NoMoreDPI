# Drop the tunnel adapter's address (as happens when Windows rebinds every adapter, for
# example after a network filter driver is installed or removed) and check the service puts
# it back within one health tick. Run elevated while the service is active with the default
# fake range. Results: tests\selfheal-result.txt
#Requires -RunAsAdministrator
$out = Join-Path $PSScriptRoot 'selfheal-result.txt'
$log = Join-Path $env:ProgramData 'No More DPI\service.log'
$ad = 'No More DPI'
$r = @()
Remove-NetIPAddress -InterfaceAlias $ad -IPAddress 100.79.0.1 -Confirm:$false
$r += 'removed: ' + (-not (Get-NetIPAddress -InterfaceAlias $ad -IPAddress 100.79.0.1 -ErrorAction SilentlyContinue))
$r += 'discord while broken=' + (curl.exe -s -o NUL -m 5 -w '%{http_code}' https://discord.com/api/v9/gateway)
$sw = [Diagnostics.Stopwatch]::StartNew()
while ($sw.Elapsed.TotalSeconds -lt 25 -and -not (Get-NetIPAddress -InterfaceAlias $ad -IPAddress 100.79.0.1 -ErrorAction SilentlyContinue)) { Start-Sleep -Milliseconds 500 }
$r += "restored after {0:n1}s: {1}" -f $sw.Elapsed.TotalSeconds, [bool](Get-NetIPAddress -InterfaceAlias $ad -IPAddress 100.79.0.1 -ErrorAction SilentlyContinue)
Start-Sleep -Seconds 1
$r += 'discord after repair=' + (curl.exe -s -o NUL -m 15 -w '%{http_code}' https://discord.com/api/v9/gateway)
$r += Get-Content $log -Encoding utf8 -Tail 20 | Where-Object { $_ -match 'reconfigured' }
$r | Set-Content $out -Encoding utf8
