# 快速开始

Nginx-Reverse-Emby 是一个纯 Go 控制面和 Agent 运行时，用于集中管理本地与远程节点上的反向代理规则。

推荐使用 Docker Compose 部署。它会在一个打包栈中启动控制面板、API、存储和内嵌 local agent。

## 前置要求

- 支持 Compose 的 Docker 环境。
- 一台可以暴露面板端口的服务器。
- 用于登录面板的强 `API_TOKEN`。
- 如果需要接入远程 Agent，还需要 `MASTER_REGISTER_TOKEN`。

## 快速部署

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，设置 `API_TOKEN`，然后启动服务：

```bash
docker compose up -d
```

打开 `http://<服务器 IP>:8080`，使用 `API_TOKEN` 登录。

## 后续阅读

- 阅读 [Docker Compose](./docker-compose.md) 了解完整部署说明。
- 添加远程节点前，先阅读 [Agent 接入](./agent.md)。
- 生产部署前，检查 [环境变量](/reference/environment.md)。
