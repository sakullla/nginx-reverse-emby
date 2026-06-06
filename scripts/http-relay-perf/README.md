# HTTP Relay Perf Harness

这个目录提供一个 Docker benchmark，用真实 `nre-agent` 进程对比 HTTP 反代的 direct 和 relay 两条路径吞吐。

## 运行

在仓库根目录执行：

```powershell
./scripts/http-relay-perf/run.ps1
```

会输出：

- `RESULT ...`：单项吞吐 JSON
- `SUMMARY ...`：整组结果 JSON
- `scripts/http-relay-perf/docker-stats.csv`：采样的容器 CPU/Mem/NetIO

## 拓扑

- `perf`：mock master + benchmark client
- `agent-a`：HTTP 反代入口，提供 direct、relay 1-hop、relay 2-hop 三个 frontend
- `relay-b`：第一跳 relay listener
- `relay-c`：第二跳 relay listener
- `backend-b`：HTTP download backend

## 可调参数

- `HARNESS_BENCHMARKS`
- `HARNESS_DOWNLOAD_BYTES`
- `HARNESS_C1_DURATION_SECONDS`
- `HARNESS_C8_DURATION_SECONDS`
- `HARNESS_C8_CONCURRENCY`
- `HARNESS_RELAY_LAYER_IDS`
- `HARNESS_RELAY2_LAYER_IDS`
- `HARNESS_RELAY_PUBLIC_HOST`
- `HARNESS_RELAY_PUBLIC_PORT`
- `HARNESS_RELAY2_PUBLIC_HOST`
- `HARNESS_RELAY2_PUBLIC_PORT`
- `HARNESS_DELAY_CLI_TO_HTTP_MS`
- `HARNESS_DELAY_HTTP_TO_BACKEND_MS`
- `HARNESS_NETEM_DELAY_MS`
- `HARNESS_PRE_MEASURE_DELAY_MS`
- `NRE_HTTP_MAX_CONNS_PER_HOST`
- `NRE_TRAFFIC_STATS_ENABLED`
