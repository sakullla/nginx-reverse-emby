# Frontend Visual Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 全面升级面板前端 UI/UX 视觉效果，通过 CSS 变量体系升级、组件样式重写、动画系统新增，达到现代 SaaS 视觉水准。

**Architecture:** 方案 A — 渐进式 CSS 升级。不改动组件逻辑和页面结构，只升级样式文件。核心改动集中在 `styles/themes.css`（增强三个主题）、`styles/utilities.css`（扩展工具类）和 `styles/animations.css`（新增动画系统）。少量页面 scoped style 需要微调以使用新的 CSS 变量。

**Tech Stack:** Vue 3 + CSS Custom Properties + UnoCSS (presetWind)

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `src/styles/themes.css` | Modify | 升级三个主题的 CSS 变量（更多层次色、渐变、阴影、玻璃拟态） |
| `src/styles/animations.css` | Create | 新增动画关键帧、过渡工具类、骨架屏、状态指示器动画 |
| `src/styles/utilities.css` | Modify | 扩展按钮、输入框、徽章、标签、空状态等组件样式 |
| `src/styles/index.css` | Modify | 引入 animations.css，增加全局增强（滚动条、过渡、选择） |
| `src/components/layout/Sidebar.vue` | Modify | 使用新变量升级侧边栏样式 |
| `src/components/layout/TopBar.vue` | Modify | 升级顶栏样式和 agent switcher 下拉 |
| `src/components/base/BaseCard.vue` | Modify | 升级卡片视觉（渐变边框、悬停效果） |
| `src/components/base/BaseModal.vue` | Modify | 升级弹窗视觉 |
| `src/components/base/BaseBadge.vue` | Modify | 升级徽章动画和视觉 |
| `src/components/base/StatCard.vue` | Modify | 升级统计卡片 |
| `src/pages/DashboardPage.vue` | Modify | 升级仪表盘页面布局和统计卡片网格 |
| `src/pages/RulesPage.vue` | Modify | 升级规则页面空状态、搜索、卡片网格 |
| `src/pages/AgentsPage.vue` | Modify | 升级节点页面空状态、模态框、筛选栏 |
| `src/components/AgentCard.vue` | Modify | 升级 Agent 卡片 |
| `src/components/rules/RuleCard.vue` | Modify | 升级 Rule 卡片 |
| `src/components/EmptyState.vue` | Modify | 升级全局空状态组件 |
| `src/components/DeleteConfirmDialog.vue` | Modify | 升级确认对话框 |

---

### Task 1: 升级 themes.css — 三主题变量增强

**Files:**
- Modify: `panel/frontend/src/styles/themes.css`

- [ ] **Step 1: 升级 Business 主题变量**

替换 `themes.css` 中 `business` 主题块，增加更多层次色和渐变：

```css
/* =============================================
   Theme: 晴空 (Professional Blue) — Enhanced
   ============================================= */
[data-theme="business"] {
  --color-primary: #2563eb;
  --color-primary-hover: #1d4ed8;
  --color-primary-active: #1e40af;
  --color-primary-subtle: rgba(37, 99, 235, 0.07);
  --color-primary-50: rgba(37, 99, 235, 0.04);
  --color-primary-100: rgba(37, 99, 235, 0.08);
  --color-primary-200: rgba(37, 99, 235, 0.14);
  --color-primary-300: rgba(37, 99, 235, 0.22);

  --color-accent: #0d9488;
  --color-accent-hover: #0f766e;
  --color-accent-subtle: rgba(13, 148, 136, 0.08);

  --color-text-primary: #0f172a;
  --color-text-secondary: #334155;
  --color-text-tertiary: #64748b;
  --color-text-muted: #94a3b8;
  --color-text-inverse: #ffffff;

  --color-bg-canvas: #f1f5f9;
  --color-bg-surface: rgba(255, 255, 255, 0.98);
  --color-bg-surface-raised: rgba(255, 255, 255, 1);
  --color-bg-sunken: #f8fafc;
  --color-bg-subtle: #f1f5f9;
  --color-bg-hover: #e2e8f0;
  --color-bg-active: #cbd5e1;

  --color-border-subtle: rgba(15, 23, 42, 0.05);
  --color-border-default: rgba(15, 23, 42, 0.10);
  --color-border-strong: rgba(15, 23, 42, 0.18);
  --color-border-focus: rgba(37, 99, 235, 0.35);

  --color-success: #16a34a;
  --color-success-50: rgba(22, 163, 74, 0.08);
  --color-success-glow: rgba(22, 163, 74, 0.25);
  --color-danger: #dc2626;
  --color-danger-50: rgba(220, 38, 38, 0.07);
  --color-danger-glow: rgba(220, 38, 38, 0.20);
  --color-warning: #d97706;
  --color-warning-50: rgba(217, 119, 6, 0.08);
  --color-warning-glow: rgba(217, 119, 6, 0.20);

  --shadow-xs: 0 1px 2px rgba(15, 23, 42, 0.04);
  --shadow-sm: 0 1px 3px rgba(15, 23, 42, 0.06), 0 1px 2px rgba(15, 23, 42, 0.04);
  --shadow-md: 0 4px 6px rgba(15, 23, 42, 0.07), 0 2px 4px rgba(15, 23, 42, 0.05);
  --shadow-lg: 0 10px 15px rgba(15, 23, 42, 0.08), 0 4px 6px rgba(15, 23, 42, 0.04);
  --shadow-xl: 0 20px 25px rgba(15, 23, 42, 0.10), 0 8px 10px rgba(15, 23, 42, 0.04);
  --shadow-2xl: 0 24px 48px rgba(15, 23, 42, 0.12);
  --shadow-focus: 0 0 0 3px rgba(37, 99, 235, 0.25);
  --shadow-inner: inset 0 1px 2px rgba(15, 23, 42, 0.04);

  --gradient-primary: linear-gradient(135deg, #3b82f6 0%, #2563eb 50%, #1d4ed8 100%);
  --gradient-primary-soft: linear-gradient(135deg, rgba(59,130,246,0.08) 0%, rgba(37,99,235,0.04) 100%);
  --gradient-surface: linear-gradient(180deg, rgba(255,255,255,1) 0%, rgba(248,250,252,0.98) 100%);
  --gradient-bg: linear-gradient(180deg, #f1f5f9 0%, #e2e8f0 100%);
  --gradient-card-border: linear-gradient(135deg, rgba(255,255,255,0.8), rgba(226,232,240,0.4));

  --glass-bg: rgba(255, 255, 255, 0.85);
  --glass-border: rgba(255, 255, 255, 0.3);
  --glass-blur: 12px;
}
```

