# Frontend Redesign Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete frontend redesign — Vue 3 + UnoCSS + TanStack Query + Vue Router — with 3-theme system, new layout, mobile-first bottom tab nav, and all pages migrated.

**Architecture:** Flat-file Vue Router, TanStack Query for all server state, Context for UI state (Agent, Theme), UnoCSS for all styling. App.vue becomes a thin shell; all UI in page components and layout components.

**Tech Stack:** Vue 3, Vite 5, UnoCSS, Vue Router 4, TanStack Query (Vue Query), Radix Vue

---

## File Map

### New files to create

```
panel/frontend/src/
├── uno.config.js                  # UnoCSS configuration
├── router/
│   └── index.js                   # Vue Router routes
├── context/
│   ├── AgentContext.js            # Current selected agent ID
│   └── ThemeContext.js            # Current theme (reads from localStorage)
├── hooks/
│   ├── useAgents.js               # useQuery/useMutation for agents
│   ├── useRules.js                # useQuery/useMutation for HTTP rules
│   ├── useL4Rules.js              # useQuery/useMutation for L4 rules
│   ├── useCertificates.js         # useQuery/useMutation for certificates
│   └── useGlobalSearch.js         # Cross-agent search
├── pages/
│   ├── DashboardPage.vue          # Route: /
│   ├── AgentsPage.vue             # Route: /agents
│   ├── RulesPage.vue              # Route: /rules
│   ├── RuleDetailPage.vue         # Route: /rules/:id
│   ├── L4RulesPage.vue            # Route: /l4
│   ├── CertsPage.vue              # Route: /certs
│   └── SettingsPage.vue           # Route: /settings
├── components/
│   ├── layout/
│   │   ├── AppShell.vue          # Root layout (TopBar + Sidebar + RouterView)
│   │   ├── TopBar.vue            # Logo + global search trigger + ThemeSelector + logout
│   │   ├── Sidebar.vue           # Desktop Agent list, collapsible
│   │   └── BottomNav.vue         # Mobile bottom tab bar (4 tabs)
│   ├── rules/
│   │   ├── RuleTable.vue         # HTTP rules list with inline edit
│   │   └── RuleForm.vue          # Add/edit HTTP rule form
│   ├── l4/
│   │   ├── L4RuleTable.vue       # L4 rules list
│   │   └── L4RuleForm.vue        # Add/edit L4 rule form
│   ├── certs/
│   │   └── CertCard.vue          # Certificate display card
│   ├── dashboard/
│   │   └── StatsGrid.vue         # Dashboard statistics cards
│   ├── GlobalSearch.vue           # ⌘K command palette overlay
│   └── base/
│       └── ThemeSelector.vue      # Reuse existing, move to this location
└── styles/
    └── themes.css                 # 3 complete theme CSS variable sets
```

### Files to modify

```
panel/frontend/package.json           # Add: vue-router, @tanstack/vue-query, radix-vue, unocss, @unocss/preset-wind, @unocss/preset-icons, @iconify-json/*, @vueuse/core
panel/frontend/vite.config.js         # Add UnoCSS plugin and Vue Router plugin
panel/frontend/src/main.js            # Add Vue Router and TanStack Query, remove old App.vue logic
panel/frontend/src/App.vue            # Replace with thin shell: just <AppShell />
panel/frontend/src/api/index.js       # Keep as-is (mock + real API, no changes needed)
panel/frontend/src/styles/index.css   # Replace imports: load themes.css, remove variables.css
panel/frontend/index.html             # Add inline ThemeScript to prevent flash (read theme from localStorage before render)
```

### Files to delete (after migration complete)

```
panel/frontend/src/styles/variables.css      # Replaced by themes.css
panel/frontend/src/styles/base.css            # Absorbed into UnoCSS
panel/frontend/src/styles/components.css      # Absorbed into component files
panel/frontend/src/styles/layout.css          # Absorbed into layout components
panel/frontend/src/styles/status-message.css  # Absorbed into StatusMessage component
panel/frontend/src/components/RuleList.vue    # Replaced by RuleTable
panel/frontend/src/components/RuleItem.vue    # Replaced by RuleTable row
panel/frontend/src/components/L4RuleList.vue # Replaced by L4RuleTable
panel/frontend/src/components/L4RuleItem.vue  # Replaced by L4RuleTable row
panel/frontend/src/components/CertificateList.vue  # Replaced by CertCard grid
panel/frontend/src/components/CertificateForm.vue  # Reused with refactor
panel/frontend/src/components/RuleForm.vue   # Reused with refactor (move to rules/)
panel/frontend/src/components/L4RuleForm.vue  # Reused with refactor (move to l4/)
panel/frontend/src/components/ActionBar.vue   # Absorbed into page headers
panel/frontend/src/components/MobileAgentSelector.vue  # Replaced by AgentsPage
panel/frontend/src/components/StatusMessage.vue    # Refactor into composable
panel/frontend/src/stores/rules.js           # Replaced by TanStack Query hooks
panel/frontend/src/utils/syncStatus.js       # Keep, refactor into hooks
```

