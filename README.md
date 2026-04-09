# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

用于集中管理 HTTP / L4 规则、证书与 Agent 的反向代理控制面。

## Runtime Architecture

- 控制面：Node.js backend + Vue frontend
- 执行面：Go `nre-agent`
- 本地节点：Master 主机上可额外运行 Go local agent
- 同步模型：heartbeat pull
- 版本更新：Master 下发 `desired_version`

当前仓库的 Docker / Compose 默认启动的是**控制面**；Go agent 作为独立执行面单独安装或单独运行。

## Quick Start

### Docker Compose

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

默认访问：

- `http://<服务器 IP>:3000`
- `http://<服务器 IP>/panel-api/health`

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

## Join Agent

### Linux

```bash
curl -fsSL http://master.example.com:3000/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

### macOS

```bash
curl -fsSL http://master.example.com:3000/panel-api/public/join-agent.sh | sh -s -- \
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

- 控制面容器默认监听 `3000`，Compose 同时把宿主机 `80` 映射到容器 `3000`，便于 smoke check。
- `/panel-api/*` 由 Node backend 直接提供，不再依赖 Nginx 做控制面反代。
- Go agent 二进制会作为公开资产暴露在 `/panel-api/public/agent-assets/` 下，供 `join-agent.sh` 下载当前已打包的平台版本。
- `deploy.sh` 仍保留为历史兼容的独立 Nginx 节点脚本，不是默认运行时路径。
- 如果你显式启用了 legacy local apply / 本地 Node 执行路径，需要额外设置 `PANEL_GENERATOR_SCRIPT` 或 `PANEL_APPLY_COMMAND`；默认控制面镜像不再内置旧的本地生成器。

## Verification

常用验证命令：

```bash
cd panel/backend && npm test
cd panel/backend && node --check server.js
cd panel/frontend && npm run build
cd go-agent && go test ./...
docker build -t nginx-reverse-emby .
```
