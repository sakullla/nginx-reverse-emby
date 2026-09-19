# Nginx-Reverse-Emby v1.5.0

对照 **v1.4.1**（2026-07-30）的发布说明。当前镜像：`sakullla/nginx-reverse-emby:1.5.0`。

v1.4.1 已经能用纯 Go 控制面做 HTTP / L4 / Relay 反代、ACME 证书和流量额度。v1.5.0 在此之上补齐插件市场、内部 Relay PKI、证书中心，以及一批代理与节点热升级的稳定性修复。登录方式不变：仍用 `API_TOKEN`。

完整文档：[https://sakullla.github.io/nginx-reverse-emby/](https://sakullla.github.io/nginx-reverse-emby/)

## 相对 v1.4.1 你能直接用到的能力

### 插件市场

面板新增 **插件** 分组：

- **插件市场**：浏览、安装、升级签名包；点卡片可看包内容，仓库源用弹窗管理
- **已安装插件**：按「待部署 / 待发布 / 已可用 / 异常」推进，详情页完成配置、部署和入口发布

官方市场锁定在 `sakullla/sakullla-plugins` 的 `official-market` 分支，刷新时校验签名。也可以自行添加 Git 仓库源。

常见用法：

- 安装加速源一类插件后，HTTP 规则后端可选 **插件提供商**，不必手填插件端口
- 安装 Cloudflare DNS 插件后，可按域名映射 Token，不再只靠全局 `CF_TOKEN`
- 安装 WAF 策略插件后，HTTP 规则可在观察模式自动挂上 `policy_ref`

插件默认在沙箱里跑。需要完整出站/监听网络的包必须显式声明高风险权限。

### 内部 Relay PKI

生产 Relay 不再长期依赖 pin-only、单向 TLS 或自签名放行。

- **证书中心** 同时管两块：公网 HTTPS/ACME，以及 **内部 PKI**（Relay 双向 TLS）
- 远程 Agent、内嵌 `local` 节点和 Relay 监听器各自有 tunnel identity
- 证书轮转、撤销、绑定重登记、受保护备份都在内部 PKI 页完成

内部 mTLS 只保护 Relay 数据面。心跳、登记、revision 仍走原来的面板端口和 `X-Agent-Token`。

SQLite 部署可从内部 PKI 页导出带 passphrase 的受保护备份（整库快照 + CA 私钥，会清掉登记 token）。PostgreSQL / MySQL 请按文档做数据库、vault 与 master key 的协同冷恢复。

运维手册：[内部 PKI](https://sakullla.github.io/nginx-reverse-emby/operations/internal-pki)

### 面板与证书

- 证书管理改成证书中心，公网证书和内部 PKI 在同一入口切换
- 加入节点弹窗重做了布局和命令展示，按平台复制一键命令
- 插件可把自己的管理页挂到侧栏（例如基础设施分组）
- 通用 secret vault：Compose 可设置 `PANEL_VAULT_MASTER_KEY`；留空则从 `API_TOKEN` 派生。已有密文后不要只改 token

### 代理与节点

- HTTP 上游在 HTTP/2 不可用时可回退 HTTP/1.1
- 独立 Linux Agent 热升级时，下载更新包不再卡住心跳；旧连接继续排空，新连接切到新进程
- 空闲反向隧道保活、连接上限、HTTP-01 诊断和历史更新包回收都更稳
- 插件可使用版本化的 GeoIP / Geosite / 社区规则 / CIDR / MMDB 数据集，做路由或省份级访问控制

## 升级注意

从 v1.4.1 升级，单机、不走 Relay 的部署通常只需拉新镜像重启。用了 Relay 的环境，内部 PKI 激活后**不会**再回退到旧 pin / 单向 TLS。

1. 先备份：设置 → 数据管理导出配置；若已启用内部 PKI（SQLite），再导出一份受保护备份。
2. 拉镜像并重启：

   ```bash
   cd nginx-reverse-emby
   docker compose pull
   docker compose up -d
   ```

   或重新执行一键脚本：

   ```bash
   curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
   ```

3. 登录仍用原来的 `API_TOKEN`。
4. 若使用 Relay：先升级控制面，再在证书中心 → 内部 PKI 为每个远程节点生成「绑定现有节点」一次性 token，在原数据目录做 PKI 重登记，核对 identity 后再做迁移激活。步骤见[内部 PKI 升级](https://sakullla.github.io/nginx-reverse-emby/operations/internal-pki#从旧-relay-认证升级)。
5. 要用官方插件时，控制面需要能访问 GitHub（或配置 `HTTP_PROXY` / `HTTPS_PROXY`）。

新环境变量都是可选的：

| 变量 | 用途 |
| --- | --- |
| `PANEL_VAULT_MASTER_KEY` | 显式 secret vault 密钥；已有密文后轮换必须带 `PANEL_VAULT_PREVIOUS_*` |
| `NRE_PKI_MASTER_KEY_FILE` | 内部 PKI master key 的容器内路径；父目录须可写 |
| `NRE_MARKETPLACE_REFRESH_TIMEOUT` | 插件市场刷新超时，默认 30 分钟 |

## 不兼容与边界

- 内部 PKI 激活后，旧 Relay pin-only / 单向 TLS / 自签名放行不是故障回退路径。
- 受保护 PKI 备份目前只支持 file-backed SQLite，且恢复要求目标 schema 与导出时相同。
- 若你中途跑过 `develop` 并建了用户名密码，v1.5.0 面板操作员仍只认访问令牌；用户 / 资源组菜单已去掉。
- Windows 原生 `nre-agent.exe` 不随控制面镜像发布，从 GitHub Release 下客户端。

## 建议一起看的项目

同一作者的两个轻量项目，适合和本面板搭配自建：

- [LightInk](https://github.com/sakullla/LightInk)：Markdown 优先的轻量博客 / 知识库，静态生成，可选 Node 服务。
- [Rillight](https://github.com/sakullla/Rillight)：轻量视频分享，Docker 一键部署，可对接 Emby / Jellyfin。

把它们的 HTTP 入口交给本面板反代即可。
