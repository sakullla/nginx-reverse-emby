# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

面向 Emby、Jellyfin 以及常见 HTTP/TCP 服务的反向代理控制面。  
典型场景：你有一台线路较好的 VPS，想把公费服 / 公益服 Emby、Jellyfin 或其它服务反代到自己的域名，减少观看时必须挂代理的问题。

完整中文文档：

- [文档首页](https://sakullla.github.io/nginx-reverse-emby/)
- [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)
- [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)

## 它能做什么

共享规则数据集由 Host 管理，支持 GeoIP、GeoSite、完整社区文件集和本产品省份 CIDR 模型。管理员通过 `/api/datasets` 管理源，`PUT /api/datasets/{sourceID}` 设置源描述和固定修订/摘要；私网来源及重定向目标需在 retrieval 配置中明确授权。`POST /api/datasets/control` 提供 import、refresh、activate、rollback 及受引用保护的删除操作；原始上传走 `/api/datasets/{sourceID}/uploads` 二进制路径，分类与版本历史通过 `/catalog` 分页查询。

retrieval 默认使用固定 `revision` + `expected_digest`；管理员也可选择 `mode: "rolling-sha256"` 并设置 `checksum_url`，例如指向对应数据文件的 `.sha256sum`。滚动模式每次先读取有界校验文件（最多 4 KiB、15 秒），接受单个 SHA256 或文件名匹配的 sha256sum 行，再按取得的精确摘要下载数据。捕获的摘要形成 `checksum-sha256:…` 不可变修订，同时保存校验文件 URL、摘要和取得时间；它是完整性证据，不是独立发布者签名。私网和重定向授权同样适用于校验文件。

下载在拨号前验证全部 DNS 结果，IPv4-mapped IPv6 先转换为 IPv4。公共源仅接受一般全局单播地址（IPv6 限 `2000::/3`），排除私网、回环、链路本地及特殊用途段。显式 `allow_private` 额外允许 RFC1918、ULA 和回环源，但不开放链路本地、`100.64.0.0/10`（包含 `100.100.100.200`）、文档/基准测试段、协议转换段、保留地址及 `168.63.129.16`、`fd00:ec2::254` 平台端点；具体拒绝前缀见 `service/dataset_fetch.go`。该规则同样约束数据、校验文件和重定向后的拨号。

手动 refresh 和定时刷新均在完整校验后，通过既有 revision 事务激活源及全部消费者绑定；import 仍只准备候选，可另行 activate。`/bindings` 把实例、节点与最多 64 个分类选择器绑定到版本。本机和远端 Agent 都验证独立 `dataset-index-v1` 产物，索引随 generation 准备、切换和释放。`/status?node_id=…` 区分 desired、applied、last-good 和失败/离线；坏摘要、更新中的摘要/数据不一致、缺失分类或准备失败保留旧版本。最近三个成功版本及仍被配置、会话或 revision 引用的版本不能删除。来源、许可、覆盖与容量证据见 [规则数据集说明](docs/reference/rule-datasets.md)。

SDK 的 `dataset.control/catalog/status` 已接入控制面 HostRuntime；Agent 提供绑定实例授权与 generation 的本地查询 provider。WASM 可信来源查询 import 和受管网络调用的具体 adapter 随相应执行入口接入，当前数据集能力不自行读取插件自报来源作为 admission 依据。

- **HTTP / HTTPS 反代**：按域名转发 Web 服务，支持 ACME 自动证书
- **L4 端口转发**：转发 TCP / UDP 端口
- **多节点 Agent**：本机 `local` 节点可直接代理；也可把远端机器加入面板统一管理
- **Relay 隧道**：需要时再启用节点间中继（见文档站）

默认运行时是 **纯 Go 控制面容器**，不再依赖 Nginx。一个 Compose 即可拉起控制面和内置 local Agent。

## 5 分钟上手

### 1. 一键部署（推荐）

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token、启动服务，并在结束时打印**访问地址**和 **Panel token**（`API_TOKEN` / `NRE_PANEL_TOKEN` 面板访问令牌）。

交互通常只需两步：

1. **面板域名**：DNS 已指向本机则填入；直接回车 = 临时 HTTP
2. **Cloudflare Token**（可选）：粘贴后自动 DNS-01 申请证书；回车跳过则用 HTTP-01

把脚本输出的地址和 token 保存好。临时 HTTP 的随机路径只能降低被扫到的概率，**不能替代 HTTPS 和足够长的随机令牌**。

### 2. 用访问令牌登录

用脚本输出的地址打开面板，输入 **Panel token** 登录。全新部署只强制现有 token，不提供用户名或密码。登录成功后即可直接使用面板。

### 3. 添加第一条规则

进入 **流量管理 → HTTP 规则**，选择节点后添加规则。普通服务填写后端地址；已安装的加速源等插件可以直接选择“插件提供商”，不需要填写插件端口或额外参数。

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 入口域名 | `https://app.example.com` | 你访问用的域名；HTTPS 沿用规则的证书流程 |
| 后端 | `加速源` | 选择“插件提供商”后，从当前节点的可用插件中选择 |
| 发布 | `发布规则` | 一次提交入口、证书和插件后端 |

点击发布后，Agent 会自动应用完整配置；插件更新或重启时，已建立的请求会继续完成。更完整的图文步骤见 [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)。

## 手动部署

适合不想跑交互脚本、或要自己改 Compose 的场景。

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

配置两个访问 token 和通用 secret vault 的 envelope key。可先生成 vault key：

```bash
openssl rand -hex 32
```

可将输出的 64 位 hex 字符串写入 `PANEL_VAULT_MASTER_KEY`：

```yaml
environment:
  API_TOKEN: <面板 bootstrap 令牌>
  MASTER_REGISTER_TOKEN: <远程节点注册令牌>
  PANEL_VAULT_MASTER_KEY: <64 位随机 hex>
  PANEL_VAULT_KEY_ID: primary
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 和 `MASTER_REGISTER_TOKEN` 都要用 32 位以上随机字符串，且互不相同；仓库附带的 Compose 会同时要求这两个值。`PANEL_VAULT_MASTER_KEY` 可省略；省略时控制面会从 `API_TOKEN` 确定性派生标准 32-byte Vault key，重启不需要额外迁移。显式配置仍优先，一键部署脚本也会继续生成并持久保留独立 key。请把包含这些值的 `.env` 权限收紧为 `0600` 并纳入受控备份，不要只备份数据库。

已存储 secret 后不能只更换 `API_TOKEN`，也不能单独填入一个新的 `PANEL_VAULT_MASTER_KEY`，否则既有 ciphertext 将无法解密。允许在线替换 Vault key：设置新的 `PANEL_VAULT_MASTER_KEY` 和不同的 `PANEL_VAULT_KEY_ID`，再临时设置旧的 `PANEL_VAULT_PREVIOUS_MASTER_KEY`；旧部署由 API token 派生时，改用 `PANEL_VAULT_PREVIOUS_API_TOKEN`。同时用 `PANEL_VAULT_PREVIOUS_KEY_ID` 指明旧部署的 key ID（Compose 默认是 `primary`）。控制面启动时会在事务内重加密全部 active secret，版本号和引用不变；确认启动和 secret 读取成功后，再删除三个 `PANEL_VAULT_PREVIOUS_*` 变量并备份新 key。

官方插件市场默认读取镜像内的 `/opt/nginx-reverse-emby/official-market.lock`，并跟踪 `sakullla/sakullla-plugins` 的 `official-market` 分支。需要使用其它策略文件时，可设置 `PANEL_OFFICIAL_MARKET_LOCK_FILE`；该值必须是容器内的绝对路径，并指向普通文件而非符号链接。该文件只固定官方仓库身份、可切换的 `ref_kind: branch`/`ref_name` 更新通道、支持的 SDK ABI 与官方签名根，不固定 commit、tag、版本或 `market.yaml` 摘要。每次刷新都会解析所配置 branch 的当前 full OID，复核刷新期间 ref 未移动，完整验证市场与包签名后才持久化该 OID 和摘要 provenance；切换 branch 会先使旧 catalog 失效并建立新的 source generation。文件缺失、身份或签名验证失败会拒绝刷新并保留当前快照。

官方发布物只接受一套 v1 契约：`market.yaml` 使用 `schema_version`、`commit`、`sdk_abi` 与 `packages`；根目录 `provenance.json` 必须配套 `provenance.signature.json`；每个包使用完整的 `plugin.yaml` runtime manifest、`package.files.json` 和 `signature.json`。两类签名都必须由 `sakullla-official-root-2026` 对各自 `payload_sha256` 解码后的原始 32-byte SHA-256 做 Ed25519 签名，其中根签名的 payload 是 `provenance.json` 原始字节的 SHA-256。旧的 `package.sha256`、`package.sig`、未签名 provenance 或对 ASCII hex digest 的签名不会作为官方兼容格式接受。包内 manifest、文件清单、任一 payload 文件、签名、完整 package digest 或 SDK provenance 不一致时，整个市场刷新失败并保留上一个可用快照。

启动：

```bash
docker compose up -d
```

默认只监听本机 `127.0.0.1:8080`。首次访问可先开 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@<服务器 IP>
```

浏览器打开 `http://127.0.0.1:8080`，用 `API_TOKEN` 令牌登录后即可使用面板。

也可用 `.env` 管理配置，参考 [`.env.example`](.env.example)。**不要把真实 token、证书或私钥提交到仓库。**

### 给面板自身上 HTTPS（手动部署时推荐）

一键脚本在填写域名后会尽量自动完成；手动部署时可以自己加一条自代理规则：

| 字段 | 示例 |
| --- | --- |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080` |

确认防火墙放行 `80/443`。证书申请成功后，在 Compose 中设置：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
  # bundled local Agent 会清洗并重写 X-Forwarded-*；其它上游代理必须具备同样行为
  NRE_TRUST_FORWARDED_HEADERS: "true"
```

然后 `docker compose up -d`。

### 非交互部署

CI / 已知全部参数时：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | \
  sh -s -- --public-url https://panel.example.com --cf-token YOUR_CF_TOKEN --yes --non-interactive
```

## 加入更多节点

面板所在机器默认已有 `local` 节点。如果还要在其它服务器上跑代理：

1. 打开面板 **节点管理**
2. 点击 **加入节点**
3. 选择 Linux / macOS，复制一键命令到目标机执行

默认会签发**一次性登记令牌**（约 10 分钟有效）。节点上线后会出现在列表中。  
Windows 目前需要手工安装 Go agent，详见 [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents)。

## 接下来看什么

| 目标 | 文档 |
| --- | --- |
| 跑通第一条反代 | [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart) |
| 理解部署细节与环境变量 | [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy) |
| 配置 HTTP 规则 | [HTTP 反向代理](https://sakullla.github.io/nginx-reverse-emby/guides/http-rules) |
| 配置端口转发 | [L4 端口转发](https://sakullla.github.io/nginx-reverse-emby/guides/l4-rules) |
| 申请 / 管理公网证书 | [证书与 HTTPS](https://sakullla.github.io/nginx-reverse-emby/guides/certificates) |
| 多节点与加入 Agent | [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents) |
| Relay 中继隧道 | [Relay 指南](https://sakullla.github.io/nginx-reverse-emby/guides/relay) |
| 备份与恢复 | [备份恢复](https://sakullla.github.io/nginx-reverse-emby/operations/backup-restore) |
| 故障排查 | [故障排查](https://sakullla.github.io/nginx-reverse-emby/operations/troubleshooting) |

更偏运维与内部机制的内容（内部 PKI、revision 异步生效、热升级等）已放在文档站的运维章节，不在本 README 展开。

## 本地开发

```bash
# 前端
cd panel/frontend && npm ci && npm run dev

# 控制面
cd panel/backend-go && go test ./... && go run ./cmd/nre-control-plane

# Agent
cd go-agent && go test ./...

# 镜像
docker build -t nginx-reverse-emby .
```

文档站源码在 `docs-site/`：

```bash
cd docs-site
npm ci
npm run dev
```

## 许可证

本项目基于 GNU General Public License v3.0 授权发布，详见 [LICENSE](./LICENSE)。
