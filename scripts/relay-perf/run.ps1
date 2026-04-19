param(
    [string]$ComposeFile = (Join-Path $PSScriptRoot 'docker-compose.yaml'),
    [string]$StatsFile = (Join-Path $PSScriptRoot 'docker-stats.csv'),
    [switch]$KeepUp
)

$ErrorActionPreference = 'Stop'

function Cleanup {
    docker compose -f $ComposeFile down -v | Out-Null
}

Cleanup
docker compose -f $ComposeFile up -d --build | Out-Null

'ts,name,cpu,mem,net' | Set-Content $StatsFile
try {
    while ($true) {
        $running = docker inspect -f '{{.State.Running}}' nre-perf 2>$null
        if ($LASTEXITCODE -ne 0 -or $running -ne 'true') {
            break
        }

        docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.NetIO}}' `
            nre-agent-a nre-relay-a nre-agent-b nre-perf 2>$null |
            ForEach-Object { "$(Get-Date -Format o),$_" | Add-Content $StatsFile }

        Start-Sleep -Milliseconds 500
    }

    docker logs nre-perf
}
finally {
    if (-not $KeepUp) {
        Cleanup
    }
}
