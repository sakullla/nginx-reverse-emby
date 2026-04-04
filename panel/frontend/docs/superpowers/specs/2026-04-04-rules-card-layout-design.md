# Rules/L4 Cards & Margin Fix Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert HTTP/L4 rules from table layout to card grid layout, fix margin/padding issues across all pages, and optimize modal form layouts.

**Architecture:** Following CertsPage card pattern with enhanced protocol badges. CSS variables for consistent spacing. Modal forms unified across all three add/edit flows.

**Tech Stack:** Vue 3 Composition API, CSS Custom Properties, TanStack Vue Query

---

## 1. HTTP Rules Card Layout (RulesPage.vue)

### Card Structure
```
┌─────────────────────────────────────┐
│ [🔗] [http badge] [enabled badge]   │  ← header row
│ em.example.com/emby                 │  ← frontend URL (mono, bold)
│ → 192.168.1.10:8096                 │  ← backend URL (mono)
│ [HTTP] [media] [backup]              │  ← protocol tag + user tags
│ [签发] [编辑] [删除]                 │  ← action buttons
└─────────────────────────────────────┘
```

### Fields per Card
- **Icon**: Link icon (SVG)
- **Protocol badge**: `http` or `https` (colored tag)
- **Enabled badge**: `生效中` (green) / `已禁用` (gray) / `同步中` (yellow) / `失败` (red)
- **Frontend URL**: monospace, bold, primary text
- **Backend URL**: monospace, with `→` prefix
- **Tags**: protocol type tag + user-defined tags (max 3 shown)
- **Actions**: 签发 (if pending), 编辑, 删除

### Protocol Badge Colors
- `http`: `color-primary` blue background
- `https`: `color-success` green background
- `TCP`: `color-warning` orange background
- `UDP`: purple background

### Grid Layout
- `grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))`
- Gap: `1rem`
- Card padding: `1.25rem`

---

## 2. L4 Rules Card Layout (L4RulesPage.vue)

Same structure as HTTP cards with TCP/UDP protocol badges:

```
┌─────────────────────────────────────┐
│ [🔗] [TCP badge] [enabled badge]    │
│ :8443                               │  ← listen port
│ → 192.168.1.10:3306                 │  ← backend target
│ [TCP] [mysql]                       │  ← protocol + tags
│ [编辑] [删除]                       │
└─────────────────────────────────────┘
```

### L4-specific Fields
- **Listen Port**: displayed as `:8443` (monospace)
- **Backend**: `→ IP:PORT` format
- **Protocol badge**: `TCP` or `UDP`

---

## 3. CertsPage Card Layout (CertsPage.vue)

No structural changes, only margin/padding fixes:
- Card padding: `1.25rem` (already correct)
- Gap between cards: `1rem` (already correct)
- Review and normalize tag display spacing

---

## 4. Modal/Form Layout Optimization

### Current Issues (observed from CertsPage.vue)
- Modal body padding: `1.25rem`
- Form group gap: `1rem`
- Form group label margin-bottom: `0.375rem`
- Input padding: `0.5rem 0.75rem`

### Standard Modal Spacing
```
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; }
.input-base { padding: 0.625rem 0.875rem; } /* slightly larger */
.modal__footer { padding: 1rem 1.5rem; }
```

### Modal Width Standardization
- Standard modal: `min(480px, 90vw)`
- Large modal (join agent): `min(640px, 92vw)` (keep existing)

---

## 5. Page Margin Fixes

### DashboardPage.vue
- `dashboard__header` margin-bottom: `2rem` → `2.5rem`
- `stats-grid` margin-bottom: `2rem` → `2.5rem`
- `dashboard-section` margin-bottom: `2rem` → `2.5rem`

### AgentsPage.vue
- `agents-page__header` margin-bottom: `1.5rem` → `2rem`
- `agents-list` gap: `0.5rem` → `0.75rem`

### RulesPage.vue / L4RulesPage.vue
- Page header margin-bottom: `1.5rem` → `2rem`
- Card grid gap: `1rem`

### CertsPage.vue
- Page header margin-bottom: `1.5rem` → `2rem`
- Card grid gap: `1rem`

---

## 6. Component Changes

### New/Modified Files
- `src/pages/RulesPage.vue` — table → card grid, modal form
- `src/pages/L4RulesPage.vue` — table → card grid, modal form
- `src/pages/CertsPage.vue` — margin fixes only (already has card layout)

### Shared Style Pattern (to follow CertsPage)
```css
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
.rule-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__url { font-family: var(--font-mono); font-weight: 600; }
.rule-card__backend { font-family: var(--font-mono); color: var(--color-text-secondary); }
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rule-card__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
```

---

## 7. Summary of Changes by File

| File | Changes |
|------|---------|
| RulesPage.vue | Table → card grid, protocol badges, modal form spacing |
| L4RulesPage.vue | Table → card grid, TCP/UDP badges, modal form spacing |
| CertsPage.vue | Margin/padding review only |
| DashboardPage.vue | Margin increases |
| AgentsPage.vue | Margin increases |
