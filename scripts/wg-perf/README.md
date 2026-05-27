# WG Perf Harness

这个目录提供 `wg -> tls -> tls` 的可复用 Docker benchmark，用真实 remote `nre-agent` 进程测试握手延迟、吞吐、内存和稳定性。

默认拓扑：

`perf(cli+master+wg client) -> relay-wg(wireguard L4 entry) -> relay-a1/a2(tls_tcp) -> relay-b3/b4(tls_tcp) -> agent-b -> backend-b`

## 拓扑

- `perf`
  - mock master，给各 agent 下发 heartbeat snapshot
  - 创建 `wg0`，把最终目标 `172.30.3.12/32` 路由进 WireGuard，并访问 `172.30.3.12:9001`
  - 压测 client
- `relay-wg`
  - UDP 端点 `172.30.2.15:51820`
  - WireGuard profile 地址 `10.80.0.1/24`
  - L4 WireGuard 透明入站 `listen_port=0`
  - 捕获 WG 内原始目的地址后，使用 `proxy_egress_mode=relay` 走后续 `tls_tcp` hop
- `relay-a1` / `relay-a2`
  - 第二跳 `tls_tcp`，通过 `relay-a*.wg-perf.test` 域名解析
- `relay-b3` / `relay-b4`
  - 第三跳 `tls_tcp`，通过 `relay-b*.wg-perf.test` 域名解析
- `agent-b`
  - L4 TCP/UDP 入口 `:9001`
  - 转发到 `backend-b-local:9002`
- `backend-b`
  - 原始 TCP/UDP backend
  - TCP 支持 RTT echo、下行 bulk download、上行 upload
  - UDP 支持 datagram echo，用于 RTT 和固定 payload 循环吞吐测试

benchmark 的 WireGuard 路径故意使用 relay 域名作为每跳入口，覆盖真实链路里的 relay hop DNS 解析和缓存行为。最终目标仍使用 `agent-b` 的 `b_local` IP，让客户端路由把目标连接送入 WG 并由 `relay-wg` 透明捕获。

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

默认按“每一跳 RTT 40ms”测试，即每段 netem 单向延迟 20ms。

可以通过环境变量覆盖：

- `HARNESS_BENCHMARKS`：逗号分隔，只跑指定项，例如 `wg_to_b_c1,wg_to_b_c8`
- `HARNESS_DELAY_CLI_TO_WG_MS`
- `HARNESS_DELAY_WG_TO_RELAY_MS`
- `HARNESS_DELAY_RELAY_A_TO_RELAY_B_MS`
- `HARNESS_WG_BIND_ADDRESSES`：逗号分隔的 relay-wg WireGuard UDP 监听地址；留空使用默认 `0.0.0.0`/`::` 绑定。
- `HARNESS_C1_DURATION_SECONDS` / `HARNESS_C8_DURATION_SECONDS`：吞吐测试默认各跑 10 秒；设为 `0` 时改用固定字节数。
- `HARNESS_UDP_PAYLOAD_BYTES`：UDP echo payload 大小，默认 `1200` 字节。
- `HARNESS_UDP_INFLIGHT`：UDP 吞吐测试每个 socket 允许同时在途的 echo 包数量，默认 `512`；`*_udp_rtt` 不使用窗口，仍是单包 RTT。
- `HARNESS_UDP_BURST`：UDP 吞吐测试每轮最多补充发送的包数，默认 `32`，用于避免启动时一次性灌满窗口导致丢包。

如果不设置，脚本默认按每段 20ms 单向延迟执行。

## 结果解释

- `direct_b_*`：不经过 relay，直接打 `agent-b-control`
- `wg_to_b_*`：经过 `perf wg0 -> relay-wg -> relay-a1/a2 -> relay-b3/b4 -> agent-b`
- `*_connect`：每次新建 TCP 连接后的 echo 首包延迟
- `*_rtt`：长连接下单字节 echo RTT
- `*_c1`：单连接下行大流吞吐
- `*_c8`：8 并发下行吞吐
- `*_upload_*`：TCP 上行吞吐
- `*_udp_rtt`：UDP datagram echo RTT
- `*_udp_c1` / `*_udp_c8`：UDP echo 窗口化循环吞吐，按成功收到的回包 payload 字节计数

看优化效果时，优先关注：

- `wg_to_b_rtt` 是否接近预期 RTT
- `wg_to_b_udp_rtt` 是否接近 TCP 长连接 RTT
- `wg_to_b_c1` 和 `wg_to_b_c8` 的跌幅
- `wg_to_b_udp_c1` / `wg_to_b_udp_c8` 在延迟场景下是否出现明显断流或超时
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
