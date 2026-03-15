# Master / Agent 示例

## 场景

- **Master**：继续使用当前 Docker + 面板部署
- **Agent**：不需要 Docker、不需要面板，只需要：
  - 能访问 Master
  - 本机能执行 Nginx 配置生成 / `nginx -t` / `nginx -s reload`
  - 安装了 `node` 与 `curl`

Agent 可以在 **NAT 后**，因为它通过心跳主动连接 Master 拉取规则。

## 1) 直接加入 Agent

```bash
/opt/nginx-reverse-emby/scripts/join-agent.sh \
  --master-url http://master.example.com:8080 \
  --register-token change-this-register-token \
  --agent-name edge-01 \
  --tags edge,emby \
  --apply-command '/usr/local/bin/nginx-reverse-emby-apply.sh' \
  --install-systemd
```

## 2) 示例 agent.env

见：`examples/light-agent.env.example`

核心字段：

- `MASTER_PANEL_URL`：Master 面板地址
- `AGENT_NAME`：节点名
- `AGENT_TOKEN`：Agent 心跳 token
- `RULES_JSON`：Master 下发的规则 JSON 落盘位置
- `APPLY_COMMAND`：本机应用 Nginx 配置的命令

## 3) 示例 systemd

见：`examples/light-agent.service.example`

## 4) 示例应用脚本

见：`examples/agent-apply.example.sh`

说明：

- Agent 只负责：
  - 心跳
  - 拉取规则
  - 写入 `RULES_JSON`
  - 执行 `APPLY_COMMAND`
- 你可以把自己的 Nginx 生成逻辑封装到 `APPLY_COMMAND` 里

最小示例：

```bash
#!/bin/sh
set -eu

# 这里读取 $RULES_JSON 自行生成 nginx 配置
nginx -t
nginx -s reload
```

## 5) Master 本机节点

Master 节点仍然会以“本机节点”形式直接出现在面板中，可直接管理，不依赖心跳。
