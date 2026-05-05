---
name: Frontend Visual Refresh
description: Pure CSS upgrade of panel frontend UI/UX — theme enhancement, component design system, animations, modern SaaS visual style
type: design
---

# Frontend Visual Refresh Design

## Summary

全面重构 `panel/frontend` 的 UI/UX 视觉效果，采用渐进式 CSS 升级方案（方案 A），保持现有组件结构和页面布局不变，通过升级 CSS 变量体系、组件样式、动画效果，达到现代 SaaS 的视觉水准。参考 pilipili、sub2api、misaka_danmu_server 的设计风格。

**Scope:** 纯视觉焕新，不改动组件逻辑和页面结构

**Priority Pages:** Dashboard（仪表盘）、规则管理页（HTTP/L4/Relay）、节点管理页

**Style Direction:** 现代渐变 SaaS 风 — 丰富的色彩层次、柔和渐变、玻璃拟态卡片、精致微动画

## Architecture

### File Structure (unchanged, content upgraded)

```
panel/frontend/src/styles/
  themes.css        ← 升级：增强三个主题的变量（更多层次色、渐变、阴影）
  utilities.css     ← 升级：增加新的工具类（玻璃拟态、动画、排版层次）
  index.css         ← 升级：全局样式增强（滚动条、选择、过渡）
```

三个主题（sakura、business、midnight）全部保留并升级。

## Component Design System

### 1. Card System

| Property | Current | Upgraded |
|----------|---------|----------|
| Border radius | 8px | 12px / 16px (by elevation) |
| Shadow | Single layer | Multi-layer composite |
| Border | Solid line | Semi-transparent gradient border |
| Hover | Color change | Lift 2px + shadow deepen + border highlight |
| Background | Solid color | Subtle gradient + optional backdrop-filter |

### 2. Button System

- **Primary:** Gradient background + subtle inner shadow + hover lift 1px
- **Secondary:** Semi-transparent background + border + hover to solid
- **Ghost:** No border, background on hover
- **Danger:** Red gradient + hover deepen
- **Icon:** Circle/rounded square + micro-animation

### 3. Table System

- **Header:** Light gray bg + small uppercase labels + bottom border
- **Rows:** Alternating zebra stripes (50% opacity) + hover highlight
- **Cells:** Rounded inset + status badges + action buttons show on hover
- **Empty:** Large icon + description + action button

### 4. Form System

- **Inputs:** Larger radius + focus glow ring + error red border
- **Selects:** Custom arrow animation + option hover bg
- **Toggles:** Smooth transition animation + color change
- **Sliders:** Gradient track + circular handle

## Animation System

### Page-level
- Route transitions: fade + micro-displacement (200ms)
- Page load: skeleton → content reveal

### Component-level
- Card entrance: fade + 12px displacement (staggered)
- Button click: scale(0.98) elastic bounce
- Table row: hover bg color 150ms transition
- Form validation: error slide-in + shake

### Status Indicators
- Loading: pulse animation (skeleton)
- Success: checkmark draw animation
- Error: red flash
- Online status: breathing light effect

### Scroll
- Sidebar: smooth scroll indicator
- List: sticky header shadow on scroll

## Theme Details

### Business (晴空) — Primary Theme

```css
Background layers:
  canvas: #f0f4f8 (softer base)
  surface: pure white + subtle texture gradient
  raised: elevated cards (brighter white + shadow)
  sunken: inset areas (slate-50 bg)

Primary: #2563eb (kept)
  + accent colors (teal/emerald) for charts and status
  + subtle gradient: 50/100/200/300 levels

5 shadow levels:
  xs: 0 1px 2px    (card default)
  sm: 0 2px 8px    (card hover)
  md: 0 4px 16px   (modal default)
  lg: 0 8px 32px   (modal hover)
  xl: 0 16px 64px  (modal backdrop)

Gradients:
  primary: 135deg 3-color (kept, softened)
  surface: card bg gradient
  bg: page bg gradient (canvas to subtle)
```

### Sakura (二次元) — Enhanced

More pink/purple hierarchy, glassmorphism cards.

### Midnight (暗夜) — Enhanced

Deeper dark-specific surfaces, more distinct layer levels.

## Implementation Approach

1. Upgrade `themes.css` — add all new CSS variables to each theme
2. Create component styles in a new `components.css` or expand `utilities.css`
3. Add animation keyframes and transition utilities
4. Apply upgraded styles globally (minimal per-component changes needed)
5. Verify all three pages (dashboard, rules, agents) look polished

## Constraints

- No component logic changes
- No page structure changes
- All three themes must remain functional
- UnoCSS existing patterns should be respected
- Must not break existing tests
