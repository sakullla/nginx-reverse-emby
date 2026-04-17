# 被动自适应持续吞吐模型设计

- 日期: `2026-04-17`
- 状态: `已确认`
- 范围: `go-agent` 自适应后端排序、诊断摘要、前端诊断文案

## 背景

当前自适应排序里的“带宽”来自单次样本的 `bytesTransferred / totalDuration`。这个口径把首包前等待、后端业务处理时间和实际传输阶段混在一起，导致两个问题：

1. 小响应和慢接口会把“带宽”压得很低，但这并不代表链路或节点持续吞吐差。
2. HTTP 与 TCP 的真实流量对吞吐的学习不对称，TCP 主要只学到了建连延迟。

现有诊断字段名为 `estimated_bandwidth_bps`，但前端实际按 `B/s` 展示。本轮不改 JSON 字段形状，只修正其语义，使其代表“持续吞吐估计（B/s）”。

## 目标

1. 只使用真实业务流量被动学习，不引入主动探测。
2. 面向混合流量场景，让系统在低负载小请求场景更偏延迟，在近期中大响应更多时逐步偏向吞吐。
3. 吞吐样本不足时不输出吞吐值，也不让吞吐参与排序。
4. 保留现有 `backoff`、稳定性、恢复慢启动、冷启动探索等机制。
5. 让 HTTP 运行时、TCP 运行时和诊断页使用同一套吞吐口径。

## 非目标

1. 本轮不引入主动测速、后台定时探测或额外探针请求。
2. 本轮不修改控制面 API 结构，不新增控制面配置项。
3. 本轮不将 UDP 纳入持续吞吐排序模型，UDP 仍只学习延迟与可用性。
4. 本轮不处理 JSON 字段名兼容迁移，`estimated_bandwidth_bps` 暂时保留。

## 术语与口径

- `headerLatency`: 从请求开始到收到后端首个响应头或建立连接成功的时间。
- `totalDuration`: 整次请求或连接从开始到观测结束的总时长。
- `transferDuration`: 实际有效传输阶段时长。
- `sustainedThroughputBps`: `bytes / transferDuration`，内部与展示都按 `B/s` 解释。
- `qualified throughput sample`: 允许进入吞吐模型的真实业务样本。

### transferDuration 定义

- HTTP: `max(totalDuration - headerLatency, 0)`
- TCP: 后端到客户端方向首次写出到最后一次写出之间的真实传输活跃窗口
- UDP: 本轮不参与持续吞吐模型

## 样本分层

所有成功请求都继续学习延迟；只有满足条件的真实业务响应才学习吞吐。

### 分层规则

按单次成功样本的 `bytes` 和 `transferDuration` 将流量分为三层：

- `small`: `bytes < 128 KiB` 或 `transferDuration < 80 ms`
- `medium`: `128 KiB <= bytes < 1 MiB`
- `large`: `bytes >= 1 MiB`

### 样本进入吞吐模型的条件

样本必须同时满足以下条件才进入吞吐模型：

1. `bytes > 0`
2. `transferDuration > 0`
3. 不属于 `small`

`small` 样本只更新延迟、稳定性和流量结构，不更新吞吐估计，也不触发吞吐异常判断。

### 吞吐样本权重

- `medium` 权重: `0.5`
- `large` 权重: `1.0`

吞吐 EWMA 使用现有平滑思想，但按上述样本权重更新，避免单个中等样本与大样本具有同等影响力。

## 状态模型调整

`candidateObservation` 需要补充以下信息：

1. 持续吞吐 EWMA 与最近样本值
2. 合格吞吐样本计数
3. 合格吞吐样本累计权重
4. 最近 24h 的流量分层分布

建议新增一组按小时滚动的流量结构桶，与现有成功/失败计数桶保持相同的 `24h` 观测窗口。每个小时桶至少记录：

- `smallWeight`
- `mediumWeight`
- `largeWeight`
- `qualifiedThroughputSamples`
- `qualifiedThroughputWeight`

这样可以用同一时间窗口同时驱动动态权重、诊断展示和样本充足性判断。
所有样本计数、样本权重和流量分层分布都只统计最近 `24h observationWindow` 内的数据，超窗数据必须自然失效。

## 持续吞吐估计

### 单次样本估计

单次样本的瞬时持续吞吐定义为：

`instantThroughputBps = bytes / transferDuration`

### 平滑更新

当样本满足吞吐条件时：

1. 更新 `lastThroughputBps`
2. 按样本权重更新吞吐 EWMA
3. 更新 `lastThroughputAt`
4. 更新合格样本计数和累计权重

### 对外可见条件

只有在以下条件全部满足时，吞吐值才对外可见，也才允许进入排序：

1. 最近 `24h observationWindow` 内的合格吞吐样本数 `>= 2`
2. 最近 `24h observationWindow` 内的合格吞吐累计权重 `>= 1.5`
3. 最近吞吐样本仍在当前 `24h observationWindow` 内

不满足任一条件时，吞吐视为“未知”，对外返回空值，排序阶段也不使用吞吐分。

## 排序模型

排序主序保持不变，仍然按以下优先级进行：

1. `inBackoff`
2. `stability`
3. `performance`

### latencyScore

延迟评分继续沿用当前低延迟高分逻辑。

### throughputScore

吞吐评分基于持续吞吐估计计算，仍使用对数压缩，防止高吞吐样本对排序产生线性碾压。

### 动态权重

根据最近 `24h` 的真实流量结构决定 `performance` 中延迟与吞吐的权重。

