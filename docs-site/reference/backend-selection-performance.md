# 后端选择与 DNS 缓存性能

这次优化针对 Go Agent 的共享后端选择路径，HTTP、L4 和 Relay 的调用方沿用现有接口，无新增配置。

## 实现变化

- `backends_dns.go` 负责 DNS 解析、缓存和同域名并发请求合并。等待者可以独立取消；发起者取消后，仍有效的等待者重新发起解析。不同域名不互相等待，网络解析期间不持有缓存锁。
- DNS 缓存内部结果不可变，命中时不再复制 IP 字符串切片；对外返回的 Candidate 切片仍独立。Clone 只复制已完成缓存，不共享正在进行的解析。
- DNS TTL 从解析完成时计算。错误兜底也按完成时间检查过期窗口；取消不再返回旧地址。无有效 IP 的解析结果也可在既有窗口内使用旧地址。失败不会负缓存。
- `backends_selection.go` 集中处理轮询、随机、自适应选择和探索。轮询直接填充最终顺序，探索在本次请求拥有的切片上旋转。
- 后端和 IP 选择共用稳定排序比较器，去掉比较过程中的字符串归一化与 map 查找。单个 IP 不再建立评分快照。
- 观测摘要一次汇总 24 小时桶，再复用快照评分；旧评分实现只保留在测试中作为独立回归对照。

## 验证

Go 1.27.0、Linux amd64。以下检查通过：

```sh
cd go-agent
go test -race -count=1 -timeout=30s ./internal/model
go vet ./internal/model
go build ./cmd/nre-agent
go test -count=1 -timeout=30s ./internal/model ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay
```

新增 11 个测试覆盖并发合并、取消隔离、失败重试、不同域名隔离、TTL/旧缓存窗口、Clone 隔离、随机数据与旧排序算法对照、轮询和探索旋转、摘要评分一致性。

已执行根目录 `TESTING.md` 中 Agent 的规范 fast 命令：24 个包中 22 个通过；`internal/plugins/process` 和 `internal/plugins/rpc` 因执行环境不支持测试所需的 UID 所有权操作而失败（`chown/lchown: invalid argument`）。两个失败包在基线提交上用同样的 fast 参数复现，因此不能把完整 fast 层标记为通过。仍须在支持该隔离能力的 CI/主机完成验证。未运行 Docker 构建、真实媒体服务器或生产流量压测。

## 基准复现

基线：`8d700075d31b861610f30a37fbed92260e2b4492`。对照时将本分支的 `go-agent/internal/model/backends_perf_bench_test.go` 复制到基线工作树；其他文件保持原样。基线和修改版顺序执行，避免并行测试/编译干扰。

```sh
cd go-agent
go test -run '^$' -bench 'Benchmark(BackendSelection|DNSCacheHit|ObservationSummary)$' -benchmem -benchtime=300ms -count=3 ./internal/model
```

下表为同一机器三次运行的中位数。机器为 AMD EPYC 9V74，Go 使用 8 个逻辑处理器。微基准只衡量这些函数，不能推导整体代理吞吐、视频首帧或公网延迟改善比例；共享机器计时存在波动，内存分配计数更稳定。DNS 并发测试确认 17 个同时等待的请求只调用一次 resolver。

| 场景 | 原耗时 ns/op | 修改后 ns/op | 耗时变化 | 原 B/op → 新 B/op | 原 allocs/op → 新 allocs/op |
|---|---:|---:|---:|---:|---:|
| round_robin/1 | 28.8 | 32.9 | +13.9% | 48 → 48 | 1 → 1 |
| adaptive/1 | 29.0 | 33.5 | +15.4% | 48 → 48 | 1 → 1 |
| resolved/1 | 914.3 | 33.7 | -96.3% | 1512 → 48 | 5 → 1 |
| round_robin/4 | 168.2 | 123.0 | -26.9% | 280 → 160 | 1 → 1 |
| adaptive/4 | 2424.0 | 2333.0 | -3.8% | 2056 → 1696 | 6 → 3 |
| resolved/4 | 2451.0 | 2273.0 | -7.3% | 2504 → 1696 | 7 → 3 |
| round_robin/16 | 554.3 | 320.3 | -42.2% | 1364 → 704 | 1 → 1 |
| adaptive/16 | 12322.0 | 12083.0 | -1.9% | 7720 → 7488 | 6 → 3 |
| resolved/16 | 13483.0 | 10944.0 | -18.8% | 9872 → 7488 | 9 → 3 |
| round_robin/64 | 2391.0 | 1126.0 | -52.9% | 5334 → 2688 | 1 → 1 |
| adaptive/64 | 53287.0 | 53310.0 | +0.0% | 29544 → 27264 | 6 → 3 |
| resolved/64 | 59791.0 | 52184.0 | -12.7% | 37712 → 27264 | 9 → 3 |
| DNSCacheHit | 353.4 | 315.7 | -10.7% | 197 → 160 | 7 → 5 |
| ObservationSummary | 623.8 | 335.5 | -46.2% | 0 → 0 | 0 → 0 |
