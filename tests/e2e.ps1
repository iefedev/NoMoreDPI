# End-to-end check of the engine through the real system path (no proxy settings).
# Runs `nomoredpi-cli run` in the background, probes, hard-kills it (crash), then restores.
# Run elevated, with the No More DPI service stopped and nothing else listening on port 53
# (the engine needs 127.0.0.1:53). It changes system DNS while it runs and needs a network
# where discord.com and www.roblox.com are reachable through byedpi. Profiles for those two
# come from test-profiles.ps1 for the run; your own config.json is put back afterwards.
# Results go to tests\e2e-result.txt.
#Requires -RunAsAdministrator
param([string]$Bin = (Join-Path (Split-Path $PSScriptRoot -Parent) 'dist'))

$out = Join-Path $PSScriptRoot 'e2e-result.txt'
$runLog = Join-Path $env:ProgramData 'No More DPI\service.log'
$cli = Join-Path $Bin 'nomoredpi-cli.exe'
Remove-Item $runLog -ErrorAction SilentlyContinue
$lines = [System.Collections.Generic.List[string]]::new()
function Log($s) { $lines.Add($s); Write-Host $s }
function Check($name, [bool]$ok, $detail = '') { Log ("[{0}] {1} {2}" -f ($(if ($ok) { 'PASS' } else { 'FAIL' })), $name, $detail) }
function DnsSnapshot { Get-DnsClientServerAddress | Where-Object ServerAddresses | Sort-Object InterfaceAlias, AddressFamily | ForEach-Object { "$($_.InterfaceAlias)/$($_.AddressFamily)=$($_.ServerAddresses -join ',')" } }
function Http($url) { curl.exe -s -o NUL -m 15 -w '%{http_code}' $url 2>$null }

$before = DnsSnapshot
$busy = Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue | Where-Object LocalAddress -in '127.0.0.1', '::1', '0.0.0.0', '::'
if ($busy) {
    $owner = ($busy | ForEach-Object { (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).Name } | Sort-Object -Unique) -join ', '
    Log "port 53 is in use by: $owner. Stop it (and the No More DPI service) before running this test."
    $lines | Set-Content $out -Encoding utf8
    exit 1
}
# The default gateway usually answers DNS too; the lock must block it like any other server.
$gateway = (Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1).NextHop