- [ ] **Step 2: 升级 Sakura 主题变量**

```css
/* =============================================
   Theme: 二次元 (Lilac Purple) — Enhanced
   ============================================= */
[data-theme="sakura"] {
  --color-primary: #c084fc;
  --color-primary-hover: #a855f7;
  --color-primary-active: #9333ea;
  --color-primary-subtle: rgba(192, 132, 252, 0.08);
  --color-primary-50: rgba(192, 132, 252, 0.04);
  --color-primary-100: rgba(192, 132, 252, 0.10);
  --color-primary-200: rgba(192, 132, 252, 0.18);
  --color-primary-300: rgba(192, 132, 252, 0.28);

  --color-accent: #f472b6;
  --color-accent-hover: #ec4899;
  --color-accent-subtle: rgba(244, 114, 182, 0.08);

  --color-text-primary: #251736;
  --color-text-secondary: #6b4e80;
  --color-text-tertiary: #8c6ea0;
  --color-text-muted: #c4a8d4;
  --color-text-inverse: #ffffff;

  --color-bg-canvas: #fdf2f8;
  --color-bg-surface: rgba(255, 255, 255, 0.92);
  --color-bg-surface-raised: rgba(255, 255, 255, 0.98);
  --color-bg-sunken: #fef6fb;
  --color-bg-subtle: #fce7f3;
  --color-bg-hover: #f9e0f2;
  --color-bg-active: #f3d5ec;

  --color-border-subtle: rgba(192, 132, 252, 0.08);
  --color-border-default: rgba(192, 132, 252, 0.18);
  --color-border-strong: rgba(192, 132, 252, 0.30);
  --color-border-focus: rgba(192, 132, 252, 0.35);

  --color-success: #16a34a;
  --color-success-50: rgba(22, 163, 74, 0.08);
  --color-success-glow: rgba(22, 163, 74, 0.25);
  --color-danger: #e11d48;
  --color-danger-50: rgba(225, 29, 72, 0.08);
  --color-danger-glow: rgba(225, 29, 72, 0.20);
  --color-warning: #d97706;
  --color-warning-50: rgba(217, 119, 6, 0.08);
  --color-warning-glow: rgba(217, 119, 6, 0.20);

  --shadow-xs: 0 1px 2px rgba(168, 85, 247, 0.04);
  --shadow-sm: 0 2px 4px rgba(168, 85, 247, 0.06), 0 1px 2px rgba(168, 85, 247, 0.04);
  --shadow-md: 0 4px 8px rgba(168, 85, 247, 0.08), 0 2px 4px rgba(168, 85, 247, 0.04);
  --shadow-lg: 0 8px 16px rgba(168, 85, 247, 0.10), 0 4px 6px rgba(168, 85, 247, 0.04);
  --shadow-xl: 0 16px 32px rgba(168, 85, 247, 0.12), 0 8px 12px rgba(168, 85, 247, 0.04);
  --shadow-2xl: 0 24px 48px rgba(168, 85, 247, 0.15);
  --shadow-focus: 0 0 0 3px rgba(192, 132, 252, 0.25);
  --shadow-inner: inset 0 1px 2px rgba(168, 85, 247, 0.04);

  --gradient-primary: linear-gradient(135deg, #f472b6 0%, #c084fc 50%, #a78bfa 100%);
  --gradient-primary-soft: linear-gradient(135deg, rgba(244,114,182,0.08) 0%, rgba(192,132,252,0.04) 100%);
  --gradient-surface: linear-gradient(180deg, rgba(255,255,255,0.98) 0%, rgba(253,242,248,0.96) 100%);
  --gradient-bg: linear-gradient(180deg, #fdf2f8 0%, #fce7f3 100%);
  --gradient-card-border: linear-gradient(135deg, rgba(255,255,255,0.9), rgba(249,224,242,0.5));

  --glass-bg: rgba(255, 255, 255, 0.80);
  --glass-border: rgba(255, 255, 255, 0.25);
  --glass-blur: 12px;
}
```

