# Rewrite Docs-Site Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite all VitePress documentation pages with improved information architecture, unified terminology, and natural Chinese prose.

**Architecture:** Three-tier progressive structure — `getting-started/` (新手区, conversational), `guides/` (操作区, task-driven), `reference/` (深入区, precise), `operations/` (运维区, pragmatic).

**Tech Stack:** VitePress 1.x, Markdown. No framework changes.

**Spec:** `docs/superpowers/specs/2026-06-14-rewrite-docs-site-design.md`

---

### Task 1: Create New Directory Structure

**Files:**
- Create: `docs-site/getting-started/` (dir)
- Create: `docs-site/guides/` (dir)

- [ ] **Step 1: Create directories and move files**

```bash
mkdir -p docs-site/getting-started docs-site/guides

# Move getting-started pages
git mv docs-site/guide/quickstart.md docs-site/getting-started/quickstart.md
git mv docs-site/guide/deploy.md docs-site/getting-started/deploy.md

# Move and rename guides pages
git mv docs-site/guide/http-rule.md docs-site/guides/http-rules.md
git mv docs-site/guide/l4-relay.md docs-site/guides/l4-rules.md
git mv docs-site/guide/certificates.md docs-site/guides/certificates.md
git mv docs-site/guide/agent.md docs-site/guides/agents.md
git mv docs-site/guide/wireguard.md docs-site/guides/wireguard.md

# Rename reference pages
git mv docs-site/reference/environment.md docs-site/reference/environment-variables.md

# Clean up old empty directory
rmdir docs-site/guide
```

- [ ] **Step 2: Commit**

```bash
git add -A && git commit -m "refactor(docs): restructure directories for three-tier layout"
```

---

### Task 2: Write core-concepts.md (New)

**Files:**
- Create: `docs-site/getting-started/core-concepts.md`

- [ ] **Step 1: Write the page**

Content covers (in plain Chinese, conversational tone):
1. 什么是反向代理 — simple diagram: 访客→域名→VPS→后端
2. 域名和 DNS — "域名要解析到 VPS，VPS 要放行端口"
3. 控制面和 Agent 节点 — table explaining roles; local Agent concept
4. 规则、端口和证书 — HTTP rules vs L4 rules; ports as "门牌号"; certificates for HTTPS
5. Relay 隧道 — encrypted channel between agents, use cases
6. WireGuard — VPN protocol, three usage modes
7. 下一步 — links to quickstart, deploy

Cross-reference: link to every guides page under "了解更多".

- [ ] **Step 2: Commit**

```bash
git add docs-site/getting-started/core-concepts.md
git commit -m "docs: add core concepts page"
```

---

### Task 3: Write troubleshooting.md (New, replaces FAQ)

**Files:**
- Create: `docs-site/operations/troubleshooting.md`

- [ ] **Step 1: Write the page**

Content covers these Q&A sections (merge from faq.md + enhance):
1. 访问入口域名没反应 — DNS → 防火墙 → 规则启用 → Agent 选择
2. 规则保存了但访问不到后端 — curl test, protocol/port check, backend restrictions
3. HTTPS 访问失败 — verify HTTP first, cert status, port 443
4. local Agent 是什么？什么时候需要远程 Agent？
5. HTTP 规则和 L4 规则怎么选？— comparison table
6. 何时关闭 302/307 重定向改写？
7. 证书要手动申请吗？
8. 流量额度超了怎么办？
9. 数据库可以换吗？— link to migration.md
10. 为什么默认用 host 网络？
11. 面板数据存在哪里？
12. 远程 Agent 需要开放入站端口吗？

- [ ] **Step 2: Commit**

```bash
git add docs-site/operations/troubleshooting.md
git commit -m "docs: add troubleshooting guide (replaces FAQ)"
```

---

### Task 4: Write security.md (New)

**Files:**
- Create: `docs-site/reference/security.md`

- [ ] **Step 1: Write the page**

Content covers: 密码和令牌 / data 目录保护 / 面板访问控制 / 防火墙 / 证书安全 / Agent 注册安全 / 数据库安全 / 升级。Each section 2-3 sentences, pragmatic tone. Cross-link to relevant guide pages.

