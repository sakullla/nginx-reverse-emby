# Agent 节点管理

Agent 是执行代理规则的工作单元。每台 VPS 上运行一个 Agent，它们通过心跳主动连接控制面拉取配置。

Docker Compose 默认自带一个 `local` Agent。单机使用时所有规则选 `local` 即可。

## 节点列表

进入 **节点管理**，能看到所有已注册的 Agent 节点及其在线状态。

![节点管理列表](/screenshots/panel-agents.png)

## 加入新节点

在 **节点管理** 页面点击 **加入节点**，面板会给出对应平台的接入命令。

![加入节点](/screenshots/panel-join-agent.png)

### Linux

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

### macOS

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

脚本支持 `amd64` 和 `arm64` 架构。

### 内网 / NAT 环境

Agent 主动连接控制面，所以即使在内网或 NAT 后面，只要 Agent 能访问控制面就能工作。给 NAT 节点起个名字方便管理：

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

## join-agent.sh 参数

| 参数 | 说明 |
| --- | --- |
| `--register-token` | 必填。控制面的注册令牌 |
| `--master-url` | 控制面 URL。默认用脚本下载时的地址 |
| `--agent-name` | Agent 显示名称，默认主机名 |
| `--agent-token` | 心跳认证令牌，默认自动生成 |
| `--data-dir` | 数据存放目录。Linux 默认 `/var/lib/nre-agent` |
| `--tags` | 节点标签，逗号分隔，方便面板筛选 |
| `--install-systemd` | 注册为 systemd 服务（Linux） |
| `--install-launchd` | 注册为 launchd 服务（macOS） |

## Windows 节点

Windows 版 Agent 通过 GitHub Releases 提供客户端安装包。在面板获取注册令牌后，下载客户端，填写面板 URL 和令牌即可。

## 从旧版 Agent 迁移

从旧版轻量 Agent 迁移到当前 Go Agent：

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本会自动读取旧配置、复用 Agent Token、切换到新目录、验证连接，然后清理旧的运行时文件。详见 [数据迁移](../operations/migration.md)。

## 卸载 Agent

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

或在线卸载：

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

卸载只清理本机文件，面板里的 Agent 记录需要手动删除。

## 配置变量

Agent 行为通过环境变量控制，完整列表见 [环境变量速查](../reference/environment-variables.md)。
