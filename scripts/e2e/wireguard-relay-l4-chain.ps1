param(
  [string]$Image = 'nre-wg-chain:e2e',
  [string]$Network = 'nre-wg-chain-e2e-net',
  [string]$Prefix = 'nre-wg-chain-e2e',
  [int]$HostPort = 18088,
  [switch]$KeepResources
)

$ErrorActionPreference = 'Stop'

$PanelToken = 'e2e-panel-token'
$RegisterToken = 'e2e-register-token'
$AgentBinary = '/opt/nginx-reverse-emby/panel/public/agent-assets/nre-agent-linux-amd64'
$ClientConfigPath = Join-Path $PSScriptRoot 'wireguard-relay-l4-chain-client.conf'
$AgentIDs = @{}

function Write-Section {
  param([string]$Text)
  Write-Host "==> $Text"
}

function Get-ContainerName {
  param([string]$Name)
  return "$Prefix-$Name"
}

function Remove-ContainerIfExists {
  param([string]$Name)
  $existing = & docker ps -a --filter "name=^$Name$" --format "{{.Names}}"
  if ($existing -and ($existing | Select-Object -First 1) -eq $Name) {
    & docker rm -f $Name *> $null
  }
  $global:LASTEXITCODE = 0
}

function Cleanup {
  if ($KeepResources) {
    return
  }

  foreach ($name in @(
    (Get-ContainerName 'wg-client'),
    (Get-ContainerName 'backend'),
    (Get-ContainerName 'b'),
    (Get-ContainerName 'relay-c'),
    (Get-ContainerName 'relay-b'),
    (Get-ContainerName 'relay-a'),
    (Get-ContainerName 'a'),
    (Get-ContainerName 'master')
  )) {
    Remove-ContainerIfExists -Name $name
  }

  $networkExists = & docker network ls --filter "name=^$Network$" --format "{{.Name}}"
  if ($networkExists -and ($networkExists | Select-Object -First 1) -eq $Network) {
    & docker network rm $Network *> $null
  }

  if (Test-Path $ClientConfigPath) {
    Remove-Item -LiteralPath $ClientConfigPath -Force
  }
}

function Fail-WithLogs {
  param([string]$Message)

  Write-Host "FAIL $Message" -ForegroundColor Red
  foreach ($name in @(
    (Get-ContainerName 'master'),
    (Get-ContainerName 'a'),
    (Get-ContainerName 'relay-a'),
    (Get-ContainerName 'relay-b'),
    (Get-ContainerName 'relay-c'),
    (Get-ContainerName 'b'),
    (Get-ContainerName 'backend')
  )) {
    $existing = & docker ps -a --filter "name=^$name$" --format "{{.Names}}"
    if ($existing -and ($existing | Select-Object -First 1) -eq $name) {
      Write-Host "--- logs: $name ---"
      & docker logs $name 2>$null
    }
  }
  throw $Message
}

function Ensure-Network {
  $existing = & docker network ls --filter "name=^$Network$" --format "{{.Name}}"
  if (-not $existing -or ($existing | Select-Object -First 1) -ne $Network) {
    & docker network create $Network | Out-Null
  }
}

function Invoke-PanelJson {
  param(
    [string]$Method,
    [string]$Path,
    [object]$Body = $null
  )

  $uri = "http://127.0.0.1:$HostPort/panel-api$Path"
  $headers = @{ 'X-Panel-Token' = $PanelToken }

  if ($null -eq $Body) {
    return Invoke-RestMethod -Method $Method -Uri $uri -Headers $headers
  }

  return Invoke-RestMethod -Method $Method -Uri $uri -Headers $headers -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Depth 20 -Compress)
}

function Invoke-RegisterAgent {
  param(
    [string]$AgentName,
    [string[]]$Capabilities = @('http_rules', 'l4', 'relay_quic', 'wireguard')
  )

  $payload = @{
    name         = $AgentName
    agent_token  = "token-$AgentName"
    capabilities = $Capabilities
  } | ConvertTo-Json -Depth 10 -Compress

  $uri = "http://127.0.0.1:$HostPort/panel-api/agents/register"
  $response = Invoke-RestMethod -Method Post -Uri $uri -Headers @{ 'X-Register-Token' = $RegisterToken } -ContentType 'application/json' -Body $payload
  $AgentIDs[$AgentName] = $response.agent.id
  return $response
}

