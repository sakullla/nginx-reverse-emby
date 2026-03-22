# Master / Agent 示例

## 场景

- **Master**：继续使用当前 Docker + 面板部署
- **Agent**：不需要 Docker，也不需要面板，只需要：
  - 能访问 Master
  - 本机能执行 Nginx 配置生成 / `nginx -t` / `nginx -s reload`
  - 安装了 `node` 和 `curl`

Agent 可以位于 **NAT 后**，因为它通过心跳主动连接 Master 拉取规则。

## 1）直接加入 Agent

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token change-this-register-token \
  --install-systemd
```

默认流程会从 Master 面板下载 light-agent、默认 nginx apply 脚本与模板资源，不依赖外部托管。

如果目标机器缺少 `Node.js 18+`、`curl` 或 `nginx`，安装脚本会尝试自动安装；建议使用 `root` 或具备 `sudo` 权限的用户执行。

如需覆盖本机 apply 逻辑，可额外传入：

```bash
--apply-command '/usr/local/bin/nginx-reverse-emby-apply.sh'
```

## 1.1）NAT 场景

如果 Agent 在 NAT 后：

- 不需要开放入站端口给 Master
- `--agent-url` 可以留空
- Agent 只要能主动访问 `--master-url` 即可

示例：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token change-this-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

## 2）示例 agent.env

见：`examples/light-agent.env.example`

核心字段：
- `MASTER_PANEL_URL`：Master 面板地址
- `AGENT_NAME`：节点名
- `AGENT_TOKEN`：Agent 心跳 token
- `RULES_JSON`：Master 下发规则的 JSON 落盘位置
- `APPLY_COMMAND`：本机应用 Nginx 配置的命令

## 3）示例 systemd

见：`examples/light-agent.service.example`

## 4）示例应用脚本

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

## 5）Master 本机节点

Master 节点仍然会以“本机节点”形式直接出现在面板中，可直接管理，不依赖心跳。
