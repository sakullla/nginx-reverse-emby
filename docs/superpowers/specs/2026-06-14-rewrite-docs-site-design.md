# 重写 docs-site — 设计文档

> 状态：已批准 | 日期：2026-06-14

## 目标

重写 nginx-reverse-emby 的 VitePress 文档站，解决三个核心问题：

1. **内容深度参差不齐** — 部分页面过浅，部分缺乏关键细节
2. **概念重复冗余** — 同一概念在多处重复解释，没有统一信息源
3. **语言表达不佳** — 翻译腔重、术语不统一

## 约束

- 保留 VitePress 框架
- 保留现有 CI/CD 流程（GitHub Actions → gh-pages）
- 保留蓝色主题配色、本地搜索、截图等现有资产

## 信息架构

采用**渐进式三阶段 + 运维手册**结构，按用户熟练度旅程组织：

```
docs-site/
├── index.md                         (重写)
│
├── getting-started/                 ← 新手区，口语化
│   ├── quickstart.md                (重写)
│   ├── deploy.md                    (重写)
│   └── core-concepts.md             (新增)
│
├── guides/                          ← 操作区，任务驱动
│   ├── http-rules.md                (重写)
│   ├── l4-rules.md                  (重写)
│   ├── certificates.md              (重写)
│   ├── agents.md                    (重写)
│   ├── wireguard.md                 (重写)
│   ├── relay.md                     (从 reference/relay 拆出操作部分)
│   └── traffic-quota.md             (从 reference/traffic 拆出操作部分)
│
├── reference/                       ← 深入区，架构/原理/速查
│   ├── architecture.md              (重写)
│   ├── environment-variables.md     (重写)
│   ├── relay-internals.md           (从 reference/relay 拆出协议部分)
│   ├── traffic-accounting.md        (从 reference/traffic 拆出细节部分)
│   ├── development.md               (重写)
│   └── security.md                  (新增)
│
└── operations/                      ← 运维区
    ├── backup-restore.md            (重写)
    ├── migration.md                 (重写)
    └── troubleshooting.md           (新增，合并 FAQ)
```

### 页面变化清单

| 操作 | 页面 | 说明 |
|------|------|------|
| 新增 | `getting-started/core-concepts.md` | 一站式核心概念，用白话解释反代/域名/DNS/端口/证书等 |
| 新增 | `guides/relay.md` | 从 `reference/relay.md` 拆出的操作部分 |
| 新增 | `guides/traffic-quota.md` | 从 `reference/traffic.md` 拆出的操作部分 |
| 新增 | `reference/security.md` | 安全最佳实践 |
| 新增 | `operations/troubleshooting.md` | 排障指南，吸收 `faq.md` 内容 |
| 重写 | 其余 13 个现有页面（含 `environment.md` → `environment-variables.md`、`agent.md` → `agents.md` 重命名） | 按新规范重写内容 |
| 拆分 | `reference/relay.md` → `guides/relay.md` + `reference/relay-internals.md` | 操作与原理分离 |
| 拆分 | `reference/traffic.md` → `guides/traffic-quota.md` + `reference/traffic-accounting.md` | 操作与原理分离 |
| 删除 | `operations/faq.md` | 内容合并到 `troubleshooting.md` |

## 写作规范

### 口吻分层

| 区域 | 口吻 | 目标 |
|------|------|------|
| getting-started | 口语化，"你"、"我们"，每步解释为什么 | 消除新手畏惧感 |
| guides | 任务导向，简洁直白，前置条件明确 | 快速完成任务 |
| reference | 精确、客观、术语严格 | 准确查阅参数和原理 |
| operations | 务实，交代后果和风险 | 安全运维 |

### 去翻译腔规则

