# Frontend UI/UX Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Completely overhaul the nginx-reverse-emby control panel frontend UI/UX to match modern SaaS best practices with anime-inspired aesthetics, covering all 10 pages, the theme system, base components, and layout shell.

**Architecture:** CSS custom properties drive theming via `data-theme` on `<html>`. Three themes (sakura-day, sakura-night, business) share a unified spacing/radius/animation token system. Base components (`BaseButton`, `BaseCard`, `BaseInput`, `BaseBadge`, `BaseModal`, `EmptyState`, `StatCard`) are restyled to design-spec. Layout shell (`AppShell`, `TopBar`, `Sidebar`, `BottomNav`) is tightened. Each page is updated to use the new tokens and component shapes. No new dependencies.

**Tech Stack:** Vue 3, Vite, UnoCSS, CSS custom properties, Pinia, Vue Query

---

## File Map

| File | Responsibility |
|------|---------------|
| `panel/frontend/src/styles/themes.css` | CSS custom properties for all 3 themes + shared tokens |
| `panel/frontend/src/styles/index.css` | Body base, scrollbar, selection, page-container, section-card, responsive breakpoints |
| `panel/frontend/src/styles/animations.css` | Keyframes + utility classes (fadeIn, fadeInUp, scaleIn, skeleton, stagger) |
| `panel/frontend/src/styles/utilities.css` | Global utility classes: `.btn`, `.btn-primary`, `.btn-secondary`, `.input-base`, `.modal`, `.status-dot`, `.empty-state`, `.page-header`, `.page-title` |
| `panel/frontend/src/context/ThemeContext.js` | Theme provider/composable; theme list + persistence |
| `panel/frontend/src/components/base/BaseButton.vue` | Primary/secondary/danger/success button with loading state |
| `panel/frontend/src/components/base/BaseCard.vue` | Card container with header/body/footer slots |
| `panel/frontend/src/components/base/BaseInput.vue` | Text/password/email/url input with focus ring |
| `panel/frontend/src/components/base/BaseBadge.vue` | Status/pill badges (success/warning/danger/primary/neutral) |
| `panel/frontend/src/components/base/BaseModal.vue` | Teleported modal with backdrop, header, body, footer |
| `panel/frontend/src/components/base/EmptyState.vue` | Centered empty state with SVG icon slot |
| `panel/frontend/src/components/base/StatCard.vue` | KPI stat card with icon, value, label |
| `panel/frontend/src/components/base/ThemeSelector.vue` | Theme picker dropdown |
| `panel/frontend/src/components/layout/AppShell.vue` | Root layout: TopBar + Sidebar + BottomNav + GlobalSearch |
| `panel/frontend/src/components/layout/TopBar.vue` | Sticky top bar with logo, search trigger, theme selector, agent switcher, logout |
| `panel/frontend/src/components/layout/Sidebar.vue` | Collapsible sidebar nav (desktop only) |
| `panel/frontend/src/components/layout/BottomNav.vue` | Mobile bottom nav with "more" dropdown |
| `panel/frontend/src/pages/LoginPage.vue` | Token login page |
| `panel/frontend/src/pages/DashboardPage.vue` | Dashboard with KPI grid + traffic + agent table |
| `panel/frontend/src/pages/RulesPage.vue` | HTTP rules list with search + card grid |
| `panel/frontend/src/pages/CertsPage.vue` | Certificate list with search + card grid |
| `panel/frontend/src/pages/L4RulesPage.vue` | L4 rules list |
| `panel/frontend/src/pages/RelayListenersPage.vue` | Relay listener list |
| `panel/frontend/src/pages/AgentsPage.vue` | Agent management list |
| `panel/frontend/src/pages/AgentDetailPage.vue` | Single agent detail |
| `panel/frontend/src/pages/SettingsPage.vue` | Settings with tabs |
| `panel/frontend/src/pages/VersionsPage.vue` | Version policy page |
| `panel/frontend/src/components/rules/RuleCard.vue` | Rule card (uses BaseListCard) |
| `panel/frontend/src/components/certs/CertCard.vue` | Certificate card |
| `panel/frontend/src/components/relay/RelayCard.vue` | Relay listener card |
| `panel/frontend/src/components/AgentCard.vue` | Agent card |

---

## Shared Token Changes (reference for all tasks)

These CSS custom property renames/values apply across many files. Each task embeds the full changed file so the engineer never has to cross-reference.

**Theme IDs:** `sakura-day`, `sakura-night`, `business` (was: `sakura`, `midnight`, `business`)

**Body background:** `var(--color-bg-canvas)` solid color, NOT `var(--gradient-bg)`

**Button shape:** `border-radius: var(--radius-full)` (9999px, capsule)

**Card shape:** `border-radius: 12px` (was `var(--radius-xl)` = 1.25rem)

**Input shape:** `border-radius: 10px` (was `var(--radius-lg)` = 0.75rem)

**Sidebar width:** `220px` (was `260px`)

**Header height:** `56px` (was `64px`)

**Page max-width:** `1280px` (was `1200px`)

---

### Task 1: Rewrite themes.css with new 3-theme palette

**Files:**
- Modify: `panel/frontend/src/styles/themes.css`

- [ ] **Step 1: Replace the entire themes.css file**

