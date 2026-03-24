# Master / Agent 示例

本文基于当前仓库的实际实现整理，适用于：

- **Master**：运行完整面板与后端，通常是 Docker 部署
- **Agent**：运行 `scripts/light-agent.js`，通过心跳从 Master 拉取 HTTP 规则、L4 规则和证书同步数据

当前 Agent 是**主动拉取**模式，因此适合放在 **NAT / 内网** 环境中；Master 不需要主动连回 Agent。

---

## 1. 推荐方式：一键加入 Agent

### Linux + systemd

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token change-this-register-token \
  --install-systemd
```

这个流程会尽量自动完成：

- 注册 Agent
- 下载 `light-agent.js`、`light-agent-apply.sh` 和运行时模板
- 生成 `agent.env`
- 安装并启动 systemd 服务

如需指定名称与标签：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token change-this-register-token \
  --agent-name edge-hk-01 \
  --tags edge,hk,emby \
  --install-systemd
```

### NAT / 无公网回连场景

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token change-this-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

说明：

- `--agent-url` 可以留空
- 只要 Agent 能访问 Master 即可

### 本地节点模式

如果希望远端节点跟随 Master 的部署模式处理：

```bash
join-agent.sh \
  --master-url http://master.example.com:8080 \
  --register-token change-this-register-token \
  --local-node \
  --deploy-mode front_proxy \
  --install-systemd
```

---

## 2. 手工部署时会用到的文件

- `examples/light-agent.env.example`：环境变量示例
- `examples/light-agent.service.example`：简化版 systemd 示例
- `examples/agent-apply.example.sh`：自定义 apply 脚本示例

实际运行链路通常是：

1. `light-agent.js` 心跳并拉取同步数据
2. 规则写入本地 JSON
3. 执行 `APPLY_COMMAND`
4. `light-agent-apply.sh` 生成配置并执行 `nginx -t` / reload

---

## 3. 关键环境变量

手工部署时至少关注这些字段：

- `MASTER_PANEL_URL`：Master 面板地址
- `MASTER_REGISTER_TOKEN`：注册令牌
- `AGENT_NAME` / `AGENT_TOKEN`
- `RULES_JSON` / `L4_RULES_JSON`
- `APPLY_COMMAND`
- `PROXY_DEPLOY_MODE`
- `PROXY_PASS_PROXY_HEADERS`
- `AGENT_FOLLOW_MASTER_DEPLOY_MODE`

如需证书同步，建议同时明确：

- `MANAGED_CERTS_JSON`
- `MANAGED_CERTS_POLICY_JSON`

---

## 4. 自定义 apply 脚本

如果你不想使用内置 `light-agent-apply.sh`，可以传入自定义命令：

```bash
join-agent.sh \
  --master-url http://master.example.com:8080 \
  --register-token change-this-register-token \
  --apply-command '/usr/local/bin/nginx-reverse-emby-apply.sh'
```

最小职责是：

- 读取同步后的规则文件
- 生成 Nginx 配置
- 执行 `nginx -t`
- 成功后 reload Nginx

---

## 5. 说明

- 一键安装生成的 systemd 服务比 `examples/light-agent.service.example` 更完整，通常还会额外生成证书续期服务。
- 如果只是接入普通远端节点，优先使用 `join-agent.sh`，不要手工拼装运行时文件。