- [ ] **Step 2: Commit**

```bash
git add docs-site/reference/security.md && git commit -m "docs: add security best practices"
```

---

### Task 5: Rewrite getting-started/quickstart.md

**Files:**
- Modify: `docs-site/getting-started/quickstart.md`

- [ ] **Step 1: Rewrite**

Key changes from existing guide/quickstart.md:
1. Tighter opening — remove "它能做什么" section (covered in core-concepts)
2. Merge "你需要准备什么" into a concise checklist
3. Keep the 3-step flow: 下载启动 → 添加规则 → 验证
4. Update all internal links to new paths (`../guides/certificates.md` etc.)
5. Apply glossary: "入口域名" not "前端访问地址", "控制面" not "面板"
6. Remove "下一步" redundant links — one clear pointer per topic

- [ ] **Step 2: Commit**

```bash
git add docs-site/getting-started/quickstart.md
git commit -m "docs: rewrite quickstart with improved prose and terminology"
```

---

### Task 6: Rewrite getting-started/deploy.md

**Files:**
- Modify: `docs-site/getting-started/deploy.md`

- [ ] **Step 1: Rewrite**

Key changes:
1. Remove duplicate content shared with quickstart (docker-compose.yaml contents)
2. Focus on: dir structure, env var reference, startup commands, host network explanation, database switching
3. "为什么使用 Host 网络模式？" → shorter, one paragraph
4. Update all internal links
5. Apply glossary consistently

- [ ] **Step 2: Commit**

```bash
git add docs-site/getting-started/deploy.md
git commit -m "docs: rewrite deploy guide"
```

---

### Task 7: Rewrite guides/http-rules.md

**Files:**
- Modify: `docs-site/guides/http-rules.md`

- [ ] **Step 1: Rewrite**

Key changes from existing guide/http-rule.md:
1. Remove "登录面板" section (covered in quickstart/deploy)
2. Remove duplicate troubleshooting section — link to troubleshooting.md
3. Streamline: 概念 → 创建规则 → 高级选项 → 验证 → HTTPS → 流式恢复
4. Remove "排查清单" — covered by troubleshooting.md
5. Apply glossary: "入口域名", "后端地址", "Relay 隧道"

- [ ] **Step 2: Commit**

```bash
git add docs-site/guides/http-rules.md
git commit -m "docs: rewrite HTTP rules guide"
```

---

### Task 8: Rewrite guides/l4-rules.md

**Files:**
- Modify: `docs-site/guides/l4-rules.md`

- [ ] **Step 1: Rewrite**

Key changes from existing guide/l4-relay.md:
1. Split Relay content — keep L4 configuration, move Relay creation to guides/relay.md
2. Focus on: L4 vs HTTP comparison → prerequisites → basic config → advanced options → verify
3. Relay section becomes a concise pointer to guides/relay.md
4. Apply glossary: "L4 规则", "Agent 节点", "出口 Profile"

- [ ] **Step 2: Commit**

```bash
git add docs-site/guides/l4-rules.md
git commit -m "docs: rewrite L4 rules guide"
```

---

### Task 9: Rewrite guides/certificates.md

**Files:**
- Modify: `docs-site/guides/certificates.md`

- [ ] **Step 1: Rewrite**

Key changes:
1. Tighter opening — 3 certificate sources in a table
2. Merge certificate template and form field tables
3. DNS-01 section: streamline token获取 steps, add security warning with link to security.md
4. Relay certificate section: shorten to one paragraph + link to relay-internals.md
5. Apply glossary: "控制面", "Agent 节点", "Relay 隧道"

- [ ] **Step 2: Commit**

```bash
git add docs-site/guides/certificates.md
git commit -m "docs: rewrite certificates guide"
```

---

### Task 10: Rewrite guides/agents.md

**Files:**
- Modify: `docs-site/guides/agents.md`

- [ ] **Step 1: Rewrite**