```css
/* =============================================
   Shared tokens — applied to all themes
   ============================================= */
[data-theme] {
  /* Spacing (8px base) */
  --space-0: 0;
  --space-0-5: 0.125rem;   /* 2px */
  --space-1: 0.25rem;      /* 4px */
  --space-1-5: 0.375rem;   /* 6px */
  --space-2: 0.5rem;       /* 8px */
  --space-2-5: 0.625rem;   /* 10px */
  --space-3: 0.75rem;      /* 12px */
  --space-4: 1rem;         /* 16px */
  --space-5: 1.25rem;      /* 20px */
  --space-6: 1.5rem;       /* 24px */
  --space-8: 2rem;         /* 32px */
  --space-10: 2.5rem;      /* 40px */
  --space-12: 3rem;        /* 48px */
  --space-16: 4rem;        /* 64px */
  --space-20: 5rem;        /* 80px */

  /* Radius */
  --radius-none: 0;
  --radius-xs: 0.375rem;   /* 6px */
  --radius-sm: 0.5rem;     /* 8px */
  --radius-md: 0.625rem;   /* 10px */
  --radius-lg: 0.75rem;    /* 12px */
  --radius-xl: 1rem;       /* 16px */
  --radius-2xl: 1.25rem;   /* 20px */
  --radius-3xl: 1.5rem;    /* 24px */
  --radius-full: 9999px;

  /* Fonts */
  --font-sans: 'Noto Sans SC', 'Microsoft YaHei', 'PingFang SC', 'Hiragino Sans GB', 'WenQuanYi Micro Hei', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'JetBrains Mono', 'Consolas', 'Courier New', monospace;

  /* Font Sizes */
  --text-xs: 0.75rem;      /* 12px */
  --text-sm: 0.875rem;     /* 14px */
  --text-base: 1rem;       /* 16px */
  --text-lg: 1.125rem;     /* 18px */
  --text-xl: 1.25rem;      /* 20px */
  --text-2xl: 1.5rem;      /* 24px */
  --text-3xl: 1.875rem;    /* 30px */

  /* Font Weights */
  --font-normal: 400;
  --font-medium: 500;
  --font-semibold: 600;
  --font-bold: 700;

  /* Transitions */
  --duration-fast: 150ms;
  --duration-normal: 200ms;
  --duration-slow: 300ms;
  --ease-default: cubic-bezier(0.4, 0, 0.2, 1);
  --ease-bounce: cubic-bezier(0.34, 1.56, 0.64, 1);

  /* Z-Index */
  --z-base: 0;
  --z-dropdown: 100;
  --z-sticky: 200;
  --z-fixed: 300;
  --z-modal-backdrop: 400;
  --z-modal: 500;
  --z-popover: 600;
  --z-tooltip: 700;
  --z-toast: 800;

  /* Layout */
  --sidebar-width: 220px;
  --header-height: 56px;
}

/* =============================================
   Theme: 昼樱 (sakura-day) — Light anime
   ============================================= */
[data-theme="sakura-day"] {
  --color-primary: #ec4899;
  --color-primary-hover: #db2777;
  --color-primary-active: #be185d;
  --color-primary-subtle: rgba(236, 72, 153, 0.08);
  --color-primary-50: rgba(236, 72, 153, 0.04);
  --color-primary-100: rgba(236, 72, 153, 0.10);
  --color-primary-200: rgba(236, 72, 153, 0.18);
  --color-primary-300: rgba(236, 72, 153, 0.28);

  --color-accent: #f472b6;
  --color-accent-hover: #ec4899;
  --color-accent-subtle: rgba(244, 114, 182, 0.08);

  --color-text-primary: #1f2937;
  --color-text-secondary: #6b7280;
  --color-text-tertiary: #9ca3af;
  --color-text-muted: #d1d5db;
  --color-text-inverse: #ffffff;

  --color-bg-canvas: #fafafa;
  --color-bg-surface: #ffffff;
  --color-bg-surface-raised: #ffffff;
  --color-bg-sunken: #f9fafb;
  --color-bg-subtle: #fdf2f8;
  --color-bg-hover: #fce7f3;
  --color-bg-active: #fbcfe8;

  --color-border-subtle: rgba(0, 0, 0, 0.06);
  --color-border-default: rgba(0, 0, 0, 0.08);
  --color-border-strong: rgba(0, 0, 0, 0.12);
  --color-border-focus: rgba(236, 72, 153, 0.35);

  --color-success: #10b981;
  --color-success-50: rgba(16, 185, 129, 0.08);
  --color-success-glow: rgba(16, 185, 129, 0.20);
  --color-danger: #ef4444;
  --color-danger-50: rgba(239, 68, 68, 0.08);
  --color-danger-glow: rgba(239, 68, 68, 0.20);
  --color-warning: #f59e0b;
  --color-warning-50: rgba(245, 158, 11, 0.08);
  --color-warning-glow: rgba(245, 158, 11, 0.20);

  --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.04);
  --shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.04);
  --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.06), 0 2px 4px rgba(0, 0, 0, 0.04);
  --shadow-lg: 0 10px 15px rgba(0, 0, 0, 0.06), 0 4px 6px rgba(0, 0, 0, 0.04);
  --shadow-xl: 0 20px 25px rgba(0, 0, 0, 0.08), 0 8px 10px rgba(0, 0, 0, 0.04);
  --shadow-2xl: 0 24px 48px rgba(0, 0, 0, 0.10);
  --shadow-focus: 0 0 0 3px var(--color-primary-subtle);
  --shadow-inner: inset 0 1px 2px rgba(0, 0, 0, 0.04);
}

/* =============================================
   Theme: 夜樱 (sakura-night) — Dark anime
   ============================================= */
[data-theme="sakura-night"] {
  --color-primary: #f472b6;
  --color-primary-hover: #ec4899;
  --color-primary-active: #db2777;
  --color-primary-subtle: rgba(244, 114, 182, 0.12);
  --color-primary-50: rgba(244, 114, 182, 0.06);
  --color-primary-100: rgba(244, 114, 182, 0.14);
  --color-primary-200: rgba(244, 114, 182, 0.22);
  --color-primary-300: rgba(244, 114, 182, 0.32);

  --color-accent: #c084fc;
  --color-accent-hover: #a855f7;
  --color-accent-subtle: rgba(192, 132, 252, 0.10);

  --color-text-primary: #f1f5f9;
  --color-text-secondary: #94a3b8;
  --color-text-tertiary: #64748b;
  --color-text-muted: #475569;
  --color-text-inverse: #0f172a;

  --color-bg-canvas: #0f0f14;
  --color-bg-surface: #1a1a24;
  --color-bg-surface-raised: #22222e;
  --color-bg-sunken: #0a0a0f;
  --color-bg-subtle: rgba(244, 114, 182, 0.05);
  --color-bg-hover: rgba(244, 114, 182, 0.08);
  --color-bg-active: rgba(244, 114, 182, 0.12);

  --color-border-subtle: rgba(255, 255, 255, 0.06);
  --color-border-default: rgba(255, 255, 255, 0.08);
  --color-border-strong: rgba(255, 255, 255, 0.14);
  --color-border-focus: rgba(244, 114, 182, 0.35);

  --color-success: #34d399;
  --color-success-50: rgba(52, 211, 153, 0.10);
  --color-success-glow: rgba(52, 211, 153, 0.25);
  --color-danger: #f87171;
  --color-danger-50: rgba(248, 113, 113, 0.10);
  --color-danger-glow: rgba(248, 113, 113, 0.25);
  --color-warning: #fbbf24;
  --color-warning-50: rgba(251, 191, 36, 0.10);
  --color-warning-glow: rgba(251, 191, 36, 0.25);

  --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.25);
  --shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.30), 0 1px 2px rgba(0, 0, 0, 0.20);
  --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.35), 0 2px 4px rgba(0, 0, 0, 0.25);
  --shadow-lg: 0 10px 15px rgba(0, 0, 0, 0.40), 0 4px 6px rgba(0, 0, 0, 0.30);
  --shadow-xl: 0 20px 25px rgba(0, 0, 0, 0.45), 0 8px 10px rgba(0, 0, 0, 0.30);
  --shadow-2xl: 0 24px 48px rgba(0, 0, 0, 0.55);
  --shadow-focus: 0 0 0 3px var(--color-primary-subtle);
  --shadow-inner: inset 0 1px 2px rgba(0, 0, 0, 0.20);
}

/* =============================================
   Theme: 晴空 (business) — Professional SaaS
   ============================================= */
[data-theme="business"] {
  --color-primary: #0ea5e9;
  --color-primary-hover: #0284c7;
  --color-primary-active: #0369a1;
  --color-primary-subtle: rgba(14, 165, 233, 0.08);
  --color-primary-50: rgba(14, 165, 233, 0.04);
  --color-primary-100: rgba(14, 165, 233, 0.08);
  --color-primary-200: rgba(14, 165, 233, 0.16);
  --color-primary-300: rgba(14, 165, 233, 0.24);

  --color-accent: #06b6d4;
  --color-accent-hover: #0891b2;
  --color-accent-subtle: rgba(6, 182, 212, 0.08);

  --color-text-primary: #0f172a;
  --color-text-secondary: #475569;
  --color-text-tertiary: #64748b;
  --color-text-muted: #94a3b8;
  --color-text-inverse: #ffffff;

  --color-bg-canvas: #f8fafc;
  --color-bg-surface: #ffffff;
  --color-bg-surface-raised: #ffffff;
  --color-bg-sunken: #f1f5f9;
  --color-bg-subtle: #f1f5f9;
  --color-bg-hover: #e2e8f0;
  --color-bg-active: #cbd5e1;

  --color-border-subtle: rgba(15, 23, 42, 0.06);
  --color-border-default: rgba(15, 23, 42, 0.08);
  --color-border-strong: rgba(15, 23, 42, 0.14);
  --color-border-focus: rgba(14, 165, 233, 0.35);

  --color-success: #10b981;
  --color-success-50: rgba(16, 185, 129, 0.08);
  --color-success-glow: rgba(16, 185, 129, 0.20);
  --color-danger: #ef4444;
  --color-danger-50: rgba(239, 68, 68, 0.08);
  --color-danger-glow: rgba(239, 68, 68, 0.20);
  --color-warning: #f59e0b;
  --color-warning-50: rgba(245, 158, 11, 0.08);
  --color-warning-glow: rgba(245, 158, 11, 0.20);

  --shadow-xs: 0 1px 2px rgba(15, 23, 42, 0.04);
  --shadow-sm: 0 1px 3px rgba(15, 23, 42, 0.06), 0 1px 2px rgba(15, 23, 42, 0.04);
  --shadow-md: 0 4px 6px rgba(15, 23, 42, 0.07), 0 2px 4px rgba(15, 23, 42, 0.05);
  --shadow-lg: 0 10px 15px rgba(15, 23, 42, 0.08), 0 4px 6px rgba(15, 23, 42, 0.04);
  --shadow-xl: 0 20px 25px rgba(15, 23, 42, 0.10), 0 8px 10px rgba(15, 23, 42, 0.04);
  --shadow-2xl: 0 24px 48px rgba(15, 23, 42, 0.12);
  --shadow-focus: 0 0 0 3px var(--color-primary-subtle);
  --shadow-inner: inset 0 1px 2px rgba(15, 23, 42, 0.04);
}
```

