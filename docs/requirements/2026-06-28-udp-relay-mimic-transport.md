# 需求文档：UDP Relay Mimic Transport

日期：2026-06-28
确认状态：已确认

## 0. 原始材料与需求锚点
- source_materials：
  - source_type：chat
  - source_ref：用户请求“https://github.com/hack3ric/mimic 参考这个思路，结合项目实际情况实现一种新的udp传输方式，而不是现在的uot”
  - local_ref：无
  - captured_at：2026-06-28
  - content_scope：原始需求描述与后续澄清确认
  - source_type：link
  - source_ref：https://github.com/hack3ric/mimic
  - local_ref：无
  - captured_at：2026-06-28
  - content_scope：README 与仓库元信息；核心信息为 eBPF UDP -> TCP obfuscator、UDP 包扩展 12 字节、UDP header 原地转换为 TCP header、GPL-2.0-only
  - source_type：other
  - source_ref：当前项目代码上下文
  - local_ref：go-agent/internal/modules/relay/uot.go；go-agent/internal/modules/relay/udp_stream.go；go-agent/internal/modules/relay/transport_selection.go；go-agent/internal/modules/l4/udp.go
  - captured_at：2026-06-28
  - content_scope：现有 UOT 包封装、UDP Relay 包流和 L4 UDP 入口相关实现位置
- requirement_anchors：
  - R1：新增一种项目内置的 UDP Relay 传输方式，参考 mimic 的 UDP->TCP 伪装思路，但不依赖外部 mimic；来源：用户确认“内置替代 UOT”；状态：confirmed
  - R2：本次成功标准以穿透/伪装优先，性能不能明显劣于现有 UOT；来源：用户确认“穿透/伪装优先”；状态：confirmed
  - R3：新传输先作为实验性可选能力，UOT 继续保持默认；来源：用户确认“先实验性可选”；状态：confirmed
  - R4：最小交付覆盖 go-agent、控制面下发和面板配置；来源：用户确认“后端+面板配置”；状态：confirmed
  - R5：不引入 eBPF、内核模块、TC/XDP、root 运维依赖，不复制 GPL-2.0-only 的 mimic 代码；来源：用户确认“不引入 eBPF/GPL 代码”；状态：confirmed
  - R6：核心验收路径为 L4 UDP Relay；WireGuard 透明 UDP 不作为本次强验收路径；来源：用户确认“L4 UDP Relay 优先”；状态：confirmed
- 原始材料偏差说明：mimic 原项目是系统级 eBPF/TC/XDP 实现，本需求只参考 UDP->TCP 伪装思路，明确排除系统级依赖和 GPL 代码复用。

## 1. 问题陈述
- 问题句：我们要怎样让本项目的 L4 UDP Relay 在不依赖当前 UOT 作为唯一包流封装的情况下，获得更好的 TCP 形态伪装和受限网络可用性。
- 当前问题：现有 UDP Relay 主要通过 UOT 在流式连接中承载 UDP datagram；在 UDP 被限速、阻断或特征识别的网络里，用户希望有一种更接近 TCP 形态的传输方式可选。
- 受影响用户 / 角色：面板管理员、配置 L4 UDP Relay 的使用者、运行 go-agent 的部署维护者。
- 为什么现在要做：用户明确希望参考 mimic 思路，结合当前项目实现新的 UDP 传输方式，而不是继续只依赖 UOT。
- 现有替代方案 / 当前做法：当前项目使用 UOT 包长前缀封装 UDP datagram，并已有 QUIC/TLS TCP Relay 传输选择；UOT 仍保留为默认路径。

## 2. 推荐方向
- 推荐方向：实现项目内置、实验性可选的 UDP Relay Mimic Transport，作为 L4 UDP Relay 的新传输模式。
- 选择理由：该方向贴合当前 Go agent 架构和面板配置模型，不引入 Linux 内核/eBPF/root 约束，也避免复制 GPL-2.0-only 实现代码；同时保留 UOT 默认路径降低升级风险。
- 备选方向与取舍：
  - 方向 A：直接集成外部 mimic；穿透思路最接近原项目，但引入 eBPF、内核模块、发行版和许可证边界，部署复杂。
  - 方向 B：彻底替换 UOT；用户目标最直接，但会破坏旧 agent、旧链路和现有配置的兼容性。
  - 方向 C：仅做 go-agent 原型；验证最快，但面板用户无法稳定启用或回退。
