# Settings Page Redesign — Design Spec

Date: 2026-04-20

## Goal

Redesign the settings page from a single-page scroll layout to a left-sidebar tab navigation structure. Enhance data management with selective export and import preview. Enrich the About page with project links, dynamic version info, and system status.

## Layout

**Left sidebar navigation** with 3 tabs, right side shows tab content.

```
┌──────────────┬─────────────────────────────┐
│  ⚙️ 通用      │                             │
│  💾 数据管理  │    Tab content area          │
│  ℹ️ 关于      │                             │
└──────────────┴─────────────────────────────┘
```

- Active tab: primary color left border + subtle background tint
- Inactive tabs: secondary text color
- On mobile (< 768px): sidebar collapses to horizontal tabs above content
- Content area inherits existing card styles and design tokens

## Tab 1: 通用 (General)

### Theme Section (外观主题)

Keep current functionality, adjust layout to fit new structure:
- Theme buttons in a flex row with same selection UX (checkmark, active state, hover)
- No functional changes, only spacing/typography adaptation

### Deploy Mode Section (部署模式)

Keep current read-only display:
- Role and local agent status as info rows
- Same data source: `fetchSystemInfo()` API

## Tab 2: 数据管理 (Data Management)

### Export: Selective Export

Replace the current single-button export with a resource selection UI:

1. Show a checklist of resource types with counts fetched from the backend
2. Resource types: Agents, HTTP Rules, L4 Rules, Relay Listeners, Certificates, Version Policies
3. All checked by default; user toggles individual types
4. "Export" button sends the selected types to the backend

**Backend changes:**
- Add query parameters to `GET /system/backup/export`: `?include=agents,rules,l4_rules,relay_listeners,certificates,version_policies`
- Export service filters the bundle to only include selected types
- If a type is excluded, its dependent items should also be noted (e.g., excluding agents means rules won't reference them)

### Import: Three-Step Flow

Replace the current direct-import flow with a preview-confirm pipeline:

**Step 1 — Select File** (current behavior)
- File picker for `.tar.gz` backup files
- Show selected filename

**Step 2 — Preview & Confirm** (new)
- Backend parses the backup and returns a dry-run preview (no changes applied)
- Display: manifest info (source architecture, export time), per-type counts with conflict predictions (new / conflict / invalid)
- "Confirm Import" and "Cancel" buttons

**Step 3 — Import Results** (current behavior, enhanced)
- Summary cards (imported, skipped-conflict, skipped-invalid, skipped-missing-material)
- Detailed report sections
- Manifest metadata

**Backend changes:**
- Add `POST /system/backup/import/preview` endpoint: accepts file upload, parses and validates but does NOT write to DB, returns preview counts and conflict analysis
- Existing `POST /system/backup/import` remains for the actual import after confirmation

**Frontend state machine:**
```
idle → file_selected → previewing → preview_loaded → confirmed → importing → done
```

## Tab 3: 关于 (About)

Three card sections, all data fetched from backend:

### Project Identity
- Centered header: project name "Nginx Reverse Emby" + tagline
- Static content, no API call needed

### Version Info
- Current version (from build info)
- Build time
- Architecture
- Go version

**Backend changes:**
- Extend `GET /info` response with `version`, `build_time`, `go_version` fields
- Values injected at build time via `-ldflags`

### Project Links
- GitHub repository URL (configurable via env var, e.g., `NRE_PROJECT_URL`)
- Issues URL (derived from repo URL or configurable)
- Links open in new tab

**Backend changes:**
- Add `project_url` field to `GET /info` response (from env var or default)

### System Status
- Role (from existing SystemInfo)
- Local Agent status (from existing SystemInfo)
- Online/total agents count (new aggregation)
- Uptime (new: track server start time)
- Data directory path (from config)

**Backend changes:**
- Add fields to `GET /info` response: `started_at`, `online_agents`, `total_agents`, `data_dir`

## Component Structure

```
SettingsPage.vue
├── SettingsNav.vue          (left sidebar navigation)
├── SettingsGeneral.vue      (theme + deploy mode)
├── SettingsDataMgmt.vue     (export/import)
└── SettingsAbout.vue        (version, links, status)
```

`SettingsPage.vue` manages active tab state and renders the corresponding child component. Each child is self-contained with its own API calls and state.

## API Changes Summary

| Endpoint | Change |
|----------|--------|
| `GET /info` | Add `version`, `build_time`, `go_version`, `started_at`, `online_agents`, `total_agents`, `data_dir`, `project_url` |
| `GET /system/backup/export` | Add `include` query param for selective export |
| `POST /system/backup/import/preview` | **New** — dry-run preview endpoint |

## Mobile Responsive

- < 768px: sidebar transforms to horizontal tab bar at top
- Tab content full-width
- Export checklist stacks vertically
- Import steps remain vertical

## No Changes

- Theme system (ThemeContext, CSS variables, theme options)
- Existing backup file format (tar.gz structure)
- Auth/token handling
- Sidebar navigation (AppShell/Sidebar)
