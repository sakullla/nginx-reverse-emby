# 开发与构建

本页给项目开发者使用，普通部署用户不需要执行这些命令。

## 前置要求

- Go 1.26.4+
- Node.js 24+
- Docker

## 控制面

```bash
cd panel/backend-go
go run ./cmd/nre-control-plane
go test ./...
```

## 前端

```bash
cd panel/frontend
npm ci
npm run dev
npm run build
npm run test
```

开发服务器会把 `/panel-api` 代理到控制面。用于截图 / 教程时，应使用真实控制面服务的生产前端，而不是 Vite mock 数据。

## Go Agent

```bash
cd go-agent
go run ./cmd/nre-agent
make build
go test ./...
```

默认发布构建不包含 pprof。需要 `NRE_PPROF_ADDR` 时，用 debug tags 启用：

```bash
go run -tags debug ./cmd/nre-agent
go build -tags debug ./cmd/nre-agent
```

## Docker 构建

```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

Dockerfile 使用多阶段构建，会交叉编译 Go agent 到 Linux / macOS 的 AMD64 / ARM64 平台。Windows 客户端包不随控制面镜像构建或公开，通过 GitHub Release 分发。

## 版本更新

`desired_version` 由控制面下发，用于驱动 Go agent 升级：

1. 在控制面 **版本策略** 页面创建策略，设置通道、`desired_version` 和各平台安装包。
2. 准备安装包来源：Linux / macOS 可使用控制面公开的 agent 资产，或在策略中配置自托管 URL 与 `sha256`。
3. Agent 心跳同步时收到 `desired_version`、`version_package` 和 `version_sha256`。
4. 平台匹配且包信息完整时，Agent 下载、校验 SHA256 并执行更新（原子替换二进制后重启进程）。

未匹配到当前平台的版本包时，控制面保留 `desired_version`，但 Agent 不执行更新。内嵌 local agent 不参与自更新。

## HTTP / Relay 吞吐测试

```powershell
./scripts/http-relay-perf/run.ps1
```

这个 harness 用 Docker Compose 跑真实 `nre-agent`，对比 HTTP 直连和 Relay 入口下载吞吐。默认延迟模型与 `relay-perf` 一致，`CLI -> HTTP` 以及 `HTTP -> backend` 两段都会走 `tc netem`。可用下面的变量覆盖：

```text
HARNESS_DELAY_CLI_TO_HTTP_MS
HARNESS_DELAY_HTTP_TO_BACKEND_MS
HARNESS_NETEM_DELAY_MS
```

## 纯 Go 控制面验证

替换生产镜像前，可先用复制的 `panel/data` 做影子验证：

```bash
cd panel/backend-go && go test ./...
cd ../../go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```

验证要点：

- `/panel-api/health`、`/panel-api/info`、`/panel-api/agents` 等端点返回 2xx。
- `/panel-api/agents` 中能看到 `id=local` 且 `is_local=true`。
- `join-agent.sh` 可以正常下载 Agent 二进制。