function Wait-HTTP {
  param(
    [string]$Uri,
    [int]$Retries = 60
  )

  for ($i = 0; $i -lt $Retries; $i++) {
    try {
      Invoke-WebRequest -UseBasicParsing -Uri $Uri -TimeoutSec 2 | Out-Null
      return
    } catch {
      Start-Sleep -Seconds 1
    }
  }

  throw "Timed out waiting for $Uri"
}

function Wait-AgentStatus {
  param(
    [string[]]$AgentIDs,
    [int]$Retries = 120
  )

  for ($i = 0; $i -lt $Retries; $i++) {
    $agents = (Invoke-PanelJson -Method Get -Path '/agents').agents
    $pending = @()
    foreach ($agentID in $AgentIDs) {
      $agent = $agents | Where-Object { $_.id -eq $agentID } | Select-Object -First 1
      if ($null -eq $agent) {
        $pending += "${agentID}:missing"
        continue
      }
      $desired = [int]$agent.desired_revision
      $current = [int]$agent.current_revision
      $applied = [int]$agent.last_apply_revision
      if ($agent.last_apply_status -ne 'success' -or ($desired -gt 0 -and ($current -lt $desired -or $applied -lt $desired))) {
        $pending += "${agentID}:status=$($agent.last_apply_status),desired=$desired,current=$current,applied=$applied"
      }
    }
    if ($pending.Count -eq 0) {
      return
    }
    Start-Sleep -Seconds 1
  }

  $agents = (Invoke-PanelJson -Method Get -Path '/agents').agents
  $summary = $agents | Where-Object { $AgentIDs -contains $_.id } | ConvertTo-Json -Depth 10
  throw "Agents did not reach success: $summary"
}

function Start-Agent {
  param([string]$AgentName)

  $agentID = [string]$AgentIDs[$AgentName]
  if ([string]::IsNullOrWhiteSpace($agentID)) {
    throw "Missing registered agent id for $AgentName"
  }

  $name = Get-ContainerName $AgentName
  Remove-ContainerIfExists -Name $name

  docker run -d --name $name --network $Network `
    --entrypoint $AgentBinary `
    -e NRE_MASTER_URL="http://$(Get-ContainerName 'master'):8080" `
    -e NRE_AGENT_ID=$agentID `
    -e NRE_AGENT_NAME=$AgentName `
    -e NRE_AGENT_TOKEN="token-$AgentName" `
    -e NRE_DATA_DIR='/var/lib/nre-agent' `
    -e NRE_TRAFFIC_STATS_ENABLED=false `
    $Image | Out-Null
}

