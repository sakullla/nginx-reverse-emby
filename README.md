# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

面向 Emby、Jellyfin 以及 HTTP/TCP 服务的纯 Go 反向代理控制面。典型使用场景是：你有一台优化线路 VPS，想把购买的公费服或加入的公益服 Emby/Jellyfin 反代到自己的域名，减少观看时必须挂代理的问题。

完整中文文档已经迁移到 `docs-site/`：

- [从 0 到 HTTP 代理](./docs-site/guide/getting-started.md)
- [Docker Compose 部署](./docs-site/guide/docker-compose.md)
- [添加 HTTP 规则](./docs-site/guide/http-rule.md)
- [L4 + Relay 从 0 到可用](./docs-site/guide/l4-relay.md)
- [架构与特性](./docs-site/reference/architecture.md)
- [环境变量](./docs-site/reference/environment.md)
- [备份与恢复](./docs-site/operations/backup-restore.md)

## 快速开始

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，至少修改：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

启动：

```bash
docker compose up -d
```

打开面板：

```text
http://<服务器 IP>:8080
```

使用 `API_TOKEN` 登录后，按文档添加 HTTP 规则或 L4/Relay 规则。

## 开发

常用命令：

```bash
cd panel/frontend && npm run build
cd panel/backend-go && go test ./...
cd go-agent && go test ./...
docker build -t nginx-reverse-emby .
```

更多开发、测试、构建和版本更新说明见 [开发与构建](./docs-site/reference/development.md)。

## 文档站

文档站源码在 `docs-site/`，根目录 `docs/` 可继续放临时设计、计划或内部文档。

```bash
cd docs-site
npm ci
npm run dev
npm run build
```

GitHub Pages 工作流见 `.github/workflows/docs-pages.yml`。

## 许可证

本项目基于 GNU General Public License v3.0 授权发布，详见 [LICENSE](./LICENSE)。