定义：

- `smallMix = recentSmallWeight`
- `bulkMix = recentMediumWeight + recentLargeWeight`
- `totalMix = smallMix + bulkMix`
- `bulkBias = clamp(bulkMix / totalMix, 0, 1)`

当 `totalMix <= 0` 时，`bulkBias` 取 `0`，系统按纯延迟偏置退化。

权重公式：

- `latencyWeight = 0.75 - 0.40 * bulkBias`
- `throughputWeight = 0.25 + 0.40 * bulkBias`

结果范围为：

- 延迟权重: `0.75 -> 0.35`
- 吞吐权重: `0.25 -> 0.65`

### performance 计算规则

- 当延迟和吞吐都可用时：
  `performance = latencyWeight * latencyScore + throughputWeight * throughputScore`
- 当只有延迟可用时：
  `performance = latencyScore`
- 当只有吞吐可用时：
  `performance = throughputScore`
- 两者都不可用时：
  `performance = 0`

这保证了吞吐样本不足时系统自动退化为“稳定性 + 延迟优先”，不会猜测吞吐或生成默认值。

## 异常值与降权

现有吞吐异常值逻辑保留，但只对合格吞吐样本生效。

- `small` 样本不触发吞吐异常判定
- 只有在吞吐已进入可见状态后，极端低吞吐或极端高吞吐样本才可触发异常降权
- 延迟学习与稳定性学习不受此变更影响

这样可以避免单次小响应、心跳请求或短时抖动把节点错误打成吞吐异常点。

## 协议侧实现

### HTTP 运行时

在真实代理流量中：

1. `headerLatency` 继续在收到响应头时记录
2. `bytes` 使用实际写给客户端的响应体字节数
3. `transferDuration = max(totalDuration - headerLatency, 0)`

只有 `bytes > 0` 且 `transferDuration > 0` 时才进入吞吐分层与持续吞吐估计。

### HTTP 诊断

诊断探测需与运行时保持同一口径：

1. 读取完整响应体得到 `bytes`
2. 使用 `totalDuration` 与首包前耗时计算 `transferDuration`
3. 应用与运行时相同的样本分层和吞吐可见规则

诊断页展示值必须和真实运行时摘要使用同一算法，避免“线上排序”与“诊断展示”口径分裂。

### TCP 运行时

TCP 需要新增真实传输计量：

1. 连接成功时仍立即学习建连延迟
2. 在后端到客户端方向的 copy 过程中记录：
   - 首次向客户端写出时间
   - 最后一次向客户端写出时间
   - 累计写出字节数
3. 若连接全程无有效 payload，则只学习延迟，不学习吞吐
4. 若存在有效 payload，则按真实传输活跃窗口计算 `transferDuration`

只统计后端到客户端方向，确保 TCP 与 HTTP 的吞吐语义一致，均代表“回包/下载侧持续吞吐”。

### UDP

UDP 本轮不纳入持续吞吐模型，仍只更新延迟、成功率和失败回退状态。

## 诊断与前端兼容

### JSON 兼容

本轮继续输出现有字段：

- `estimated_bandwidth_bps`

但其语义明确调整为：

- `持续吞吐估计（B/s）`

该字段在吞吐样本不足时返回空值或省略，由前端显示为 `-`。

### 前端文案

诊断页文案从“评估带宽”统一改为“持续吞吐”，避免用户将其理解为链路峰值带宽或测速结果。

## 受影响文件

核心实现预计集中在以下文件：

- `go-agent/internal/backends/cache.go`
- `go-agent/internal/backends/types.go`
- `go-agent/internal/proxy/server.go`
- `go-agent/internal/l4/server.go`
- `go-agent/internal/diagnostics/http.go`
- `go-agent/internal/diagnostics/result.go`
- `go-agent/internal/task/diagnostics.go`
- `panel/frontend/src/components/RuleDiagnosticModal.vue`

## 测试策略

实现必须先以测试驱动方式落地，至少覆盖以下行为：

1. `small` 成功样本只学习延迟，不更新吞吐估计
2. 合格吞吐样本数或权重不足时，吞吐不出值，也不参与排序
3. 最近真实流量以 `small` 为主时，排序更偏延迟
4. 最近真实流量中 `medium/large` 占比上升时，排序逐步偏向吞吐
5. 单次极端大响应不能立刻压倒稳定低延迟节点
6. HTTP 运行时按 `transferDuration` 而非 `totalDuration` 学习吞吐
7. TCP 只有出现真实 payload 时才学习吞吐
8. UDP 保持当前仅学习延迟与可用性的行为
9. 诊断页摘要与运行时摘要对吞吐的可见性和数值口径一致

## 验证要求

本轮实现完成后，至少需要验证：

- `cd go-agent && go test ./internal/backends ./internal/proxy ./internal/l4 ./internal/diagnostics ./internal/task`
- `cd panel/frontend && npm run build`

如果实现中波及控制面或镜像装配，再追加更大范围验证，但这不属于本设计的默认最小验证集。

## 风险与取舍

1. 吞吐样本门槛提高后，低流量节点在较长时间内可能只按延迟排序。
   这是有意取舍，优先避免无效吞吐估计误导主流量。
2. TCP 计量需要在 copy 路径上增加轻量观测。
   设计要求只记录时间点与字节累计，避免引入额外缓冲或改变数据面行为。
3. 保留旧 JSON 字段名会继续造成语义历史包袱。
   这是兼容优先的阶段性取舍，后续可以单独做字段迁移。
