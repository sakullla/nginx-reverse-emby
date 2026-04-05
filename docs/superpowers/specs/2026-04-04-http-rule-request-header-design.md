# HTTP 规则请求头配置设计

- 日期：2026-04-04
- 状态：已评审，通过进入实现规划
- 范围：HTTP 规则表单、规则存储、规则接口、Nginx 请求头生成

## 1. 背景

当前 HTTP 规则只支持基础的前后端 URL、标签、启用状态，以及 `proxy_redirect`。请求头相关行为主要由 Nginx 生成脚本中的固定逻辑决定，其中 `PROXY_PASS_PROXY_HEADERS` 仅提供全局级别的转发头开关，无法按规则单独控制，也无法为单条规则配置自定义 `User-Agent` 或额外 Header。

本次需求是在 HTTP 规则里补齐请求头配置能力，同时保持现有规则和部署尽量兼容。

## 2. 目标

### 2.1 本次要支持

1. 在 HTTP 规则表单中新增“请求头配置” tab。
2. 支持为单条 HTTP 规则配置独立 `User-Agent`。
3. `User-Agent` 提供内置预设，下拉选择后回填到可编辑输入框。
4. 支持为单条 HTTP 规则配置额外自定义请求头。
5. 自定义 Header 支持覆盖系统默认头。
6. 支持为单条 HTTP 规则配置是否透传客户端 IP / `X-Forwarded-*` 头。
7. 保持旧规则兼容。
8. 保留全局环境变量 `PROXY_PASS_PROXY_HEADERS`，但其语义调整为“全局禁用优先”。

### 2.2 本次明确不做

1. 不支持用户输入 Nginx 变量，如 `$host`、`$remote_addr`。
2. 不支持用户输入原始 Nginx 片段。
3. 不支持响应头配置。
4. 不支持 Header 模板、条件注入、按路径注入。
5. 不支持把 `User-Agent` 放进自定义 Header 列表里配置。

## 3. 推荐方案

采用“在现有 HTTP 规则上增量扩展一个请求头配置 tab”的方案。

### 原因

1. 改动范围聚焦，和现有 `RuleForm` 的交互方式一致。
2. 不需要把 HTTP 规则重新抽象成更重的策略对象。
3. 能直接满足本次需求，并为后续扩展其他请求头选项保留空间。
4. 对已有数据、已有接口、已有部署影响最小。

## 4. UI 与交互设计

## 4.1 表单结构

HTTP 规则表单调整为两个 tab：

### 基础配置

保留现有字段：

- `frontend_url`
- `backend_url`
- `tags`
- `enabled`
- `proxy_redirect`

### 请求头配置

新增字段：

- UA 预设下拉
- `User-Agent` 可编辑输入框
- “透传客户端 IP 与转发头”开关
- 自定义 Header 列表

这样可以避免基础表单继续变长，同时把同一类配置收拢到一个独立区域。

## 4.2 User-Agent 区块

UI 由两部分组成：

1. 一个预设下拉。
2. 一个可编辑输入框。

### 交互规则

1. 选择预设后，把对应的完整 UA 字符串写入输入框。
2. 输入框允许继续手动修改。
3. 如果输入框的值不再匹配任何预设，下拉显示“自定义”。
4. 输入框为空表示“不额外设置自定义 UA”。

### v1 预设

v1 先提供以下预设：

| 显示名称 | 实际值 |
| --- | --- |
| 自定义 | 不写固定值，完全由输入框决定 |
| Chrome | `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36` |
| 小幻影视 | `RodelPlayer` |
| Hills | `Hills` |
| SenPlayer | `SenPlayer` |

说明：

1. `RodelPlayer` 先不带版本号。
2. `Hills` 先暂时按完整字符串 `Hills` 处理。
3. 预设只作为前端编辑辅助，后端只存最终字符串。

## 4.3 透传 IP 开关

该开关控制规则级的客户端 IP / 转发头透传行为。

### 文案

建议文案为：

- 标题：`透传客户端 IP 与转发头`
- 说明：`控制 X-Real-IP、X-Forwarded-Host、X-Forwarded-Port、X-Forwarded-For、X-Forwarded-Proto`