---

## Phase 1: Project Scaffold — Dependencies, Config, Shell

### Task 1: Install all new dependencies

**Files:**
- Modify: `panel/frontend/package.json`

- [ ] **Step 1: Install dependencies**

```bash
cd panel/frontend
npm install vue-router@4 @tanstack/vue-query @vueuse/core
npm install -D unocss @unocss/preset-wind @unocss/preset-icons @iconify-json/mdi @iconify-json/ph
```

Expected: All packages installed without errors.

- [ ] **Step 2: Commit**

```bash
git add package.json package-lock.json
git commit -m "chore: add vue-router, tanstack query, vueuse, unocss dependencies"
```

---

### Task 2: Create UnoCSS config

**Files:**
- Create: `panel/frontend/uno.config.js`

- [ ] **Step 1: Write uno.config.js**

```js
import { defineConfig, presetWind, presetIcons } from 'unocss'

export default defineConfig({
  presets: [
    presetWind(),
    presetIcons({
      scale: 1.2,
      extraProperties: {
        display: 'inline-block',
        'vertical-align': 'middle'
      }
    })
  ],
  theme: {
    // Share CSS variable tokens with our themes.css
    colors: {
      primary: 'var(--color-primary)',
      surface: 'var(--color-bg-surface)',
      canvas: 'var(--color-bg-canvas)'
    }
  },
  shortcuts: {
    // Common utility groups used across components
    'btn': 'px-4 py-2 rounded-xl font-medium text-sm transition-all duration-250 cursor-pointer',
    'btn-primary': 'btn bg-primary text-white hover:opacity-90',
    'btn-secondary': 'btn bg-surface border border-default hover:bg-hover',
    'card': 'bg-surface rounded-2xl border border-default shadow-sm',
    'input-base': 'w-full px-3 py-2 rounded-xl bg-subtle border border-default text-sm outline-none focus:border-primary transition-all duration-250'
  }
})
```

- [ ] **Step 2: Commit**

```bash
git add uno.config.js
git commit -m "feat: add UnoCSS configuration with Tailwind preset"
```

---

### Task 3: Update Vite config

**Files:**
- Modify: `panel/frontend/vite.config.js`

- [ ] **Step 1: Update vite.config.js**

```js
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'

export default defineConfig({
  plugins: [
    vue(),
    UnoCSS()
  ],
  // ... keep existing build and server config unchanged
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: undefined
      }
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/panel-api': {
        target: 'http://localhost:18081',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/panel-api/, '/api')
      }
    }
  }
})
```

- [ ] **Step 2: Commit**

```bash
git add vite.config.js
git commit -m "feat: integrate UnoCSS into Vite"
```

---

### Task 4: Create Vue Router config

**Files:**
- Create: `panel/frontend/src/router/index.js`

- [ ] **Step 1: Write router/index.js**

```js
import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    name: 'dashboard',
    component: () => import('../pages/DashboardPage.vue'),
    meta: { title: '首页' }
  },
  {
    path: '/agents',
    name: 'agents',
    component: () => import('../pages/AgentsPage.vue'),
    meta: { title: '节点管理' }
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('../pages/RulesPage.vue'),
    meta: { title: 'HTTP 规则' }
  },
  {
    path: '/rules/:id',
    name: 'rule-detail',
    component: () => import('../pages/RuleDetailPage.vue'),
    meta: { title: '规则详情' }
  },
  {
    path: '/l4',
    name: 'l4',
    component: () => import('../pages/L4RulesPage.vue'),
    meta: { title: 'L4 规则' }
  },
  {
    path: '/l4/:id',
    name: 'l4-detail',
    component: () => import('../pages/L4RulesPage.vue'),
    meta: { title: 'L4 规则详情' }
  },
  {
    path: '/certs',
    name: 'certs',
    component: () => import('../pages/CertsPage.vue'),
    meta: { title: '证书' }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('../pages/SettingsPage.vue'),
    meta: { title: '设置' }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router
```

- [ ] **Step 2: Commit**

```bash
git add router/index.js
git commit -m "feat: add Vue Router with flat route structure"
```

