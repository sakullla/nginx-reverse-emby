# HTTP / L4 优化验证（2026-11-15）

基线为 `v1.5.18` / `4c943ff5e1851e705a3a9a1b01276fec18a47017`，候选为本次提交。Go 1.27.0，Windows amd64（i7-9700F），Docker Desktop Linux 容器。原始测量保存在 [results/2026-11-15](results/2026-11-15)。

## 生产行为

- HTTP 普通网络响应复用现有 32 KiB copy pool，保留源 `WriterTo` 优先级、流式 flush 和流量统计。
- L4 UDP 以监听地址、客户端和请求目标固定会话索引，取消持锁遍历；慢建链期间并发报文共享同一会话。退休会话的读循环和 generation handle 不再关闭同一 tuple 的新会话。
- L4 TCP 每连接减少一个复制 goroutine，仍保留双向半关闭和最终流量结算。
- UOT 接收按报文长度分配，保留部分帧跨读 deadline 的解析状态；回复复用写缓冲。长度字段上限纠正为 65535，拒绝 65536 字节，避免长度回绕。
- 多跳 open ACK 等待预算包含后续每跳 dial、TLS handshake 和 open frame 预算。默认还剩一跳时为 20 秒；mux 仍受调用方 context 限制。
- 未测量直连 UDP 从现有自适应策略上限 5 秒开始，已测量快速路径继续自适应；中转 UDP 默认等待回复 30 秒。多跳可靠流上的大 UDP 报文会经历慢启动和排队，实测 60 KB 报文可超过 12 秒，原 1 秒预算会丢弃有效回复。

代价：失效中转 UDP 会话可能等更久才触发失败切换；非默认显式回复超时仍优先。没有压缩 TCP bulk buffer、调整传输窗口或取消超时边界。

## 分配与查找微基准

基线和候选使用相同 benchmark，依次运行，没有并行 Docker 吞吐或镜像构建。命令（在各自 `go-agent` 下）：

```sh
go test -run '^$' -bench 'Benchmark(CopyResponseNetworkBody|UDPFlowLookup|UDPStreamPeerFirstPacket)$' -benchmem -benchtime=500ms -count=3 ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay
```

| 场景 | 基线 | 候选 |
| --- | ---: | ---: |
| HTTP 128 B / 32 KiB / 1 MiB 响应分配 | 33512 B/op，12 allocs/op | 779–789 B/op，13 allocs/op |
| UOT 首个 128 B 报文分配 | 65603 B/op，3 allocs/op | 256 B/op，3 allocs/op |
| 10000 会话 UDP 查找耗时 | 82.6–103.0 µs/op | 0.343–0.378 µs/op |
| 10000 会话 UDP 查找分配 | 116–117 B/op，7 allocs/op | 138 B/op，8 allocs/op |

HTTP 分配字节减少约 97.7%，UOT 小包首读约 99.6%。HTTP 包装器和 UDP 完整键构造各多一次小分配；没有声称所有 allocs/op 都减少。HTTP benchmark 的源是隐藏 `WriterTo` 的内存 reader，目标是 discard writer，用于隔离复制路径分配，不代表网络带宽。UOT 大报文仍需相应缓冲。池中空闲缓冲也可能暂留，因此这些结果不能换算成进程 RSS 降幅。

## Docker 正确性和高延迟

使用 integration 专用 `proxyperf` 进程加载生产 HTTP、L4、Relay 模块，采用临时固定 pin 证书，禁止协议回退。该 fixture 不覆盖完整 Agent 注册、PKI 轮换、控制端同步或热重启。未放宽产品 PKI 检查。

每个 profile 验证 12 个场景：直连、单跳、双跳分别检查 HTTP、L4 TCP、UDP、TCP 半关闭。HTTP/TCP 校验 1 MiB 双向内容哈希，流式首段需要客户端确认后才能发送尾段；UDP 校验 128、1400、60000、128 字节；后端只在 TCP 半关闭后回复。先测试双跳冷建链，首次请求禁止重试预热。

| profile | 结果 |
| --- | --- |
| 无额外延迟 TLS/TCP，基线及候选 | 均通过 12 个场景 |
| 每段每方向 500 ms，TLS/TCP | 12 个场景及 8 项吞吐任务通过 |
| 每段每方向 500 ms，QUIC，禁止回退 | 12 个场景通过 |
| 每段每方向 750 ms，TLS/TCP，双跳 RTT 约 6 秒 | 12 个场景通过；双跳冷 HTTP 24.16 秒，60 KB UDP 15.01 秒 |