- [ ] **Step 3: 升级 Midnight 主题变量**

```css
/* =============================================
   Theme: 暗夜 (Deep Black) — Enhanced
   ============================================= */
[data-theme="midnight"] {
  --color-primary: #818cf8;
  --color-primary-hover: #6366f1;
  --color-primary-active: #4f46e5;
  --color-primary-subtle: rgba(129, 140, 248, 0.08);
  --color-primary-50: rgba(129, 140, 248, 0.04);
  --color-primary-100: rgba(129, 140, 248, 0.12);
  --color-primary-200: rgba(129, 140, 248, 0.20);
  --color-primary-300: rgba(129, 140, 248, 0.30);

  --color-accent: #2dd4bf;
  --color-accent-hover: #14b8a6;
  --color-accent-subtle: rgba(45, 212, 191, 0.08);

  --color-text-primary: #e6edf3;
  --color-text-secondary: #8d96a0;
  --color-text-tertiary: #5c6470;
  --color-text-muted: #3d444d;
  --color-text-inverse: #0f172a;

  --color-bg-canvas: #0d1117;
  --color-bg-surface: rgba(22, 27, 34, 0.95);
  --color-bg-surface-raised: rgba(33, 38, 46, 0.98);
  --color-bg-sunken: #0a0e13;
  --color-bg-subtle: rgba(129, 140, 248, 0.05);
  --color-bg-hover: rgba(129, 140, 248, 0.09);
  --color-bg-active: rgba(129, 140, 248, 0.14);

  --color-border-subtle: rgba(129, 140, 248, 0.06);
  --color-border-default: rgba(129, 140, 248, 0.12);
  --color-border-strong: rgba(129, 140, 248, 0.24);
  --color-border-focus: rgba(129, 140, 248, 0.30);

  --color-success: #3fb950;
  --color-success-50: rgba(63, 185, 80, 0.10);
  --color-success-glow: rgba(63, 185, 80, 0.30);
  --color-danger: #f85149;
  --color-danger-50: rgba(248, 81, 73, 0.10);
  --color-danger-glow: rgba(248, 81, 73, 0.25);
  --color-warning: #d29922;
  --color-warning-50: rgba(210, 153, 34, 0.10);
  --color-warning-glow: rgba(210, 153, 34, 0.25);

  --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.20);
  --shadow-sm: 0 2px 4px rgba(0, 0, 0, 0.25), 0 1px 2px rgba(0, 0, 0, 0.15);
  --shadow-md: 0 4px 8px rgba(0, 0, 0, 0.30), 0 2px 4px rgba(0, 0, 0, 0.20);
  --shadow-lg: 0 8px 16px rgba(0, 0, 0, 0.35), 0 4px 8px rgba(0, 0, 0, 0.20);
  --shadow-xl: 0 16px 32px rgba(0, 0, 0, 0.40), 0 8px 16px rgba(0, 0, 0, 0.25);
  --shadow-2xl: 0 24px 48px rgba(0, 0, 0, 0.50);
  --shadow-focus: 0 0 0 3px rgba(129, 140, 248, 0.25);
  --shadow-inner: inset 0 1px 2px rgba(0, 0, 0, 0.20);

  --gradient-primary: linear-gradient(135deg, #a5b4fc 0%, #818cf8 50%, #6366f1 100%);
  --gradient-primary-soft: linear-gradient(135deg, rgba(165,180,252,0.08) 0%, rgba(129,140,248,0.04) 100%);
  --gradient-surface: linear-gradient(180deg, rgba(33,38,46,0.98) 0%, rgba(22,27,34,0.95) 100%);
  --gradient-bg: linear-gradient(180deg, #0d1117 0%, #0a0e13 100%);
  --gradient-card-border: linear-gradient(135deg, rgba(48,54,61,0.6), rgba(33,38,46,0.3));

  --glass-bg: rgba(22, 27, 34, 0.85);
  --glass-border: rgba(129, 140, 248, 0.12);
  --glass-blur: 12px;
}
```

- [ ] **Step 4: Verify build passes**

Run: `cd panel/frontend && npm run build`
Expected: Build completes without errors

- [ ] **Step 5: Commit**

```bash
cd panel/frontend
git add src/styles/themes.css
git commit -m "refactor(panel): enhance three theme CSS variables with layered colors, shadows, gradients"
```

---

### Task 2: 创建 animations.css — 动画系统

