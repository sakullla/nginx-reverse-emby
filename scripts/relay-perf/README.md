# Relay Perf Harness

这个目录提供可复用的 Docker benchmark，用真实 remote `nre-agent` 进程测试下行吞吐。

默认拓扑：

`perf(cli+master) -> agent-a -> relay-b(tls_tcp) -> agent-b -> backend-b`

## 拓扑

- `perf`
  - mock master，给 3 个 agent 下发 heartbeat snapshot
  - 压测 client
- `agent-a`
  - L4 入口 `:7000`
  - 通过 `172.29.2.11:9443` 转发到 `172.29.3.12:9001`
- `relay-b`
  - `tls_tcp` relay listener
  - 与 `agent-b` / `backend-b` 处于同一侧低延迟网络
- `agent-b`
  - L4 入口 `:9001`
  - 转发到 `backend-b-local:9002`
- `backend-b`
  - 原始 TCP backend
  - 支持 RTT echo 和下行 bulk download 两种模式

为了避免 Docker 多网络别名解析带来的不确定性，benchmark 默认使用固定容器 IP 作为 relay 和 backend 跳点。

## 运行

在仓库根目录执行：

```powershell
./scripts/relay-perf/run.ps1
```

会输出：

- `RESULT ...`：单项 RTT/吞吐 JSON
- `SUMMARY ...`：整组结果 JSON
- `scripts/relay-perf/docker-stats.csv`：采样的容器 CPU/Mem/NetIO

`run.ps1` 默认仍然会每轮 `docker compose up -d --build`，避免压测误用旧 agent 代码。
`perf` / `backend-b` 用的是专用 runner 镜像，`agent-a` / `relay-b` / `agent-b` 用的是测试专用 agent 镜像，`tc` 依赖都预装好了，不再在容器里临时装 `iproute2`。

## 可调参数

`harness.go` 支持这些环境变量：

- `HARNESS_RTT_ITERATIONS`
- `HARNESS_C1_BYTES`
- `HARNESS_C1_DURATION_SECONDS`
- `HARNESS_C8_BYTES_PER_CONN`
- `HARNESS_C8_DURATION_SECONDS`
- `HARNESS_C8_CONCURRENCY`
- `HARNESS_MASTER_ADDR`
- `HARNESS_ENTRY_ADDRESS`
- `HARNESS_DIRECT_ADDRESS`
- `HARNESS_PRE_MEASURE_DELAY_MS`
- `HARNESS_BACKEND_HOST`
- `HARNESS_BACKEND_PORT`
- `HARNESS_RELAY_PUBLIC_HOST`
- `HARNESS_RELAY_PUBLIC_PORT`

`run.ps1` 另外支持这些宿主机环境变量：

- `HARNESS_DELAY_CLI_TO_A_MS`
- `HARNESS_DELAY_A_TO_RELAY_MS`
- `HARNESS_NETEM_DELAY_MS`
- `HARNESS_AGENT_HEARTBEAT_INTERVAL`

`run.ps1` 还支持这些开关：

- `-SkipStats`

例如：

```powershell
$env:HARNESS_C1_DURATION_SECONDS = 30
$env:HARNESS_C8_DURATION_SECONDS = 30
./scripts/relay-perf/run.ps1
```

如果没有设置 `*_DURATION_SECONDS`，会继续走原来的按字节模式。

## 真实链路复现

如果要本机模拟：

- `CLI -> A` 单向 `30ms`
- `A -> Relay B` 单向 `10ms`

可以这样跑：

```powershell
$env:HARNESS_DELAY_CLI_TO_A_MS = 30
$env:HARNESS_DELAY_A_TO_RELAY_MS = 10
./scripts/relay-perf/run.ps1
```

脚本会按链路接口注入 `tc netem`：

- `perf(cli)` 和 `agent-a` 的 `cli_a` 接口加 `30ms`
- `agent-a` 和 `relay-b` 的 `a_relay` 接口加 `10ms`
- `relay-b -> agent-b -> backend-b` 保持本地低延迟

检测到任一延迟变量时，脚本会自动设置 `HARNESS_PRE_MEASURE_DELAY_MS=8000`，给 `tc` 安装和规则下发留时间；也可以手动覆盖。

`HARNESS_NETEM_DELAY_MS` 保留为兼容参数；如果没设置 `HARNESS_DELAY_A_TO_RELAY_MS`，它会退化为对 `A <-> Relay` 链路注入同样的单向延迟。

## 结果解释

- `direct_b_*`：不经过 relay，直接打 `agent-b-control`
- `relay_a_to_b_*`：经过 `agent-a -> relay-b -> agent-b`
- `*_rtt`：长连接下单字节 echo RTT
- `*_c1`：单连接下行大流吞吐
- `*_c8`：8 并发下行吞吐

这个 harness 现在的吞吐测试是下载模型，不再是客户端写入后端回显。

为了降低压测时的非业务噪声：

- `agent-a` / `relay-b` / `agent-b` 的 `NRE_DATA_DIR=/data` 现在挂在容器 `tmpfs`
- agent heartbeat 默认从 `500ms` 调整为 `${HARNESS_AGENT_HEARTBEAT_INTERVAL:-30s}`
- `perf` / `backend-b` 的 `go run` 编译缓存现在放在容器 `/tmp` 的 `tmpfs`
- `docker-stats.csv` 改成采样期间先存在内存里，结束后一次性落盘

之前看到的磁盘写入，主要不是 bulk traffic 本身，而是 agent 每次 heartbeat 同步后落盘：

- `desired-snapshot.json`
- `applied-snapshot.json`
- `runtime-state.json`

底层保存路径会走 `CreateTemp -> Sync -> Rename`，所以高频 heartbeat 会放大 `fsync`/rename 噪声。现在默认把这部分写入留在内存盘里，避免干扰吞吐和 CPU 观察。

如果这轮只关心吞吐，不关心 CPU/Mem 采样，可以再加：

```powershell
./scripts/relay-perf/run.ps1 -SkipStats
```

这样连 `docker-stats.csv` 也不会写。

看 Relay 优化时，优先关注：

- `relay_a_to_b_c1` 和 `relay_a_to_b_c8` 是否同时提升
- `relay_a_to_b_c8` 是否明显高于 `relay_a_to_b_c1`
- `nre-agent-a` / `nre-relay-b` 的 CPU 占用是否下降，或者在同等 CPU 下吞吐更高

排查高延迟下的 speedtest 回退时，优先比较：

- 不加链路时延时的局域网吞吐
- 加 `HARNESS_DELAY_CLI_TO_A_MS=30` 和 `HARNESS_DELAY_A_TO_RELAY_MS=10` 后 `relay_a_to_b_c1` 的跌幅
- 不同版本在同一链路时延模型下的 `relay_a_to_b_c1` / `relay_a_to_b_c8`
