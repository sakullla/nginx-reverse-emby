# Relay Perf Harness

这个目录提供可复用的 Docker benchmark，用真实 remote `nre-agent` 进程测试：

`perf client -> agent-a -> relay-a(tls_tcp) -> agent-b -> echo backend`

## 拓扑

- `perf`
  - mock master，给 3 个 agent 下发 heartbeat snapshot
  - echo backend
  - 压测 client
- `agent-a`
  - L4 入口 `:7000`
  - 通过 `relay-a:9443` 转发到 `agent-b:9001`
- `relay-a`
  - `tls_tcp` relay listener
- `agent-b`
  - L4 入口 `:9001`
  - 转发到 `perf:9002` echo backend

## 运行

在仓库根目录执行：

```powershell
./scripts/relay-perf/run.ps1
```

会输出：

- `RESULT ...`：单项 RTT/吞吐 JSON
- `SUMMARY ...`：整组结果 JSON
- `scripts/relay-perf/docker-stats.csv`：采样的容器 CPU/Mem/NetIO

## 可调参数

`harness.go` 支持这些环境变量：

- `HARNESS_RTT_ITERATIONS`
- `HARNESS_C1_BYTES`
- `HARNESS_C8_BYTES_PER_CONN`
- `HARNESS_C8_CONCURRENCY`
- `HARNESS_MASTER_ADDR`
- `HARNESS_ECHO_ADDR`
- `HARNESS_ENTRY_ADDRESS`
- `HARNESS_DIRECT_ADDRESS`

例如：

```powershell
$env:HARNESS_C1_BYTES = 1073741824
$env:HARNESS_C8_BYTES_PER_CONN = 536870912
./scripts/relay-perf/run.ps1
```

## 结果解释

- `direct_b_*`：不经过 relay，直接打 `agent-b`
- `relay_a_to_b_*`：经过 `agent-a -> relay-a -> agent-b`
- `*_rtt`：长连接下单字节 echo RTT
- `*_c1`：单连接大流吞吐
- `*_c8`：8 并发连接吞吐

看 Relay 优化时，优先关注：

- `relay_a_to_b_c1` 和 `relay_a_to_b_c8` 是否同时提升
- `relay_a_to_b_c8` 是否明显高于 `relay_a_to_b_c1`
- `nre-agent-a` / `nre-relay-a` 的 CPU 占用是否下降，或者在同等 CPU 下吞吐更高