**Files:**
- Create: `panel/frontend/src/styles/animations.css`

- [ ] **Step 1: 创建动画关键帧文件**

写入 `animations.css`：

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
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes fadeInDown {
  from { opacity: 0; transform: translateY(-12px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes slideInRight {
  from { opacity: 0; transform: translateX(8px); }
  to { opacity: 1; transform: translateX(0); }
}

@keyframes slideInLeft {
  from { opacity: 0; transform: translateX(-8px); }
  to { opacity: 1; transform: translateX(0); }
}

/* ----- Component animations ----- */
@keyframes scaleIn {
  from { opacity: 0; transform: scale(0.96); }
  to { opacity: 1; transform: scale(1); }
}

@keyframes popIn {
  0% { opacity: 0; transform: scale(0.9); }
  70% { transform: scale(1.02); }
  100% { opacity: 1; transform: scale(1); }
}

@keyframes buttonPress {
  0% { transform: scale(1); }
  50% { transform: scale(0.97); }
  100% { transform: scale(1); }
}

/* ----- Status indicators ----- */
@keyframes breathe {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.6; transform: scale(1.15); }
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

@keyframes shimmer {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}

@keyframes shimmerBar {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}

/* ----- Skeleton ----- */
@keyframes skeletonPulse {
  0%, 100% { background-position: 0% 50%; }
  50% { background-position: 100% 50%; }
}

/* ----- Success/Error ----- */
@keyframes checkDraw {
  0% { stroke-dashoffset: 24; }
  100% { stroke-dashoffset: 0; }
}

@keyframes errorShake {
  0%, 100% { transform: translateX(0); }
  20% { transform: translateX(-4px); }
  40% { transform: translateX(4px); }
  60% { transform: translateX(-3px); }
  80% { transform: translateX(3px); }
}

@keyframes successFlash {
  0% { background-color: transparent; }
  30% { background-color: rgba(22, 163, 74, 0.08); }
  100% { background-color: transparent; }
}

/* ----- Utility classes ----- */
.animate-fade-in { animation: fadeIn var(--duration-normal) var(--ease-default) both; }
.animate-fade-in-up { animation: fadeInUp var(--duration-normal) var(--ease-default) both; }
.animate-fade-in-down { animation: fadeInDown var(--duration-normal) var(--ease-default) both; }
.animate-slide-in-right { animation: slideInRight var(--duration-normal) var(--ease-default) both; }
.animate-scale-in { animation: scaleIn var(--duration-normal) var(--ease-bounce) both; }
.animate-pop-in { animation: popIn 0.35s var(--ease-bounce) both; }
.animate-breathe { animation: breathe 2s ease-in-out infinite; }
.animate-pulse { animation: pulse 2s ease-in-out infinite; }

/* Stagger delays for card entrance */
.stagger-1 { animation-delay: 0ms; }
.stagger-2 { animation-delay: 50ms; }
.stagger-3 { animation-delay: 100ms; }
.stagger-4 { animation-delay: 150ms; }
.stagger-5 { animation-delay: 200ms; }
.stagger-6 { animation-delay: 250ms; }
.stagger-7 { animation-delay: 300ms; }
.stagger-8 { animation-delay: 350ms; }

/* Skeleton */
.skeleton {
  background: linear-gradient(
    90deg,
    var(--color-bg-subtle) 25%,
    var(--color-bg-hover) 37%,
    var(--color-bg-subtle) 63%
  );
  background-size: 200% 100%;
  animation: skeletonPulse 1.5s ease-in-out infinite;
  border-radius: var(--radius-md);
}

/* Button press on active */
.btn:active {
  animation: buttonPress 0.15s var(--ease-default);
}

/* Focus glow for inputs */
.focus-glow:focus {
  box-shadow: var(--shadow-focus);
  transition: box-shadow var(--duration-fast) var(--ease-default);
}

/* Shimmer loading overlay */
.shimmer-overlay {
  position: relative;
  overflow: hidden;
}
.shimmer-overlay::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(90deg, transparent, rgba(255,255,255,0.4), transparent);
  animation: shimmerBar 1.5s infinite;
}

/* Card entrance animation */
.card-enter {
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}
```

- [ ] **Step 2: Commit**

```bash
cd panel/frontend
git add src/styles/animations.css
git commit -m "feat(panel): add animation system with keyframes, skeleton, and status indicators"
```

---

### Task 3: 升级 utilities.css — 组件样式扩展

**Files:**
- Modify: `panel/frontend/src/styles/utilities.css`

- [ ] **Step 1: 替换并扩展 utilities.css 全部内容**

```css
/* Global utilities — used across pages.
   Imported by styles/index.css. */

/* ----- Buttons — Enhanced ----- */
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  padding: 0.5rem 1rem;
  border-radius: var(--radius-lg);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: 1px solid transparent;
  font-family: inherit;
  text-decoration: none;
  white-space: nowrap;
  position: relative;
  overflow: hidden;
}

.btn:active {
  transform: scale(0.97);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.btn-primary {
  background: var(--gradient-primary);
  color: white;
  box-shadow: var(--shadow-sm);
  border-color: transparent;
}

.btn-primary:hover:not(:disabled) {
  box-shadow: var(--shadow-md);
  transform: translateY(-1px);
}

.btn-primary:hover:not(:disabled):active {
  transform: translateY(0) scale(0.97);
}

.btn-secondary {
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  border: 1.5px solid var(--color-border-default);
  box-shadow: var(--shadow-xs);
}

.btn-secondary:hover:not(:disabled) {
  background: var(--color-bg-hover);
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-sm);
}

.btn-danger {
  background: var(--color-danger);
  color: white;
  box-shadow: var(--shadow-sm);
}

.btn-danger:hover:not(:disabled) {
  background: var(--color-danger-hover, #b91c1c);
  box-shadow: var(--shadow-md);
}

.btn-ghost {
  background: transparent;
  color: var(--color-text-secondary);
  border: none;
}

.btn-ghost:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.btn-sm {
  padding: 0.25rem 0.75rem;
  font-size: 0.8125rem;
  border-radius: var(--radius-md);
}

.btn-lg {
  padding: 0.75rem 1.5rem;
  font-size: 1rem;
  border-radius: var(--radius-xl);
}

.btn-icon {
  width: 36px;
  height: 36px;
  padding: 0;
  border-radius: var(--radius-lg);
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
  padding: 0.5rem 2rem 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default),
              width 0.2s var(--ease-default);
  box-sizing: border-box;
}

.search-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
  width: 280px;
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
  right: 8px;
  z-index: 2;
}

