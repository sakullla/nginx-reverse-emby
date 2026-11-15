# HTTP / L4 Relay Docker Harness

本地验证生产 HTTP、L4、Relay 转发模块的直连、单跳和双跳路径。支持 TLS/TCP 与 QUIC；使用临时证书和固定 pin，默认禁止传输协议回退。

## 运行

在仓库根目录执行：

    $env:HARNESS_VERIFY = '1'
    $env:HARNESS_C1_DURATION_SECONDS = '20'
    $env:HARNESS_C8_DURATION_SECONDS = '20'
    ./scripts/http-relay-perf/run.ps1 -ModuleFixture

-ModuleFixture 编译 go-agent/testing/proxyperf（仅 integration 构建），在独立容器中启动生产转发模块。它使用一次性模拟配置，不运行 Agent 注册、PKI 轮换、控制端同步或热重启流程，也不修改这些产品入口的安全检查。完整 Agent 镜像构建入口 Dockerfile.agent 保留，但旧模拟 heartbeat 不提供当前 Agent 所需的 PKI 注册协议，不能作为完整 Agent 端到端验收。

runner 在镜像构建时编译，测量期间没有 go run 编译器或 Go 构建缓存干扰。脚本默认重新构建；仅对已确认来源的镜像使用 -SkipBuild -AgentImage <image>。

## 高延迟及多跳

    $env:HARNESS_VERIFY = '1'
    $env:HARNESS_DELAY_CLI_TO_HTTP_MS = '500'
    $env:HARNESS_DELAY_RELAY_MS = '500'
    $env:HARNESS_DELAY_HTTP_TO_BACKEND_MS = '500'
    $env:HARNESS_C1_DURATION_SECONDS = '20'
    $env:HARNESS_C8_DURATION_SECONDS = '20'
    ./scripts/http-relay-perf/run.ps1 -ModuleFixture

延迟施加于各网络接口的两个方向：

- client ↔ 入口：每方向 500 ms；
- 入口 ↔ relay-b、relay-b ↔ relay-c：每段每方向 500 ms；
- 最后一跳 ↔ backend：每方向 500 ms。

对应直连 RTT 约 2 秒、单跳约 3 秒、双跳约 4 秒。控制网络不加延迟。tc netem limit 10000 避免默认小队列额外造成未声明的丢包；脚本输出实际 qdisc 配置。所有延迟安装完成且模块节点确认就绪后，先验证双跳冷连接，随后才验证短路径。模块模式首次健康请求失败会直接失败，不能靠重试预热掩盖冷启动故障。

只跑数据正确性：

    $env:HARNESS_BENCHMARKS = 'verify_only'
    $env:HARNESS_VERIFY = '1'
    ./scripts/http-relay-perf/run.ps1 -ModuleFixture

QUIC 必须真实成功，不会静默回退 TLS/TCP：

    $env:HARNESS_RELAY_TRANSPORT = 'quic'
    ./scripts/http-relay-perf/run.ps1 -ModuleFixture

无延迟基线将上述三个 HARNESS_DELAY_* 都设为 0。

## 校验与测量

HARNESS_VERIFY=1 同时检查 12 个场景：直连、单跳、双跳各自的 HTTP、L4 TCP、L4 UDP、TCP 半关闭。

HTTP/L4 TCP 校验 1 MiB 上传和下载的 SHA-256；流式端点在首段送达客户端并获得独立确认后才发送尾段，因此可检测整段缓冲，不依赖短暂的时间间隔。半关闭后后端才回送数据，校验双向关闭语义。UDP 校验 128、1400、60000、128 字节报文，并跨过 250 ms 读轮询边界。

- VERIFY：一个场景成功；任意失败使进程退出非零。
- UDP_PACKET：报文大小及包含建链在内的往返时间。
- RESULT / SUMMARY：单连接、8 并发吞吐。
- docker-stats.csv：容器 CPU、内存和 NetIO 采样；不是 Go 堆或进程 RSS 的等价物。

吞吐项目：http_{direct,relay,relay2}_{c1,c8} 和 l4_{direct,relay,relay2}_{c1,c8}。用 HARNESS_BENCHMARKS 逗号列表选择。所有 HTTP 响应必须是 200；零字节吞吐失败。定时吞吐包含连接建立、慢启动及截止取消，不能直接当成稳态带宽。

默认拓扑：perf -> agent-a -> relay-b -> relay-c -> backend-b。HTTP 入口为 8081/8082/8083，L4 TCP 为 7001/7002/7003，UDP 为 7101/7102/7103，半关闭入口为 7201/7202/7203。后端分别在 9002、9003、9004。

其他参数：

- HARNESS_C8_CONCURRENCY、HARNESS_DOWNLOAD_BYTES（未设置 duration 时使用固定字节模式）。
- HARNESS_RELAY_LAYER_IDS、HARNESS_RELAY2_LAYER_IDS。
- HARNESS_PRE_MEASURE_DELAY_MS：可选等待，不替代启动屏障。
- HARNESS_NETEM_DELAY_MS：后端链路延迟的兼容别名。
- NRE_TRAFFIC_STATS_ENABLED：模块测试默认开启流量统计。
- -StatsFile、-SkipStats、-KeepUp、-TimeoutSeconds（默认 900）。

脚本成功或失败都会清理容器和网络，除非显式使用 -KeepUp。保留后执行：

    docker compose -f scripts/http-relay-perf/docker-compose.yaml down -v

本轮实测和限制见 [PERFORMANCE.md](PERFORMANCE.md)。