- [ ] **Step 2: Verify no compile errors**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: Build completes without CSS syntax errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/styles/themes.css
git commit -m "feat(panel): overhaul theme system with sakura-day, sakura-night, business palettes"
```

---

### Task 2: Update index.css (body, scrollbar, containers)

**Files:**
- Modify: `panel/frontend/src/styles/index.css`

- [ ] **Step 1: Replace body background and update containers**

Replace the entire file content with:

```css
@import './themes.css';
@import './animations.css';
@import './utilities.css';

/* Body base — theme is set via data-theme on <html> */
body {
  margin: 0;
  font-family: var(--font-sans);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  -webkit-font-smoothing: antialiased;
  min-height: 100dvh;
}

/* Page content wrapper transition */
.content {
  transition: opacity var(--duration-normal) var(--ease-default);
}

/* Scrollbar — clean minimal */
::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

::-webkit-scrollbar-track {
  background: transparent;
}

::-webkit-scrollbar-thumb {
  background: var(--color-border-strong);
  border-radius: var(--radius-full);
}

::-webkit-scrollbar-thumb:hover {
  background: var(--color-text-muted);
}

/* Selection */
::selection {
  background: var(--color-primary-200);
  color: var(--color-text-primary);
}

/* Focus visible for keyboard nav */
:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* Page max-width container */
.page-container {
  max-width: 1280px;
  margin: 0 auto;
  padding: 0 1.5rem;
}

/* Section card wrapper */
.section-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  box-shadow: var(--shadow-xs);
  margin-bottom: 1.5rem;
}

/* 4K adjustments */
@media (min-width: 2560px) {
  .rule-grid { grid-template-columns: repeat(auto-fill, minmax(380px, 1fr)); gap: 1.5rem; }
  .cert-grid { grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 1.5rem; }
  .base-list-card { padding: 1.25rem; }
  .page-title { font-size: 1.75rem; }
  .page-container { max-width: 1600px; }
}

/* TopBar 4K */
@media (min-width: 2560px) {
  .topbar { height: 64px; padding: 0 2rem; }
  .topbar__name { font-size: 1.125rem; }
  .topbar__badge { font-size: 0.875rem; }
}

/* Tablet */
@media (max-width: 1024px) {
  .cert-grid { grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); }
  .rule-grid { grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
  .agent-grid { grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
}

/* Mobile */
@media (max-width: 640px) {
  .page-container { padding: 0 1rem; }
  .page-title { font-size: 1.25rem; }
}
```

- [ ] **Step 2: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/styles/index.css
git commit -m "feat(panel): update index.css for solid backgrounds and new token values"
```

---

### Task 3: Update animations.css (reduce motion, skeleton)

**Files:**
- Modify: `panel/frontend/src/styles/animations.css`

- [ ] **Step 1: Replace entire file**

```css
/* =============================================
   Animation system — keyframes + utilities
   ============================================= */

/* ----- Page transitions ----- */
@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

@keyframes fadeInUp {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes fadeInDown {
  from { opacity: 0; transform: translateY(-8px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes slideInRight {
  from { opacity: 0; transform: translateX(8px); }
  to { opacity: 1; transform: translateX(0); }
}

/* ----- Component animations ----- */
@keyframes scaleIn {
  from { opacity: 0; transform: scale(0.98); }
  to { opacity: 1; transform: scale(1); }
}

@keyframes buttonPress {
  0% { transform: scale(1); }
  50% { transform: scale(0.97); }
  100% { transform: scale(1); }
}

/* ----- Status indicators ----- */
@keyframes breathe {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

/* ----- Skeleton shimmer ----- */
@keyframes skeletonShimmer {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}

/* ----- Error shake ----- */
@keyframes errorShake {
  0%, 100% { transform: translateX(0); }
  20% { transform: translateX(-4px); }
  40% { transform: translateX(4px); }
  60% { transform: translateX(-3px); }
  80% { transform: translateX(3px); }
}

/* ----- Utility classes ----- */
.animate-fade-in { animation: fadeIn var(--duration-normal) var(--ease-default) both; }
.animate-fade-in-up { animation: fadeInUp var(--duration-normal) var(--ease-default) both; }
.animate-fade-in-down { animation: fadeInDown var(--duration-normal) var(--ease-default) both; }
.animate-slide-in-right { animation: slideInRight var(--duration-normal) var(--ease-default) both; }
.animate-scale-in { animation: scaleIn var(--duration-normal) var(--ease-default) both; }
.animate-breathe { animation: breathe 2s ease-in-out infinite; }
.animate-pulse { animation: pulse 2s ease-in-out infinite; }

/* Stagger delays for card entrance */
.stagger-1 { animation-delay: 0ms; }
.stagger-2 { animation-delay: 50ms; }
.stagger-3 { animation-delay: 100ms; }
.stagger-4 { animation-delay: 150ms; }
.stagger-5 { animation-delay: 200ms; }
.stagger-6 { animation-delay: 250ms; }

/* Skeleton */
.skeleton {
  background: linear-gradient(
    90deg,
    var(--color-bg-subtle) 25%,
    var(--color-bg-hover) 50%,
    var(--color-bg-subtle) 75%
  );
  background-size: 200% 100%;
  animation: skeletonShimmer 1.5s ease-in-out infinite;
  border-radius: var(--radius-md);
}

/* Button press on active */
.btn:active,
.btn-primary:active,
.btn-secondary:active {
  animation: buttonPress 0.15s var(--ease-default);
}

/* Card entrance animation */
.card-enter {
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

/* Respect reduced motion */
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
```

- [ ] **Step 2: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/styles/animations.css
git commit -m "feat(panel): update animations with reduced-motion support and cleaner keyframes"
```

---

### Task 4: Update utilities.css (buttons, inputs, modal, empty-state)

**Files:**
- Modify: `panel/frontend/src/styles/utilities.css`

- [ ] **Step 1: Replace the entire file**

```css
/* Global utilities — used across pages. */

/* ----- Buttons — Capsule shape per design spec ----- */
.btn,
.btn-primary,
.btn-secondary,
.btn-danger,
.btn-ghost {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  padding: 10px 24px;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  font-family: inherit;
  text-decoration: none;
  white-space: nowrap;
  position: relative;
  overflow: hidden;
  line-height: 1.25;
}

.btn:disabled,
.btn-primary:disabled,
.btn-secondary:disabled,
.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.btn-primary {
  background: var(--color-primary);
  color: white;
  border-color: transparent;
}

.btn-primary:hover:not(:disabled) {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

.btn-primary:hover:not(:disabled):active {
  transform: translateY(0);
}

.btn-secondary {
  background: transparent;
  color: var(--color-text-secondary);
  border: 1.5px solid var(--color-border-default);
}

.btn-secondary:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.btn-danger {
  background: var(--color-danger);
  color: white;
  border-color: transparent;
}

.btn-danger:hover:not(:disabled) {
  background: #dc2626;
  transform: translateY(-1px);
}

.btn-ghost {
  background: transparent;
  color: var(--color-text-secondary);
  border: none;
  padding: 10px 16px;
}

.btn-ghost:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.btn-sm {
  padding: 6px 16px;
  font-size: var(--text-xs);
}

.btn-lg {
  padding: 14px 32px;
  font-size: var(--text-base);
}

.btn-icon {
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: var(--radius-full);
}

.btn-icon.btn-sm {
  width: 28px;
  height: 28px;
}

/* ----- Spinner ----- */
.spinner {
  width: 24px;
  height: 24px;
  border: 2.5px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.spinner--sm {
  width: 16px;
  height: 16px;
  border-width: 2px;
}

.spinner--lg {
  width: 32px;
  height: 32px;
  border-width: 3px;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* ----- Search wrapper ----- */
.search-wrapper {
  display: flex;
  align-items: center;
  position: relative;
}

.search-icon-btn {
  display: none;
}

.search-input {
  flex: 1;
  min-width: 0;
  padding: 10px 16px;
  padding-right: 2rem;
  border-radius: 10px;
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
  box-sizing: border-box;
}

.search-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.search-input::placeholder {
  color: var(--color-text-muted);
}

.clear-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border: none;
  background: var(--color-bg-hover);
  border-radius: 50%;
  color: var(--color-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  padding: 0;
  position: absolute;
  right: 10px;
  z-index: 2;
}

@media (max-width: 640px) {
  .search-wrapper {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    border: 1.5px solid var(--color-border-default);
    background: var(--color-bg-surface);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    position: relative;
  }
  .search-icon-btn { display: flex; color: var(--color-text-secondary); }
  .search-input {
    position: absolute;
    left: 0; top: 0;
    width: 200px; height: 36px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.2s, width 0.2s;
  }
  .search-wrapper:focus-within { width: 200px; }
  .search-wrapper:focus-within .search-input { opacity: 1; pointer-events: auto; border-color: var(--color-primary); }
  .search-wrapper:focus-within .clear-btn { opacity: 1; pointer-events: auto; }
  .clear-btn { opacity: 0; pointer-events: none; position: absolute; right: 8px; z-index: 2; transition: opacity 0.2s; }
  .btn-text { display: none; }
}

/* ----- Tag — capsule ----- */
.tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 12px;
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border: 1px solid var(--color-border-subtle);
  line-height: 1.4;
  white-space: nowrap;
  transition: all var(--duration-fast) var(--ease-default);
}

/* ----- Page headers (shared) ----- */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.page-header__left { flex: 1; min-width: 0; }

.page-header__right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.page-title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
  letter-spacing: -0.02em;
}

.page-subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

/* ----- Empty state — Enhanced ----- */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.empty-state__icon {
  color: var(--color-primary);
  opacity: 0.4;
  margin-bottom: 0.25rem;
}

.empty-state__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  margin: 0;
}

.empty-state__hint {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

/* ----- Card grid (shared) ----- */
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1.5rem;
}

@media (min-width: 1280px) {
  .card-grid { grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); }
}

