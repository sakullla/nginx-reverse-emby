# Docker Compose

Docker Compose 是新手部署的推荐方式。默认 Compose 会启动一个纯 Go 控制面容器，并启用内嵌 local agent。你不需要另外安装 Nginx。

## 一键准备目录

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

## 数据目录

Compose 文件会把面板状态、SQLite 数据、证书和运行时文件持久化到宿主机的 `./data`。

不要提交这个目录里的文件。

## 必要环境变量

启动前先编辑 `docker-compose.yaml`：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 用于面板访问。`MASTER_REGISTER_TOKEN` 用于 Agent 向 Master 注册。

对于只使用 `local` 节点的新手，`MASTER_REGISTER_TOKEN` 可以先设置成另一个强随机字符串，暂时不需要用到。

## 启动和查看状态

```bash
docker compose up -d
docker compose ps
docker compose logs -f
```

面板默认监听：

```text
http://<服务器 IP>:8080
```

登录后就可以继续添加 [HTTP 规则](./http-rule.md)。

## Host 网络模式

默认 Compose 文件使用 host 网络，因为代理监听端口由面板动态配置。Docker bridge 网络无法在容器启动后自动发布新的监听端口。

这也意味着：你在 HTTP 规则里配置的监听端口，会直接占用宿主机端口。请提前检查端口没有被其他程序占用，并在防火墙里放行。
