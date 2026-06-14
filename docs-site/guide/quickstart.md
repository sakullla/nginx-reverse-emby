# 快速开始

这篇文章带你用最短的时间跑通第一个反向代理。假设你有一台 VPS，想把你想反代的 Emby / Jellyfin / 网站等服务通过自己的域名访问到。

## 它能做什么

简单来说，就是：

```
你访问 app.example.com → VPS 接收请求 → VPS 转发给真正的后端服务
```

这样你就能用自己的域名访问后端服务，同时利用 VPS 的线路优势。

## 你需要准备什么

1. 一台安装了 Docker 和 Docker Compose 的 VPS。
2. 一个域名（例如 `app.example.com`），并把域名解析到这台 VPS 的 IP。
3. 后端服务的地址（例如 `https://origin.example.net` 或 `http://192.168.1.100:8096`）。
4. 自己设置一个登录密码（后面称为 `API_TOKEN`）。

先确认 VPS 能访问后端：

```bash
curl -I https://origin.example.net
```

如果这一步不通，后面代理也一定不通。

## 第一步：下载并启动

在 VPS 上执行：

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

然后编辑 `docker-compose.yaml`，修改这两个密码：

```yaml
environment:
  API_TOKEN: 改成你自己的强密码
  MASTER_REGISTER_TOKEN: 改成另一个强密码
  NRE_TIMEZONE: Asia/Shanghai
```

- `API_TOKEN`：登录面板用的密码，越随机越好。
- `MASTER_REGISTER_TOKEN`：远程节点注册用的密码。如果你只在一台机器上用，可以先随便填一个。

启动：

```bash
docker compose up -d
```

打开浏览器访问：

```text
http://<你的 VPS IP>:8080
```

输入刚才设置的 `API_TOKEN` 登录。登录后看到的首页类似这样：

![仪表盘首页](/screenshots/panel-dashboard.png)

## 第二步：添加一条 HTTP 规则

进入 **流量管理 → HTTP 规则**，选择 `local` 节点，点击 **添加规则**：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 前端访问地址 | `app.example.com` | 你自己访问用的域名，需要解析到 VPS。 |
| 后端地址 | `https://origin.example.net` | 真正的服务地址，含协议和端口。 |
| 启用规则 | 开启 | 只有开启才会生效。 |

![添加 HTTP 规则](/screenshots/panel-http-rule-form.png)

点击 **创建规则**。`local` 节点会自动同步配置，稍等片刻即可访问。

## 第三步：验证访问

在浏览器打开你的入口域名：

```text
http://app.example.com
```

如果打不开，按这个顺序检查：

1. 域名 DNS 是否解析到 VPS。
2. VPS 防火墙是否放行 `80` 端口（HTTPS 需要 `443`）。
3. VPS 能否访问后端服务。
4. 规则是否选择了 `local` 节点并开启。

## 下一步

- 想启用 HTTPS：看 [证书与 HTTPS](./certificates.md)。
- 想转发 TCP / UDP 端口：看 [L4 规则与 Relay](./l4-relay.md)。
- 想接入其他 VPS 作为节点：看 [Agent 接入](./agent.md)。
- 想看更详细的部署参数：看 [部署](./deploy.md)。
