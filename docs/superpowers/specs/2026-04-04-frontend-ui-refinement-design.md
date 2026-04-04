# Frontend UI Refinement Design

## Date: 2026-04-04

## Context

Frontend UI needs polish across multiple pages: HTTP/证书/L4 列表风格统一、弹窗行为修正、快捷键提示、主题模块、节点加入命令、以及全分辨率适配（从 375px mobile 到 4K）。

---

## 1. HTTP 规则列表 → L4 卡片风格统一

### Problem
- L4 cards: hover shows action buttons, inline mapping display, protocol/status badge horizontal layout
- HTTP cards: toggle on left (wastes space), buttons always visible (not elegant), no mapping visualization

### Solution
Apply L4 card visual pattern to HTTP rule cards:

**Card structure:**
```
┌──────────────────────────────────────────────────────────┐
│ #1  HTTPS  生效中           [停用] [复制] [编辑] [删除]   │
│ https://emby.example.com  →  http://192.168.1.10:8096   │
│ [jellyfin] [media]                                        │
└──────────────────────────────────────────────────────────┘
```

**Changes:**
- 操作按钮默认 `opacity: 0`，hover 时 `opacity: 1`（同 L4 卡片）
- URL + backend_url 用 `→` 连接，内联展示（无需 toggle 占据左侧空间）
- Protocol badge + status badge 横向排列在右上角
- `rule-card` 使用 flex column gap，通过 justify-content: space-between 保持对齐

**Files:** `panel/frontend/src/pages/RulesPage.vue`

---

## 2. 证书列表卡片 → Hover 显示操作

### Problem
Cert cards always show action buttons (编辑/删除) without hover behavior.

### Solution
- Apply same hover-reveal pattern: buttons default `opacity: 0`, visible on hover
- Keep 签发 button always visible (primary action)

**Files:** `panel/frontend/src/pages/CertsPage.vue`

---

## 3. 弹窗宽度 & 水平滚动修复

### Problem
- Certificate modal has potential horizontal overflow
- Join agent modal command overflows horizontally at all resolutions
- Mobile: join modal command severely overflows

### Solution

**Modal body overflow:**
```css
.modal__body {
  overflow-x: hidden;
  overflow-y: auto;
}
```

**Join command (AgentsPage.vue):**
```css
.join-command code {
  flex: 1;
  word-break: break-all;
  overflow-x: hidden;
  white-space: pre-wrap;
}
```

---

## 4. 弹窗点击 Overlay 关闭行为统一

### Problem
- L4 添加/编辑弹窗：点击 overlay 不关闭 ✓
- HTTP/证书/节点管理弹窗：点击 overlay 会关闭 ✗

### Solution
**所有添加/编辑弹窗：** 移除 `@click.self` 关闭行为，只有关闭按钮（X）或显式取消按钮才能关闭。

**删除确认弹窗：** 保持 `@click.self` 可关闭（这是正确的确认/取消模式）。

**Files to fix:**
- `panel/frontend/src/pages/RulesPage.vue` — 移除 `@click.self="closeForm"`
- `panel/frontend/src/pages/CertsPage.vue` — 移除 `@click.self="closeForm"`
- `panel/frontend/src/pages/AgentsPage.vue` — 移除 `@click.self="showJoinModal = false"`

**Note:** L4RulesPage add/edit modal already correctly does NOT close on overlay click.

---

## 5. 全局搜索 ⌘K 快捷键提示修复

### Problem
TopBar shows `⌘K` on all platforms including Windows.

### Solution
Detect platform and show appropriate shortcut:
```javascript
const isMac = /Mac|iPod|iPhone|iPad/.test(navigator.platform)
```
```html
<kbd>{{ isMac ? '⌘K' : 'Ctrl+K' }}</kbd>
```

**Files:** `panel/frontend/src/components/layout/TopBar.vue`

---

## 6. 外观主题模块细节调整

### Problem
- Theme options use emoji + text, cramped at `min-width: 100px`
- Simple vertical stack, weak active state feedback
- 4K: cards look too small

### Solution

**A) Remove emojis, text only:**
```html
<span class="theme-option__label">二次元</span>  <!-- was "🌸 二次元" -->
<span class="theme-option__label">晴空</span>
<span class="theme-option__label">暗夜</span>
```

**B) Larger cards, horizontal layout:**
```css
.theme-grid {
  display: flex;
  gap: 1rem;
}
.theme-option {
  min-width: 140px;
  padding: 1.25rem 1rem;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;
}
```

**C) Enhanced active state with glow:**
```css
.theme-option.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 20%, transparent);
  transform: translateY(-2px);
}
.theme-option__check {
  animation: checkPop 0.3s var(--ease-bounce);
}
@keyframes checkPop {
  0% { transform: scale(0); }
  100% { transform: scale(1); }
}
```

**D) 4K adaptation:**
```css
@media (min-width: 2560px) {
  .theme-option {
    min-width: 180px;
    padding: 1.5rem;
  }
  .theme-option__label {
    font-size: 1rem;
  }
}
```

**Files:** `panel/frontend/src/pages/SettingsPage.vue`

---

## 7. 节点管理加入命令单行显示

### Problem
Join command overflows horizontally at all resolutions (375px mobile especially bad).

