param(
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yaml'),
    [string]$StatsFile = (Join-Path $PSScriptRoot 'docker-stats.csv'),
    [switch]$KeepUp,
    [switch]$SkipStats
)

$ErrorActionPreference = 'Stop'

function Get-OptionalEnvInt {
    param(
        [string]$Name
    )

    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $null
    }
    return [int]$value
}

function Ensure-Tc {
    param(
        [string]$Container
    )

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
    Write-Host "Applying ${DelayMs}ms netem delay to $Container on $CIDR"
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

$delayCliToA = Get-OptionalEnvInt 'HARNESS_DELAY_CLI_TO_A_MS'
$delayAToRelay = Get-OptionalEnvInt 'HARNESS_DELAY_A_TO_RELAY_MS'
$fallbackDelay = Get-OptionalEnvInt 'HARNESS_NETEM_DELAY_MS'
if ($null -eq $delayAToRelay -and $null -ne $fallbackDelay) {
    $delayAToRelay = $fallbackDelay
}

if (
    ($null -ne $delayCliToA -or $null -ne $delayAToRelay) -and
    [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable('HARNESS_PRE_MEASURE_DELAY_MS'))
) {
    $env:HARNESS_PRE_MEASURE_DELAY_MS = '8000'
}

Cleanup
docker compose -f $ComposeFile up -d --build | Out-Null

if ($null -ne $delayCliToA -and $delayCliToA -gt 0) {
    Apply-NetemDelayOnCIDR -Container 'nre-perf' -CIDR '172.29.1.2/24' -DelayMs $delayCliToA
    Apply-NetemDelayOnCIDR -Container 'nre-agent-a' -CIDR '172.29.1.10/24' -DelayMs $delayCliToA
}

if ($null -ne $delayAToRelay -and $delayAToRelay -gt 0) {
    Apply-NetemDelayOnCIDR -Container 'nre-agent-a' -CIDR '172.29.2.10/24' -DelayMs $delayAToRelay
    Apply-NetemDelayOnCIDR -Container 'nre-relay-b' -CIDR '172.29.2.11/24' -DelayMs $delayAToRelay
}

$statsRows = [System.Collections.Generic.List[string]]::new()
if (-not $SkipStats) {
    $statsRows.Add('ts,name,cpu,mem,net')
}
try {
    while ($true) {
        $running = docker inspect -f '{{.State.Running}}' nre-perf 2>$null
        if ($LASTEXITCODE -ne 0 -or $running -ne 'true') {
            break
        }

        if (-not $SkipStats) {
            docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.NetIO}}' `
                nre-agent-a nre-relay-b nre-agent-b nre-backend-b nre-perf 2>$null |
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
