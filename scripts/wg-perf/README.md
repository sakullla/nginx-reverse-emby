# WG Perf Harness

这个目录提供 `wg -> tls -> tls` 的可复用 Docker benchmark，用真实 remote `nre-agent` 进程测试握手延迟、吞吐、内存和稳定性。

默认拓扑：

`perf(cli+master+wg client) -> relay-wg(wireguard L4 entry) -> relay-a1/a2(tls_tcp) -> relay-b3/b4(tls_tcp) -> agent-b -> backend-b`

## 拓扑

- `perf`
  - mock master，给各 agent 下发 heartbeat snapshot
  - 创建 `wg0`，作为 WireGuard client 访问 `10.80.0.1:7000`
  - 压测 client
- `relay-wg`
  - UDP 端点 `172.30.2.15:51820`
  - WireGuard profile 地址 `10.80.0.1/24`
  - L4 WireGuard 入站 `10.80.0.1:7000`
  - relay 出站继续走后续 `tls_tcp` hop
- `relay-a1` / `relay-a2`
  - 第二跳 `tls_tcp`
- `relay-b3` / `relay-b4`
  - 第三跳 `tls_tcp`
- `agent-b`
  - L4 入口 `:9001`
  - 转发到 `backend-b-local:9002`
- `backend-b`
  - 原始 TCP backend
  - 支持 RTT echo 和下行 bulk download 两种模式

为了避免 Docker 多网络别名解析带来的不确定性，benchmark 默认使用固定容器 IP 作为 relay 和 backend 跳点。

## 运行

在 `scripts/wg-perf` 目录执行：

```powershell
./run.ps1
```

会输出：

- `RESULT ...`：单项 RTT/吞吐 JSON
- `SUMMARY ...`：整组结果 JSON
- `docker-stats.csv`：采样的容器 CPU/Mem/NetIO

## 延迟模型

默认按“每一跳 40ms 单向延迟”测试。

可以通过环境变量覆盖：

- `HARNESS_DELAY_CLI_TO_WG_MS`
- `HARNESS_DELAY_WG_TO_RELAY_MS`
- `HARNESS_DELAY_RELAY_A_TO_RELAY_B_MS`

如果不设置，脚本默认按 40ms 单向延迟执行。

## 结果解释

- `direct_b_*`：不经过 relay，直接打 `agent-b-control`
- `wg_to_b_*`：经过 `perf wg0 -> relay-wg -> relay-a1/a2 -> relay-b3/b4 -> agent-b`
- `*_connect`：每次新建 TCP 连接后的 echo 首包延迟
- `*_rtt`：长连接下单字节 echo RTT
- `*_c1`：单连接下行大流吞吐
- `*_c8`：8 并发下行吞吐

看优化效果时，优先关注：

- `wg_to_b_rtt` 是否接近预期 RTT
- `wg_to_b_c1` 和 `wg_to_b_c8` 的跌幅
- `nre-relay-wg` 的内存和线程数是否异常增长

## pprof

所有 agent 默认启用 `NRE_PPROF_ADDR=:6060`。宿主机默认端口：

- `relay-wg`: `6060`
- `relay-a1`: `6061`
- `relay-a2`: `6062`
- `relay-b3`: `6063`
- `relay-b4`: `6064`
- `agent-b`: `6065`

示例：

```powershell
go tool pprof -top http://127.0.0.1:6060/debug/pprof/profile?seconds=20
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
curl http://127.0.0.1:6060/debug/pprof/goroutine?debug=1
```

端口可通过对应的 `HARNESS_*_PPROF_PORT` 覆盖；监听地址可通过对应的 `HARNESS_*_PPROF_ADDR` 覆盖。设为空值会关闭该 agent 的 pprof。