### Solution
```css
.join-command code {
  flex: 1;
  word-break: break-all;
  overflow-x: hidden;
  white-space: pre-wrap;
}
```

Also at 4K, ensure the command block has proper line-height.

**Files:** `panel/frontend/src/pages/AgentsPage.vue`

---

## 8. 全分辨率响应式适配

### Breakpoints
- **Mobile:** 320px – 767px
- **Tablet:** 768px – 1279px
- **Laptop/Desktop:** 1280px – 2559px
- **4K:** 2560px+

### Global Modals

```css
/* Mobile */
@media (max-width: 640px) {
  .modal-overlay { padding: var(--space-2); }
  .modal--large, .modal { width: min(94vw, 100%); }
  .modal__header { padding: var(--space-4) var(--space-5); font-size: var(--text-base); }
  .modal__body { padding: var(--space-4); }
  .modal__footer { padding: var(--space-3) var(--space-5); }
}

/* 4K */
@media (min-width: 2560px) {
  .modal {
    width: min(520px, 85vw);
  }
  .modal--large {
    width: min(680px, 88vw);
  }
  .modal__header {
    padding: var(--space-6) var(--space-7);
    font-size: var(--text-xl);
  }
  .modal__body {
    padding: var(--space-7);
    gap: var(--space-6);
  }
  .modal__footer {
    padding: var(--space-5) var(--space-7);
  }
}
```

### Cards

```css
/* 4K: larger cards and fonts */
@media (min-width: 2560px) {
  .rule-grid { grid-template-columns: repeat(auto-fill, minmax(380px, 1fr)); gap: 1.25rem; }
  .rule-card { padding: 1.5rem; font-size: 1.0625rem; }
  .cert-grid { grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 1.25rem; }
  .cert-card { padding: 1.5rem; }
  .l4-card { padding: 1.5rem; }
}

/* Tablet */
@media (max-width: 1024px) {
  .cert-grid { grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); }
}
```

### TopBar 4K

```css
@media (min-width: 2560px) {
  .topbar { height: 72px; padding: 0 2rem; }
  .topbar__name { font-size: 1.125rem; }
  .topbar__badge { font-size: 0.875rem; }
}
```

### Sidebar / BottomNav already have existing responsive handling.

---

## 9. RuleForm / CertificateForm → L4RuleForm 样式统一

### Problem
- `RuleForm` (创建/编辑 HTTP 规则) 使用了大按钮 (`btn--lg`)、大圆角 (`radius-lg`)、大 padding (`space-3 space-4`) 和 input-wrapper icon 样式
- `CertificateForm` (创建/编辑证书) 使用了类似的大号样式
- `L4RuleForm` 使用更紧凑的样式：`btn--primary` (标准)、`radius-md`、紧凑间距 (`space-2/space-3`)

### Solution
改造 `RuleForm` 和 `CertificateForm`，使其与 `L4RuleForm` 视觉风格完全一致：

**按钮统一为 L4 风格：**
```css
/* 替换 */
.btn--primary.btn--full.btn--lg { ... }
/* 改为 */
.btn--primary { padding: var(--space-2) var(--space-4); }
```

**表单元素尺寸统一：**
```css
.input {
  padding: var(--space-2) var(--space-3);  /* 原为 space-3 space-4 */
  border-radius: var(--radius-md);             /* 原为 radius-lg */
  font-size: var(--text-sm);
}
```

**表单布局（与 L4RuleForm 一致）：**
- `form-group`: `flex-direction: column; gap: var(--space-2)`
- `form-row`: `grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: var(--space-3)`
- 标签输入区域紧凑排列

**提交按钮放在表单底部：**
```html
<button type="submit" class="btn btn--primary btn--full">
```

**注意：** `RuleForm` 的 `input-wrapper`（input 左侧图标）需要移除或重构为与 L4 一致的简洁样式。

**Files:** `panel/frontend/src/components/RuleForm.vue`, `panel/frontend/src/components/CertificateForm.vue`

---

## 10. L4 规则列表保持原样（已是标准）

L4RulesPage 和 L4RuleItem 已是目标风格，无需修改。

---

## Implementation Order

1. TopBar ⌘K shortcut fix (trivial, isolated)
2. Theme module: remove emoji, larger cards, enhanced active state
3. AgentsPage: join command overflow fix
4. RulesPage: card style update to match L4
5. CertsPage: card hover behavior
6. **RuleForm + CertificateForm → L4RuleForm 样式统一**
7. Modal overflow + all modal @click.self removal
8. Global responsive fixes (4K, mobile)
9. Verify with Chrome DevTools at 375px, 1024px, 1920px, 2560px, 3840px

---

## Files to Modify

- `panel/frontend/src/components/layout/TopBar.vue`
- `panel/frontend/src/pages/SettingsPage.vue`
- `panel/frontend/src/pages/AgentsPage.vue`
- `panel/frontend/src/pages/RulesPage.vue`
- `panel/frontend/src/pages/CertsPage.vue`
- `panel/frontend/src/components/RuleForm.vue`
- `panel/frontend/src/components/CertificateForm.vue`
- `panel/frontend/src/styles/index.css` (global modal responsive)
