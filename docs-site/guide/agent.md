# Agent 接入

Agent 在本地或远程节点上执行代理规则。它们通过心跳同步从 Master 拉取期望状态。

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

## Windows

Windows 节点预期使用 GitHub Releases 分发的客户端安装包。