### 正常状态

全局未禁用时，用户可以直接按规则开关该项。

### 被全局禁用覆盖时

如果全局 `PROXY_PASS_PROXY_HEADERS` 处于禁用状态：

1. 规则开关显示禁用态。
2. UI 保留该规则已保存的值，便于用户知道规则自身的意图。
3. UI 显示提示：当前规则设置已被全局禁用覆盖，不会生效。

## 4.4 自定义 Header 列表

每一行包含：

- Header Name
- Header Value
- 删除按钮

底部提供“+ 添加 Header”按钮，用户点击后新增一行空白项。

### 编辑规则

1. 不自动插入默认空行。
2. 仅在用户点击“添加 Header”时新增行。
3. 前端校验错误尽量定位到具体行。

### 列表校验

1. `name` 必填。
2. `name` 需要去首尾空格。
3. `value` 允许为空字符串。
4. `name` 按大小写不敏感判重。
5. `User-Agent` 不允许出现在列表里，必须使用独立字段。

## 4.5 规则列表页提示

HTTP 规则卡片不直接展开显示所有请求头细节，只做轻量标识：

- `UA`：配置了自定义 UA
- `Headers`：存在自定义 Header
- `No IP Forward`：规则关闭了透传客户端 IP / 转发头

这样能保持列表页简洁，同时让用户快速识别特殊规则。

## 5. 数据结构设计

在现有 HTTP rule 上新增以下字段：

```json
{
  "pass_proxy_headers": true,
  "user_agent": "",
  "custom_headers": [
    { "name": "Referer", "value": "https://example.com/" }
  ]
}
```

### 字段定义

- `pass_proxy_headers: boolean`
  - 规则级是否透传客户端 IP / 转发头
  - 默认值 `true`
- `user_agent: string`
  - 独立 UA 字符串
  - 默认值 `""`
- `custom_headers: Array<{ name: string, value: string }>`
  - 自定义请求头列表
  - 默认值 `[]`

### 为什么不额外引入嵌套对象

本次需求仍然是对单条 HTTP 规则的少量扩展，直接加字段更符合现有代码结构，前后端改动也更小。为了控制范围，本次不引入新的大层级对象如 `request_header_policy`。

## 6. API 与存储设计

## 6.1 API

以下接口直接透传新增字段：

- `GET /api/agents/:id/rules`
- `POST /api/agents/:id/rules`
- `PUT /api/agents/:id/rules/:ruleId`

前端对应：

- `fetchRules`
- `createRule`
- `updateRule`

## 6.2 后端归一化

`normalizeRulePayload()` 需要新增处理：

- `pass_proxy_headers`
- `user_agent`
- `custom_headers`

默认值策略：

- `pass_proxy_headers` 缺失时视为 `true`
- `user_agent` 缺失时视为 `""`
- `custom_headers` 缺失时视为 `[]`

同时新增校验：

1. `custom_headers[].name` 必须是合法 header name。
2. `custom_headers[].value` 必须是普通固定字符串，不允许控制字符。
3. `custom_headers` 内部名称大小写不敏感去重。
4. 禁止 `custom_headers` 中出现 `User-Agent`。

## 6.3 存储层

以下存储路径都要支持新字段：

- JSON 文件存储
- sqlite 归一化存储
- Prisma schema / 映射
- 属性测试 round-trip / 兼容性 / 隔离性

旧规则未包含新字段时，读取后按默认值补齐。

## 7. 全局与规则级生效优先级

## 7.1 目标语义

`PROXY_PASS_PROXY_HEADERS` 改为“全局禁用优先”：

1. 默认不禁用。
2. 默认情况下，按每条规则的 `pass_proxy_headers` 决定是否透传 IP / 转发头。
3. 如果全局显式禁用，则所有规则都强制不透传 IP / 转发头。
4. 规则配置值仍然保留，但在全局禁用时不生效。

## 7.2 生效优先级

优先级从高到低：

1. 全局禁用
2. 规则级 `pass_proxy_headers`
3. 系统默认行为

## 7.3 兼容性说明

这样做可以满足两个目标：

1. 旧部署不需要立即迁移所有规则。
2. 新规则可以获得按条控制能力。