500 ms profile 的直连、单跳、双跳 RTT 分别约 2、3、4 秒。`tc netem limit 10000`，无额外指定丢包和抖动。它证明该拓扑和负载下的数据正确性，不能推导任意跳数、任意延迟或丢包下均可用。

原始基线 UOT 回归测试在半个长度头超时后读出错误长度（应为 14 B，实际 3686 B），记录见 `baseline-uot-regression.txt`。候选逐字节边界恢复测试通过。

## 吞吐观察及限制

Docker 无延迟吞吐存在大幅波动，不据此宣称整体吞吐提升，也没有排除所有环境下的回退。首轮 10 秒结果（MiB/s）：

| 场景 | 基线 | 候选 |
| --- | ---: | ---: |
| HTTP 直连 C1 | 489.71 | 262.78 |
| HTTP 双跳 C1 | 306.04 | 248.59 |
| HTTP 双跳 C8 | 455.20 | 383.91 |
| L4 直连 C1 | 288.11 | 268.93 |
| L4 双跳 C1 | 62.17 | 60.69 |
| L4 双跳 C8 | 306.64 | 219.24 |

随后候选→基线交替运行两轮：HTTP 直连候选 371–463、基线 472–485；HTTP 双跳候选 289–290、基线 243–433；L4 双跳 C8 候选 429–495、基线 543–697。将全部容器固定 `GOMAXPROCS=1`，仅测 HTTP 直连 C1 20 秒，候选 542.66、基线 443.49，方向反转。原始各轮结果全部保留，没有只选最快数据。

500 ms profile 的 20 秒吞吐：HTTP 直连/单跳/双跳 C1 为 1.423/1.176/0.934 MiB/s，双跳 C8 为 1.091；L4 对应为 1.464/0.872/0.382，双跳 C8 为 0.345。定时任务包含建链、慢启动和结束取消，不能视作稳态链路带宽。JSON 历史字段名为 `MBps`，实际单位是 MiB/s。

该高延迟运行中 Docker 内存采样峰值：入口 29.11 MiB、relay-b 108.2 MiB、relay-c 65.57 MiB。它们是容器 usage，包含运行时/缓存等影响，并非 RSS，也没有同条件基线可据此声称内存下降。

## 复现

代码验证：`TESTING.md` 的完整 Agent fast manifest（Windows）和 untagged full manifest（WSL Linux、`CGO_ENABLED=0`、root 测试进程）通过；HTTP/L4 的 `integration` 用例通过；Relay 该筛选没有 integration 用例，其行为由包测试和上述 Docker 覆盖。HTTP/L4/Relay 的 Linux `-race` 检查通过，harness `go test -count=1 ./...` 通过。

完整生产镜像构建通过：`docker build --build-arg APP_VERSION=1.5.19 -t nginx-reverse-emby:http-l4-local .`，包含前端、控制面及 Linux/macOS 的 amd64/arm64 Agent。镜像 ID 为 `sha256:6f94533424f3771c64d105bd9ab40392a1805e6672ac5a45f1af727f5cfdbb72`。这是构建验证，运行行为证据来自前述模块容器。

Windows 首轮 fast 检查曾出现心跳等待和监听端口释放失败，两用例各单独重复 3 次及完整 fast 重跑均通过；原始失败不计为通过。WSL 首次 `sudo -n` 因凭据不可用没有执行测试，随后以 WSL root 运行完整静态测试套件通过。未为消除这些现象放宽产品测试断言。

具体运行参数与清理命令见 [README.md](README.md)。基线镜像使用上述 Git OID，加上相同的 integration fixture；候选镜像 `nre-http-l4-candidate:local` 的镜像 ID 为 `sha256:5475cbb327579aeff62830ab3df49089eef1b455e1bf6162704e03962bf961af`，基线为 `sha256:bd029cfb731fd190527c8c9de952fc4a8bbf938e69bbbbb389770ec852ed591c`。镜像是本地验证产物，不是发布镜像。

单核对照使用 `docker compose config --format json` 导出同一配置，仅向各 service 的 environment 加入 `GOMAXPROCS=1`；以 `run.ps1 -ComposeFile <json> -SkipBuild -ModuleFixture -AgentImage <image>` 启动，所有延迟设为 0、`HARNESS_BENCHMARKS=http_direct_c1`、C1 duration 为 20 秒。
