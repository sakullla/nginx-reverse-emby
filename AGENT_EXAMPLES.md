# Agent Examples

当前执行面已经切换到 Go `nre-agent`。

## 推荐方式

优先使用控制面提供的 `join-agent.sh`：

### Linux

```bash
curl -fsSL http://master.example.com:3000/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

### macOS

```bash
curl -fsSL http://master.example.com:3000/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

脚本会：

1. 下载 Go `nre-agent`
2. 生成 `agent.env`
3. 向 Master 注册当前节点
4. 安装并启动 systemd / launchd 服务

## 常见参数

- `--agent-name edge-01`
- `--tags edge,emby`
- `--agent-url https://edge-01.example.com`
- `--binary-url https://example.com/custom/nre-agent`

## Windows

Windows 执行面同样使用 Go agent，但通常建议直接分发独立安装包或由控制面公开的二进制资产手工安装。
