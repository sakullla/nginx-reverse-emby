# Settings Page Redesign Spec

Date: 2026-04-19

## Overview

Redesign the system settings page from a single-page card layout to a Tab-based layout with scrollable pill-style navigation. Add three new settings categories: Security (API Token), Agent Management (heartbeat + registration), and Certificate Rotation (full management panel).

## Goals

- Reorganize settings into clear Tab categories
- Add API Token management
- Add Agent heartbeat configuration and registration/approval settings
- Add self-signed certificate management panel with rotation
- Improve import flow with three-stage modal
- Keep existing functionality (theme, backup, about) intact

## Page Structure

### Overall Layout

```
┌──────────────────────────────────────────┐
│  系统设置                                │
├──────────────────────────────────────────┤
│  [外观] [安全] [Agent] [证书] [数据] [关于]│  ← pill tabs, scrollable
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
| 证书   | 📜   | `SettingsTabCertificate.vue`     |
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

### Tab 4: 证书 (Certificate)

**Section 1: Certificate Status Card**
- Display: CN, Issuer, Valid From, Valid To, Days Remaining
- Status indicator: green (healthy) / yellow (expiring soon <30d) / red (expired)
- Auto-refresh status

**Section 2: Rotation Policy**
- Auto-rotation toggle (switch)
- Rotation period selector: 30 / 90 / 180 / 365 days
- Next rotation date display (computed from last rotation + period)

**Section 3: Manual Rotation**
- "Rotate Now" button with confirmation dialog
- Shows which certificates will be affected

**Section 4: Rotation History**
- Recent 10 rotation records
- Each record: timestamp, trigger type (auto/manual), result (success/failed), affected count

**Backend API:**
- `GET /api/settings/certificate` — certificate status + rotation policy + history
- `PUT /api/settings/certificate/policy` — update rotation policy
- `POST /api/settings/certificate/rotate` — trigger manual rotation

### Tab 5: 数据 (Data)

Keep current export/import functionality with upgrade:

**Export:** Unchanged — downloads `.tar.gz` backup.

**Import:** Three-stage modal flow:
1. **Upload stage** — drag-and-drop zone, file validation
2. **Preview stage** — show manifest info and import summary before applying
3. **Result stage** — color-coded statistics (green imported, yellow conflicts, red failures)

### Tab 6: 关于 (About)

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
├── SettingsTabCertificate.vue  (certificate management)
├── SettingsTabData.vue         (backup export/import)
└── SettingsTabAbout.vue        (system info)
```

`SettingsPage.vue` manages active tab state and renders the corresponding component. No router changes — stays as a single `/settings` route with local tab state.

## Data Flow

- Tab state: local component state in `SettingsPage.vue`
- Settings data: fetched from API on tab activation (lazy loading)
- Form saves: optimistic UI with rollback on error, toast notifications for feedback
- Token operations: always require explicit user confirmation before execution

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
