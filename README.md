# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

用于集中管理 HTTP / L4 规则、证书与 Agent 的反向代理控制面。

## Runtime Architecture

- 控制面：Go control-plane + Vue frontend
- 执行面：Go `go-agent`
- 本地节点：Master 容器默认内嵌 local agent 能力
- 同步模型：heartbeat pull
- 版本更新：Master 下发 `desired_version`

当前仓库的 Docker / Compose 默认启动的是**纯 Go 控制面**；Master 容器内默认启用本地 agent 能力，远端节点仍通过公开的 agent 资产单独安装或升级。

## Quick Start

### Docker Compose

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

默认访问：

- `http://<服务器 IP>:8080`

请先在 `docker-compose.yaml` 中设置：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
```

然后启动：

```bash
docker compose up -d
```

## Streaming / Relay Resilience Env

以下变量同时作用于：

- Master 容器内嵌 local agent（通过 control-plane 下发到本地执行面）
- 独立部署的 `go-agent`

时间类变量使用 Go `time.ParseDuration` 格式（如 `500ms`、`5s`、`2m`）。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | HTTP upstream 连接建立超时。 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | HTTPS upstream TLS 握手超时。 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待 upstream 响应头超时。 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | upstream 空闲连接回收超时。 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | upstream TCP keepalive 间隔。 |
| `NRE_HTTP_STREAM_RESUME_ENABLED` | `true` | 是否启用中断流恢复。 |
| `NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS` | `2` | 单次请求最多追加恢复次数（正整数）。 |
| `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` | `1` | 同一 backend 的额外重试次数（`1` 代表最多 2 次尝试：初次 + 1 次重试）。仅对 retry-safe 方法生效，且只在上游返回响应前的可重试 transport/read 错误上触发。 |
| `NRE_BACKEND_FAILURE_BACKOFF_BASE` | `1s` | backend 连续失败退避起始值。 |
| `NRE_BACKEND_FAILURE_BACKOFF_LIMIT` | `60s`（未显式覆盖时） | backend 连续失败退避上限（指数退避封顶）；显式设置时可按需改为 `15s` 或其他值。 |
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | relay 上游拨号超时。 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | relay 握手超时。 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | relay 单帧读写超时。 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | relay 空闲连接超时。 |

`NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` 不是通用 POST/5xx 重试开关：不会对 POST 生效，也不会在已收到普通 HTTP 5xx 响应后做同 backend 重放。Master 内嵌 local agent 场景要求正整数；独立 `go-agent` 允许设置为 `0`（仅首发，不做同 backend 额外重试）。

### Resumable Streaming Scope

中断流恢复是保守开启的，仅覆盖以下场景：

- `GET` 全量响应（`200`，且原请求无 `Range`）
- `GET` 单区间响应（`206`，且请求是单个 byte-range）

并且 upstream 必须持续满足 `Accept-Ranges: bytes`，且返回稳定校验器（强 `ETag` 或 `Last-Modified`）。恢复请求会携带 `Range` / `If-Range`；若校验器或 `Content-Range` 不一致、或变成 multipart ranges，则停止拼接并返回错误，不会盲目续传。

## Join Agent

独立 Agent 默认数据目录为 `/var/lib/nre-agent`。控制面默认数据目录为 `/opt/nginx-reverse-emby/panel/data`。

### Linux

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

### macOS

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

### Windows

Windows 执行面同样使用 Go agent，但当前控制面镜像默认只公开 Linux / macOS agent 资产。Windows 节点请使用自行构建或单独发布的 `nre-agent.exe` 手工安装。

推荐步骤：

1. 在控制面获取 `register token`。
2. 准备 Windows `nre-agent.exe` 安装包。
3. 手工向控制面注册 agent，或先在其他平台完成注册后复用生成的 `agent_token`。
4. 在 Windows 服务或计划任务中启动 `nre-agent.exe`，并确保能访问控制面 URL。

常见参数：

- `--agent-name edge-01`
- `--tags edge,emby`
- `--agent-url https://edge-01.example.com`
- `--binary-url https://example.com/custom/nre-agent`
- `--data-dir /var/lib/nre-agent`

### Migrate From Legacy `main` Agent

旧 `main` 版本的轻量 Agent 节点迁移到当前 Go agent 时，先在旧控制面执行 `导出备份`，升级控制面并 `导入备份`，然后在每台旧 Agent 机器执行：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

