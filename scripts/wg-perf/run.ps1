param(
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yaml'),
    [string]$StatsFile = (Join-Path $PSScriptRoot 'docker-stats.csv'),
    [switch]$KeepUp,
    [switch]$SkipStats
)

$scriptName = 'wg-perf'
$ErrorActionPreference = 'Stop'

function Get-OptionalEnvInt {
    param([string]$Name)
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $null
    }
    return [int]$value
}

function Ensure-Tc {
    param([string]$Container)
    docker exec $Container sh -lc "set -eu; export DEBIAN_FRONTEND=noninteractive; command -v tc >/dev/null 2>&1 || (apt-get update >/dev/null && apt-get install -y --no-install-recommends iproute2 >/dev/null)"
}

function Apply-NetemDelayOnCIDR {
    param(
        [string]$Container,
        [string]$CIDR,
        [int]$DelayMs
    )

    if ($DelayMs -le 0) {
        return
    }

    Ensure-Tc -Container $Container
    $script = @(
        'set -eu'
        ('iface=$(ip -o -4 addr show | grep -F ''{0}'' | awk ''{{print $2}}'' | head -n 1)' -f $CIDR)
        'test -n "$iface"'
        ('tc qdisc replace dev "$iface" root netem delay {0}ms' -f $DelayMs)
    ) -join "`n"
    docker exec $Container sh -lc $script
}

function Cleanup {
    docker compose -f $ComposeFile down -v | Out-Null
}

$delayCliToWg = Get-OptionalEnvInt 'HARNESS_DELAY_CLI_TO_WG_MS'
$delayWgToRelay = Get-OptionalEnvInt 'HARNESS_DELAY_WG_TO_RELAY_MS'
$delayRelayAToRelayB = Get-OptionalEnvInt 'HARNESS_DELAY_RELAY_A_TO_RELAY_B_MS'
if ($null -eq $delayWgToRelay) {
    $delayWgToRelay = 40
}
if ($null -eq $delayCliToWg) {
    $delayCliToWg = 40
}
if ($null -eq $delayRelayAToRelayB) {
    $delayRelayAToRelayB = 40
}

if (
    ($delayCliToWg -gt 0 -or $delayWgToRelay -gt 0 -or $delayRelayAToRelayB -gt 0) -and
    [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable('HARNESS_PRE_MEASURE_DELAY_MS'))
) {
    $env:HARNESS_PRE_MEASURE_DELAY_MS = '8000'
}

Cleanup
docker compose -f $ComposeFile up -d --build | Out-Null

Apply-NetemDelayOnCIDR -Container 'nre-perf' -CIDR '172.30.2.2/24' -DelayMs $delayCliToWg
Apply-NetemDelayOnCIDR -Container 'nre-relay-wg' -CIDR '172.30.2.15/24' -DelayMs $delayWgToRelay
Apply-NetemDelayOnCIDR -Container 'nre-relay-a1' -CIDR '172.30.2.11/24' -DelayMs $delayWgToRelay
Apply-NetemDelayOnCIDR -Container 'nre-relay-a2' -CIDR '172.30.2.12/24' -DelayMs $delayWgToRelay
Apply-NetemDelayOnCIDR -Container 'nre-agent-b' -CIDR '172.30.3.12/24' -DelayMs $delayRelayAToRelayB
Apply-NetemDelayOnCIDR -Container 'nre-backend-b' -CIDR '172.30.3.13/24' -DelayMs $delayRelayAToRelayB
Apply-NetemDelayOnCIDR -Container 'nre-relay-a1' -CIDR '172.30.4.11/24' -DelayMs $delayRelayAToRelayB
Apply-NetemDelayOnCIDR -Container 'nre-relay-a2' -CIDR '172.30.4.12/24' -DelayMs $delayRelayAToRelayB
Apply-NetemDelayOnCIDR -Container 'nre-relay-b3' -CIDR '172.30.4.13/24' -DelayMs $delayRelayAToRelayB
Apply-NetemDelayOnCIDR -Container 'nre-relay-b4' -CIDR '172.30.4.14/24' -DelayMs $delayRelayAToRelayB

$statsRows = [System.Collections.Generic.List[string]]::new()
if (-not $SkipStats) {
    $statsRows.Add('ts,name,cpu,mem,net,ps,threads')
}
try {
    while ($true) {
        $running = docker inspect -f '{{.State.Running}}' nre-perf 2>$null
        if ($LASTEXITCODE -ne 0 -or $running -ne 'true') {
            break
        }

        if (-not $SkipStats) {
            docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.NetIO}}' `
                nre-relay-a1 nre-relay-a2 nre-relay-b3 nre-relay-b4 nre-relay-wg nre-agent-b nre-backend-b nre-perf 2>$null |
                ForEach-Object { $statsRows.Add("$(Get-Date -Format o),$_") }
        }

        Start-Sleep -Milliseconds 500
    }

    docker logs nre-perf
}
finally {
    if (-not $SkipStats -and $statsRows.Count -gt 0) {
        $statsRows | Set-Content $StatsFile
    }
    if (-not $KeepUp) {
        Cleanup
    }
}
