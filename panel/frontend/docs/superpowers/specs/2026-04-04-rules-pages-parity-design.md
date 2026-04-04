# Rules Pages Feature Parity Design

## Goal

Bring HTTP rules, L4 rules, and certificate management pages on `refactor/frontend-minimax` to feature parity with the `develop` branch, while preserving the current branch's Vue Query architecture and visual style.

## Scope

Three pages receive unified improvements; L4 rules page gets the most work.

## Shared Features (All Three Pages)

### Search Bar

Each page gets a search input below the header. Filtering is local (computed from fetched data), no backend changes.

- **HTTP rules**: searches `frontend_url`, `backend_url`, `name`, `tags`, `id`
- **L4 rules**: searches `protocol`, `listen_host`, `upstream_host`, `listen_port`, `tags`, `id`
- **Certificates**: searches `domain`, `tags`, `id`
- All support `#id=123` exact ID match prefix

### Tag Filter

Below the search bar, a row of tag chips extracted from the current dataset. Clicking a tag toggles it as a filter (OR logic: show items matching ANY selected tag). Clicking again deselects.

Tags are sorted alphabetically, system tags (TCP, UDP, HTTP, HTTPS, :port, RR, LC, etc.) are visually de-emphasized.

### Copy Feature

HTTP rules and L4 rules get a copy button on each card. Clicking opens the edit modal pre-filled with the rule's data (minus `id`), so the form treats it as a new rule creation.

### ID Display

Cards show `#id` for quick identification.

### Search No-Results Empty State

When search/filter yields zero results, show a dedicated empty state with the search icon and "没有匹配的规则/证书" message.

## Page-Specific Changes

### 1. L4 Rules Page (`L4RulesPage.vue`)

The biggest change. The page currently has an inline basic form and simple cards.

#### Replace Inline Form with L4RuleForm Component

The existing `L4RuleForm.vue` is already complete (multi-backend, load balancing, advanced tuning, tag chips). The page stops using its own inline form and instead renders `<L4RuleForm>` inside modals for add/edit/copy.

The page passes `agentId` (as computed ref) and `initialData` to `L4RuleForm`, and listens for the `@success` event to close modals.

#### L4RuleForm Tab Layout

The form currently has a single scrollable layout with a collapsible advanced section. Refactor into three tabs:

**Tab 1: 基础配置 (Basic)**
- Protocol (TCP/UDP)
- Listen address + port
- Load balancing strategy + hash key (when hash)
- Backend servers list (add/remove/drag-sort/weight)
- Tags
- Enabled toggle

**Tab 2: 高级调优 (Advanced Tuning)**
- Timeouts: `proxy_connect_timeout`, `proxy_idle_timeout`
- Health check: `max_fails`, `fail_timeout`
- Connection limit: `limit_conn` (count, key, zone_size)
- Buffer: `proxy_buffer_size`, `upstream max_conns`
- Backend extensions: backup flag and `max_conns` per backend

**Tab 3: 协议与监听 (Protocol & Listen)**
- PROXY Protocol: decode/send toggles (TCP only)
- Listen options: `reuseport`, `tcp_nodelay`, `so_keepalive`, `backlog`
- UDP-specific: `proxy_requests`, `proxy_responses` (UDP only)

When creating a new rule, default to Tab 1. When editing a rule, always start on Tab 1 — the "已配置" badges on Tab 2/3 tabs signal that advanced config exists, and the user navigates there if needed.

The "已配置" badge appears on Tab 2 and/or Tab 3 when non-default values are detected.

#### New L4RuleItem Component

Create `panel/frontend/src/components/l4/L4RuleItem.vue` — a card component for displaying a single L4 rule.

**Card content:**
- Header: `#id` badge, status badge (启用/已禁用), action buttons (toggle, copy, edit, delete)
- Protocol badge (TCP/UDP with color)
- Listen address: `listen_host:listen_port`
- Arrow indicator
- Upstream target:
  - Single backend: `host:port`
  - Multiple backends: `primary +N` with tooltip listing all backends (host, port, weight, backup status)
- Load balancing badge: `RR` / `LC` / `RND` / `HASH` with hover title
- Tuning summary tags (compact, only shown for non-default values): `超时:30s`, `限连:100`, `健检:3/30s`, `PP接收`, `PP发送`, `reuseport`, etc.
- User tags row

### 2. HTTP Rules Page (`RulesPage.vue`)

- Add search bar and tag filter
- Add copy button to cards
- Show `#id` on cards
- Convert inline tag input in the add/edit form to chip-based input (reference L4RuleForm's tag implementation)
- Add search no-results empty state

### 3. Certificates Page (`CertsPage.vue`)

- Add search bar and tag filter
- Show `#id` on cards
- Show `last_error` on cards when present
- Show `last_issue_at` date on cards
- Convert inline tag input to chip-based input
- Add search no-results empty state

## Component Architecture

```
pages/
├── RulesPage.vue       (HTTP rules — add search, tag filter, copy, ID)
├── L4RulesPage.vue     (L4 rules — rewrite to use L4RuleForm + L4RuleItem)
└── CertsPage.vue       (Certificates — add search, tag filter, ID, error/date)

components/
├── L4RuleForm.vue      (refactor into tabs)
└── l4/
    └── L4RuleItem.vue  (new — rich L4 rule card)

hooks/
├── useL4Rules.js       (unchanged)
└── useGlobalSearch.js  (unchanged)
```

No new base components (BaseModal, EmptyState, etc.) are introduced. Pages continue using `<Teleport>` modals with inline styles, consistent with the current branch's pattern.

## Technical Notes

- All search/filter logic is implemented as local `computed` properties in each page component, reading from Vue Query's `data` ref
- Tag extraction uses `computed` to collect unique tags from the current rule/cert list
- Copy opens the same form modal as edit, but with `id` stripped from `initialData`
- L4RuleForm's `buildPayload()` function remains unchanged — only the template/script layout is reorganized into tabs
- The "已配置" badge detection logic (`hasTuningChanges`) is split per-tab to show badges on the relevant tabs