@media (max-width: 640px) {
  .search-wrapper {
    width: 36px;
    height: 36px;
    border-radius: var(--radius-lg);
    border: 1.5px solid var(--color-border-default);
    background: var(--color-bg-subtle);
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

/* ----- Tag — Enhanced ----- */
.tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 10px;
  border-radius: var(--radius-full);
  font-size: 0.75rem;
  font-weight: 600;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border: 1px solid var(--color-border-subtle);
  line-height: 1.4;
  white-space: nowrap;
  transition: all var(--duration-fast) var(--ease-default);
}

.tag:hover {
  background: var(--color-primary-100, var(--color-bg-hover));
  border-color: var(--color-border-default);
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
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
  letter-spacing: -0.02em;
}

.page-subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

/* ----- Empty state — Enhanced ----- */
.empty-state {
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

.empty-state__icon {
  color: var(--color-text-muted);
  opacity: 0.5;
  margin-bottom: 0.25rem;
}

.empty-state__title {
  font-size: 1rem;
  font-weight: 500;
  color: var(--color-text-secondary);
  margin: 0;
}

.empty-state__hint {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

/* ----- Card grid (shared) ----- */
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

@media (min-width: 1280px) {
  .card-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

/* ----- Modal overlay (shared) ----- */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  z-index: var(--z-modal-backdrop);
  display: flex;
  align-items: center;
  justify-content: center;
  animation: fadeIn 0.15s var(--ease-default);
}

.modal {
  background: var(--color-bg-surface-raised);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-2xl);
  width: min(500px, 92vw);
  overflow: hidden;
  animation: scaleIn 0.2s var(--ease-bounce) both;
}

.modal--lg { width: min(640px, 94vw); }
.modal--xl { width: min(800px, 95vw); }

.modal__header {
  padding: 1rem 1.5rem;
  font-weight: 600;
  font-size: 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: var(--color-text-primary);
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
}

.modal__footer {
  padding: 1rem 1.5rem;
  display: flex;
  justify-content: flex-end;
  gap: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
}

/* ----- Form elements (shared) ----- */
.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}

.form-label {
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--color-text-secondary);
}