---

### Task 5: Create Context providers

**Files:**
- Create: `panel/frontend/src/context/AgentContext.js`
- Create: `panel/frontend/src/context/ThemeContext.js`

- [ ] **Step 1: Write AgentContext.js**

```js
import { createContext, useContext, ref, computed } from 'vue'

const AgentContext = createContext(null)

export function AgentProvider({ children }) {
  // Default to 'local' agent
  const selectedAgentId = ref(localStorage.getItem('selected_agent_id') || 'local')

  function selectAgent(id) {
    selectedAgentId.value = id
    localStorage.setItem('selected_agent_id', id)
  }

  return (
    <AgentContext.Provider value={{ selectedAgentId, selectAgent }}>
      {children}
    </AgentContext.Provider>
  )
}

export function useAgent() {
  const ctx = useContext(AgentContext)
  if (!ctx) throw new Error('useAgent must be used within AgentProvider')
  return ctx
}
```

- [ ] **Step 2: Write ThemeContext.js**

```js
import { createContext, useContext, ref, computed } from 'vue'

const ThemeContext = createContext(null)

const THEMES = ['sakura', 'business', 'midnight']

export function ThemeProvider({ children }) {
  const currentThemeId = ref(
    localStorage.getItem('theme') ||
    (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'midnight' : 'sakura')
  )

  function setTheme(id) {
    if (!THEMES.includes(id)) return
    currentThemeId.value = id
    document.documentElement.setAttribute('data-theme', id)
    localStorage.setItem('theme', id)
  }

  // Apply on init
  document.documentElement.setAttribute('data-theme', currentThemeId.value)

  return (
    <ThemeContext.Provider value={{ currentThemeId, setTheme, themes: THEMES }}>
      {children}
    </ThemeContext.Provider>
  )
}

export function useTheme() {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}
```

Note: Use `defineComponent` with render function or script-setup JSX setup as shown above.

- [ ] **Step 3: Commit**

```bash
git add context/AgentContext.js context/ThemeContext.js
git commit -m "feat: add AgentContext and ThemeContext"
```

---

### Task 6: Rewrite main.js and App.vue

**Files:**
- Modify: `panel/frontend/src/main.js`
- Modify: `panel/frontend/src/App.vue`

- [ ] **Step 1: Rewrite main.js**

```js
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import router from './router'
import './styles/index.css'
import 'virtual:uno.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin, {
  queryClientConfig: {
    defaultOptions: {
      queries: {
        staleTime: 30_000,      // 30s before re-fetching
        refetchInterval: 10_000  // poll every 10s for agents
      }
    }
  }
})

app.mount('#app')
```

- [ ] **Step 2: Rewrite App.vue**

```vue
<template>
  <RouterView />
</template>

<script setup>
// App.vue is now a thin shell — all layout is in AppShell
</script>
```

- [ ] **Step 3: Commit**

```bash
git add main.js App.vue
git commit -m "refactor: simplify App.vue to shell, add Vue Router and TanStack Query"
```

---

### Task 7: Create themes.css with 3 themes

**Files:**
- Create: `panel/frontend/src/styles/themes.css`

- [ ] **Step 1: Write themes.css**

This file contains the full 3-theme CSS variable system. Copy the 4 theme blocks from the existing `variables.css` (sakura, business, midnight — skip cyberpunk), converting them to use `[data-theme="sakura"]`, `[data-theme="business"]`, `[data-theme="midnight"]` selectors. Also add a `[data-theme]` selector with shared base styles.

