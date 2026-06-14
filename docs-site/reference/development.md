# 开发与构建

本页面向希望修改或从源码构建项目的开发者。如果你只是部署，则不需要这些命令。

## 前置要求

| 工具 | 最低版本 |
|------|----------|
| Go | 1.26.4+ |
| Node.js | 24+ |
| Docker | 任何近期版本 |

## 控制平面（后端）

本地运行 Go 控制平面：

```bash
cd panel/backend-go
go run ./cmd/nre-control-plane
```

运行测试套件：

```bash
cd panel/backend-go
go test ./...
```

## 前端（Web UI）

安装依赖并启动开发服务器：

```bash
cd panel/frontend
npm ci
npm run dev
```

其他常用命令：

| 命令 | 作用 |
|------|------|
| `npm run dev` | 启动 Vite 开发服务器，支持热重载。 |
| `npm run build` | 构建生产包。 |
| `npm run test` | 运行前端测试套件。 |

开发服务器自动将 `/panel-api` 请求代理到控制平面。对于截图或教程，请使用真实的控制平面配合生产前端构建，而不是使用模拟数据的 Vite 开发服务器。

## Go 代理

本地运行代理：

```bash
cd go-agent
go run ./cmd/nre-agent
```

构建发布二进制文件：

```bash
cd go-agent
make build
```

运行代理测试：

```bash
cd go-agent
go test ./...
```

### 调试构建（带 pprof）

发布构建不包含 pprof。要启用调试端点（`NRE_PPROF_ADDR`），请使用 debug 标签构建：

```bash
go run -tags debug ./cmd/nre-agent
go build -tags debug ./cmd/nre-agent
```

## Docker 构建

构建控制平面镜像并用 Compose 启动：

```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

Dockerfile 使用多阶段构建，并为 Linux 和 macOS 在 AMD64 和 ARM64 上交叉编译 Go 代理。Windows 代理二进制文件不会构建到控制平面镜像中；它们通过 GitHub Releases 分发。

## 代理版本更新

控制平面可以使用 `desired_version` 远程向代理推送新版本：

| 步骤 | 发生了什么 |
|------|------------|
| 1. 创建策略 | 在控制面板的 **版本策略** 页面，创建一个带有频道、`desired_version` 和平台包的策略。 |
| 2. 准备包 | 对于 Linux/macOS，使用控制平面的公共代理资源或提供带有 `sha256` 的自托管 URL。 |
| 3. 代理同步 | 每次心跳时，代理接收 `desired_version`、`version_package` 和 `version_sha256`。 |
| 4. 代理更新 | 如果平台匹配且包信息完整，代理下载包、验证 SHA256 校验和，并执行原子二进制替换后重启进程。 |

如果没有包匹配代理的平台，控制平面保持分配 `desired_version`，但代理不会更新。主控上的内置 `local` 代理不参与自更新。

## HTTP / 中继性能测试

在 Windows PowerShell 上运行性能测试工具：

```powershell
./scripts/http-relay-perf/run.ps1
```

该工具使用 Docker Compose 运行真实的 `nre-agent` 实例，并比较 HTTP 直接访问与中继入站吞吐量。默认情况下，`CLI -> HTTP` 和 `HTTP -> 后端` 两段都使用 `tc netem` 人工延迟。你可以用这些变量覆盖延迟：

| 变量 | 说明 |
|------|------|
| `HARNESS_DELAY_CLI_TO_HTTP_MS` | 测试客户端到 HTTP 入口点的延迟。 |
| `HARNESS_DELAY_HTTP_TO_BACKEND_MS` | HTTP 入口点到后端的延迟。 |
| `HARNESS_NETEM_DELAY_MS` | 全局 netem 延迟覆盖。 |

## 纯 Go 控制平面验证

在替换生产镜像之前，你可以使用数据副本来验证纯 Go 控制平面：

```bash
cd panel/backend-go && go test ./...
cd ../../go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```

### 验证清单

| 检查 | 预期结果 |
|------|----------|
| `/panel-api/health` | 返回 HTTP 2xx。 |
| `/panel-api/info` | 返回 HTTP 2xx。 |
| `/panel-api/agents` | 返回 HTTP 2xx，并包含一个 `id=local` 且 `is_local=true` 的代理。 |
| `join-agent.sh` | 可从公共代理资源路由下载。 |