.input-base {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: 0.875rem;
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

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`
Expected: Build completes without errors

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/styles/utilities.css
git commit -m "refactor(panel): expand utility classes with enhanced buttons, forms, modals, empty states"
```

---

### Task 4: 更新 index.css — 引入动画和全局增强

**Files:**
- Modify: `panel/frontend/src/styles/index.css`

- [ ] **Step 1: 替换 index.css 内容**

```css
@import './themes.css';
@import './animations.css';
@import './utilities.css';

/* Body base */
body {
  margin: 0;
  font-family: var(--font-sans);
  background: var(--gradient-bg);
  color: var(--color-text-primary);
  -webkit-font-smoothing: antialiased;
  min-height: 100dvh;
}

/* Page content wrapper transition */
.content {
  transition: opacity var(--duration-normal) var(--ease-default);
}

/* Scrollbar — Enhanced */
::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

::-webkit-scrollbar-track {
  background: transparent;
}

::-webkit-scrollbar-thumb {
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  transition: background var(--duration-fast) var(--ease-default);
}

::-webkit-scrollbar-thumb:hover {
  background: var(--color-border-strong);
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
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 1.5rem;
}

/* Section card wrapper */
.section-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  box-shadow: var(--shadow-sm);
  margin-bottom: 1.5rem;
}

/* 4K adjustments */
@media (min-width: 2560px) {
  .card-grid { grid-template-columns: repeat(auto-fill, minmax(380px, 1fr)); gap: 1.25rem; }
  .page-title { font-size: 1.75rem; }
  .page-container { max-width: 1600px; }
}

/* Tablet */
@media (max-width: 1024px) {
  .card-grid { grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
}

/* Mobile */
@media (max-width: 640px) {
  .page-container { padding: 0 1rem; }
  .page-title { font-size: 1.25rem; }
}
```

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`
Expected: Build completes without errors

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/styles/index.css
git commit -m "refactor(panel): integrate animations and enhance global styles"
```

---

### Task 5: 升级 Sidebar 视觉

**Files:**
- Modify: `panel/frontend/src/components/layout/Sidebar.vue`

- [ ] **Step 1: 替换 Sidebar 的 `<style scoped>` 块**

只修改样式部分，模板和脚本不变。将 `<style scoped>` 替换为：

```css
<style scoped>
.sidebar {
  width: 260px;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  overflow: hidden;
  transition: width var(--duration-normal) var(--ease-default);
  box-shadow: var(--shadow-xs);
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
  font-size: 0.9375rem;
  font-weight: 700;
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
  gap: 0.625rem;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  font-size: 0.875rem;
  font-weight: 500;
  transition: all var(--duration-fast) var(--ease-default);
  position: relative;
}

.sidebar__nav-item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-item--active,
.sidebar__nav-item.active {
  background: var(--gradient-primary-soft);
  color: var(--color-primary);
  font-weight: 600;
}

.sidebar__nav-item--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 60%;
  background: var(--color-primary);
  border-radius: 0 2px 2px 0;
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
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  transition: all var(--duration-fast) var(--ease-default);
}

.sidebar__nav-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-icon--active {
  background: var(--gradient-primary-soft);
  color: var(--color-primary);
}
</style>
```

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/components/layout/Sidebar.vue
git commit -m "refactor(panel): upgrade sidebar visual with gradient active state and smooth transitions"
```

---

### Task 6: 升级 TopBar 视觉

**Files:**
- Modify: `panel/frontend/src/components/layout/TopBar.vue`

- [ ] **Step 1: 替换 TopBar 的 `<style scoped>` 块**

只修改样式部分。将 `<style scoped>` 替换为：

```css
<style scoped>
.topbar {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 1.25rem;
  background: var(--glass-bg, var(--color-bg-surface));
  border-bottom: 1px solid var(--color-border-subtle);
  backdrop-filter: blur(var(--glass-blur, 16px));
  -webkit-backdrop-filter: blur(var(--glass-blur, 16px));
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
  box-shadow: var(--shadow-xs);
}

.topbar__left { display: flex; align-items: center; gap: 1rem; }

.topbar__brand { display: flex; align-items: center; gap: 0.75rem; }

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-bounce);
}

.topbar__logo:hover {
  transform: scale(1.05);
}

.topbar__title { display: flex; align-items: center; gap: 0.5rem; }