- 不选其它方向的原因：本次目标是可产品化的内置能力，并且要求兼容现有部署，因此排除外部系统依赖和强制替换。

## 3. 目标与成功标准
- 目标：
  - 新增一种可配置的 UDP Relay 传输方式，用于 L4 UDP Relay。
  - 让 UDP Relay 流量在受限网络中具备更好的 TCP 形态伪装。
  - 保留 UOT 默认路径和可回退能力，避免现有部署升级后断流。
- 成功标准：
  - 面板中可以为 L4 UDP Relay 显式启用新传输方式。
  - 控制面可以存储、校验、下发该配置到 go-agent。
  - go-agent 能在启用新传输时完成 L4 UDP datagram 的双向转发。
  - 默认行为仍使用现有 UOT，不影响未启用新模式的规则。
  - 新传输在基础吞吐/RTT 验证中不能明显劣于现有 UOT。
  - 不依赖外部 mimic、eBPF、TC/XDP、内核模块或 root 专属安装步骤。
- 验收标准：
  - 新增或更新 go-agent 单元测试，覆盖新 UDP 传输的包编码/解码、双向转发、异常关闭和回退边界。
  - 新增或更新控制面测试，覆盖配置字段校验、存储/备份/恢复、agent snapshot 下发。
  - 新增或更新前端测试，覆盖 L4 UDP Relay 表单可选新传输、默认 UOT 保持不变、切回 UOT。
  - 至少通过相关 Go 测试和前端构建/测试命令。
- 验收与需求锚点映射：R1 对应新增内置传输；R2 对应穿透/伪装与性能验收；R3 对应默认和回退；R4 对应交付范围；R5 对应依赖和许可证边界；R6 对应核心验收路径。
- 度量 / 可观察结果：
  - L4 UDP echo RTT/吞吐可通过现有或新增局部测试脚本观察。
  - 配置启用前后 agent snapshot 中的新传输模式可观察。
  - 未启用新模式的 UDP Relay 行为与现有 UOT 路径一致。

## 4. 范围
- 最小可交付范围 / 本次包含：
  - go-agent 内置 UDP Relay 新传输实现。
  - L4 UDP Relay 规则增加实验性传输模式配置。
  - panel/backend-go 增加字段校验、存储、备份恢复和下发。
  - panel/frontend 增加 L4 UDP Relay 表单配置入口。
  - 基础测试覆盖和必要用户文案。
- 不包含：
  - 不把新模式设为默认。
  - 不删除 UOT。
  - 不强制覆盖 WireGuard 透明 UDP。
  - 不做外部 mimic 运行时集成。
  - 不要求本次交付完整压测报告。
- 关键约束：
  - 默认配置必须保持现有行为。
  - 新模式必须可显式启用和切回 UOT。
  - 不复制 mimic GPL-2.0-only 代码。
  - 不增加 eBPF、TC/XDP、内核模块、root 安装等部署前置。
- 依赖与前置条件：
  - 需要在技术方案阶段确认新传输在当前 Relay 协议、Listener transport mode、agent 版本兼容中的协商方式。
  - 需要确认配置字段命名和备份兼容策略。

## 5. 业务规则与交互要求
- 核心业务能力：
  - 用户在 L4 UDP Relay 配置中选择 UDP 传输模式。
  - 系统按用户选择下发给 agent。
  - agent 根据规则使用 UOT 或新 UDP Relay Mimic Transport。
- 关键业务规则：
  - 默认值为现有 UOT。
  - 新传输为实验性可选项。
  - 仅当协议为 UDP 且使用 Relay/L4 中继路径时展示或启用该配置。
  - TCP 规则不受该配置影响。
- 关键数据 / 信息：
  - UDP 传输模式字段，例如 uot / mimic_transport，具体字段名由技术方案确定。
  - agent 能力或版本兼容信息，具体协商方式由技术方案确定。
- 用户操作与反馈：
  - 面板应明确展示当前 UDP 传输模式。
  - 切换为实验性模式时，应有简短提示说明可回退到 UOT。
  - 不需要在面板解释底层协议细节。
- 权限与数据范围：沿用现有 L4 规则管理权限与 agent 数据范围。
- 状态 / 流程变化：规则保存后通过现有控制面 snapshot/sync 流程下发。
- 审计、导入导出、历史记录等特殊要求：备份/恢复必须保留该配置；若导入旧备份缺失字段，应回落为 UOT。

