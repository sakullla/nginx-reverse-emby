# nre_client

A new Flutter project.

## Getting Started

This project is a starting point for a Flutter application.

A few resources to get you started if this is your first Flutter project:

- [Learn Flutter](https://docs.flutter.dev/get-started/learn-flutter)
- [Write your first Flutter app](https://docs.flutter.dev/get-started/codelab)
- [Flutter learning resources](https://docs.flutter.dev/reference/learning-resources)

For help getting started with Flutter development, view the
[online documentation](https://docs.flutter.dev/), which offers tutorials,
samples, guidance on mobile development, and a full API reference.

## Windows Local Agent Runtime

The Windows client can start and stop a local `nre-agent.exe` after the client
is registered with a control plane.

Build the agent binary locally:

```powershell
cd ..\..\go-agent
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -o ..\clients\flutter\build\agent\nre-agent.exe .\cmd\nre-agent
```

Install it for the client:

```powershell
$installDir = Join-Path $env:LOCALAPPDATA 'NRE Client\agent'
New-Item -ItemType Directory -Force -Path $installDir
Copy-Item .\build\agent\nre-agent.exe (Join-Path $installDir 'nre-agent.exe') -Force
```

After registration, open Runtime and click `Start Agent`. Logs are written
under `%LOCALAPPDATA%\NRE Client\logs\nre-agent.log`, and agent data is stored
under `%LOCALAPPDATA%\NRE Client\agent-data`.