```css
/* Base shared styles */
[data-theme] {
  /* Spacing, radius, font, z-index — copy from :root in variables.css */
  --space-0: 0; /* ... all --space-* vars */
  --radius-none: 0; /* ... all --radius-* vars */
  --font-sans: 'Noto Sans SC', 'Microsoft YaHei', ...;
  --font-mono: 'JetBrains Mono', Consolas, monospace;
  --text-xs: 0.75rem; /* ... all text-* vars */
  --font-normal: 400; /* ... font weights */
  --leading-none: 1; /* ... line heights */
  --duration-fast: 150ms;
  --duration-normal: 250ms;
  --duration-slow: 400ms;
  --ease-default: cubic-bezier(0.4, 0, 0.2, 1);
  --ease-bounce: cubic-bezier(0.34, 1.56, 0.64, 1);
  --z-base: 0;
  --z-dropdown: 100;
  --z-sticky: 200;
  --z-fixed: 300;
  --z-modal-backdrop: 400;
  --z-modal: 500;
  --sidebar-width: 260px;
  --header-height: 64px;
}

/* Theme: 二次元 (sakura) */
[data-theme="sakura"] {
  --color-primary: #c084fc;
  --color-primary-hover: #a855f7;
  --color-primary-active: #9333ea;
  --color-primary-subtle: #faf5ff;
  --color-text-primary: #251736;
  --color-text-secondary: #6b4e80;
  --color-text-tertiary: #8c6ea0;
  --color-text-muted: #c4a8d4;
  --color-text-inverse: #ffffff;
  --color-bg-canvas: #fdf2f8;
  --color-bg-surface: rgba(255, 255, 255, 0.82);
  --color-bg-surface-raised: rgba(255, 255, 255, 0.94);
  --color-bg-subtle: #fce7f3;
  --color-bg-hover: #f9e0f2;
  --color-bg-active: #f3d5ec;
  --color-border-subtle: rgba(192, 132, 252, 0.12);
  --color-border-default: rgba(192, 132, 252, 0.22);
  --color-border-strong: rgba(192, 132, 252, 0.38);
  --color-success: #16a34a;
  --color-success-50: rgba(22, 163, 74, 0.1);
  --color-danger: #e11d48;
  --color-danger-50: rgba(225, 29, 72, 0.08);
  --color-warning: #d97706;
  --color-warning-50: rgba(217, 119, 6, 0.1);
  --shadow-sm: 0 4px 12px rgba(192, 132, 252, 0.12);
  --shadow-md: 0 8px 24px rgba(192, 132, 252, 0.15);
  --shadow-lg: 0 12px 32px rgba(192, 132, 252, 0.18);
  --shadow-focus: 0 0 0 4px rgba(192, 132, 252, 0.22);
  --gradient-primary: linear-gradient(135deg, #ff6b9d 0%, #c084fc 50%, #818cf8 100%);
}

/* Theme: 晴空 (business) */
[data-theme="business"] {
  --color-primary: #2563eb;
  --color-primary-hover: #1d4ed8;
  --color-primary-active: #1e40af;
  --color-primary-subtle: rgba(37, 99, 235, 0.07);
  --color-text-primary: #0f172a;
  --color-text-secondary: #334155;
  --color-text-tertiary: #64748b;
  --color-text-muted: #94a3b8;
  --color-text-inverse: #ffffff;
  --color-bg-canvas: #f8fafc;
  --color-bg-surface: rgba(255, 255, 255, 0.96);
  --color-bg-surface-raised: rgba(255, 255, 255, 0.99);
  --color-bg-subtle: #f1f5f9;
  --color-bg-hover: #e2e8f0;
  --color-bg-active: #cbd5e1;
  --color-border-subtle: rgba(15, 23, 42, 0.06);
  --color-border-default: rgba(15, 23, 42, 0.11);
  --color-border-strong: rgba(15, 23, 42, 0.2);
  --color-success: #16a34a;
  --color-success-50: rgba(22, 163, 74, 0.08);
  --color-danger: #dc2626;
  --color-danger-50: rgba(220, 38, 38, 0.07);
  --color-warning: #d97706;
  --color-warning-50: rgba(217, 119, 6, 0.08);
  --shadow-sm: 0 1px 3px rgba(15, 23, 42, 0.08), 0 2px 6px rgba(15, 23, 42, 0.05);
  --shadow-md: 0 4px 12px rgba(15, 23, 42, 0.1), 0 2px 4px rgba(15, 23, 42, 0.06);
  --shadow-lg: 0 8px 24px rgba(15, 23, 42, 0.12), 0 4px 8px rgba(15, 23, 42, 0.07);
  --shadow-focus: 0 0 0 3px rgba(37, 99, 235, 0.28);
  --gradient-primary: linear-gradient(135deg, #3b82f6 0%, #2563eb 45%, #1d4ed8 80%, #1e40af 100%);
}

/* Theme: 暗夜 (midnight) */
[data-theme="midnight"] {
  --color-primary: #818cf8;
  --color-primary-hover: #6366f1;
  --color-primary-active: #4f46e5;
  --color-primary-subtle: rgba(129, 140, 248, 0.1);
  --color-text-primary: #e6edf3;
  --color-text-secondary: #8d96a0;
  --color-text-tertiary: #5c6470;
  --color-text-muted: #3d444d;
  --color-text-inverse: #0f172a;
  --color-bg-canvas: #0d1117;
  --color-bg-surface: rgba(22, 27, 34, 0.9);
  --color-bg-surface-raised: rgba(33, 38, 46, 0.92);
  --color-bg-subtle: rgba(129, 140, 248, 0.07);
  --color-bg-hover: rgba(129, 140, 248, 0.11);
  --color-bg-active: rgba(129, 140, 248, 0.17);
  --color-border-subtle: rgba(129, 140, 248, 0.08);
  --color-border-default: rgba(129, 140, 248, 0.15);
  --color-border-strong: rgba(129, 140, 248, 0.3);
  --color-success: #3fb950;
  --color-success-50: rgba(63, 185, 80, 0.1);
  --color-danger: #f85149;
  --color-danger-50: rgba(248, 81, 73, 0.1);
  --color-warning: #d29922;
  --color-warning-50: rgba(210, 153, 34, 0.1);
  --shadow-sm: 0 4px 12px rgba(0, 0, 0, 0.5);
  --shadow-md: 0 8px 24px rgba(0, 0, 0, 0.55);
  --shadow-lg: 0 12px 32px rgba(0, 0, 0, 0.6);
  --shadow-focus: 0 0 0 4px rgba(129, 140, 248, 0.22);
  --gradient-primary: linear-gradient(135deg, #a5b4fc 0%, #818cf8 38%, #6366f1 72%, #4f46e5 100%);
}
```

