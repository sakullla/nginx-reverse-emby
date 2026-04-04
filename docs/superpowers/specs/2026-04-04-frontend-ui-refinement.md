# Frontend UI Refinement Design

Date: 2026-04-04
Status: Draft

## Overview

Six frontend UI improvements for the nginx-reverse-emby management panel:
1. Fix sidebar "首页" active state always highlighted
2. Add collapse button to desktop sidebar
3. Refactor TopBar layout — search icon on right side before theme toggle
4. Refactor mobile BottomNav — 4 tabs (首页/HTTP规则/证书/更多) with "更多" containing L4规则、节点管理、设置
5. Fix GlobalSearch result click — navigate to RulesPage and auto-open edit modal

---

## 1. Sidebar "首页" Always-Active Fix

### Problem
`RouterLink to="/"` uses `active-class` for highlighting. Since `/` is a prefix match, it remains active on all child routes (`/rules`, `/l4`, etc.).

### Solution
Add `exact` prop to the 首页 RouterLink so it only matches when `path === '/'` exactly:

```vue
<RouterLink to="/" class="sidebar__nav-item" active-class="sidebar__nav-item--active" exact>
```

### Files
- `panel/frontend/src/components/layout/Sidebar.vue` — line 5

---

## 2. Sidebar Collapse Button

### Problem
Sidebar has `collapsed` state but no button to trigger it.

### Solution
Add a toggle button at the top of the sidebar (above the nav items, aligned to the right). Shows a chevron icon that rotates based on collapsed state. Toggling saves to `localStorage('sidebar_collapsed')`.

### UI
```
┌─────────────────────┐
│  [logo]  NginxProxy  │  ← brand stays visible
│              [▸/◂]  │  ← collapse toggle button (new)
│─────────────────────│
│  🏠 首页            │
│  🔗 HTTP 规则       │
│  📡 L4 规则         │
│  🔒 证书            │
│  🖥️ 节点管理         │
│  ⚙️ 设置            │
└─────────────────────┘
```

When collapsed (width 64px), only icons show. Brand + toggle button remain visible at top.

### Files
- `panel/frontend/src/components/layout/Sidebar.vue`

---

## 3. TopBar Layout Refactor

### Current Layout
```
[品牌] [居中搜索按钮................] [主题切换] [Agent切换] [登出]
```

### New Layout (desktop)
```
[品牌]                                         [🔍搜索] [主题] [Agent] [登出]
```

### Changes
1. **Remove** center search bar (`topbar__center` flex section)
2. **Add** search icon button in `topbar__actions` area (triggers existing GlobalSearch modal)
3. **Reorder** `topbar__actions` to: `[搜索图标] [主题切换] [Agent切换] [登出]`
4. **L4规则/节点管理/设置** accessible via sidebar on desktop, BottomNav "更多" on mobile

### Mobile (< 768px)
Hide search icon. Show only: `[主题] [登出]`

### Files
- `panel/frontend/src/components/layout/TopBar.vue`
- `panel/frontend/src/components/GlobalSearch.vue` (already exists, triggered by `open-search` event)

---

## 4. Mobile BottomNav Refactor

### Current BottomNav
首页 / 规则(含HTTP+L4) / 证书 / 设置

### New BottomNav
**首页** / **HTTP 规则** / **证书** / **更多 ▾**

"更多" dropdown contains:
- L4 规则
- 节点管理
- 设置

### Implementation
Replace the 4-tab flat navigation with a dropdown-style "更多" tab. The dropdown is a native `<select>` or custom dropdown showing L4 / 节点管理 / 设置 options that navigate on selection.

### Files
- `panel/frontend/src/components/layout/BottomNav.vue`

---

## 5. Global Search Result Click Navigation Fix

### Problem
`GlobalSearch.navigateToRule()` only emits `select` event — no route navigation happens. Users cannot open a rule from search results.

### Solution
1. **GlobalSearch.vue** — change `navigateToRule(agentId, rule)`:
   ```javascript
   function navigateToRule(agentId, rule) {
     close()
     // Switch agent context, then navigate to rules page with ruleId
     setSelectedAgentId(agentId)
     router.push({ path: '/rules', query: { agentId, ruleId: rule.id } })
   }
   ```
2. **RulesPage.vue** — add `watch` on `route.query.ruleId`:
   - On mount or route change, if `ruleId` is present, find that rule and set `editingRule = rule`
   - This auto-opens the edit modal for the clicked rule

### Files
- `panel/frontend/src/components/GlobalSearch.vue`
- `panel/frontend/src/pages/RulesPage.vue`

---

## 6. Chrome Debug & Detail Adjustment

After implementing all above, use Chrome DevTools to:
- Verify sidebar collapse animation smoothness
- Check TopBar layout at 768px, 1024px, 1440px breakpoints
- Verify BottomNav dropdown opens/closes correctly on mobile
- Check GlobalSearch modal styling consistency
- Review all hover/active states across components

---

## Summary of File Changes

| File | Changes |
|------|---------|
| `Sidebar.vue` | Fix `exact` prop on 首页 RouterLink; add collapse toggle button |
| `TopBar.vue` | Remove center search; add search icon to actions; reorder |
| `BottomNav.vue` | Replace 4 tabs with 首页/HTTP规则/证书/更多 (dropdown) |
| `GlobalSearch.vue` | Fix `navigateToRule` — navigate to `/rules?agentId=&ruleId=` instead of emitting `select` |
| `RulesPage.vue` | Watch `route.query.ruleId` — auto-open edit modal for that rule |