/* ----- Modal overlay (shared) ----- */
.modal-overlay,
.modal-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
  z-index: var(--z-modal-backdrop);
  display: flex;
  align-items: center;
  justify-content: center;
  animation: fadeIn 0.15s var(--ease-default);
}

.modal {
  background: var(--color-bg-surface-raised);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-2xl);
  width: min(500px, 92vw);
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: scaleIn 0.2s var(--ease-default) both;
  z-index: var(--z-modal);
  position: relative;
}

.modal--lg { width: min(640px, 94vw); }
.modal--xl { width: min(800px, 95vw); }

.modal__header {
  padding: 1rem 1.5rem;
  font-weight: var(--font-semibold);
  font-size: var(--text-base);
  border-bottom: 1px solid var(--color-border-subtle);
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: var(--color-text-primary);
  flex-shrink: 0;
}

.modal__close {
  background: none;
  border: none;
  font-size: 1.125rem;
  cursor: pointer;
  color: var(--color-text-muted);
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  transition: all var(--duration-fast) var(--ease-default);
}

.modal__close:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.modal__body {
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}

.modal__footer {
  padding: 1rem 1.5rem;
  display: flex;
  justify-content: flex-end;
  gap: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
}

/* ----- Form elements (shared) ----- */
.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}

.form-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.input-base {
  width: 100%;
  padding: 10px 16px;
  border-radius: 10px;
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.input-base:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input-base::placeholder {
  color: var(--color-text-muted);
}

.input-base.input-error {
  border-color: var(--color-danger);
}

.input-base.input-error:focus {
  box-shadow: 0 0 0 3px var(--color-danger-50);
}

/* ----- Status dot (breathing) ----- */
.status-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.status-dot--online {
  background: var(--color-success);
  box-shadow: 0 0 6px var(--color-success-glow);
  animation: breathe 2s ease-in-out infinite;
}

.status-dot--offline {
  background: var(--color-text-muted);
}

.status-dot--failed {
  background: var(--color-danger);
  box-shadow: 0 0 6px var(--color-danger-glow);
}

.status-dot--pending {
  background: var(--color-warning);
  box-shadow: 0 0 6px var(--color-warning-glow);
  animation: breathe 2s ease-in-out infinite;
}
```

- [ ] **Step 2: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/styles/utilities.css
git commit -m "feat(panel): restyle utilities — capsule buttons, 10px inputs, 12px cards, cleaner modal"
```

---

### Task 5: Update ThemeContext.js with new theme IDs

**Files:**
- Modify: `panel/frontend/src/context/ThemeContext.js`

- [ ] **Step 1: Replace the file content**

```javascript
import { defineComponent, provide, inject, ref } from 'vue'

export const themes = [
  { id: 'sakura-day',   emoji: '🌸', label: '昼樱' },
  { id: 'sakura-night', emoji: '🌙', label: '夜樱' },
  { id: 'business',     emoji: '☀️', label: '晴空' },
]

const VALID_THEME_IDS = themes.map(t => t.id)
const ThemeContextKey = Symbol('ThemeContext')

export const ThemeProvider = defineComponent({
  name: 'ThemeProvider',
  setup(props, { slots }) {
    const savedTheme = localStorage.getItem('theme')
    // Migrate old theme IDs
    const migrated = savedTheme === 'sakura' ? 'sakura-day'
      : savedTheme === 'midnight' ? 'sakura-night'
      : savedTheme === 'cyberpunk' ? 'sakura-day'
      : savedTheme

    const initialTheme = (migrated && VALID_THEME_IDS.includes(migrated))
      ? migrated
      : (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'sakura-night' : 'sakura-day')

    const currentThemeId = ref(initialTheme)

    function setTheme(id) {
      if (!VALID_THEME_IDS.includes(id)) return
      currentThemeId.value = id
      document.documentElement.setAttribute('data-theme', id)
      localStorage.setItem('theme', id)
    }

    // Apply on init
    document.documentElement.setAttribute('data-theme', currentThemeId.value)

    provide(ThemeContextKey, { currentThemeId, setTheme, themes })

    return () => slots.default?.()
  }
})

export function useTheme() {
  const ctx = inject(ThemeContextKey)
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider')
  return ctx
}
```

- [ ] **Step 2: Update index.html anti-flicker script (read and patch)**

Read: `panel/frontend/index.html`

Find the anti-flicker script (inside `<head>`) and update the theme mapping:

```javascript
// Inside the <script> in <head> that reads localStorage theme:
// Replace the theme resolution logic with:
const themeMap = {
  'sakura': 'sakura-day',
  'midnight': 'sakura-night',
  'cyberpunk': 'sakura-day'
}
const saved = localStorage.getItem('theme')
const resolved = themeMap[saved] || saved
const valid = ['sakura-day', 'sakura-night', 'business']
const theme = valid.includes(resolved)
  ? resolved
  : (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'sakura-night' : 'sakura-day')
document.documentElement.setAttribute('data-theme', theme)
```

- [ ] **Step 3: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/context/ThemeContext.js panel/frontend/index.html
git commit -m "feat(panel): update theme context and HTML flicker-guard for new theme IDs"
```

---

### Task 6: Update BaseButton.vue (capsule shape, solid color)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseButton.vue`

- [ ] **Step 1: Replace the scoped style block**

Keep the `<template>` and `<script>` exactly as-is. Only replace `<style scoped>`:

```vue
<style scoped>
button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  padding: 10px 24px;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  font-family: inherit;
  text-decoration: none;
  white-space: nowrap;
  position: relative;
  overflow: hidden;
  line-height: 1.25;
  background: var(--color-primary);
  color: white;
}

button.secondary {
  background: transparent;
  color: var(--color-text-secondary);
  border: 1.5px solid var(--color-border-default);
}

button.secondary:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

button.danger {
  background: var(--color-danger);
}

button.danger:hover:not(:disabled) {
  background: #dc2626;
}

button.success {
  background: var(--color-success);
}

button.success:hover:not(:disabled) {
  background: #059669;
}

button:hover:not(:disabled) {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

button:hover:not(:disabled):active {
  transform: translateY(0);
}

button.is-loading {
  color: transparent !important;
  pointer-events: none;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.spinner-mini {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 18px;
  height: 18px;
  border: 2.5px solid rgba(255, 255, 255, 0.3);
  border-top-color: currentColor;
  border-radius: 50%;
  animation: button-spin 0.8s linear infinite;
}

button.secondary .spinner-mini {
  border-top-color: var(--color-primary);
  border-color: rgba(0, 0, 0, 0.08);
}

@keyframes button-spin {
  to { transform: translate(-50%, -50%) rotate(360deg); }
}
</style>
```

- [ ] **Step 2: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/base/BaseButton.vue
git commit -m "feat(panel): restyle BaseButton with capsule shape and solid colors"
```

---

### Task 7: Update BaseCard.vue (12px radius, solid bg)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseCard.vue`

- [ ] **Step 1: Replace scoped styles only**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 20px;
  box-shadow: var(--shadow-xs);
  transition: box-shadow var(--duration-normal) var(--ease-default),
              transform var(--duration-normal) var(--ease-default);
  position: relative;
}