默认会从 `/opt/nginx-reverse-emby-agent` 读取旧 lightweight-Agent 目录，复用原 `agent_token`，切换到新的 `/var/lib/nre-agent`，并在新服务验证通过后清理旧 runtime、旧 nginx 服务与动态配置、以及旧 `.acme.sh` 续期状态。

### Uninstall Agent

如需从 VPS 上完全移除本地 Go agent 运行时，可执行：

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

安装脚本在 Linux 和 macOS 上都会安装这个固定卸载入口，便于像 k3s 一样在主机上直接卸载，无需重新下载 join 脚本。

也可继续使用在线卸载方式：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

该命令只清理本机 service、数据目录、legacy lightweight-Agent 残留和 legacy nginx runtime 残留；控制面里的 agent 记录仍需手动删除。

## Backup Import / Export

系统设置里统一提供：

- `导出备份`
- `导入备份`

旧 `main` 控制面和当前纯 Go 控制面都使用同一个可移植备份包格式，支持跨架构导出后再导入。导入时会跳过冲突项并返回详细报告；证书 PEM 和私钥材料也会被一并包含，便于从旧版本无感迁移到新版本。

## Desired Version Updates

`desired_version` 由控制面下发，用来驱动 Go agent 的版本升级。推荐流程：

1. 在控制面中为 agent 或版本策略设置 `desired_version`。
2. 为目标平台准备安装包来源：
   - 直接使用控制面公开的 agent 资产；或
   - 在版本策略中配置自托管下载 URL 与 `sha256`。
3. agent 在心跳同步时会收到 `desired_version`、`version_package` 与 `version_sha256`。
4. 当平台匹配且包信息完整时，agent 下载、校验并执行更新，随后在后续心跳中上报新的 `version`。

如果没有匹配到当前平台的版本包，控制面会继续保留 `desired_version`，但 agent 不会执行更新。

## Notes

- 控制面容器默认监听 `8080`。
- `/panel-api/*` 由 Go control-plane 直接提供，不再依赖 Nginx 做控制面反代。
- Go agent 二进制会作为公开资产暴露在 `/panel-api/public/agent-assets/` 下，供 `join-agent.sh` 下载当前已打包的平台版本。
- `deploy.sh` 仍保留为历史兼容的独立 Nginx 节点脚本，不是默认运行时路径。
- `NRE_HTTP3_ENABLED=true` 会让 HTTPS 入口同时启用 HTTP/3。
- Relay listener 支持 `transport_mode=tls_tcp|quic`，默认 `tls_tcp`。`tls_tcp` 现为单外层 TLS 连接承载多逻辑流的复用隧道。
- Relay listener 支持 `obfs_mode=off|early_window_v2`，且仅对 `tls_tcp` 生效。
- UDP relay 支持通过 Relay 中继，`quic` 走流内包帧，`tls_tcp` 走 UoT。
- `tls_tcp` 当前支持长连接复用和多路复用；由于 Go 标准库 `crypto/tls` 对普通 `tls.Conn` 不支持通用 early data，真实 TLS 0-RTT 仅在 `quic` 路径可用，`tls_tcp` 未启用真实 0-RTT。

## Verification

常用验证命令：

```bash
cd panel/backend-go && go test ./...
cd panel/backend-go && go run ./cmd/nre-control-plane
cd panel/frontend && npm run build
cd go-agent && go test ./...
docker build -t nginx-reverse-emby .
```


## Pure Go Cutover Verification

在替换生产 master 镜像前，先使用复制出来的 `panel/data` 做一次影子验证：

```bash
cd panel/backend-go && go test ./...
cd ../../go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```

建议额外执行：

```bash
docker compose config
```

要求：

- `/panel-api/health`、`/panel-api/info`、`/panel-api/agents`、`/panel-api/agents/local/rules`、`/panel-api/certificates`、`/panel-api/version-policies`、`/panel-api/public/join-agent.sh` 全部返回 2xx
- `/panel-api/agents` 中必须能看到 `id=local` 且 `is_local=true`
- `join-agent.sh` 必须继续指向 `/panel-api/public/agent-assets/`
- 复制数据中的 `panel.db`、managed certificate material 与本地状态都能被纯 Go 控制面直接读取
