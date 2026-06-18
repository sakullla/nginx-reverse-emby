# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

面向 Emby、Jellyfin 以及 HTTP/TCP 服务的纯 Go 反向代理控制面。典型使用场景是：你有一台优化线路 VPS，想把购买的公费服或加入的公益服 Emby/Jellyfin 反代到自己的域名，减少观看时必须挂代理的问题。

完整中文文档站：

- [文档首页](https://sakullla.github.io/nginx-reverse-emby/)
- [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)
- [部署](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)
- [HTTP 反向代理](https://sakullla.github.io/nginx-reverse-emby/guides/http-rules)
- [L4 端口转发](https://sakullla.github.io/nginx-reverse-emby/guides/l4-rules)
- [WireGuard 隧道](https://sakullla.github.io/nginx-reverse-emby/guides/wireguard)
- [证书与 HTTPS](https://sakullla.github.io/nginx-reverse-emby/guides/certificates)

## 快速开始

新手推荐直接运行部署脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token、拉取 `docker-compose.yaml` 并启动服务。
如果系统还没有 Docker Compose，脚本会自动安装。脚本会优先引导你填写 Cloudflare API Token，用域名自动创建 `https://面板域名 -> http://127.0.0.1:8080` 的面板自代理规则并申请证书；Cloudflare Token 权限需要包含 `区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`。填入 Token 时脚本会调用 Cloudflare API 在线校验是否有效，校验失败会提示并允许重新粘贴。没有域名时会提示 HTTP 风险，临时监听 `0.0.0.0:8080` 并生成一个随机面板路径。

非交互部署（CI / 已知所有参数）可跳过提问：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | \
  sh -s -- --public-url https://panel.example.com --cf-token YOUR_CF_TOKEN --yes --non-interactive
```

也可改用环境变量 `API_TOKEN`、`MASTER_REGISTER_TOKEN`、`CF_TOKEN`、`NRE_NONINTERACTIVE=1` 达到同样效果。

手动部署：

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，至少修改：

```yaml
environment:
  API_TOKEN: <随机 32 位以上字符串>
  MASTER_REGISTER_TOKEN: <另一个随机 32 位以上字符串>
  NRE_TIMEZONE: Asia/Shanghai
```

默认 Compose 只监听 `127.0.0.1:8080`。首次登录建议用 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@<服务器 IP>
```

然后打开 `http://127.0.0.1:8080`。登录后可以在面板里创建一条 `https://panel.example.com` HTTP 规则，后端填 `http://127.0.0.1:8080`，由内置 `local` Agent 给面板自身提供 HTTPS。完成后在 Compose 中设置 `NRE_PUBLIC_URL: https://panel.example.com`，让 join script 和 Agent 更新 URL 使用 HTTPS 面板地址。无域名临时 HTTP 部署时，可以设置 `NRE_PANEL_PUBLIC_PATH=/panel-随机字符串`，降低默认入口被扫到的概率；它不能替代 token 和 HTTPS。

启动：

```bash
docker compose up -d
```

使用 `API_TOKEN` 登录后，按文档添加 HTTP 规则或 L4/Relay 规则。生产环境建议尽快给面板自身配置 HTTPS。

## 开发

常用命令：

```bash
cd panel/frontend && npm run build
cd panel/backend-go && go test ./...
cd go-agent && go test ./...
docker build -t nginx-reverse-emby .
```

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