- [ ] **Step 2: Update styles/index.css**

Replace existing content with:

```css
@import './themes.css';

/* Body base — theme is set via data-theme on <html> */
body {
  margin: 0;
  font-family: var(--font-sans);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  -webkit-font-smoothing: antialiased;
}

/* Remove old style imports that are now redundant */
```

- [ ] **Step 3: Add ThemeScript to index.html to prevent flash**

In `index.html`, add this `<script>` tag before `<script type="module" src="/src/main.js">`:

```html
<script>
  // Prevent theme flash: read theme before first paint
  (function() {
    var t = localStorage.getItem('theme');
    if (!t) {
      t = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'midnight' : 'sakura';
    }
    document.documentElement.setAttribute('data-theme', t);
  })();
</script>
```

- [ ] **Step 4: Commit**

```bash
git add styles/themes.css styles/index.css index.html
git commit -m "feat: create 3-theme CSS system, add theme pre-blink script"
```

---

## Phase 2: TanStack Query Hooks

### Task 8: Create Query hooks

**Files:**
- Create: `panel/frontend/src/hooks/useAgents.js`
- Create: `panel/frontend/src/hooks/useRules.js`
- Create: `panel/frontend/src/hooks/useL4Rules.js`
- Create: `panel/frontend/src/hooks/useCertificates.js`
- Create: `panel/frontend/src/hooks/useGlobalSearch.js`

- [ ] **Step 1: Write useAgents.js**

```js
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: api.fetchAgents,
    refetchInterval: 10_000  // poll every 10s like existing refreshTimer
  })
}

export function useCreateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createAgent(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}

export function useDeleteAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (agentId) => api.deleteAgent(agentId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}

export function useRenameAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, name }) => api.renameAgent(agentId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agents'] })
  })
}
```

- [ ] **Step 2: Write useRules.js**

```js
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { computed } from 'vue'

export function useRules(agentId) {
  return useQuery({
    queryKey: ['rules', agentId],
    queryFn: () => api.fetchRules(agentId),
    enabled: computed(() => !!agentId.value)
  })
}

export function useCreateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createRule(agentId.value, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}

export function useUpdateRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateRule(agentId.value, id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}

export function useDeleteRule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (ruleId) => api.deleteRule(agentId.value, ruleId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules', agentId] })
  })
}
```

- [ ] **Step 3: Write useL4Rules.js**

```js
// Mirror useRules.js pattern for L4 rules
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'
import { computed } from 'vue'

export function useL4Rules(agentId) {
  return useQuery({
    queryKey: ['l4Rules', agentId],
    queryFn: () => api.fetchL4Rules(agentId.value),
    enabled: computed(() => !!agentId.value)
  })
}

export function useCreateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => api.createL4Rule(agentId.value, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useUpdateL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateL4Rule(agentId.value, id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}

export function useDeleteL4Rule(agentId) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteL4Rule(agentId.value, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['l4Rules', agentId] })
  })
}
```

- [ ] **Step 4: Write useCertificates.js**

```js
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import * as api from '../api'

export function useCertificates() {
  return useQuery({
    queryKey: ['certificates'],
    queryFn: () => api.fetchCertificates()
  })
}

export function useCreateCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.createCertificate,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useUpdateCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...payload }) => api.updateCertificate(id, payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useDeleteCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.deleteCertificate(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}

export function useIssueCertificate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.issueCertificate(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['certificates'] })
  })
}
```