## 6. 边界与异常
- 异常场景：
  - 对端 agent 不支持新模式时，规则不能静默失败；应有校验、回退或明确错误，具体由技术方案确定。
  - 新模式连接失败时，实验性配置不应影响未配置该模式的其它规则。
  - 包长度、半关闭、超时、上游 UDP 无响应等异常要有测试覆盖。
- 边界场景：
  - 大 UDP datagram 接近现有最大包限制。
  - 多并发 UDP association。
  - Relay 链路断开、重连或中间 hop 异常。
- 空数据 / 重复数据 / 无权限：沿用现有 L4 规则创建、编辑和权限逻辑。
- 兼容与历史数据：
  - 旧规则缺失新字段时必须按 UOT 处理。
  - 旧 agent 不支持新模式时不能导致默认 UOT 路径不可用。

## 7. 假设与验证
- 当前假设：
  - 当前用户主要问题来自 L4 UDP Relay 的 UOT 包流路径。
  - 新模式可以在 Go 层独立实现，不需要内核级包改写。
  - mimic 的价值点是 UDP->TCP 形态伪装思路，而不是必须复刻其 eBPF 性能路径。
- 关键假设待验证：
  - [ ] 假设：Go 层新传输能提供足够的 TCP 形态伪装；验证方式：技术方案阶段定义可观察行为和测试样例。
  - [ ] 假设：新传输性能不明显劣于 UOT；验证方式：复用或扩展现有 UDP echo RTT/吞吐测试。
  - [ ] 假设：现有 Relay 协议可以支持新模式协商且不破坏旧版本；验证方式：代码探索现有 relay protocol、snapshot 和 agent capability。
- 风险：
  - 如果只在应用层做封装，伪装效果可能不同于 mimic 的内核级 header 转换。
  - 如果没有明确版本协商，混合版本 agent 可能出现连接失败。
  - 如果 UI 暴露过多底层概念，会增加配置误用。
- 待补齐：
  - 新传输命名。
  - 协议协商方式。
  - 性能“不明显劣于”的具体阈值。

## 8. 不做事项
- 外部 mimic 集成：会引入 eBPF、内核模块、发行版和 root 运维依赖。
- 复制 mimic GPL-2.0-only 代码：存在许可证边界，不符合本次“参考思路、独立实现”的要求。
- 删除 UOT 或改默认值：会增加升级断流风险。
- 全量 UDP 路径统一改造：范围过大，本次只要求 L4 UDP Relay 优先。
- WireGuard 透明 UDP 强验收：不是本次直接核心路径。
- 完整压测报告：不是最小可交付范围，可作为后续增强。

## 9. 交接给研发
- 需求摘要：为 L4 UDP Relay 新增一种实验性可选的项目内置 UDP 传输方式，参考 mimic 的 UDP->TCP 伪装思路，作为 UOT 之外的可选模式；UOT 保持默认和可回退。
- 原始材料 refs：用户聊天确认；https://github.com/hack3ric/mimic；go-agent/internal/modules/relay/uot.go；go-agent/internal/modules/relay/udp_stream.go；go-agent/internal/modules/relay/transport_selection.go；go-agent/internal/modules/l4/udp.go
- 需求锚点 refs：R1/R2/R3/R4/R5/R6
- 代码探索关注点：
  - UOT 读写路径和 `udpPacketPeer` 抽象能否扩展为多 UDP 传输模式。
  - Relay 协议是否已有能力协商或需要新增字段。
  - L4 规则模型、snapshot、备份/恢复和前端表单的字段流转。
  - 混合版本 agent 的失败/回退行为。
- 技术方案待确认：
  - 新传输协议格式和命名。
  - 是否需要 per-rule transport mode 字段或 listener/relay 层能力字段。
  - 对端不支持时是拒绝保存、运行时报错还是自动回退。
  - 性能验收阈值。
- 任务清单：
  - 探索当前 Relay UDP/UOT/L4 配置链路。
  - 设计新传输协议与兼容策略。
  - 实现 go-agent 新传输和测试。
  - 实现 backend-go 字段校验、存储、下发、备份恢复和测试。
  - 实现 frontend 配置入口和测试。
  - 补充必要文档或帮助文案。
- 交付核验提示：交付前必须回看 R3，确保 UOT 默认不变；回看 R5，确保未引入 eBPF/GPL 代码；回看 R6，确保 L4 UDP Relay 路径有明确验收。
- 本轮追问：已完成；最终确认选项为“确认，生成需求文档”。
