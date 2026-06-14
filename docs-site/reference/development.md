# 开发与构建

面向想修改源码或从源码构建的开发者。如果只是部署使用，不需要这一页。

## 前置要求

| 工具 | 最低版本 |
|------|----------|
| Go | 1.26.4+ |
| Node.js | 24+ |
| Docker | 近期版本 |

## 控制面（后端）

```bash
cd panel/backend-go
go run ./cmd/nre-control-plane   # 本地运行
go test ./...                     # 运行测试
```

## 前端

```bash
cd panel/frontend
npm ci          # 安装依赖
npm run dev     # 开发服务器，热重载
npm run build   # 生产构建
```

Vite 开发服务器自动把 `/panel-api` 代理到控制面。截图和教程请用真实控制面配生产构建，不要用 Vite 开发服务器的模拟数据。

## Go Agent

```bash
cd go-agent
go run ./cmd/nre-agent      # 本地运行
go test ./...                # 运行测试
make build                   # 构建发布二进制
```

调试构建（带 pprof）：

```bash
go run -tags debug ./cmd/nre-agent
go build -tags debug ./cmd/nre-agent
```

## Docker 构建

```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

Dockerfile 用多阶段构建，在 AMD64 和 ARM64 上为 Linux 和 macOS 交叉编译 Go Agent。Windows Agent 通过 GitHub Releases 分发。

## 版本更新

控制面通过 `desired_version` 远程推送 Agent 更新：

1. 在 **版本策略** 页面创建策略，设置频道、目标版本和平台包
2. 提供各平台安装包的 URL 和 SHA256
3. Agent 在心跳时收到版本信息
4. Agent 下载包、校验 SHA256、原子替换二进制后重启

local Agent 不参与自更新。

## 性能测试

Windows PowerShell：

```powershell
./scripts/http-relay-perf/run.ps1
```

该工具用 Docker Compose 运行真实 `nre-agent` 实例，对比 HTTP 直连和中继吞吐量。可用环境变量覆盖延迟参数。

## 控制面验证

```bash
cd panel/backend-go && go test ./...
cd ../../go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```

| 检查项 | 预期 |
|--------|------|
| `/panel-api/health` | 2xx |
| `/panel-api/info` | 2xx |
| `/panel-api/agents` | 2xx，包含 `local` 且 `is_local=true` |
| `join-agent.sh` | 可从公共资源路由下载 |