- [ ] **Step 5: Write useGlobalSearch.js**

```js
import { useQuery } from '@tanstack/vue-query'
import * as api from '../api'
import { ref, watch } from 'vue'

export function useGlobalSearch(query) {
  const debouncedQuery = ref('')
  let timer = null

  watch(query, (val) => {
    clearTimeout(timer)
    timer = setTimeout(() => { debouncedQuery.value = val }, 400)
  })

  return useQuery({
    queryKey: ['globalSearch', debouncedQuery],
    queryFn: () => api.fetchAllAgentsRules(query.value).then(/* normalize + return */),
    enabled: computed(() => debouncedQuery.value.length > 0)
  })
}
```

- [ ] **Step 6: Commit**

```bash
git add hooks/useAgents.js hooks/useRules.js hooks/useL4Rules.js hooks/useCertificates.js hooks/useGlobalSearch.js
git commit -m "feat: add TanStack Query hooks for agents, rules, L4, certificates, and global search"
```

---

## Phase 3: Layout Components

### Task 9: Create AppShell layout

**Files:**
- Create: `panel/frontend/src/components/layout/AppShell.vue`

- [ ] **Step 1: Write AppShell.vue**

Desktop: TopBar + Sidebar + main content area. Mobile: TopBar + content + BottomNav. Agent context read from AgentContext.

```vue
<template>
  <div class="app-shell">
    <TopBar />
    <div class="app-layout">
      <!-- Desktop sidebar -->
      <Sidebar v-if="!isMobile" />
      <!-- Mobile sidebar overlay -->
      <div v-if="mobileSidebarOpen" class="sidebar-overlay" @click="mobileSidebarOpen = false" />
      <main class="content">
        <RouterView />
      </main>
    </div>
    <BottomNav v-if="isMobile" />
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import BottomNav from './BottomNav.vue'

const mobileSidebarOpen = ref(false)
const isMobile = computed(() => window.innerWidth < 1024)
</script>

<style scoped>
.app-shell {
  height: 100dvh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.app-layout {
  display: flex;
  flex: 1;
  min-height: 0;
}
.content {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
}
@media (max-width: 1023px) {
  .content {
    padding-bottom: 5rem; /* space for bottom nav */
  }
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add components/layout/AppShell.vue
git commit -m "feat: add AppShell layout component"
```

---

### Task 10: Create TopBar

**Files:**
- Create: `panel/frontend/src/components/layout/TopBar.vue`

- [ ] **Step 1: Write TopBar.vue**

Contains: Logo (SVG lightning bolt), app name "Nginx Proxy · 管理端", global search trigger (⌘K), ThemeSelector, logout button.

- [ ] **Step 2: Commit**

```bash
git add components/layout/TopBar.vue
git commit -m "feat: add TopBar with logo, search trigger, theme, logout"
```

---

### Task 11: Create Sidebar

**Files:**
- Create: `panel/frontend/src/components/layout/Sidebar.vue`

- [ ] **Step 1: Write Sidebar.vue**

Desktop-only Agent list with: collapse toggle, search filter, Agent items with status indicator + mode icon, rename/delete actions on hover. Uses useAgents() hook. Reads selectedAgentId from AgentContext.

- [ ] **Step 2: Commit**

```bash
git add components/layout/Sidebar.vue
git commit -m "feat: add desktop Sidebar with Agent list"
```

---

### Task 12: Create BottomNav

**Files:**
- Create: `panel/frontend/src/components/layout/BottomNav.vue`

- [ ] **Step 1: Write BottomNav.vue**

Mobile-only (use `display: none` and show via media query). 4 tabs: Dashboard (🏠), Rules (🔗), Certs (🔒), Settings (⚙️). Active tab highlighted with primary color. Use Vue Router's `useRoute` to highlight active tab.

```vue
<template>
  <nav class="bottom-nav">
    <RouterLink to="/" class="nav-item" :class="{ active: route.path === '/' }">
      <span class="nav-icon">🏠</span>
      <span>首页</span>
    </RouterLink>
    <RouterLink to="/rules" class="nav-item">
      <span class="nav-icon">🔗</span>
      <span>规则</span>
    </RouterLink>
    <RouterLink to="/certs" class="nav-item">
      <span class="nav-icon">🔒</span>
      <span>证书</span>
    </RouterLink>
    <RouterLink to="/settings" class="nav-item">
      <span class="nav-icon">⚙️</span>
      <span>设置</span>
    </RouterLink>
  </nav>
</template>

<style scoped>
.bottom-nav {
  display: none;
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  height: 60px;
  background: var(--color-bg-surface);
  border-top: 1px solid var(--color-border-default);
  backdrop-filter: blur(16px);
  z-index: var(--z-sticky);
}
@media (max-width: 1023px) {
  .bottom-nav { display: flex; }
}
.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 10px;
  transition: color 0.15s;
}
.nav-item.active,
.nav-item:hover {
  color: var(--color-primary);
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add components/layout/BottomNav.vue
git commit -m "feat: add mobile BottomNav with 4 tabs"
```