. (Join-Path $PSScriptRoot 'test-profiles.ps1')
$ownConfig = Use-TestProfiles
$p = Start-Process $cli -ArgumentList 'run' -PassThru -WindowStyle Hidden
try {
    $up = $false
    for ($i = 0; $i -lt 40 -and -not $up; $i++) {
        Start-Sleep -Milliseconds 500
        $up = (Get-Content $runLog -ErrorAction SilentlyContinue) -match '\bactive$'
    }
    Check 'engine became active' ([bool]$up)
    if (-not $up) { Log (Get-Content $runLog -Tail 20 | Out-String); return }

    $during = DnsSnapshot
    Log ('DNS during run: ' + ($during -join ' | '))
    $wfp = Join-Path $env:TEMP 'nomoredpi-wfp.xml'
    netsh wfp show filters file=$wfp | Out-Null
    $nf = ([regex]::Matches((Get-Content $wfp -Raw), 'block port 53|block doh')).Count
    Check 'WFP DNS-lock filters present' ($nf -gt 0) "count=$nf"

    # Names not asked before, so the DNS client cache cannot answer for the blocked server.
    $leak = Resolve-DnsName wikipedia.org -Server 8.8.8.8 -DnsOnly -QuickTimeout -ErrorAction SilentlyContinue
    Check 'direct DNS to 8.8.8.8 is blocked' (-not $leak)
    if ($gateway -and $gateway -ne '0.0.0.0') {
        $leak = Resolve-DnsName mozilla.org -Server $gateway -DnsOnly -QuickTimeout -ErrorAction SilentlyContinue
        Check 'direct DNS to the default gateway is blocked' (-not $leak)
    } else { Log 'no IPv4 default gateway: skipped the gateway DNS check' }
    $n = nslookup -timeout=2 example.org 9.9.9.9 2>&1 | Out-String
    Check 'nslookup to 9.9.9.9 is blocked' ($n -notmatch 'Addresses?:\s+\d') ($n -replace '\s+', ' ')
    $a = (Resolve-DnsName discord.com -Type A -DnsOnly -ErrorAction SilentlyContinue | Where-Object IPAddress).IPAddress
    Check 'discord.com resolves to a fake address' ($a -like '100.79.*') "$a"
    $aaaa = Resolve-DnsName discord.com -Type AAAA -DnsOnly -ErrorAction SilentlyContinue | Where-Object IP6Address
    Check 'discord.com has no AAAA' (-not $aaaa)

    $c = Http 'https://discord.com/api/v9/gateway'
    Check 'discord.com API over system path' ($c -eq '200') "http=$c"
    $c = Http 'https://discord.com/app'
    Check 'discord.com/app page' ($c -match '^(200|30\d)$') "http=$c"
    $c = Http 'https://www.roblox.com/'
    Check 'www.roblox.com over system path' ($c -match '^(200|30\d)$') "http=$c"

    $ex = (Resolve-DnsName example.com -Type A -DnsOnly | Where-Object IPAddress | Select-Object -First 1).IPAddress
    $route = (Find-NetRoute -RemoteIPAddress $ex | Select-Object -Last 1).InterfaceAlias
    Check 'example.com is not routed to us' ($ex -notlike '100.79.*' -and $route -ne 'No More DPI') "ip=$ex via $route"
    $c = Http 'https://example.com/'
    Check 'example.com works normally' ($c -eq '200') "http=$c"

    $doh = Resolve-DnsName dns.google -DnsOnly -ErrorAction SilentlyContinue
    Check 'DoH hostname dns.google refused' (-not $doh)
    $c = curl.exe -s -o NUL -m 6 -w '%{http_code}' -H 'accept: application/dns-json' 'https://1.1.1.1/dns-query?name=discord.com&type=A' 2>$null
    Check 'DoH by IP (1.1.1.1:443) blocked' ($c -ne '200') "http=$c"

    # Kill the Discord profile's ciadpi and make sure it comes back.
    $ci = Get-CimInstance Win32_Process -Filter "Name='ciadpi.exe'" | Where-Object { $_.ParentProcessId -eq $p.Id -and $_.CommandLine -match ' 19091 ' }
    if ($ci) {
        Stop-Process -Id $ci.ProcessId -Force
        Start-Sleep -Seconds 2
        $ci2 = Get-CimInstance Win32_Process -Filter "Name='ciadpi.exe'" | Where-Object { $_.ParentProcessId -eq $p.Id -and $_.CommandLine -match ' 19091 ' }
        Check 'killed Discord ciadpi restarted' ([bool]$ci2 -and $ci2.ProcessId -ne $ci.ProcessId)
        $c = Http 'https://discord.com/api/v9/gateway'
        Check 'discord works after ciadpi restart' ($c -eq '200') "http=$c"
    } else { Check 'found Discord ciadpi child' $false }
    $kids = (Get-CimInstance Win32_Process -Filter "Name='ciadpi.exe'" | Where-Object ParentProcessId -eq $p.Id | Measure-Object).Count
    Check 'one ciadpi per profile' ($kids -eq 2) "count=$kids"

    # Sustained load: 30 parallel page loads through the Discord profile.
    $jobs = 1..30 | ForEach-Object { Start-Job { curl.exe -s -o NUL -m 20 -w '%{http_code}' 'https://discord.com/app' } }
    $codes = $jobs | Wait-Job -Timeout 60 | Receive-Job
    $jobs | Remove-Job -Force
    $ok = ($codes | Where-Object { $_ -eq '200' }).Count
    Check '30 parallel discord loads' ($ok -ge 28) "ok=$ok/30"
    $c = Http 'https://www.roblox.com/'
    Check 'roblox still fine after load' ($c -match '^(200|30\d)$') "http=$c"

    # Graceful pause/resume goes through the same teardown as a service stop.
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $paused = $false
    $pj = Start-Job -ArgumentList $cli { param($c) & $c pause }
    if ($pj | Wait-Job -Timeout 25) { $paused = $true }
    $pj | Remove-Job -Force
    $sw.Stop()
    $ad = Get-NetAdapter -Name 'No More DPI' -ErrorAction SilentlyContinue
    $dnsNow = DnsSnapshot
    Check 'pause completes (no teardown hang)' ($paused -and -not $ad) ("{0:n1}s" -f $sw.Elapsed.TotalSeconds)
    Check 'pause restored DNS' (($before -join '|') -eq ($dnsNow -join '|'))
    $c = Http 'https://example.com/'
    Check 'internet works while paused' ($c -eq '200') "http=$c"
    $rj = Start-Job -ArgumentList $cli { param($c) & $c resume }
    $rj | Wait-Job -Timeout 25 | Out-Null; $rj | Remove-Job -Force
    Start-Sleep -Seconds 1
    $c = Http 'https://discord.com/api/v9/gateway'
    Check 'discord works after resume' ($c -eq '200') "http=$c"

    Log '--- status'
    $lines.Add((& $cli status | Out-String))
    # Only builds made with `build.ps1 -Dev` (-tags debug) answer `stacks`.
    $stacks = & $cli stacks 2>$null | Out-String
    if ($LASTEXITCODE -eq 0) {
        Log '--- stacks (first 80 lines)'
        $stacks -split "`n" | Select-Object -First 80 | ForEach-Object { $lines.Add($_) }
    }
} finally {
    # Crash: hard kill, then the restore command must put DNS back.
    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
    $orphans = (Get-CimInstance Win32_Process -Filter "Name='ciadpi.exe'" | Where-Object { $_.CommandLine -match ' 1909[12] ' } | Measure-Object).Count
    Check 'no ciadpi left after crash' ($orphans -eq 0) "count=$orphans"
    $afterCrash = DnsSnapshot
    Log ('DNS after crash: ' + ($afterCrash -join ' | '))
    $c = Http 'https://example.com/'
    Check 'internet works right after crash (fail-open)' ($c -eq '200') "http=$c"
    & (Join-Path $Bin 'nomoredpi-cli.exe') restore
    Start-Sleep -Milliseconds 500
    $after = DnsSnapshot
    Check 'DNS restored exactly' (($before -join '|') -eq ($after -join '|')) ($after -join ' | ')
    $ad = Get-NetAdapter -Name 'No More DPI' -ErrorAction SilentlyContinue
    Check 'tunnel adapter gone' (-not $ad)
    Log '--- engine log tail'
    Get-Content $runLog -Tail 40 | ForEach-Object { $lines.Add($_) }
    $lines | Set-Content $out -Encoding utf8
    if ($ownConfig) { Restore-Config }
}