Key changes from existing guide/agent.md:
1. Remove "本地节点（local）" section — covered in core-concepts.md
2. Merge redundant script parameter documentation
3. Remove "Agent 配置变量" table — link to environment-variables.md
4. Tighten Windows node section
5. Apply glossary: "Agent 节点", "控制面", "local Agent"

- [ ] **Step 2: Commit**

```bash
git add docs-site/guides/agents.md
git commit -m "docs: rewrite agents guide"
```

---

### Task 11: Rewrite guides/wireguard.md

**Files:**
- Modify: `docs-site/guides/wireguard.md`

- [ ] **Step 1: Rewrite**

Key changes:
1. Better opening: 3 usage modes upfront
2. Merge "两个地址的区别" into field table
3. Fold "自动地址池" into Profile creation section
4. "客户端管理" → one paragraph under Profile
5. Remove or shorten "配合 Cloudflare WARP" section
6. Apply glossary

- [ ] **Step 2: Commit**

```bash
git add docs-site/guides/wireguard.md
git commit -m "docs: rewrite wireguard guide"
```

---

### Task 12: Write guides/relay.md and guides/traffic-quota.md (Split from reference)

**Files:**
- Create: `docs-site/guides/relay.md`
- Create: `docs-site/guides/traffic-quota.md`

- [ ] **Step 1: Write guides/relay.md**

Operation-focused page. Content outline:
1. What is Relay — encrypted channel between agents, use cases
2. 前置条件 — at least 2 agents, firewall ports open
3. 创建 Relay 监听器 — field table, screenshots
4. 在规则中使用 Relay — adding relay layer in HTTP/L4 rules
5. 验证 — card shows `Relay` badge, test connectivity
6. Relay vs 直接转发 — comparison table
7. 深入了解 — link to reference/relay-internals.md

- [ ] **Step 2: Write guides/traffic-quota.md**

Operation-focused page. Content outline:
1. 查看流量 — dashboard overview, screenshot
2. 配置流量策略 — field table, blocking behavior
3. 手动校准 — when and how
4. 关闭流量统计 — env var
5. 深入了解 — link to reference/traffic-accounting.md

- [ ] **Step 3: Commit**

```bash
git add docs-site/guides/relay.md docs-site/guides/traffic-quota.md
git commit -m "docs: add relay and traffic-quota guides from split reference"
```

---

### Task 13: Create reference/relay-internals.md and reference/traffic-accounting.md (Split from reference)

**Files:**
- Create: `docs-site/reference/relay-internals.md`
- Create: `docs-site/reference/traffic-accounting.md`

- [ ] **Step 1: Write relay-internals.md**

Protocol-level content extracted from existing reference/relay.md:
1. 传输方式 — tls_tcp, quic, wireguard with descriptions
2. 监听器字段 reference table
3. TLS 与信任策略 — Pin+CA, Pin only, CA only, Pin or CA modes
4. Relay 层 — relay_layers explanation with diagram, relay_chain backward compat note
5. 超时变量 table
6. Link back to guides/relay.md for how-to

- [ ] **Step 2: Write traffic-accounting.md**

Internals extracted from existing reference/traffic.md:
1. 采集流程 — netlink → boot_id → counter reset handling → persistence
2. 接口筛选 — NRE_TRAFFIC_INTERFACES, host network note
3. 计费周期 — direction, start day, quota, blocking
4. 数据保留 — hourly/daily/monthly retention table
5. 手动校准 — baseline offset, clear current period
6. Link back to guides/traffic-quota.md for how-to

- [ ] **Step 3: Commit**

```bash
git add docs-site/reference/relay-internals.md docs-site/reference/traffic-accounting.md
git commit -m "docs: split relay and traffic reference into internals pages"
```

---

### Task 14: Rewrite remaining reference pages

**Files:**
- Modify: `docs-site/reference/architecture.md`
- Modify: `docs-site/reference/environment-variables.md`
- Modify: `docs-site/reference/development.md`

- [ ] **Step 1: Rewrite architecture.md**

Key changes:
1. Remove feature table (duplicates home page)
2. Tighten all prose — remove translation-ese
3. Apply glossary consistently: "控制面", "Agent", "Relay", "面板"
4. Keep: component diagram, panel layout, request flow, agent sync flow, data storage table, host network explanation, legacy deploy.sh reference