---

## Phase 4: Page Components

### Task 13: Create DashboardPage

**Files:**
- Create: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: Write DashboardPage.vue**

Route: `/`. Shows: total agents count, online count, total HTTP rules, total L4 rules. Uses StatsGrid component + useAgents() query. If no agents, show empty state prompting to add agents or run join script.

- [ ] **Step 2: Commit**

```bash
git add pages/DashboardPage.vue components/dashboard/StatsGrid.vue
git commit -m "feat: add DashboardPage with StatsGrid"
```

---

### Task 14: Create AgentsPage

**Files:**
- Create: `panel/frontend/src/pages/AgentsPage.vue`

- [ ] **Step 1: Write AgentsPage.vue**

Route: `/agents`. Mobile-first full-screen Agent list with status, mode badge, last seen. Add button opens join script modal. Swipe to delete (or long-press). Uses useAgents(), useDeleteAgent() hooks.

- [ ] **Step 2: Commit**

```bash
git add pages/AgentsPage.vue
git commit -m "feat: add AgentsPage for mobile agent management"
```

---

### Task 15: Create RulesPage

**Files:**
- Create: `panel/frontend/src/pages/RulesPage.vue`

- [ ] **Step 1: Write RulesPage.vue**

Route: `/rules`. Header: "HTTP 规则" title + add button. Tab bar: HTTP / L4 segmented control. RuleTable component below. Reads selectedAgentId from AgentContext. If no agent selected, show prompt to select one. Uses useRules().

- [ ] **Step 2: Commit**

```bash
git add pages/RulesPage.vue
git commit -m "feat: add RulesPage with RuleTable"
```

---

### Task 16: Create RuleDetailPage

**Files:**
- Create: `panel/frontend/src/pages/RuleDetailPage.vue`

- [ ] **Step 1: Write RuleDetailPage.vue**

Route: `/rules/:id`. Single rule view/edit form. Back button to /rules. Uses useRules() filtered by id param.

- [ ] **Step 2: Commit**

```bash
git add pages/RuleDetailPage.vue
git commit -m "feat: add RuleDetailPage for single rule editing"
```

---

### Task 17: Create L4RulesPage

**Files:**
- Create: `panel/frontend/src/pages/L4RulesPage.vue`

- [ ] **Step 1: Write L4RulesPage.vue**

Route: `/l4`. Similar to RulesPage but for L4 rules. Uses useL4Rules().

- [ ] **Step 2: Commit**

```bash
git add pages/L4RulesPage.vue
git commit -m "feat: add L4RulesPage"
```

---

### Task 18: Create CertsPage

**Files:**
- Create: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Write CertsPage.vue**

Route: `/certs`. Grid of CertCard components. Uses useCertificates(). Add button opens CertForm modal.

- [ ] **Step 2: Commit**

```bash
git add pages/CertsPage.vue components/certs/CertCard.vue
git commit -m "feat: add CertsPage with certificate grid"
```

---

### Task 19: Create SettingsPage

**Files:**
- Create: `panel/frontend/src/pages/SettingsPage.vue`

- [ ] **Step 1: Write SettingsPage.vue**

Route: `/settings`. Sections: Token configuration, Theme selector (inline instead of dropdown), deploy mode info, about section. Move ThemeSelector component here and to TopBar.

- [ ] **Step 2: Commit**

```bash
git add pages/SettingsPage.vue
git commit -m "feat: add SettingsPage"
```

---

## Phase 5: Business Components

### Task 20: Create RuleTable and RuleForm

**Files:**
- Create: `panel/frontend/src/components/rules/RuleTable.vue`
- Create: `panel/frontend/src/components/rules/RuleForm.vue`
- Modify: move `RuleForm.vue` from `components/RuleForm.vue` → `components/rules/RuleForm.vue`
- Modify: move `L4RuleForm.vue` → `components/l4/L4RuleForm.vue`

- [ ] **Step 1: Write RuleTable.vue**

Table with columns: enable toggle, frontend_url, backend_url, tags, actions. Expandable rows for inline editing. Uses useRules() and useUpdateRule()/useDeleteRule().