- 不用 "在…中" 开头 → 换成"执行时"、"系统会…"或直接省略
- 不嵌套三层以上定语 → 拆成多个短句
- 不用 "请注意"、"重要提示" 做段落开头 → 用 VitePress `::: warning` / `::: tip` 容器
- "进行 + 名词" → 直接用动词（"进行配置" → "配置"）

### 术语统一表

| 旧用词（淘汰） | 统一用词 |
|---|---|
| 控制面 / Master / 主控 | **控制面** |
| 执行面 / 数据面 / Agent / 节点 | **Agent 节点** / **Agent** |
| 面板 / Web 面板 / 管理后台 | **面板** |
| local / local agent / 内嵌 agent | **local Agent** |
| 反代 / 反向代理 / 代理 | 首次用**反向代理**，后续用**反代** |
| 后端 / 上游 / origin | **后端地址** |
| 前端 / 入口 / frontend | **入口域名** |
| Relay / 中继 / 隧道 | **Relay 隧道** |
| 节点 / Agent 节点 / 远程节点 | 按上下文用**远程 Agent** 或 **Agent 节点** |

### 页面结构模板

```markdown
# 页面标题（动词短语或名词短语）

一句话概述（50字以内）

## 前置条件（新手区必须有）
- 你已完成 XXX

## 正文 H2 小节
...

## 常见问题（可选）
### 问题？
...
```

## VitePress 配置调整

### nav（精简为 5 项）

```js
nav: [
  { text: '新手入门', link: '/getting-started/quickstart' },
  { text: '操作指南', link: '/guides/http-rules' },
  { text: '参考',     link: '/reference/architecture' },
  { text: '运维',     link: '/operations/backup-restore' },
  { text: 'GitHub',   link: 'https://github.com/sakullla/nginx-reverse-emby' }
]
```

### sidebar（四组，映射新目录）

```js
sidebar: [
  {
    text: '新手入门',
    items: [
      { text: '快速开始',         link: '/getting-started/quickstart' },
      { text: '部署指南',         link: '/getting-started/deploy' },
      { text: '核心概念',         link: '/getting-started/core-concepts' },
    ]
  },
  {
    text: '操作指南',
    items: [
      { text: 'HTTP 反向代理',    link: '/guides/http-rules' },
      { text: 'L4 端口转发',      link: '/guides/l4-rules' },
      { text: '证书与 HTTPS',     link: '/guides/certificates' },
      { text: 'Agent 节点管理',   link: '/guides/agents' },
      { text: 'WireGuard 隧道',   link: '/guides/wireguard' },
      { text: 'Relay 隧道',       link: '/guides/relay' },
      { text: '流量额度',         link: '/guides/traffic-quota' },
    ]
  },
  {
    text: '参考',
    items: [
      { text: '架构与设计',       link: '/reference/architecture' },
      { text: '环境变量速查',     link: '/reference/environment-variables' },
      { text: 'Relay 协议内幕',   link: '/reference/relay-internals' },
      { text: '流量统计原理',     link: '/reference/traffic-accounting' },
      { text: '安全最佳实践',     link: '/reference/security' },
      { text: '开发与构建',       link: '/reference/development' },
    ]
  },
  {
    text: '运维',
    items: [
      { text: '备份与恢复',       link: '/operations/backup-restore' },
      { text: '数据迁移',         link: '/operations/migration' },
      { text: '排障指南',         link: '/operations/troubleshooting' },
    ]
  }
]
```

### 保留不变的配置

- `base`、`lang: 'zh-CN'`、`cleanUrls`、`lastUpdated`
- 本地搜索、蓝色主题配色（custom.css）
- 页脚、社交链接、favicon

## 实施注意事项

- 截图文件保留在 `public/screenshots/` 不变，页面内引用路径不变
- CI 工作流（`.github/workflows/docs-pages.yml`）无需修改
- 旧 `guide/`、`reference/`、`operations/` 下的 md 文件在重写时移动到新目录
- 新增页面在重写阶段创建，旧页面保留原位直到新页面完成后才删除
