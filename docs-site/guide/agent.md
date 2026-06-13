# Agent 接入

Agent 在本地或远程节点上执行代理规则。它们通过心跳同步从 Master 拉取期望状态，NAT 环境下只需要 Agent 能主动访问 Master，不需要 Master 主动连进 Agent。

Docker Compose 默认会启用内嵌 `local` agent。新手只在一台 VPS 上反代 Emby 时，通常不需要额外接入 Agent。

## 本地节点

默认 Compose 启动后，面板中会看到一个 `local` 节点。HTTP、L4、Relay 监听端口都会直接绑定到宿主机网络上。

如果你只想让这台 VPS 承担入口和出站，后续教程都选择 `local` 即可。

## Linux

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

## macOS

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

## NAT 节点

NAT Agent 只需要能主动访问 Master。Master 不需要向 Agent 发起入站连接。

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

常见可选参数：

| 参数 | 说明 |
| --- | --- |
| `--agent-name` | 自定义 Agent 名称。 |
| `--tags` | 节点标签，逗号分隔。 |
| `--agent-url` | Agent 外部访问地址。 |
| `--binary-url` | 自定义 Agent 下载地址。 |
| `--data-dir` | 自定义数据目录。 |

## Windows

Windows 节点预期使用 GitHub Releases 分发的客户端安装包，不随控制面镜像发布。

基本流程：

1. 在控制面获取 `register token`。
2. 从 GitHub Release 下载 Windows 客户端安装包。
3. 在客户端中配置控制面 URL 和注册令牌。
4. 由客户端完成注册、运行和后续连接管理。

## 卸载 Agent

Linux/macOS 安装脚本会部署本地卸载入口：

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

也可以在线卸载：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

卸载只清理本机运行时和数据；控制面中的 Agent 记录需要在面板里手动删除。
