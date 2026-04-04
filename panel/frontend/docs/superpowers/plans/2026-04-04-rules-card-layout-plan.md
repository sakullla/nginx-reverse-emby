# Rules/L4 Card Layout & Margin Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert HTTP/L4 rules from table layout to card grid layout, fix margin/padding issues across all pages, and optimize modal form layouts.

**Architecture:** Following CertsPage card pattern with enhanced protocol badges (http/https for HTTP rules, TCP/UDP for L4 rules). CSS variables for consistent spacing. Modal forms unified with increased padding.

**Tech Stack:** Vue 3 Composition API, CSS Custom Properties, TanStack Vue Query

---

## File Overview

| File | Changes |
|------|---------|
| `src/pages/RulesPage.vue` | Table → card grid with http/https badges, modal spacing fixes |
| `src/pages/L4RulesPage.vue` | Table → card grid with TCP/UDP badges, modal spacing fixes |
| `src/pages/CertsPage.vue` | Modal spacing fixes (already has card layout) |
| `src/pages/DashboardPage.vue` | Margin increases |
| `src/pages/AgentsPage.vue` | Margin increases |

---

## Task 1: RulesPage.vue — Table to Card Grid

**Files:**
- Modify: `src/pages/RulesPage.vue`

### Step 1: Replace table with card grid in template

Replace lines 44-92 (the `<div v-else-if="selectedAgentId && rules.length" class="rules-list">` table block) with:

```html
<div v-else-if="selectedAgentId && rules.length" class="rule-grid">
  <div v-for="rule in rules" :key="rule.id" class="rule-card">
    <div class="rule-card__header">
      <div class="rule-card__icon">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </div>
      <div class="rule-card__badges">
        <span class="proto-badge" :class="rule.frontend_url?.startsWith('https') ? 'proto-badge--https' : 'proto-badge--http'">
          {{ rule.frontend_url?.startsWith('https') ? 'HTTPS' : 'HTTP' }}
        </span>
        <span class="rule-card__status" :class="`rule-card__status--${getStatus(rule)}`">
          {{ getStatusLabel(rule) }}
        </span>
      </div>
    </div>
    <div class="rule-card__url">{{ rule.frontend_url }}</div>
    <div class="rule-card__backend">→ {{ rule.backend_url }}</div>
    <div class="rule-card__tags">
      <span v-for="tag in (rule.tags || []).slice(0, 3)" :key="tag" class="tag">{{ tag }}</span>
    </div>
    <div class="rule-card__actions">
      <button class="toggle toggle--sm" :class="{ 'toggle--on': rule.enabled }" @click="toggleRule(rule)">
        <span class="toggle__knob"></span>
      </button>
      <button class="btn btn-secondary btn-sm" @click="startEdit(rule)">编辑</button>
      <button class="btn btn-danger btn-sm" @click="startDelete(rule)">删除</button>
    </div>
  </div>
</div>
```

### Step 2: Add getStatus and getStatusLabel functions

Add after `enabledCount` computed (after line 171):

```js
function getStatus(rule) {
  if (!rule.enabled) return 'disabled'
  if (rule.last_apply_status === 'failed') return 'failed'
  return 'active'
}

function getStatusLabel(rule) {
  if (!rule.enabled) return '已禁用'
  if (rule.last_apply_status === 'failed') return '同步失败'
  return '生效中'
}
```

### Step 3: Replace all CSS (lines 215-261)

Replace the entire `<style scoped>` block with:

```css
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.rules-page__prompt-hint { font-size: 0.875rem; color: var(--color-text-tertiary); }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
/* Rule card */
.rule-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__icon { color: var(--color-primary); }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__url { font-family: var(--font-mono); font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-card__backend { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rule-card__actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--http { background: var(--color-primary-subtle); color: var(--color-primary); }
.proto-badge--https { background: var(--color-success-50); color: var(--color-success); }
/* Tags */
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
/* Toggle */
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; flex-shrink: 0; }
.toggle--on { background: var(--color-primary); }
.toggle--sm { width: 36px; height: 20px; border-radius: 10px; }
.toggle--sm .toggle__knob { width: 14px; height: 14px; }
.toggle--sm.toggle--on .toggle__knob { transform: translateX(16px); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
/* Modals - standardized spacing */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-group--check { flex-direction: row; align-items: center; }
.form-group--check label { display: flex; align-items: center; gap: 0.5rem; cursor: pointer; font-weight: normal; }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.input-base:focus { border-color: var(--color-primary); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
```

### Step 4: Commit

```bash
git add src/pages/RulesPage.vue
git commit -m "feat(frontend): convert RulesPage table to card grid with protocol badges"
```

---

## Task 2: L4RulesPage.vue — Table to Card Grid

**Files:**
- Modify: `src/pages/L4RulesPage.vue`

### Step 1: Replace table with card grid in template

Replace lines 38-84 (the `<div v-else-if="rules.length" class="rules-list">` table block) with:

```html
<div v-else-if="rules.length" class="rule-grid">
  <div v-for="rule in rules" :key="rule.id" class="rule-card">
    <div class="rule-card__header">
      <div class="rule-card__icon">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
        </svg>
      </div>
      <div class="rule-card__badges">
        <span class="proto-badge" :class="rule.protocol === 'udp' ? 'proto-badge--udp' : 'proto-badge--tcp'">
          {{ rule.protocol?.toUpperCase() }}
        </span>
        <span class="rule-card__status" :class="`rule-card__status--${getStatus(rule)}`">
          {{ getStatusLabel(rule) }}
        </span>
      </div>
    </div>
    <div class="rule-card__url">:{{ rule.listen_port }}</div>
    <div class="rule-card__backend">→ {{ rule.upstream_host }}:{{ rule.upstream_port }}</div>
    <div class="rule-card__tags">
      <span v-for="tag in (rule.tags || []).slice(0, 3)" :key="tag" class="tag">{{ tag }}</span>
    </div>
    <div class="rule-card__actions">
      <button class="toggle toggle--sm" :class="{ 'toggle--on': rule.enabled }" @click="toggleRule(rule)">
        <span class="toggle__knob"></span>
      </button>
      <button class="btn btn-secondary btn-sm" @click="startEdit(rule)">编辑</button>
      <button class="btn btn-danger btn-sm" @click="startDelete(rule)">删除</button>
    </div>
  </div>
</div>
```

### Step 2: Add getStatus and getStatusLabel functions

Add after `enabledCount` computed (after line 176):

```js
function getStatus(rule) {
  if (!rule.enabled) return 'disabled'
  if (rule.last_apply_status === 'failed') return 'failed'
  return 'active'
}

function getStatusLabel(rule) {
  if (!rule.enabled) return '已禁用'
  if (rule.last_apply_status === 'failed') return '同步失败'
  return '生效中'
}
```

### Step 3: Replace all CSS (lines 227-269)

Replace the entire `<style scoped>` block with:

```css
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
/* Rule card */
.rule-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__icon { color: var(--color-warning); }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__url { font-family: var(--font-mono); font-size: 1.25rem; font-weight: 700; color: var(--color-text-primary); }
.rule-card__backend { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-secondary); }
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rule-card__actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--tcp { background: var(--color-warning-50); color: var(--color-warning); }
.proto-badge--udp { background: #f3e8ff; color: #7c3aed; }
/* Tags */
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
/* Toggle */
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; flex-shrink: 0; }
.toggle--on { background: var(--color-primary); }
.toggle--sm { width: 36px; height: 20px; border-radius: 10px; }
.toggle--sm .toggle__knob { width: 14px; height: 14px; }
.toggle--sm.toggle--on .toggle__knob { transform: translateX(16px); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
/* Modals - standardized spacing */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-row { display: flex; gap: 0.75rem; }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.input-base:focus { border-color: var(--color-primary); }
select.input-base { appearance: auto; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
```

### Step 4: Commit

```bash
git add src/pages/L4RulesPage.vue
git commit -m "feat(frontend): convert L4RulesPage table to card grid with TCP/UDP badges"
```

---

## Task 3: CertsPage.vue — Modal Spacing Fixes

**Files:**
- Modify: `src/pages/CertsPage.vue`

### Step 1: Update modal CSS in CertsPage.vue

Find and replace the modal CSS section (`.modal-overlay`, `.modal`, `.modal__header`, `.modal__body`, `.modal__footer`, `.form-group`, `.input-base`, `.btn`) with the standardized spacing:

```css
/* Modals - standardized spacing */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.input-base:focus { border-color: var(--color-primary); }
select.input-base { appearance: auto; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
```

### Step 2: Commit

```bash
git add src/pages/CertsPage.vue
git commit -m "fix(frontend): standardize modal spacing in CertsPage"
```

---

## Task 4: DashboardPage.vue — Margin Increases

**Files:**
- Modify: `src/pages/DashboardPage.vue`

### Step 1: Update margin CSS

Find and replace in the `<style scoped>` block:

- `.dashboard__header` margin-bottom: `2rem` → `2.5rem`
- `.stats-grid` margin-bottom: `2rem` → `2.5rem`
- `.dashboard-section` margin-bottom: `2rem` → `2.5rem`

The CSS should look like:

```css
.dashboard__header { margin-bottom: 2.5rem; }
.stats-grid { margin-bottom: 2.5rem; }
.dashboard-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); overflow: hidden; margin-bottom: 2.5rem; }
```

### Step 2: Commit

```bash
git add src/pages/DashboardPage.vue
git commit -m "fix(frontend): increase section margins in DashboardPage"
```

---

## Task 5: AgentsPage.vue — Margin Increases

**Files:**
- Modify: `src/pages/AgentsPage.vue`

### Step 1: Update margin CSS

- `.agents-page__header` margin-bottom: `1.5rem` → `2rem`
- `.agents-list` gap: `0.5rem` → `0.75rem`

The CSS should look like:

```css
.agents-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.agents-list { display: flex; flex-direction: column; gap: 0.75rem; }
```

### Step 2: Commit

```bash
git add src/pages/AgentsPage.vue
git commit -m "fix(frontend): increase margins in AgentsPage"
```

---

## Verification

After all tasks complete, verify in browser:
1. RulesPage shows card grid with http/https badges instead of table
2. L4RulesPage shows card grid with TCP/UDP badges instead of table
3. All modal forms (Rules, L4, Certs) have consistent spacing
4. Dashboard sections have increased margins
5. AgentsPage has increased margins
6. No console errors