- [ ] **Step 2: Rewrite environment-variables.md**

Key changes from existing reference/environment.md:
1. Add "快速索引" at top — common tasks mapped to vars
2. Consistent column naming across all tables: 变量 | 默认值 | 说明
3. Preserve ALL variable entries from original (this is a reference page)
4. Reorganize sections: 控制面 → 数据库 → 证书/ACME → HTTP传输 → Relay → WireGuard/流量 → Agent
5. Apply glossary in descriptions (e.g., "控制面" not "主控")

- [ ] **Step 3: Rewrite development.md**

Key changes:
1. Tighter opening: "面向想修改源码的开发者。只部署不需要这个。"
2. Keep all commands and tables intact
3. Improve section transitions
4. Apply glossary

- [ ] **Step 4: Commit**

```bash
git add docs-site/reference/architecture.md \
        docs-site/reference/environment-variables.md \
        docs-site/reference/development.md
git commit -m "docs: rewrite architecture, environment-vars, and development pages"
```

---

### Task 15: Rewrite operations pages

**Files:**
- Modify: `docs-site/operations/backup-restore.md`
- Modify: `docs-site/operations/migration.md`

- [ ] **Step 1: Rewrite backup-restore.md**

Key changes:
1. Better opening — comparison table kept, prose tightened
2. "控制面" not "面板"
3. "Agent" not "节点/Agent" mixed
4. Keep all procedure steps, commands, and screenshots

- [ ] **Step 2: Rewrite migration.md**

Key changes:
1. Tighten opening — 2 migration types
2. Keep all command examples and parameter tables
3. Apply glossary consistently
4. Better section flow: 存储迁移 → 旧Agent迁移

- [ ] **Step 3: Commit**

```bash
git add docs-site/operations/backup-restore.md docs-site/operations/migration.md
git commit -m "docs: rewrite backup-restore and migration pages"
```

---

### Task 16: Rewrite index.md (Home Page)

**Files:**
- Modify: `docs-site/index.md`

- [ ] **Step 1: Rewrite home page**

Key changes:
1. Polish hero tagline — punchier
2. Update all feature links to new URLs
3. Unify link text with new page titles
4. Feature link updates:
   - 纯 Go 运行时 → `/getting-started/deploy`
   - HTTP/HTTPS 反代 → `/guides/http-rules`
   - L4 端口转发 → `/guides/l4-rules`
   - Relay 隧道 → `/guides/relay`
   - 流量统计与额度 → `/guides/traffic-quota`
   - 证书管理 → `/guides/certificates`

- [ ] **Step 2: Commit**

```bash
git add docs-site/index.md
git commit -m "docs: update home page links and polish hero text"
```

---

### Task 17: Update VitePress Config and Cleanup

**Files:**
- Modify: `docs-site/.vitepress/config.mjs`
- Delete: `docs-site/operations/faq.md`
- Delete: `docs-site/reference/relay.md`
- Delete: `docs-site/reference/traffic.md`

- [ ] **Step 1: Update config.mjs nav**

Replace existing nav array with:
```js
nav: [
  { text: '新手入门', link: '/getting-started/quickstart' },
  { text: '操作指南', link: '/guides/http-rules' },
  { text: '参考',     link: '/reference/architecture' },
  { text: '运维',     link: '/operations/backup-restore' },
  { text: 'GitHub',   link: 'https://github.com/sakullla/nginx-reverse-emby' }
],
```

- [ ] **Step 2: Update config.mjs sidebar**

Replace existing sidebar array with:
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
],
```

- [ ] **Step 3: Delete obsolete files**

```bash
git rm docs-site/operations/faq.md
git rm docs-site/reference/relay.md
git rm docs-site/reference/traffic.md
```

- [ ] **Step 4: Build and verify**

```bash
cd docs-site && npm ci && npm run build
```

Expected: build succeeds with no dead links. Fix any warnings before committing.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: update vitepress config and remove obsolete pages"
```
```