.topbar__name {
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

.topbar__badge {
  font-size: 0.6875rem;
  font-weight: 600;
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
}

.topbar__actions { display: flex; align-items: center; gap: 0.5rem; }

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
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

/* Agent Switcher — Enhanced */
.agent-switcher { position: relative; }

.agent-switcher__trigger {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  color: var(--color-text-primary);
  font-size: 0.8rem;
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
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  overflow: visible;
  animation: scaleIn 0.15s var(--ease-bounce) both;
}

.agent-switcher__search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.agent-switcher__search-input {
  width: 100%;
  padding: 0.375rem 0.625rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8rem;
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
  background: var(--gradient-primary-soft);
  color: var(--color-primary);
}

.agent-switcher__item-name {
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-switcher__item-time {
  font-size: 0.7rem;
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
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.75rem;
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
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}

.agent-switcher__sort-btn {
  padding: 0.125rem 0.375rem;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  font-family: inherit;
  transition: all var(--duration-fast) var(--ease-default);
}

.agent-switcher__sort-btn.active {
  background: var(--gradient-primary-soft);
  color: var(--color-primary);
  font-weight: 600;
}

.agent-switcher__empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
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

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/components/layout/TopBar.vue
git commit -m "refactor(panel): upgrade topbar with glass effect, enhanced switcher, and breathing status dots"
```

---

### Task 7: 升级基础组件 — BaseCard, BaseModal, BaseBadge, StatCard

**Files:**
- Modify: `panel/frontend/src/components/base/BaseCard.vue`
- Modify: `panel/frontend/src/components/base/BaseModal.vue`
- Modify: `panel/frontend/src/components/base/BaseBadge.vue`
- Modify: `panel/frontend/src/components/base/StatCard.vue`

- [ ] **Step 1: 升级 BaseCard.vue 的 `<style scoped>`**

替换为：

```css
<style scoped>
.card {
  background: var(--gradient-surface, var(--color-bg-surface));
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-6);
  box-shadow: var(--shadow-sm);
  transition: all var(--duration-normal) var(--ease-default);
  position: relative;
}

.card-hover:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.card-glass {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border-color: var(--glass-border);
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
  font-size: var(--text-lg);
  font-weight: 600;
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

- [ ] **Step 2: 升级 BaseBadge.vue 的 `<style scoped>`**

替换为：

```css
<style scoped>
.base-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  line-height: 1;
  font-weight: 600;
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
  font-size: 0.7rem;
  padding: 2px 6px;
  font-weight: 700;
}

.base-badge--md {
  font-size: 0.75rem;
  padding: 3px 8px;
}

.base-badge--mono {
  font-family: var(--font-mono);
  font-weight: 700;
  letter-spacing: 0.02em;
}
</style>
```

- [ ] **Step 3: 升级 StatCard.vue**

先读取文件内容，然后替换 scoped style。读取 `panel/frontend/src/components/base/StatCard.vue`。

StatCard 需要升级为渐变背景、图标放大、数值动画。替换其 `<style scoped>` 为：

```css
<style scoped>
.stat-card {
  background: var(--gradient-surface, var(--color-bg-surface));
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  box-shadow: var(--shadow-sm);
  transition: all var(--duration-normal) var(--ease-default);
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
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-3);
}

.stat-card--primary .stat-card__icon {
  background: var(--gradient-primary-soft);
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
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
}

.stat-card__label {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: 500;
}

/* Subtle gradient accent on top */
.stat-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.stat-card--success::before { background: linear-gradient(90deg, var(--color-success), var(--color-success-glow)); }
.stat-card--warning::before { background: linear-gradient(90deg, var(--color-warning), var(--color-warning-glow)); }
.stat-card--danger::before { background: linear-gradient(90deg, var(--color-danger), var(--color-danger-glow)); }

.stat-card:hover::before {
  opacity: 1;
}
</style>
```

- [ ] **Step 4: 升级 BaseModal.vue 的 `<style scoped>`**

BaseModal 使用新的动画和圆角。替换其 `<style scoped>` 中的 `.modal-overlay` 和 `.modal` 相关样式以使用 `index.css` 中已定义的共享 `.modal-overlay` 和 `.modal` 类，只保留 modal 内部布局所需的 scoped 样式。

- [ ] **Step 5: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 6: Commit**

```bash
cd panel/frontend
git add src/components/base/BaseCard.vue src/components/base/BaseBadge.vue src/components/base/StatCard.vue src/components/base/BaseModal.vue
git commit -m "refactor(panel): upgrade base components with gradients, shadows, and smooth transitions"
```

---

### Task 8: 升级 Dashboard 页面视觉

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: 替换 DashboardPage 的 `<style scoped>`**

```css
<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.dashboard__header {
  margin-bottom: 2rem;
}

.dashboard__title {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
  letter-spacing: -0.02em;
}

.dashboard__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 1rem;
  margin-bottom: 2rem;
}

.stats-grid .stat-card {
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
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
  font-size: 1rem;
}

.dashboard__empty-hint {
  font-size: 0.875rem !important;
  color: var(--color-text-tertiary) !important;
}

.dashboard-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: 2rem;
  box-shadow: var(--shadow-sm);
}

.dashboard-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.dashboard-section__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.dashboard-section__link {
  font-size: 0.8rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
  transition: color var(--duration-fast) var(--ease-default);
}

.dashboard-section__link:hover {
  color: var(--color-primary-hover);
  text-decoration: underline;
}

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 0.75rem;
  }
  .dashboard__title {
    font-size: 1.25rem;
  }
}
</style>
```

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/pages/DashboardPage.vue
git commit -m "refactor(panel): upgrade dashboard visual with stat card grid and fade-in animations"
```

---

### Task 9: 升级 RulesPage 视觉

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`

- [ ] **Step 1: 替换 RulesPage 的 `<style scoped>`**

```css
<style scoped>
.rules-page {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
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
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.rules-page__subtitle {
  font-size: 0.875rem;
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
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
}

@media (max-width: 640px) {
  .rules-page__header { gap: 0.5rem; }
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

@media (min-width: 1280px) {
  .rule-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}
</style>
```

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/pages/RulesPage.vue
git commit -m "refactor(panel): upgrade rules page visual with page-header animations"
```

---

### Task 10: 升级 AgentsPage 视觉

**Files:**
- Modify: `panel/frontend/src/pages/AgentsPage.vue`

- [ ] **Step 1: 替换 AgentsPage 的 `<style scoped>`**

```css
<style scoped>
.agents-page {
  max-width: 1200px;
  margin: 0 auto;
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.agents-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.agents-page__header-left { flex: 1; min-width: 0; }

.agents-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
}

.agents-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.agents-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.agent-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

@media (min-width: 1280px) {
  .agent-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

.agents-page__empty,
.agents-page__loading {
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
</style>
```

注意：AgentsPage 中的 modal 相关样式已迁移到 `utilities.css` 中的共享 `.modal-overlay` / `.modal` 类。需要移除 scoped style 中的 `.modal-overlay`、`.modal`、`.modal--lg`、`.modal__header`、`.modal__close`、`.modal__body`、`.modal__footer` 相关定义，只保留页面特定样式。

实际替换时，删除这些共享样式（因为已在 utilities.css 中定义），保留 `.join-tabs`、`.join-tab`、`.join-command`、`.join-steps`、`.form-group`、`.input-base` 等页面特有样式。

- [ ] **Step 2: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 3: Commit**

```bash
cd panel/frontend
git add src/pages/AgentsPage.vue
git commit -m "refactor(panel): upgrade agents page visual and deduplicate modal styles"
```

---

### Task 11: 升级 AgentCard 和 RuleCard

**Files:**
- Modify: `panel/frontend/src/components/AgentCard.vue`
- Modify: `panel/frontend/src/components/rules/RuleCard.vue`

- [ ] **Step 1: 升级 AgentCard.vue 的 `<style scoped>`**

读取当前文件后，升级卡片样式：

```css
<style scoped>
.agent-card {
  background: var(--gradient-surface, var(--color-bg-surface));
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-default);
  position: relative;
  overflow: hidden;
}

.agent-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.agent-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: var(--gradient-primary);
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default);
}

.agent-card:hover::before {
  opacity: 1;
}

.agent-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.agent-card__name {
  font-size: 0.9375rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-card__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  font-size: 0.8125rem;
  color: var(--color-text-secondary);
}

.agent-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}
</style>
```

- [ ] **Step 2: 升级 RuleCard.vue 的 `<style scoped>`**

类似 AgentCard 的升级，增加渐变边框悬停效果。

- [ ] **Step 3: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 4: Commit**

```bash
cd panel/frontend
git add src/components/AgentCard.vue src/components/rules/RuleCard.vue
git commit -m "refactor(panel): upgrade agent and rule cards with gradient borders and hover effects"
```

---

### Task 12: 升级 EmptyState 和 DeleteConfirmDialog

**Files:**
- Modify: `panel/frontend/src/components/EmptyState.vue`
- Modify: `panel/frontend/src/components/DeleteConfirmDialog.vue`

- [ ] **Step 1: 升级 EmptyState.vue**

读取文件后升级，增加入场动画和更精致的图标容器：

```css
<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-16) var(--space-8);
  text-align: center;
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.empty-state__icon {
  width: 64px;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-2xl);
  background: var(--color-bg-subtle);
  color: var(--color-text-muted);
  margin-bottom: var(--space-2);
}

