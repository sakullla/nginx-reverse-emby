# Agent 接入指南

Agent 是 nginx-reverse-emby 的执行单元——它们负责在各自的节点上执行你配置的代理规则。Agent 通过**心跳同步**（heartbeat pull）机制从控制面板拉取最新的配置，因此即使在 NAT 或内网环境下，只要 Agent 能主动访问控制面板，就能正常工作，**不需要**控制面板反向连入 Agent。

Docker Compose 默认会启用内嵌的 `local` agent。如果你只在一台 VPS 上做反向代理，通常不需要额外接入其他 Agent。

## 本地节点（local）

默认用 Compose 启动后，打开 Web 面板的 **节点管理** 页面，你会看到一个名为 `local` 的节点。这个节点就是运行在同一台服务器上的内置 Agent，HTTP、L4、Relay 的监听端口会直接绑定到宿主机的网络（host 模式）。

如果你只想让这一台 VPS 同时承担"入口流量"和"反向代理出站"的角色，后续教程里选择 `local` 节点即可。

![节点管理列表](/screenshots/panel-agents.png)

## 加入新节点

如果你有多台服务器，想把它们都纳入统一管理，可以在 **节点管理** 页面点击 **加入节点**。面板会根据你的平台给出对应的接入命令。下面也列出了手动执行的完整方式。

![加入节点](/screenshots/panel-join-agent.png)

### Linux 系统

在目标服务器上执行以下命令：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

**注意：** 把 `master.example.com:8080` 换成你实际控制面板的地址和端口。

### macOS 系统

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

脚本目前支持 `amd64` 和 `arm64` 两种 CPU 架构，以及 Linux（systemd）和 macOS（launchd）两种服务管理方式。Windows 节点不随控制面板镜像一起发布，接入方式见下文。

### NAT 环境下的节点

如果你的 Agent 服务器在内网或 NAT 后面（比如家里的路由器后面），也不用担心——因为 Agent 是**主动连接**控制面板，所以只要 Agent 能访问控制面板即可，控制面板不需要向 Agent 发起入站连接。

给 NAT 节点命名并打上标签，方便后续管理：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

## 脚本参数详解

`join-agent.sh` 脚本支持以下参数，你可以根据需要调整：

| 参数 | 说明 |
| --- | --- |
| `--register-token` | **必填。** 控制面板的注册令牌，用于验证 Agent 身份。 |
| `--master-url` | 控制面板的 URL。默认使用脚本内置的地址（即你下载脚本时访问的地址）。 |
| `--asset-base-url` | Agent 二进制文件的下载基地址。默认使用脚本内置地址。 |
| `--agent-name` | Agent 的显示名称。默认使用主机名（hostname）。 |
| `--agent-token` | Agent 的心跳认证令牌。默认会自动生成一个随机值。 |
| `--agent-url` | Agent 的对外访问地址（可选，用于面板展示或直连测试）。 |
| `--data-dir` | Agent 的数据存放目录。Linux 默认 `/var/lib/nre-agent`，macOS 默认 `~/.nre-agent`。 |
| `--version` | 注册时上报的版本号。默认是 `1`。 |
| `--tags` | 节点标签，用逗号分隔。例如 `edge,emby`，方便在面板里筛选和分组。 |
| `--binary-url` | 自定义 `nre-agent` 二进制文件的下载地址。 |
| `--install-systemd` | 安装并注册为 systemd 服务（仅限 Linux）。 |
| `--install-launchd` | 安装并注册为 launchd 服务（仅限 macOS）。 |
| `--source-dir` | 旧版轻量 Agent 的目录路径。用于 `migrate-from-main` 或 `uninstall-agent` 命令。 |

## Windows 节点

Windows 版本的 Agent 不随控制面板镜像一起构建或发布，预期通过 GitHub Releases 提供客户端安装包：

1. 在 Web 面板的 **节点管理** 页面获取注册令牌（`register token`）。
2. 前往 GitHub Release 页面下载 Windows 客户端安装包。
3. 安装后打开客户端，填写控制面板 URL 和注册令牌。
4. 客户端会自动完成注册、运行，并持续与控制面板保持连接。

## 从旧版 Agent 迁移

如果你之前使用的是旧 `main` 版本的轻量 Agent，现在想切换到当前的 Go Agent，可以使用 `migrate-from-main` 子命令：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本会自动完成以下操作：

1. 从 `/opt/nginx-reverse-emby-agent`（可用 `--source-dir` 指定其他路径）读取旧版配置。
2. 复用原来的 `agent_token`，避免在控制面板里重新注册。
3. 切换到新的数据目录 `/var/lib/nre-agent`。
4. 验证新 Agent 能正常工作后，清理旧的运行时文件和 Nginx 服务。

完整的迁移流程说明请参考 [迁移文档](../operations/migration.md)。

## 卸载 Agent

### Linux / macOS

如果你之前通过安装脚本部署了 Agent，系统会留下一个本地卸载入口：

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

直接运行这个脚本即可卸载。

### 在线卸载

也可以直接通过 curl 在线卸载：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

**注意：** 卸载脚本只会清理本机上的运行时文件和数据，不会删除控制面板里的 Agent 记录。如果你希望彻底移除，还需要在 Web 面板的 **节点管理** 页面手动删除该节点。

## Agent 配置变量

独立运行的 Agent 通过环境变量进行配置（systemd / launchd 服务单元会自动注入这些变量）。常用的变量如下：

| 变量 | 说明 |
| --- | --- |
| `NRE_AGENT_ID` | Agent 的唯一标识符。 |
| `NRE_AGENT_NAME` | Agent 的显示名称。 |
| `NRE_AGENT_TOKEN` | Agent 心跳认证令牌（加入时自动生成）。 |
| `NRE_MASTER_URL` | 控制面板的 URL 地址。 |
| `NRE_DATA_DIR` | Agent 数据存放目录。 |
| `NRE_HEARTBEAT_INTERVAL` | 心跳同步的间隔时间。默认每 `10` 秒同步一次。 |
| `NRE_HTTP3_ENABLED` | 是否启用 HTTP/3 入口支持。 |
| `NRE_TRAFFIC_INTERFACES` | 流量采集的网卡白名单，逗号分隔。留空表示采集所有网卡。 |

完整的环境变量列表请参考 [环境变量文档](../reference/environment.md)。
