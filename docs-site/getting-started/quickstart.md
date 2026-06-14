# 快速开始

这篇文章带你用最短时间跑通第一个反向代理。假设你有一台 VPS，想把 Emby / Jellyfin / 网站通过自己的域名让别人访问。

## 你需要准备什么

1. 一台装了 Docker 和 Docker Compose 的 VPS
2. 一个域名（比如 `app.example.com`），把它的 DNS 解析到 VPS 的 IP
3. 后端服务的地址（比如 `https://origin.example.net` 或 `http://192.168.1.100:8096`）
4. 一个你自己设置的强密码（后面称为 `API_TOKEN`）

先确认 VPS 能访问后端：

```bash
curl -I https://origin.example.net
```

如果这一步不通，后面的代理也一定不通。先把网络搞通再继续。

## 第一步：下载并启动

在 VPS 上执行：

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，修改密码：

```yaml
environment:
  API_TOKEN: 改成你自己的强密码
  MASTER_REGISTER_TOKEN: 改成另一个强密码
  NRE_TIMEZONE: Asia/Shanghai
```

- `API_TOKEN`：登录面板用的密码，越随机越好。
- `MASTER_REGISTER_TOKEN`：远程 Agent 注册用的密码。只在一台机器上用的话可以先随便填。

启动：

```bash
docker compose up -d
```

浏览器访问 `http://<你的 VPS IP>:8080`，输入 `API_TOKEN` 登录。

![登录面板](/screenshots/panel-login.png)

登录后看到的首页：

![仪表盘首页](/screenshots/panel-dashboard.png)

## 第二步：添加一条 HTTP 规则

进入面板的 **流量管理 → HTTP 规则**，选择 `local` 节点，点击 **添加规则**：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 入口域名 | `app.example.com` | 你访问用的域名，确保 DNS 已指向 VPS |
| 后端地址 | `https://origin.example.net` | 真正的服务地址，带协议和端口 |
| 启用规则 | 开 | 只有开启才会生效 |

![添加 HTTP 规则](/screenshots/panel-http-rule-form.png)

点击 **创建规则**。`local` Agent 会自动同步配置并开始监听。

## 第三步：验证

浏览器打开 `http://app.example.com`。

如果打不开，按顺序检查：

1. DNS 是否解析到 VPS
2. VPS 防火墙是否放行了 80 端口
3. VPS 能不能访问后端（`curl -I <后端地址>`）
4. 规则是否选了 `local` 节点并且已启用

更多排查见 [排障指南](../operations/troubleshooting.md)。

## 下一步

- 想启用 HTTPS：[证书与 HTTPS](../guides/certificates.md)
- 想转发 TCP/UDP 端口：[L4 端口转发](../guides/l4-rules.md)
- 想接入更多 VPS 作为节点：[Agent 节点管理](../guides/agents.md)
- 想了解详细部署参数：[部署指南](./deploy.md)