## 8. Nginx 渲染设计

## 8.1 渲染目标

本次不再只拼接 `forward_headers_config`，而是明确构建一份“最终请求头配置”，再渲染为 Nginx `proxy_set_header` 片段。

## 8.2 渲染顺序

每条规则按以下顺序构建最终头部集合：

### 第一步：基础默认头

始终存在：

- `Host`
- `Upgrade`
- `Connection`

### 第二步：规则级 IP / 转发头

如果“全局未禁用”且 `rule.pass_proxy_headers === true`，加入：

- `X-Real-IP`
- `X-Forwarded-Host`
- `X-Forwarded-Port`
- `X-Forwarded-For`
- `X-Forwarded-Proto`

否则不加入这些头。

### 第三步：独立 UA

如果 `user_agent` 非空，加入：

- `User-Agent`

### 第四步：自定义 Header

逐条应用 `custom_headers`：

1. 如果名称和已有默认头重名，则覆盖已有值。
2. 如果名称不存在，则新增。
3. 名称比较大小写不敏感。

## 8.3 最终模板变量

Nginx 模板建议统一消费一个变量：

- `${proxy_headers_config}`

而不是继续把头部逻辑拆成：

- 固定 `proxy_set_header Host ...`
- `${forward_headers_config}`
- 后续更多零散插入

这样能让覆盖逻辑更清晰，也能避免未来继续追加自定义头时模板越来越碎。

## 8.4 字符串转义

渲染 Nginx 配置时需要保证安全：

1. Header 名必须先通过合法性校验。
2. Header 值要做双引号与反斜杠转义。
3. Header 值拒绝换行和控制字符。
4. 不支持变量替换，因此值始终按普通字符串处理。

## 9. 错误处理

### 前端

1. Header 名为空时阻止提交。
2. Header 名重复时阻止提交。
3. Header 名为 `User-Agent` 时阻止提交，并提示改用独立字段。
4. 全局禁用覆盖规则级透传时，给出只读提示而不是静默失效。

### 后端

1. 对所有新增字段再次校验，不能只依赖前端。
2. 非法 header name 或危险 value 直接返回 400。
3. 存储层缺失字段时补默认值，不把旧数据当成异常。

### Nginx 生成

1. 对所有用户输入值先归一化，再进入模板。
2. 头部合并逻辑应保证最终只输出一个同名 header。

## 10. 测试设计

## 10.1 前端

最少需要覆盖：

1. UA 预设选择会正确回填输入框。
2. 手动修改 UA 后，下拉能回到“自定义”。
3. 自定义 Header 可增删改。
4. 重名 Header 会被前端拦截。
5. `User-Agent` 出现在自定义 Header 列表里会被拦截。
6. 全局禁用时，规则级透传开关显示禁用态和提示文案。

## 10.2 后端

最少需要覆盖：

1. 新字段 round-trip。
2. 旧规则补默认值后 round-trip 仍然稳定。
3. Header 名大小写不敏感去重。
4. `User-Agent` 出现在自定义 Header 列表时拒绝保存。
5. 非法 header name / 控制字符 value 时拒绝保存。

## 10.3 集成

最少需要验证：

1. 开启规则级透传时，生成配置包含 `X-Real-IP` 和 `X-Forwarded-*`。
2. 关闭规则级透传时，生成配置不包含这些头。
3. 全局禁用时，无论规则配置如何，都不包含这些头。
4. 配置自定义 UA 时，最终生成 `proxy_set_header User-Agent "...";`
5. 自定义 Header 能覆盖默认头，例如覆盖 `Host` 或 `X-Forwarded-For`。

## 11. 实现边界

本次实现只聚焦 HTTP 规则，不扩展到：

- L4 规则
- 面板自身代理头配置
- 响应头配置
- 条件化 Header 策略系统

## 12. 实现建议顺序

1. 先扩展规则数据结构与后端校验。
2. 再扩展前端 `RuleForm` 与 API 调用。
3. 最后调整 Nginx 头部渲染逻辑与集成验证。

这样可以先把数据模型打通，再减少 Nginx 逻辑改动时的变量不确定性。
