# 安全审查与修复报告

日期：2026-06-17

## 结论

本次审查未发现可确认的项目代码级高危漏洞，例如未授权 RCE、明显路径穿越、前端直接 XSS 或可达高危依赖漏洞。

已确认并修复的最高风险来自默认部署配置：旧版 `docker-compose.yaml` 在 `network_mode: host` 下监听 `0.0.0.0:8080`，并提供可直接运行的弱默认 token。如果用户未修改配置且端口公网可达，攻击者可用公开默认值登录控制面。

## 已修复问题

### 1. 拒绝示例弱 Token 启动

控制面配置加载现在会拒绝示例占位 token，包括：

- `change-this-token`
- `change-this-register-token`
- `your-secure-token`
- `your-register-token`
- `changeme`
- `change-me`

影响范围：

- `NRE_PANEL_TOKEN` / `API_TOKEN`
- `NRE_REGISTER_TOKEN` / `MASTER_REGISTER_TOKEN` / `PANEL_REGISTER_TOKEN`

### 2. Docker Compose 默认不再公网监听

`docker-compose.yaml` 默认从：

```yaml
PANEL_BACKEND_HOST: 0.0.0.0
API_TOKEN: change-this-token
MASTER_REGISTER_TOKEN: change-this-register-token
```

调整为：

```yaml
PANEL_BACKEND_HOST: 127.0.0.1
API_TOKEN: ${API_TOKEN:?set API_TOKEN to a random 32+ character token}
MASTER_REGISTER_TOKEN: ${MASTER_REGISTER_TOKEN:?set MASTER_REGISTER_TOKEN to a random 32+ character token}
```

这会让默认 Compose 必须由部署者显式提供随机 token，并避免面板默认直接暴露到公网。首次登录建议通过 SSH 隧道访问 `http://127.0.0.1:8080`。

### 3. Forwarded Header 默认不再被信任

新增配置：

```ini
NRE_PUBLIC_URL=https://panel.example.com
NRE_TRUST_FORWARDED_HEADERS=false
```

默认行为：

- 不信任 `X-Forwarded-Proto`
- 不信任 `X-Forwarded-Host`
- 不信任 `X-Forwarded-Port`
- 不信任 `X-Forwarded-For`

受影响的行为：

- `join-agent.sh` 默认控制面 URL
- Agent 更新包绝对 URL
- Agent 心跳 `LastSeenIP`
- task session 远端地址

推荐生产配置是设置 `NRE_PUBLIC_URL`，让控制面使用固定可信公网地址生成 join/update URL。面板自身 HTTPS 可以由项目内置的 `local` Agent 自代理实现：创建 `https://panel.example.com -> http://127.0.0.1:8080` 的 HTTP 规则，证书申请成功后再设置 `NRE_PUBLIC_URL=https://panel.example.com`。

仅当控制面只接收可信反代流量，并且反代会清洗并重写 `X-Forwarded-*` 头时，才开启：

```ini
NRE_TRUST_FORWARDED_HEADERS=true
```

### 4. 新手部署脚本自动生成安全默认值

新增 `scripts/deploy-compose.sh`，用于降低首次部署误暴露风险：

- 中文交互提示
- 自动检测并安装 Docker / Docker Compose
- 自动生成 `API_TOKEN` 和 `MASTER_REGISTER_TOKEN`
- 优先引导用户配置域名与 Cloudflare API Token
- 有域名时自动写入 `NRE_PUBLIC_URL=https://面板域名`，并创建 `https://面板域名 -> http://127.0.0.1:8080` 的面板自代理规则
- 无域名时明确提示 HTTP 风险，并生成 `NRE_PANEL_PUBLIC_PATH=/panel-随机字符串` 作为临时面板入口

`NRE_PANEL_PUBLIC_PATH` 只用于减少默认入口被扫描到的概率，不能替代 token、HTTPS 或防火墙。

## 剩余配置风险

### 明文 HTTP

如果面板通过公网 HTTP 访问，`X-Panel-Token` 会以明文请求头传输，前端登录 token 也可能被链路或代理日志泄露。

建议：

- 首次访问使用 SSH 隧道或其他受控内网方式
- 登录后使用面板自代理 HTTPS：`https://panel.example.com -> http://127.0.0.1:8080`
- 不要把面板 8080 端口直接暴露到公网 HTTP
- 暂时没有域名时，可使用部署脚本生成的 `NRE_PANEL_PUBLIC_PATH=/panel-随机字符串` 临时降低暴露面，但它不能替代 HTTPS
- 自代理 HTTPS 可用后设置 `NRE_PUBLIC_URL=https://你的面板域名`

### Trusted Proxy 配置

开启 `NRE_TRUST_FORWARDED_HEADERS=true` 后，行为接近旧版本：控制面会信任 `X-Forwarded-*`。如果客户端仍可直连控制面，攻击者仍可伪造这些头。

建议反代示例：

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Port $server_port;
proxy_set_header X-Forwarded-For $remote_addr;
```

## 验证证据

已执行并通过：

```bash
cd panel/backend-go && go test ./...
```

结果：backend 全量测试通过。

```bash
cd go-agent && go test ./...
```

结果：go-agent 全量测试通过。

```bash
cd panel/frontend && npm run build
```

结果：前端生产构建通过。

```bash
cd docs-site && npm run build
```

结果：VitePress 文档站构建通过。

```bash
$env:API_TOKEN='test-api-token-12345678901234567890'
$env:MASTER_REGISTER_TOKEN='test-register-token-123456789012345'
docker compose config
```

结果：Compose 配置解析通过，环境中显示 `PANEL_BACKEND_HOST: 127.0.0.1`，并使用外部提供的 token。

## 变更文件

- `docker-compose.yaml`
- `README.md`
- `docs-site/reference/environment-variables.md`
- `docs-site/reference/security.md`
- `docs-site/getting-started/deploy.md`
- `docs-site/getting-started/quickstart.md`
- `docs-site/guides/http-rules.md`
- `panel/backend-go/internal/controlplane/config/config.go`
- `panel/backend-go/internal/controlplane/config/config_test.go`
- `panel/backend-go/internal/controlplane/config/compose_defaults_test.go`
- `panel/backend-go/internal/controlplane/http/static.go`
- `panel/backend-go/internal/controlplane/http/handlers_public.go`
- `panel/backend-go/internal/controlplane/http/handlers_tasks.go`
- `panel/backend-go/internal/controlplane/http/public_test.go`
- `panel/frontend/src/router/index.js`
- `scripts/deploy-compose.sh`
- `legacy/conf.d/p.example.com.conf`
- `legacy/conf.d/p.example.com.no_tls.conf`

## 部署建议

生产环境建议使用以下最小安全配置：

```ini
API_TOKEN=<随机 32 位以上字符串>
MASTER_REGISTER_TOKEN=<另一个随机 32 位以上字符串>
PANEL_BACKEND_HOST=127.0.0.1
NRE_PUBLIC_URL=https://panel.example.com
```

推荐自举流程：

1. 通过 SSH 隧道打开 `http://127.0.0.1:8080`。
2. 在面板中创建 `https://panel.example.com -> http://127.0.0.1:8080` 的 HTTP 规则。
3. 放行 80/443，等待 local Agent 自动申请证书。
4. HTTPS 面板可访问后设置 `NRE_PUBLIC_URL=https://panel.example.com` 并重启。

如果需要让控制面直接监听公网地址，必须同时配置防火墙或云安全组限制来源 IP，并避免使用明文 HTTP 访问。