- [ ] **Step 2: Write RuleForm.vue**

Refactored from existing RuleForm.vue, adapted for new styling. Add/Edit modal form.

- [ ] **Step 3: Commit**

```bash
git add components/rules/RuleTable.vue components/rules/RuleForm.vue
git commit -m "feat: add RuleTable with inline editing and RuleForm"
```

---

### Task 21: Create L4RuleTable and GlobalSearch

**Files:**
- Create: `panel/frontend/src/components/l4/L4RuleTable.vue`
- Create: `panel/frontend/src/components/GlobalSearch.vue`

- [ ] **Step 1: Write L4RuleTable.vue**

Similar to RuleTable but for L4: protocol badge, listen host:port, upstream host:port, tags, actions.

- [ ] **Step 2: Write GlobalSearch.vue**

⌘K command palette. Opens as fixed overlay. Input at top, results grouped by agent. Keyboard navigable. Uses useGlobalSearch().

- [ ] **Step 3: Commit**

```bash
git add components/l4/L4RuleTable.vue components/GlobalSearch.vue
git commit -m "feat: add L4RuleTable and GlobalSearch command palette"
```

---

## Phase 6: Integration and Cleanup

### Task 22: Remove old files

**Files:**
- Delete: `panel/frontend/src/components/RuleList.vue`
- Delete: `panel/frontend/src/components/RuleItem.vue`
- Delete: `panel/frontend/src/components/L4RuleList.vue`
- Delete: `panel/frontend/src/components/L4RuleItem.vue`
- Delete: `panel/frontend/src/components/CertificateList.vue`
- Delete: `panel/frontend/src/components/ActionBar.vue`
- Delete: `panel/frontend/src/components/MobileAgentSelector.vue`
- Delete: `panel/frontend/src/stores/rules.js`
- Delete: `panel/frontend/src/styles/variables.css`
- Delete: `panel/frontend/src/styles/base.css`
- Delete: `panel/frontend/src/styles/components.css`
- Delete: `panel/frontend/src/styles/layout.css`
- Delete: `panel/frontend/src/styles/status-message.css`

- [ ] **Step 1: Delete old files**

```bash
cd panel/frontend/src
rm components/RuleList.vue components/RuleItem.vue
rm components/L4RuleList.vue components/L4RuleItem.vue
rm components/CertificateList.vue components/ActionBar.vue
rm components/MobileAgentSelector.vue
rm stores/rules.js
rm styles/variables.css styles/base.css styles/components.css styles/layout.css styles/status-message.css
```

- [ ] **Step 2: Commit**

```bash
git add -A && git commit -m "chore: remove old App.vue, stores, and CSS files replaced by new architecture"
```

---

### Task 23: Responsive and build verification

- [ ] **Step 1: Test dev server starts**

```bash
cd panel/frontend
npm run dev
# Expected: Vite dev server starts on port 5173
```

- [ ] **Step 2: Test build**

```bash
npm run build
# Expected: Build succeeds, dist/ folder created
```

- [ ] **Step 3: Test each route**

Navigate to: `/`, `/agents`, `/rules`, `/l4`, `/certs`, `/settings`. Verify no errors in console.

- [ ] **Step 4: Commit**

```bash
git commit -m "chore: verify build and dev server"
```

---

## Spec Coverage Check

| Design Spec Section | Tasks |
|---------------------|-------|
| 技术栈 (Vue 3 + UnoCSS + Vue Router + TanStack Query) | Tasks 1-6 |
| 路由结构 (扁平 URL) | Task 4 |
| 布局 (AppShell + TopBar + Sidebar + BottomNav) | Tasks 9-12 |
| 主题系统 (3 themes) | Task 7 |
| TanStack Query hooks | Task 8 |
| Context (Agent + Theme) | Task 5 |
| 页面 (Dashboard + Agents + Rules + L4 + Certs + Settings) | Tasks 13-19 |
| 业务组件 (RuleTable + L4RuleTable + CertCard + GlobalSearch) | Tasks 20-21 |
| 移动端导航 (底部 Tab) | Task 12 |
| 清理旧文件 | Task 22 |
| 构建验证 | Task 23 |

**All design spec sections are covered.**

---

## Type Consistency Check

- `useRules(agentId)` — agentId is a `ComputedRef<string>`, not raw string
- `useL4Rules(agentId)` — same pattern
- `useCertificates()` — no agentId, global scope
- AgentContext `selectedAgentId` — ref, not computed
- All mutations call `invalidateQueries` with the matching `queryKey` array

**Type signatures are consistent across all hooks.**
