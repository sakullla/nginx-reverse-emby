# Settings Page Redesign Spec

Date: 2026-04-19

## Overview

Redesign the system settings page from a single-page card layout to a Tab-based layout with scrollable pill-style navigation. Add two new settings categories: Security (API Token) and Agent Management (heartbeat + registration). Redesign the Data tab with selective export/import.

## Goals

- Reorganize settings into clear Tab categories
- Add API Token management
- Add Agent heartbeat configuration and registration/approval settings
- Redesign data export/import with selective category control
- Keep existing functionality (theme, about) intact

## Page Structure

### Overall Layout

```
┌──────────────────────────────────────────┐
│  系统设置                                │
├──────────────────────────────────────────┤
│  [外观] [安全] [Agent] [数据] [关于]  │  ← pill tabs, scrollable
├──────────────────────────────────────────┤
│                                          │
│  Active tab content area                 │
│                                          │
└──────────────────────────────────────────┘
```

### Tab Navigation (Pill Style)

- Each tab is a rounded pill button with icon + label
- Active state: filled primary color background + white text
- Inactive state: transparent background + gray text, subtle hover background
- Container allows horizontal scroll when tabs overflow (mobile)
- Smooth scroll with hidden scrollbar

### Tabs

| Tab    | Icon | Component Path                  |
|--------|------|----------------------------------|
| 外观   | 🎨   | `SettingsTabAppearance.vue`      |
| 安全   | 🔒   | `SettingsTabSecurity.vue`        |
| Agent  | 🤖   | `SettingsTabAgent.vue`           |
| 数据   | 💾   | `SettingsTabData.vue`            |
| 关于   | ℹ️   | `SettingsTabAbout.vue`           |

## Tab Content Details

### Tab 1: 外观 (Appearance)

Unchanged from current implementation. Three theme cards: 二次元, 晴空, 暗夜.

### Tab 2: 安全 (Security)

**API Token Management:**
- Display current panel token (masked by default, eye icon to toggle visibility)
- Copy button to copy token to clipboard
- "Regenerate Token" button with confirmation dialog
- On regenerate: new token returned from API, user must confirm they've saved it

**Backend API:**
- `GET /api/auth/token-info` — return token metadata (created at, last used)
- `POST /api/auth/regenerate-token` — regenerate panel token, returns new token

### Tab 3: Agent

**Section 1: Heartbeat & Connection**
- Heartbeat interval (seconds) — number input, default from env
- Timeout (seconds) — number input
- Offline threshold (missed heartbeats) — number input
- "Save" button to persist configuration
- Values stored as master-level configuration

**Section 2: Registration & Approval**
- Display current register token (masked + copy)
- "Regenerate Register Token" button with confirmation dialog
- Registration approval toggle: auto-approve / manual approval
- Pending approvals count badge (if manual mode)

**Backend API:**
- `GET /api/settings/agent` — return heartbeat config + registration settings
- `PUT /api/settings/agent` — update heartbeat config
- `POST /api/auth/regenerate-register-token` — regenerate register token
- `PUT /api/settings/registration` — update approval mode

### Tab 4: 数据 (Data)

Redesigned with selective export/import and conflict resolution.

**Export:**
- Click "导出备份" opens a modal with category checkboxes:
  - Agents
  - HTTP 规则 (HTTP Rules)
  - L4 规则 (L4 Rules)
  - 中继监听 (Relay Listeners)
  - 证书 (Certificates)
  - 版本策略 (Version Policies)
- "全选" / "取消全选" toggle
- Only checked categories are included in the exported `.tar.gz`
- If no category selected, export button is disabled

**Import:**
- Three-stage modal flow:
1. **Upload stage** — drag-and-drop zone + file button, validates format and size
2. **Preview stage** — parses the backup file and displays:
   - Manifest info (source architecture, export time)
   - Available categories with item counts from the backup file
   - Checkboxes to select which categories to import
   - Global conflict strategy: radio group "覆盖已有" (overwrite) or "跳过冲突" (skip)
3. **Result stage** — color-coded statistics:
   - Green: imported count per category
   - Yellow: skipped conflicts (if skip mode)
   - Red: skipped invalid / missing material

**Backend API changes:**
- `GET /api/system/backup/export?categories=agents,http_rules,l4_rules` — export selected categories
- `POST /api/system/backup/import` — accepts `categories` and `conflictStrategy` fields alongside the file

### Tab 5: 关于 (About)

- Application version
- Project name
- Deployment role (master/agent)
- Local agent enabled status

## Component Architecture

```
SettingsPage.vue
├── SettingsTabBar.vue          (pill tab navigation)
├── SettingsTabAppearance.vue   (theme selection)
├── SettingsTabSecurity.vue     (API token management)
├── SettingsTabAgent.vue        (heartbeat + registration)
├── SettingsTabData.vue         (backup export/import)
│   ├── ExportModal.vue         (selective category export)
│   └── ImportModal.vue         (three-stage import flow)
└── SettingsTabAbout.vue        (system info)
```

`SettingsPage.vue` manages active tab state and renders the corresponding component. No router changes — stays as a single `/settings` route with local tab state.

## Data Flow

- Tab state: local component state in `SettingsPage.vue`
- Settings data: fetched from API on tab activation (lazy loading)
- Form saves: optimistic UI with rollback on error, toast notifications for feedback
- Token operations: always require explicit user confirmation before execution
- Backup logic moves from `SettingsPage.vue` to `SettingsTabData.vue` with new sub-components

## Error Handling

- API failures show toast notification with error message
- Form validation uses inline error messages below fields
- Token regeneration shows warning about invalidating current sessions
- Certificate rotation shows affected certificate count in confirmation dialog

## Mobile Responsiveness

- Tab bar scrolls horizontally on narrow screens
- Tab content uses full-width layout
- Form fields stack vertically on mobile
- Import modal is full-screen on mobile

## Migration Notes

- `TokenConfig.vue` is already removed, no migration needed
- Current `SettingsPage.vue` content is split across new tab components
- `ThemeContext.js` stays unchanged
- Backup logic moves from `SettingsPage.vue` to `SettingsTabData.vue`
