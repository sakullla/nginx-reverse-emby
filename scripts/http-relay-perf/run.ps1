# nre-http-relay-perf
param(
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yaml'),
    [string]$StatsFile = (Join-Path $PSScriptRoot 'docker-stats.csv'),
    [string]$AgentImage,
    [switch]$ModuleFixture,
    [switch]$SkipBuild,
    [switch]$KeepUp,
    [switch]$SkipStats,
    [int]$TimeoutSeconds = 900
)
$ErrorActionPreference = 'Stop'

function Assert-Docker {
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed: $LASTEXITCODE" }
}
function Get-Delay([string]$Name, [int]$Default) {
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrWhiteSpace($value)) { return $Default }
    $delay = [int]$value
    if ($delay -lt 0) { throw "$Name must be nonnegative" }
    return $delay
}
function Apply-NetemDelayOnCIDR([string]$Container, [string]$CIDR, [int]$DelayMs) {
    if ($DelayMs -le 0) { return }
    Write-Host "Applying ${DelayMs}ms netem delay to $Container on $CIDR"
    $script = @(
        'set -eu'
        ('iface=$(ip -o -4 addr show | grep -F ''{0}'' | awk ''{{print $2}}'' | head -n 1)' -f $CIDR)
        'test -n "$iface"'
        ('tc qdisc replace dev "$iface" root netem limit 10000 delay {0}ms' -f $DelayMs)
        'tc -s qdisc show dev "$iface"'
    ) -join "`n"
    docker exec $Container sh -lc $script
    Assert-Docker
}
function Cleanup {
    docker compose -f $ComposeFile down -v | Out-Null
    Assert-Docker
}
if ($ModuleFixture) {
    $env:HARNESS_MODULE_FIXTURE = '1'
    $env:HARNESS_AGENT_DOCKERFILE = 'scripts/http-relay-perf/Dockerfile.node'
    $env:HARNESS_AGENT_IMAGE = 'nre-proxy-perf-node:local'
}
if ($AgentImage) { $env:HARNESS_AGENT_IMAGE = $AgentImage }
$env:HARNESS_WAIT_FOR_START = '1'
$cliDelay = Get-Delay 'HARNESS_DELAY_CLI_TO_HTTP_MS' 30
$backendDelay = Get-Delay 'HARNESS_DELAY_HTTP_TO_BACKEND_MS' (Get-Delay 'HARNESS_NETEM_DELAY_MS' 10)
$relayDelay = Get-Delay 'HARNESS_DELAY_RELAY_MS' 0
$statsRows = [System.Collections.Generic.List[string]]::new()
if (-not $SkipStats) { $statsRows.Add('ts,name,cpu,mem,net') }
$exitCode = 1
Cleanup
try {
    if ($SkipBuild) { docker compose -f $ComposeFile up -d --no-build | Out-Null }
    else { docker compose -f $ComposeFile up -d --build | Out-Null }
    Assert-Docker

    Apply-NetemDelayOnCIDR 'nre-perf' '172.31.1.2/24' $cliDelay
    Apply-NetemDelayOnCIDR 'nre-agent-a' '172.31.1.10/24' $cliDelay
    foreach ($item in @(@('nre-agent-a','172.31.2.10/24'), @('nre-relay-b','172.31.2.11/24'), @('nre-relay-c','172.31.2.12/24'))) {
        Apply-NetemDelayOnCIDR $item[0] $item[1] $relayDelay
    }
    foreach ($item in @(@('nre-agent-a','172.31.3.10/24'), @('nre-relay-b','172.31.3.11/24'), @('nre-relay-c','172.31.3.12/24'), @('nre-backend-b','172.31.3.13/24'))) {
        Apply-NetemDelayOnCIDR $item[0] $item[1] $backendDelay
    }
    docker exec nre-perf wget -q -O - http://127.0.0.1:8080/start
    Assert-Docker
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ($true) {
        $running = docker inspect -f '{{.State.Running}}' nre-perf
        Assert-Docker
        if ($running -ne 'true') { break }
        if ((Get-Date) -gt $deadline) { throw "Performance suite exceeded $TimeoutSeconds seconds" }
        if (-not $SkipStats) {
            docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.NetIO}}' nre-agent-a nre-relay-b nre-relay-c nre-backend-b nre-perf |
                ForEach-Object { $statsRows.Add("$(Get-Date -Format o),$_") }
            Assert-Docker
        }
        Start-Sleep -Milliseconds 500
    }
    docker logs nre-perf
    Assert-Docker
    $exitCode = [int](docker inspect -f '{{.State.ExitCode}}' nre-perf)
    Assert-Docker
}
finally {
    if ($exitCode -ne 0) {
        foreach ($name in @('nre-agent-a','nre-relay-b','nre-relay-c','nre-backend-b')) { docker logs --tail 20 $name 2>&1 }
    }
    if (-not $SkipStats -and $statsRows.Count -gt 1) { $statsRows | Set-Content $StatsFile }
    if (-not $KeepUp) { Cleanup }
}
exit $exitCode