function Start-Backend {
  $name = Get-ContainerName 'backend'
  Remove-ContainerIfExists -Name $name
  docker run -d --name $name --network $Network python:3.13-alpine `
    sh -lc "python - <<'PY'
import socket
import socketserver
import threading

class TCPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        data = self.request.recv(4096)
        if b'GET /' in data:
            body = b'wg-e2e-ok'
            self.request.sendall(b'HTTP/1.1 200 OK\r\nContent-Length: ' + str(len(body)).encode() + b'\r\nConnection: close\r\n\r\n' + body)
        else:
            self.request.sendall(b'wg-e2e-ok')

class TCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True

def udp_server():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(('0.0.0.0', 18081))
    while True:
        data, addr = sock.recvfrom(4096)
        sock.sendto(b'udp-e2e-ok', addr)

threading.Thread(target=udp_server, daemon=True).start()
with TCPServer(('0.0.0.0', 18080), TCPHandler) as srv:
    srv.serve_forever()
PY" | Out-Null
}

function Get-FirstWireGuardProfile {
  param([string]$AgentID)
  $profiles = Invoke-PanelJson -Method Get -Path "/agents/$AgentID/wireguard-profiles"
  return $profiles.profiles | Select-Object -First 1
}

function Get-ContainerIPAddress {
  param([string]$Name)

  $ip = (& docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $Name).Trim()
  if ([string]::IsNullOrWhiteSpace($ip)) {
    throw "Unable to determine container IP for $Name"
  }
  return $ip
}

function Update-WireGuardProfile {
  param(
    [string]$AgentID,
    [int]$ProfileID,
    [hashtable]$Body
  )
  return (Invoke-PanelJson -Method Put -Path "/agents/$AgentID/wireguard-profiles/$ProfileID" -Body $Body).profile
}

function New-RelayListener {
  param(
    [string]$AgentID,
    [hashtable]$Body
  )
  return (Invoke-PanelJson -Method Post -Path "/agents/$AgentID/relay-listeners" -Body $Body).listener
}

function New-L4Rule {
  param(
    [string]$AgentID,
    [hashtable]$Body
  )
  return (Invoke-PanelJson -Method Post -Path "/agents/$AgentID/l4-rules" -Body $Body).rule
}

function New-WireGuardClientConfig {
  param([string]$AgentID)

  $profile = Get-FirstWireGuardProfile -AgentID $AgentID
  if ($null -eq $profile) {
    throw "No WireGuard profile found for agent $AgentID"
  }

  $client = (Invoke-PanelJson -Method Post -Path "/agents/$AgentID/wireguard-profiles/$($profile.id)/clients" -Body @{ name = 'docker-client' }).client
  $uri = "http://127.0.0.1:$HostPort/panel-api/agents/$AgentID/wireguard-profiles/$($profile.id)/clients/$($client.id)/config"
  Invoke-WebRequest -UseBasicParsing -Uri $uri -Headers @{ 'X-Panel-Token' = $PanelToken } -OutFile $ClientConfigPath
  return $ClientConfigPath
}

function Start-WireGuardClientProbe {
  param(
    [string]$ConfigPath,
    [string]$TargetHost
  )

  $name = Get-ContainerName 'wg-client'
  Remove-ContainerIfExists -Name $name

  $probeScript = @'
set -eu
cp /tmp/wg0.conf /tmp/wg0-runtime.conf
chmod 600 /tmp/wg0-runtime.conf
cleanup() {
  wg-quick down /tmp/wg0-runtime.conf >/dev/null 2>&1 || true
}
trap cleanup EXIT
wg-quick up /tmp/wg0-runtime.conf >/tmp/wg-up.log 2>&1

i=1
while [ "$i" -le 30 ]; do
  if wget -q -O - --timeout=3 http://TARGET_HOST_PLACEHOLDER:18080/ | grep -q 'wg-e2e-ok'; then
    echo PASS tcp wg-client '->' A transparent '->' RelayA TLS '->' RelayB WG '->' RelayC QUIC '->' B
    break
  fi
  i=$((i + 1))
  sleep 1
done
if [ "$i" -gt 30 ]; then
  cat /tmp/wg-up.log >&2 || true
  echo 'TCP probe failed' >&2
  exit 1
fi

i=1
while [ "$i" -le 30 ]; do
  if printf 'udp-e2e' | nc -u -w 3 TARGET_HOST_PLACEHOLDER 18081 | grep -q 'udp-e2e-ok'; then
    echo PASS udp wg-client '->' A transparent '->' RelayA TLS '->' RelayB WG '->' RelayC QUIC '->' B
    exit 0
  fi
  i=$((i + 1))
  sleep 1
done
cat /tmp/wg-up.log >&2 || true
echo 'UDP probe failed' >&2
exit 1
'@
  $probeScript = $probeScript.Replace('TARGET_HOST_PLACEHOLDER', $TargetHost)

  & docker run --rm --name $name --network $Network `
    --cap-add NET_ADMIN --device /dev/net/tun `
    --sysctl net.ipv4.conf.all.src_valid_mark=1 `
    -v "${ConfigPath}:/tmp/wg0.conf:ro" `
    --entrypoint /bin/sh `
    ghcr.io/linuxserver/wireguard:latest `
    -lc $probeScript
  if ($LASTEXITCODE -ne 0) {
    throw 'WireGuard client probe failed'
  }
}

Cleanup
try {
  Write-Section "Preparing Docker network"
  Ensure-Network

  Write-Section "Starting control plane"
  $master = Get-ContainerName 'master'
  Remove-ContainerIfExists -Name $master
  docker run -d --name $master --network $Network `
    -p "${HostPort}:8080" `
    -e NRE_PANEL_TOKEN=$PanelToken `
    -e NRE_REGISTER_TOKEN=$RegisterToken `
    -e NRE_ENABLE_LOCAL_AGENT=false `
    -e NRE_TRAFFIC_STATS_ENABLED=false `
    $Image | Out-Null
  Wait-HTTP -Uri "http://127.0.0.1:$HostPort/panel-api/health"

  Write-Section "Registering agents"
  foreach ($agentName in @('a', 'relay-a', 'relay-b', 'relay-c', 'b')) {
    Invoke-RegisterAgent -AgentName $agentName | Out-Null
  }

  Write-Section "Starting remote agents"
  foreach ($agentName in @('a', 'relay-a', 'relay-b', 'relay-c', 'b')) {
    Start-Agent -AgentName $agentName
  }

  Write-Section "Starting backend"
  Start-Backend

  Write-Section "Waiting for initial heartbeats"
  Start-Sleep -Seconds 6

  Write-Section "Creating relay listeners"
  $relayA = New-RelayListener -AgentID $AgentIDs['relay-a'] -Body @{
    name               = 'relay-a-tls'
    listen_host        = '0.0.0.0'
    listen_port        = 19001
    public_host        = (Get-ContainerName 'relay-a')
    public_port        = 19001
    transport_mode     = 'tls_tcp'
    certificate_source = 'auto_relay_ca'
    trust_mode_source  = 'auto'
  }

  $relayB = New-RelayListener -AgentID $AgentIDs['relay-b'] -Body @{
    name               = 'relay-b-wg'
    listen_port        = 19002
    transport_mode     = 'wireguard'
    certificate_source = 'auto_relay_ca'
    trust_mode_source  = 'auto'
  }

  $relayC = New-RelayListener -AgentID $AgentIDs['relay-c'] -Body @{
    name               = 'relay-c-quic'
    listen_host        = '0.0.0.0'
    listen_port        = 19003
    public_host        = (Get-ContainerName 'relay-c')
    public_port        = 19003
    transport_mode     = 'quic'
    certificate_source = 'auto_relay_ca'
    trust_mode_source  = 'auto'
  }

  Write-Section "Creating B-side TCP and UDP L4 rules"
  New-L4Rule -AgentID $AgentIDs['b'] -Body @{
    name        = 'b-tcp-to-backend'
    protocol    = 'tcp'
    listen_mode = 'tcp'
    listen_host = '0.0.0.0'
    listen_port = 18080
    backends    = @(@{ host = (Get-ContainerName 'backend'); port = 18080 })
  } | Out-Null

  New-L4Rule -AgentID $AgentIDs['b'] -Body @{
    name        = 'b-udp-to-backend'
    protocol    = 'udp'
    listen_mode = 'tcp'
    listen_host = '0.0.0.0'
    listen_port = 18081
    backends    = @(@{ host = (Get-ContainerName 'backend'); port = 18081 })
  } | Out-Null

  Write-Section "Creating A-side transparent WireGuard L4 rules"
  $layers = @(@($relayA.id), @($relayB.id), @($relayC.id))
  New-L4Rule -AgentID $AgentIDs['a'] -Body @{
    name                   = 'a-wg-transparent-tcp'
    protocol               = 'tcp'
    listen_mode            = 'wireguard'
    wireguard_inbound_mode = 'transparent'
    listen_host            = '0.0.0.0'
    listen_port            = 18080
    relay_layers           = $layers
    backends               = @()
  } | Out-Null

  New-L4Rule -AgentID $AgentIDs['a'] -Body @{
    name                   = 'a-wg-transparent-udp'
    protocol               = 'udp'
    listen_mode            = 'wireguard'
    wireguard_inbound_mode = 'transparent'
    listen_host            = '0.0.0.0'
    listen_port            = 18081
    relay_layers           = $layers
    backends               = @()
  } | Out-Null

  Write-Section "Setting A default WireGuard profile public endpoint"
  $entryProfile = Get-FirstWireGuardProfile -AgentID $AgentIDs['a']
  if ($null -eq $entryProfile) {
    throw 'Default WireGuard profile for agent A was not created'
  }
  Update-WireGuardProfile -AgentID $AgentIDs['a'] -ProfileID $entryProfile.id -Body @{
    name            = $entryProfile.name
    mode            = $entryProfile.mode
    private_key     = $entryProfile.private_key
    listen_port     = $entryProfile.listen_port
    public_endpoint = "$(Get-ContainerName 'a'):51820"
    addresses       = @($entryProfile.addresses)
    dns             = @($entryProfile.dns)
    mtu             = $entryProfile.mtu
    enabled         = $entryProfile.enabled
    tags            = @($entryProfile.tags)
  } | Out-Null

  Write-Section "Waiting for apply status"
  Wait-AgentStatus -AgentIDs @($AgentIDs['a'], $AgentIDs['relay-a'], $AgentIDs['relay-b'], $AgentIDs['relay-c'], $AgentIDs['b'])

  Write-Section "Fetching generated client config"
  $configPath = New-WireGuardClientConfig -AgentID $AgentIDs['a']

  Write-Section "Running real WireGuard client probes"
  $targetHost = Get-ContainerIPAddress -Name (Get-ContainerName 'b')
  Start-WireGuardClientProbe -ConfigPath $configPath -TargetHost $targetHost
}
catch {
  Fail-WithLogs -Message $_
}
finally {
  Cleanup
}
