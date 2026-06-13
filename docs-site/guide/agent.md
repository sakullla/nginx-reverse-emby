# Agent 接入

Agent 在节点上执行代理规则。它们通过心跳同步（heartbeat pull）从控制面拉取期望状态，因此 NAT 环境下只需要 Agent 能主动访问控制面，不要求控制面能反向连进 Agent。

Docker Compose 默认会启用内嵌的 `local` agent。只在一台 VPS 上反代时，通常不需要额外接入 Agent。

## 本地节点（local）

默认 Compose 启动后，**节点管理** 页面会出现一个 `local` 节点。HTTP、L4、Relay 的监听端口会直接绑定到宿主机网络（host 模式）。

如果你只想让这台 VPS 同时承担入口和出站，后续教程都选择 `local` 即可。

## 加入新节点

在 **节点管理** 页面点击 **加入节点**，面板会按平台给出接入命令。下面是手动执行的方式。

### Linux

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

### macOS

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

脚本支持 `amd64` / `arm64` 两种架构，以及 Linux（systemd）和 macOS（launchd）。Windows 不随控制面镜像发布，见下文。

### NAT 节点

NAT 节点只需要能主动访问控制面，控制面不需要向 Agent 发起入站连接：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

## 脚本参数

| 参数 | 说明 |
| --- | --- |
| `--register-token` | 控制面注册令牌（必填）。 |
| `--master-url` | 控制面 URL，默认使用脚本内置地址。 |
| `--asset-base-url` | Agent 二进制下载基地址，默认使用脚本内置地址。 |
| `--agent-name` | Agent 名称，默认为主机名。 |
| `--agent-token` | Agent 心跳令牌，默认自动生成。 |
| `--agent-url` | Agent 对外访问地址（可选，用于展示 / 直连）。 |
| `--data-dir` | 安装目录，Linux 默认 `/var/lib/nre-agent`，macOS 默认 `~/.nre-agent`。 |
| `--version` | 注册时上报的版本号，默认 `1`。 |
| `--tags` | 节点标签，逗号分隔，例如 `edge,emby`。 |
| `--binary-url` | 自定义 `nre-agent` 二进制下载地址。 |
| `--install-systemd` | 安装并启动 systemd 服务（Linux）。 |
| `--install-launchd` | 安装并加载 launchd agent（macOS）。 |
| `--source-dir` | 旧轻量 Agent 目录，供 `migrate-from-main` 或 `uninstall-agent` 使用。 |

## Windows

Windows 节点不随控制面镜像构建或公开，预期通过 GitHub Releases 分发的客户端安装包接入：

1. 在控制面 **节点管理** 页面获取注册令牌（`register token`）。
2. 从 GitHub Release 下载 Windows 客户端安装包。
3. 在客户端中填写控制面 URL 和注册令牌。
4. 由客户端完成注册、运行和后续连接管理。

## 从旧版 Agent 迁移

从旧 `main` 版本轻量 Agent 迁移到当前 Go agent，使用 `migrate-from-main` 子命令：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本会尝试从 `/opt/nginx-reverse-emby-agent`（可用 `--source-dir` 覆盖）读取旧配置，复用原 `agent_token`，切换到 `/var/lib/nre-agent`，验证通过后清理旧 runtime 和 nginx 服务。完整迁移流程见 [迁移](../operations/migration.md)。

## 卸载 Agent

Linux / macOS 安装脚本会部署本地卸载入口：

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

也可以在线卸载：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

卸载只清理本机运行时和数据；控制面里的 Agent 记录需要在 **节点管理** 页面手动删除。

## Agent 配置变量

独立 Agent 通过环境变量配置（systemd / launchd 单元会注入）。常用变量：

| 变量 | 说明 |
| --- | --- |
| `NRE_AGENT_ID` | Agent 标识。 |
| `NRE_AGENT_NAME` | Agent 显示名称。 |
| `NRE_AGENT_TOKEN` | Agent 心跳认证令牌（加入时生成）。 |
| `NRE_MASTER_URL` | 控制面 URL。 |
| `NRE_DATA_DIR` | Agent 数据目录。 |
| `NRE_HEARTBEAT_INTERVAL` | 心跳同步间隔，默认 `10s`。 |
| `NRE_HTTP3_ENABLED` | 是否启用 HTTP/3 入口。 |
| `NRE_TRAFFIC_INTERFACES` | 流量采集网卡白名单，逗号分隔，留空表示全部。 |

完整清单见 [环境变量](../reference/environment.md)。