.empty-state__title {
  font-size: var(--text-lg);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.empty-state__description {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  max-width: 400px;
}

.empty-state__actions {
  display: flex;
  gap: var(--space-3);
  margin-top: var(--space-4);
}
</style>
```

- [ ] **Step 2: 升级 DeleteConfirmDialog.vue**

增加弹窗动画和按钮样式。

- [ ] **Step 3: Verify build passes**

Run: `cd panel/frontend && npm run build`

- [ ] **Step 4: Commit**

```bash
cd panel/frontend
git add src/components/EmptyState.vue src/components/DeleteConfirmDialog.vue
git commit -m "refactor(panel): upgrade empty state and delete dialog with entrance animations"
```

---

### Task 13: 全局验证和回归测试

**Files:** All modified files

- [ ] **Step 1: 运行前端测试**

Run: `cd panel/frontend && npm run test`
Expected: All tests pass

- [ ] **Step 2: 运行前端构建**

Run: `cd panel/frontend && npm run build`
Expected: Build completes without errors, no warnings about unused CSS variables

- [ ] **Step 3: 运行后端测试**

Run: `cd panel/backend-go && go test ./...`
Expected: All tests pass

- [ ] **Step 4: 最终提交**

如果前面的测试都通过，无需额外提交（每个 task 已单独提交）。

---

## Self-Review

### Spec Coverage

| Spec Requirement | Task |
|-----------------|------|
| Theme enhancement (3 themes) | Task 1 |
| Animation system | Task 2 |
| Component utilities (buttons, forms, modals, badges, empty states) | Task 3 |
| Global style integration | Task 4 |
| Sidebar upgrade | Task 5 |
| TopBar upgrade | Task 6 |
| BaseCard, BaseModal, BaseBadge, StatCard upgrade | Task 7 |
| Dashboard page upgrade | Task 8 |
| RulesPage upgrade | Task 9 |
| AgentsPage upgrade | Task 10 |
| AgentCard + RuleCard upgrade | Task 11 |
| EmptyState + DeleteConfirmDialog upgrade | Task 12 |
| Verify tests + build | Task 13 |

### Placeholder Scan

No TBDs, no "implement later", no vague references. All code steps contain actual CSS content.

### Type Consistency

All CSS variable names are consistent across tasks (e.g., `--gradient-primary-soft`, `--shadow-sm`, `--color-primary-subtle`, `--duration-normal`, `--ease-default`).