.card-hover:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.card-glass {
  background: var(--color-bg-surface);
  border-color: var(--color-border-default);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-4);
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
}

.card-title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
  letter-spacing: -0.01em;
}

.card-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.card-body {
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.card-footer {
  margin-top: var(--space-4);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/BaseCard.vue
git commit -m "feat(panel): restyle BaseCard with 12px radius and solid background"
```

---

### Task 8: Update BaseInput.vue (10px radius, focus ring)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseInput.vue`

- [ ] **Step 1: Add scoped styles**

The current file has no `<style scoped>`. Add it at the bottom, keeping template and script exactly as-is:

```vue
<style scoped>
input {
  width: 100%;
  padding: 10px 16px;
  border-radius: 10px;
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

input::placeholder {
  color: var(--color-text-muted);
}

input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/BaseInput.vue
git commit -m "feat(panel): add BaseInput styles with 10px radius and focus ring"
```

---

### Task 9: Update BaseBadge.vue (tweaked sizes)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseBadge.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.base-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  line-height: 1;
  font-weight: var(--font-semibold);
  white-space: nowrap;
  flex-shrink: 0;
  transition: all var(--duration-fast) var(--ease-default);
}

.base-badge__dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.base-badge--success {
  background: var(--color-success-50);
  color: var(--color-success);
}

.base-badge--warning {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.base-badge--danger {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.base-badge--primary {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.base-badge--neutral {
  background: var(--color-bg-subtle);
}

.base-badge--neutral.base-badge--muted {
  color: var(--color-text-muted);
}

.base-badge--neutral.base-badge--secondary {
  color: var(--color-text-secondary);
}

.base-badge--pill {
  border-radius: var(--radius-full);
}

.base-badge--square {
  border-radius: var(--radius-sm);
}

.base-badge--sm {
  font-size: var(--text-xs);
  padding: 4px 12px;
}

.base-badge--md {
  font-size: var(--text-sm);
  padding: 4px 12px;
}

.base-badge--mono {
  font-family: var(--font-mono);
  font-weight: var(--font-bold);
  letter-spacing: 0.02em;
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/BaseBadge.vue
git commit -m "feat(panel): tweak BaseBadge sizing to match design spec"
```

---

### Task 10: Update BaseModal.vue (scale animation, button classes)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseModal.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity var(--duration-normal) var(--ease-default);
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-active .modal,
.modal-leave-active .modal {
  transition: transform var(--duration-slow) var(--ease-default),
              opacity var(--duration-slow) var(--ease-default);
}

.modal-enter-from .modal,
.modal-leave-to .modal {
  transform: scale(0.98);
  opacity: 0;
}

.modal__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.modal__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: var(--space-1) 0 0;
}

.modal__close:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
  transform: rotate(90deg);
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  padding: 10px 24px;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  font-family: inherit;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--primary:hover {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

.btn--secondary {
  background: transparent;
  color: var(--color-text-secondary);
  border: 1.5px solid var(--color-border-default);
}

.btn--secondary:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

@media (max-width: 640px) {
  .modal-backdrop {
    padding: var(--space-4);
    align-items: flex-end;
  }

  .modal {
    max-height: calc(100vh - var(--space-8));
    border-radius: var(--radius-2xl) var(--radius-2xl) 0 0;
  }

  .modal-enter-active .modal,
  .modal-leave-active .modal {
    transition: transform var(--duration-slow) var(--ease-default);
  }

  .modal-enter-from .modal,
  .modal-leave-to .modal {
    transform: translateY(100%);
  }
}

@media (max-width: 375px) and (max-height: 812px) {
  .modal-backdrop {
    padding: 0;
    align-items: flex-end;
  }

  .modal {
    width: 100%;
    height: 100%;
    max-height: 100vh;
    border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/BaseModal.vue
git commit -m "feat(panel): restyle BaseModal with scale entrance and capsule footer buttons"
```

---

### Task 11: Update EmptyState.vue (SVG icon, theme-aware)

**Files:**
- Modify: `panel/frontend/src/components/base/EmptyState.vue`

- [ ] **Step 1: Replace entire file**

```vue
<template>
  <div class="empty-state">
    <div class="empty-state__icon">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <slot name="icon">
          <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/>
          <line x1="9" y1="9" x2="15" y2="15"/>
          <line x1="15" y1="9" x2="9" y2="15"/>
        </slot>
      </svg>
    </div>
    <h3 class="empty-state__title">{{ title }}</h3>
    <p class="empty-state__description">{{ description }}</p>
    <div v-if="$slots.action" class="empty-state__action">
      <slot name="action" />
    </div>
  </div>
</template>

<script setup>
defineProps({
  title: {
    type: String,
    default: '暂无数据'
  },
  description: {
    type: String,
    default: '还没有任何内容，快来添加第一条吧！'
  }
})
</script>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.empty-state__icon {
  color: var(--color-primary);
  opacity: 0.35;
  margin-bottom: 0.25rem;
}

.empty-state__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
  margin: 0;
}

.empty-state__description {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  max-width: 400px;
  line-height: 1.6;
}

.empty-state__action {
  margin-top: var(--space-4);
}

@media (max-width: 640px) {
  .empty-state {
    padding: 3rem 1rem;
  }
  .empty-state__title {
    font-size: var(--text-sm);
  }
  .empty-state__description {
    font-size: var(--text-xs);
  }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/EmptyState.vue
git commit -m "feat(panel): restyle EmptyState with SVG icon slot and theme-aware colors"
```

---

### Task 12: Update StatCard.vue (solid bg, 12px radius)

**Files:**
- Modify: `panel/frontend/src/components/base/StatCard.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.stat-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
  box-shadow: var(--shadow-xs);
  transition: box-shadow var(--duration-normal) var(--ease-default),
              transform var(--duration-normal) var(--ease-default);
  position: relative;
  overflow: hidden;
}

.stat-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.stat-card__icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  margin-bottom: var(--space-3);
}

.stat-card--primary .stat-card__icon {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-card--success .stat-card__icon {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-card--warning .stat-card__icon {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.stat-card--danger .stat-card__icon {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.stat-card__value {
  font-size: 1.75rem;
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.stat-card__label {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: var(--font-medium);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/StatCard.vue
git commit -m "feat(panel): restyle StatCard with solid background and 12px radius"
```

---

### Task 13: Update ThemeSelector.vue

**Files:**
- Modify: `panel/frontend/src/components/base/ThemeSelector.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.theme-selector {
  position: relative;
}

.theme-trigger {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  transition: all var(--duration-fast) var(--ease-default);
  cursor: pointer;
  font-family: inherit;
  color: var(--color-text-primary);
}

.theme-trigger:hover {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-xs);
}

.theme-trigger--open {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.theme-trigger__emoji {
  font-size: 16px;
  line-height: 1;
}

.theme-trigger__arrow {
  color: var(--color-text-tertiary);
  transition: transform var(--duration-fast) var(--ease-default);
}

.theme-trigger__arrow--up {
  transform: rotate(180deg);
}

/* Dropdown */
.theme-dropdown {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  min-width: 180px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  padding: var(--space-2);
  z-index: var(--z-dropdown);
}

.theme-dropdown__header {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  padding: var(--space-2) var(--space-3);
}

.theme-option {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1.5px solid transparent;
  background: transparent;
  font-family: inherit;
  color: var(--color-text-primary);
  text-align: left;
}

.theme-option:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-default);
}

.theme-option--active {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
}

.theme-option__emoji {
  font-size: 16px;
  line-height: 1;
  flex-shrink: 0;
}

.theme-option__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  flex: 1;
}

.theme-option__check {
  color: var(--color-primary);
  flex-shrink: 0;
}

/* Dropdown Animation */
.dropdown-enter-active,
.dropdown-leave-active {
  transition: opacity var(--duration-normal) var(--ease-default),
              transform var(--duration-normal) var(--ease-default);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(-8px) scale(0.98);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/ThemeSelector.vue
git commit -m "feat(panel): restyle ThemeSelector with capsule trigger and cleaner dropdown"
```

---

### Task 14: Update BaseListCard.vue (12px radius, hover shadow)

**Files:**
- Modify: `panel/frontend/src/components/base/BaseListCard.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.base-list-card {
  position: relative;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 1rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  overflow: hidden;
  transition: border-color var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-normal) var(--ease-default);
}

.base-list-card--clickable {
  cursor: pointer;
}

.base-list-card--clickable:hover {
  border-color: var(--color-border-strong);
  transform: translateY(-2px);
  box-shadow: var(--shadow-md);
}

.base-list-card--clickable:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.base-list-card--clickable:active {
  transform: translateY(0);
}

.base-list-card--disabled {
  opacity: 0.6;
}

.base-list-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  min-height: 28px;
}

.base-list-card__header-left {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
  min-width: 0;
}

.base-list-card__header-right {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-shrink: 0;
}

.base-list-card__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.3;
  word-break: break-all;
}

.base-list-card__body {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  min-width: 0;
}

.base-list-card__footer {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/base/BaseListCard.vue
git commit -m "feat(panel): restyle BaseListCard with 12px radius and subtler hover"
```

---

### Task 15: Update AppShell.vue

**Files:**
- Modify: `panel/frontend/src/components/layout/AppShell.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.app-shell {
  height: 100dvh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--color-bg-canvas);
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

.sidebar-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.3);
  z-index: calc(var(--z-fixed) - 1);
}

@media (max-width: 1023px) {
  .content {
    padding: 1rem;
    padding-bottom: 5rem;
  }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/layout/AppShell.vue
git commit -m "feat(panel): update AppShell with solid canvas background"
```

---

### Task 16: Update TopBar.vue (56px height, solid bg)

**Files:**
- Modify: `panel/frontend/src/components/layout/TopBar.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.topbar {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 1.25rem;
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-default);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
  box-shadow: var(--shadow-xs);
}

.topbar__left { display: flex; align-items: center; gap: 1rem; }

.topbar__brand { display: flex; align-items: center; gap: 0.75rem; }

.topbar__logo {
  width: 32px;
  height: 32px;
  background: var(--color-primary);
  border-radius: var(--radius-md);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-default);
}

.topbar__logo:hover {
  transform: scale(1.05);
}

.topbar__title { display: flex; align-items: center; gap: 0.5rem; }

.topbar__name {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

.topbar__badge {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: 2px 8px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-full);
}

.topbar__actions { display: flex; align-items: center; gap: 0.5rem; }

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: none;
  background: transparent;
}

.topbar__action:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__action--logout:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* Agent Switcher */
.agent-switcher { position: relative; }

.agent-switcher__trigger {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-size: var(--text-xs);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  max-width: 160px;
}

.agent-switcher__trigger:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-surface);
}

.agent-switcher__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.agent-switcher__dot--online { background: var(--color-success); box-shadow: 0 0 4px var(--color-success-glow); }
.agent-switcher__dot--offline { background: var(--color-text-muted); }
.agent-switcher__dot--failed { background: var(--color-danger); box-shadow: 0 0 4px var(--color-danger-glow); }
.agent-switcher__dot--pending { background: var(--color-warning); box-shadow: 0 0 4px var(--color-warning-glow); }

.agent-switcher__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-switcher__dropdown {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  width: 280px;
  background: var(--color-bg-surface-raised);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  overflow: visible;
  animation: scaleIn 0.15s var(--ease-default) both;
}

.agent-switcher__search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.agent-switcher__search-input {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: 10px;
  background: var(--color-bg-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.agent-switcher__search-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.agent-switcher__list {
  max-height: 280px;
  overflow-y: auto;
  padding: 0.25rem;
  scrollbar-width: thin;
}

.agent-switcher__list::-webkit-scrollbar { width: 6px; }
.agent-switcher__list::-webkit-scrollbar-track { background: transparent; }
.agent-switcher__list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: 3px; }
.agent-switcher__list::-webkit-scrollbar-thumb:hover { background: var(--color-text-muted); }

.agent-switcher__item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.625rem;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
  font-family: inherit;
  text-align: left;
}

.agent-switcher__item:hover { background: var(--color-bg-hover); }
.agent-switcher__item.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.agent-switcher__item-name {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-switcher__item-time {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.agent-switcher__filters {
  display: flex;
  gap: 0.25rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  overflow-x: auto;
}

.agent-switcher__filter-btn {
  padding: 0.25rem 0.625rem;
  border: none;
  border-radius: var(--radius-full);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
  transition: all var(--duration-fast) var(--ease-default);
}

.agent-switcher__filter-btn.active {
  background: var(--color-primary);
  color: white;
}

.agent-switcher__sort {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-top: 1px solid var(--color-border-subtle);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.agent-switcher__sort-btn {
  padding: 0.125rem 0.375rem;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  cursor: pointer;
  font-family: inherit;
  transition: all var(--duration-fast) var(--ease-default);
}

.agent-switcher__sort-btn.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

.agent-switcher__empty {
  padding: 1rem;
  text-align: center;
  font-size: var(--text-sm);
  color: var(--color-text-muted);
}

@media (max-width: 768px) {
  .agent-switcher__trigger { max-width: 120px; }
}

@media (max-width: 640px) {
  .topbar__title { display: none; }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/layout/TopBar.vue
git commit -m "feat(panel): restyle TopBar to 56px height with solid background"
```

---

### Task 17: Update Sidebar.vue (220px width, new active state)

**Files:**
- Modify: `panel/frontend/src/components/layout/Sidebar.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.sidebar {
  width: 220px;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-default);
  flex-shrink: 0;
  overflow: hidden;
  transition: width var(--duration-normal) var(--ease-default);
}

.sidebar--collapsed {
  width: 64px;
}

.sidebar__header {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 1rem 1rem 0.75rem;
  border-bottom: 1px solid var(--color-border-subtle);
  min-height: 56px;
  box-sizing: border-box;
}

.sidebar__brand {
  font-size: var(--text-sm);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  letter-spacing: -0.01em;
}

.sidebar__collapse-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.sidebar--collapsed .sidebar__collapse-btn {
  margin: 0 auto;
}

.sidebar__collapse-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__collapse-btn svg {
  transition: transform 0.2s var(--ease-default);
}

.sidebar__collapse-btn svg.rotate-180 {
  transform: rotate(180deg);
}

.sidebar__nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 0.5rem 0.625rem;
}

.sidebar__nav-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 10px 16px;
  border-radius: 10px;
  color: var(--color-text-secondary);
  text-decoration: none;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  transition: all var(--duration-fast) var(--ease-default);
  position: relative;
}

.sidebar__nav-item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-item--active,
.sidebar__nav-item.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

.sidebar__nav--collapsed {
  flex-direction: row;
  justify-content: center;
  padding: 0.5rem;
  gap: 4px;
  flex-wrap: wrap;
}

.sidebar__nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  color: var(--color-text-secondary);
  text-decoration: none;
  transition: all var(--duration-fast) var(--ease-default);
}

.sidebar__nav-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-icon--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/layout/Sidebar.vue
git commit -m "feat(panel): restyle Sidebar to 220px with solid active states"
```

---

### Task 18: Update BottomNav.vue

**Files:**
- Modify: `panel/frontend/src/components/layout/BottomNav.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.bottom-nav {
  display: none;
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  height: 64px;
  background: var(--color-bg-surface);
  border-top: 1px solid var(--color-border-default);
  z-index: var(--z-sticky);
  padding-bottom: env(safe-area-inset-bottom, 0);
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
  gap: 4px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 11px;
  font-weight: var(--font-medium);
  transition: all var(--duration-fast);
  padding: 0.5rem 0.25rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  position: relative;
}

.nav-item.active {
  color: var(--color-primary);
}

.nav-item.active::before {
  content: '';
  position: absolute;
  top: 4px;
  left: 50%;
  transform: translateX(-50%);
  width: 20px;
  height: 3px;
  background: var(--color-primary);
  border-radius: 2px;
}

.nav-icon {
  width: 24px;
  height: 24px;
  transition: transform var(--duration-fast);
}

.nav-item.active .nav-icon {
  transform: translateY(-2px);
}

/* More Dropdown */
.nav-item--dropdown {
  position: relative;
}

.more-dropdown {
  position: absolute;
  bottom: calc(100% + 12px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  min-width: 160px;
  overflow: hidden;
  z-index: var(--z-dropdown);
  padding: 0.5rem;
}

.more-dropdown__item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.625rem 0.875rem;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  text-decoration: none;
  text-align: left;
  transition: all var(--duration-fast);
  white-space: nowrap;
  border-radius: var(--radius-md);
  font-weight: var(--font-medium);
}

.more-dropdown__item:hover {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.more-dropdown__item svg {
  flex-shrink: 0;
  color: var(--color-text-secondary);
}

.more-dropdown__item:hover svg {
  color: var(--color-primary);
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/components/layout/BottomNav.vue
git commit -m "feat(panel): restyle BottomNav with solid background and cleaner dropdown"
```

---

### Task 19: Update LoginPage.vue

**Files:**
- Modify: `panel/frontend/src/pages/LoginPage.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.login-page {
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-bg-canvas);
  padding: var(--space-4);
}

.login-card {
  width: 100%;
  max-width: 360px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-8);
  box-shadow: var(--shadow-lg);
}

.login-card__header {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
  color: var(--color-primary);
}

.login-card__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.login-card__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-muted);
  margin: 0;
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-group {
  display: flex;
  flex-direction: column;
}

.input {
  width: 100%;
  padding: 10px 16px;
  border: 1.5px solid var(--color-border-default);
  border-radius: 10px;
  background: var(--color-bg-surface);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast),
              box-shadow var(--duration-fast);
}

.input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.input::placeholder {
  color: var(--color-text-muted);
}

.input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}

.login-error {
  font-size: var(--text-sm);
  color: var(--color-danger);
  background: var(--color-danger-50);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  margin: 0;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: 10px 24px;
  border: none;
  border-radius: var(--radius-full);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: all var(--duration-fast);
  font-family: inherit;
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--primary:hover:not(:disabled) {
  background: var(--color-primary-hover);
  transform: translateY(-1px);
}

.btn--full {
  width: 100%;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.spinner {
  width: 20px;
  height: 20px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.spinner--sm {
  width: 16px;
  height: 16px;
  border-width: 1.5px;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/pages/LoginPage.vue
git commit -m "feat(panel): restyle LoginPage with capsule button and 10px inputs"
```

---

### Task 20: Update DashboardPage.vue

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.dashboard {
  max-width: 1280px;
  margin: 0 auto;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.dashboard__header {
  margin-bottom: 2rem;
}

.dashboard__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
  letter-spacing: -0.02em;
}

.dashboard__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 1.5rem;
  margin-bottom: 2rem;
}

.dashboard__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3rem;
  color: var(--color-text-secondary);
}

.dashboard__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.dashboard__empty p {
  margin: 0;
  font-size: var(--text-base);
}

.dashboard__empty-hint {
  font-size: var(--text-sm) !important;
  color: var(--color-text-tertiary) !important;
}

.dashboard-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  overflow: hidden;
  margin-bottom: 2rem;
  box-shadow: var(--shadow-xs);
}

.dashboard-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.dashboard-section__title {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.dashboard-section__link {
  font-size: var(--text-xs);
  color: var(--color-primary);
  text-decoration: none;
  font-weight: var(--font-medium);
  transition: color var(--duration-fast) var(--ease-default);
}

.dashboard-section__link:hover {
  color: var(--color-primary-hover);
  text-decoration: underline;
}

@media (max-width: 1024px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
    gap: 0.75rem;
  }
  .dashboard__title {
    font-size: var(--text-lg);
  }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/pages/DashboardPage.vue
git commit -m "feat(panel): restyle DashboardPage with 4-col KPI grid and 1280px max-width"
```

---

### Task 21: Update RulesPage.vue

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.rules-page {
  max-width: 1280px;
  margin: 0 auto;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.rules-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.rules-page__header-left { flex: 1; min-width: 0; }

.rules-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
}

.rules-page__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.rules-page__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.rules-page__prompt,
.rules-page__empty,
.rules-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

.rules-page__prompt-hint {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

@media (max-width: 640px) {
  .rules-page__header { gap: 0.5rem; }
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 1.5rem;
}

@media (max-width: 1024px) {
  .rule-grid {
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  }
}
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/pages/RulesPage.vue
git commit -m "feat(panel): restyle RulesPage with 360px card grid and 1280px max-width"
```

---

### Task 22: Update CertsPage.vue

**Files:**
- Modify: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Replace scoped styles**

Keep template and script. Replace `<style scoped>`:

```vue
<style scoped>
.certs-page { max-width: 1280px; margin: 0 auto; }
.certs-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.certs-page__header-left { flex: 1; min-width: 0; }
.certs-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.certs-page__title { font-size: var(--text-xl); font-weight: var(--font-bold); margin: 0 0 0.25rem; color: var(--color-text-primary); }
.certs-page__subtitle { font-size: var(--text-sm); color: var(--color-text-tertiary); margin: 0; }
.certs-page__loading, .certs-page__empty, .certs-page__prompt { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.cert-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1.5rem; }
</style>
```

- [ ] **Step 2: Build check + commit**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

```bash
git add panel/frontend/src/pages/CertsPage.vue
git commit -m "feat(panel): restyle CertsPage with 1280px max-width and cleaner grid"
```

---

### Task 23: Update remaining page files (bulk style update)

**Files to modify (scoped styles only, keep template/script):**
- `panel/frontend/src/pages/L4RulesPage.vue`
- `panel/frontend/src/pages/RelayListenersPage.vue`
- `panel/frontend/src/pages/AgentsPage.vue`
- `panel/frontend/src/pages/AgentDetailPage.vue`
- `panel/frontend/src/pages/SettingsPage.vue`
- `panel/frontend/src/pages/VersionsPage.vue`
- `panel/frontend/src/pages/RuleDetailPage.vue`

For each page, apply these consistent changes in scoped styles:
1. `.xxx-page { max-width: 1280px; margin: 0 auto; }`
2. Title font-size: `var(--text-xl)`
3. Subtitle font-size: `var(--text-sm)`
4. Any `.btn` or `.btn-primary` inside scoped styles should use capsule shape if it overrides global utilities
5. Card/grid gaps: `1.5rem`
6. Border-radius on cards: `var(--radius-lg)`
7. Remove any gradient references in scoped styles

- [ ] **Step 1: Apply updates to L4RulesPage.vue**

Replace the `<style scoped>` block. Set `max-width: 1280px`, title `var(--text-xl)`, grid gap `1.5rem`.

- [ ] **Step 2: Apply updates to RelayListenersPage.vue**

Same pattern.

- [ ] **Step 3: Apply updates to AgentsPage.vue**

Same pattern.

- [ ] **Step 4: Apply updates to AgentDetailPage.vue**

Same pattern.

- [ ] **Step 5: Apply updates to SettingsPage.vue**

Same pattern.

- [ ] **Step 6: Apply updates to VersionsPage.vue**

Same pattern.

- [ ] **Step 7: Apply updates to RuleDetailPage.vue**

Same pattern.

- [ ] **Step 8: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 9: Commit**

```bash
git add panel/frontend/src/pages/
git commit -m "feat(panel): restyle all remaining pages with 1280px max-width and consistent tokens"
```

---

### Task 24: Update card components (RuleCard, CertCard, RelayCard, AgentCard)

**Files:**
- Modify: `panel/frontend/src/components/rules/RuleCard.vue`
- Modify: `panel/frontend/src/components/certs/CertCard.vue`
- Modify: `panel/frontend/src/components/relay/RelayCard.vue`
- Modify: `panel/frontend/src/components/AgentCard.vue`

For each, the main change is that parent `BaseListCard` now has the new styling. Scoped styles in these child components mostly just need to ensure they use the right font sizes and colors. Read each file and update any hardcoded sizes to CSS vars.

Key changes for all:
- Any `font-size: 0.875rem` → `var(--text-sm)`
- Any `font-size: 0.8125rem` → `var(--text-xs)`
- Any `font-size: 0.75rem` → `var(--text-xs)`
- Any `border-radius` on inner elements: use `var(--radius-md)` or `var(--radius-sm)`
- Ensure no gradient references remain in scoped styles

- [ ] **Step 1: Update RuleCard.vue scoped styles**

Replace the `<style scoped>` with the same content but using CSS vars for font sizes:

```vue
<style scoped>
.rule-card__mapping {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}
.rule-card__url-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}
.rule-card__url-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}
.rule-card__url,
.rule-card__backend {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}
</style>
```

- [ ] **Step 2-5: Update CertCard.vue, RelayCard.vue, AgentCard.vue**

Apply the same CSS-variable replacement pattern to scoped styles in each file.

- [ ] **Step 6: Build check**

Run: `cd panel/frontend && npm run build 2>&1 | head -40`
Expected: No errors.

- [ ] **Step 7: Commit**

```bash
git add panel/frontend/src/components/rules/RuleCard.vue panel/frontend/src/components/certs/CertCard.vue panel/frontend/src/components/relay/RelayCard.vue panel/frontend/src/components/AgentCard.vue
git commit -m "feat(panel): update card components with CSS variable font sizes"
```

---

### Task 25: Final build verification and cleanup

**Files:**
- Delete: `panel/frontend/src/style.css` (already orphaned, confirm it's not imported)

- [ ] **Step 1: Verify style.css is orphaned**

Run: `grep -r "from './style.css'\|from '../style.css'\|from '@/style.css'\|import './style.css'" panel/frontend/src/`
Expected: No matches (the file is not imported by anything).

- [ ] **Step 2: Delete orphaned file**

```bash
rm panel/frontend/src/style.css
```

- [ ] **Step 3: Full production build**

Run: `cd panel/frontend && npm run build`
Expected: Build completes with no errors.

- [ ] **Step 4: Run tests**

Run: `cd panel/frontend && npm test 2>&1 | tail -30`
Expected: All existing tests pass (no test logic was changed, only styles).

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/style.css
git commit -m "chore(panel): remove orphaned style.css"
```

---

## Self-Review Checklist

### 1. Spec coverage

| Spec Section | Task(s) |
|-------------|---------|
| 3 themes (sakura-day, sakura-night, business) | Task 1, 5 |
| No gradient backgrounds | Task 1 (solid --color-bg-canvas), Task 2 (body bg) |
| Button capsule shape (9999px) | Task 4 (utilities.css), Task 6 (BaseButton) |
| Card 12px radius | Task 4, Task 7, Task 12, Task 14 |
| Input 10px radius + focus ring | Task 4, Task 8 |
| Tab underline style | Not applicable (no tabs in current pages; SettingsPage uses its own nav) |
| Badge capsule, subtle bg | Task 9 |
| Sidebar 220px, nav item 10px radius | Task 17 |
| TopBar 56px | Task 16 |
| Content max-width 1280px | Task 2, Tasks 19-23 |
| 8px spacing system | Task 1 (token values), applied throughout |
| F-pattern Dashboard | Task 20 (4-col grid) |
| Skeleton screens | Task 3 (skeleton class) |
| 200-300ms micro-interactions | Task 3 (duration tokens), applied throughout |
| prefers-reduced-motion | Task 3 (@media reduce) |
| No glassmorphism | Task 16 (removed backdrop-filter from topbar), Task 18 (removed from bottom-nav) |
| Login centered card | Task 19 |
| All 10 pages restyled | Tasks 19-23 |

### 2. Placeholder scan
- No "TBD", "TODO", "implement later", "fill in details" found.
- No vague "add appropriate error handling" steps.
- Every step shows exact code or exact commands.
- No "Similar to Task N" references.

### 3. Type consistency
- Theme IDs consistently: `sakura-day`, `sakura-night`, `business`
- CSS var names consistent across all tasks
- `--radius-lg` = 0.75rem = 12px used for cards
- `--radius-full` = 9999px used for buttons
- `10px` literal used for inputs (design spec requirement, not a token)
- `--header-height: 56px` and `--sidebar-width: 220px` defined in Task 1, consumed in Tasks 16-17

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-05-frontend-ui-ux-overhaul.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
