# Settings Page Redesign — Design Spec

Date: 2026-04-18

## Summary

Redesign the settings page with a **single-page card layout** (方案 B: 分区扩展型). The data management module becomes the core section with selective import/export and a three-step import wizard that expands inline within the card.

## Page Structure

Single page, max-width 700px (900px on 4K), four card sections stacked vertically:

```
┌─────────────────────────────┐
│  外观主题 (Theme)            │
├─────────────────────────────┤
│  数据管理 (Data Management)  │  ← core section, blue border accent
│  ┌─────────┐ ┌──────────┐   │
│  │一键导出  │ │ 导入备份  │   │
│  └─────────┘ └──────────┘   │
│  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─  │
│  选择性导出: checkboxes      │
│  [导出所选]                  │
│  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─  │
│  (import wizard expands here)│
├──────────┬──────────────────┤
│ 系统信息   │ 关于             │
└──────────┴──────────────────┘
```

## Module Details

### 1. 外观主题 (Theme)

No change from current behavior. Three theme buttons in a row (二次元/晴空/暗夜), each showing a gradient preview swatch. Active theme gets a checkmark and border highlight.

### 2. 数据管理 (Data Management) — Core Section

Visually emphasized with a blue border accent and "核心区域" badge.

#### Quick Actions

Two action cards side-by-side:
- **一键导出**: Triggers full backup export (existing `GET /api/system/backup/export`). Downloads `.tar.gz` immediately.
- **导入备份**: Expands the three-step import wizard inline.

#### Selective Export

Below a dashed separator:
- Six category checkboxes with item counts fetched from the manifest/system state: HTTP 规则, L4 规则, 证书, Agent 节点, Relay 监听器, 版本策略
- "导出所选" button — sends the selected categories to a new API endpoint

#### Import Wizard (Inline Expand)

When "导入备份" is clicked, the card expands to show a three-step wizard with a step indicator at the top. The wizard replaces the quick actions area within the card.

**Step 1 — Upload File:**
- Drag-and-drop zone or click to browse
- Accepts `.tar.gz`, `.tgz`, `.gz`
- On file selected, parse the backup manifest and advance to step 2

**Step 2 — Preview & Select:**
- Display source info: architecture, version, export timestamp (from manifest)
- List all data categories with counts from the backup
- Each category has a checkbox — user selects which to import
- Certificate entries show "(含私钥)" badge when materials are present
- "下一步" button advances to step 3

**Step 3 — Confirm & Result:**
- "确认导入" button triggers the import
- During import: loading spinner
- After import: result summary with three stat blocks:
  - **已导入** (green) — successfully imported count
  - **冲突跳过** (yellow) — items skipped due to existing conflicts
  - **失败** (red) — items that failed validation
- "完成" button collapses the wizard back to the normal card view

### 3. 系统信息 (System Info) + 关于 (About)

Two smaller cards displayed side-by-side at the bottom, each taking half width.

**系统信息:**
- 角色 (role)
- 本地 Agent 状态
- Agent ID (if local agent enabled)

**关于:**
- 版本号
- 架构标识
- 项目链接

## API Changes

### New Endpoint: Selective Export

```
POST /api/system/backup/export
Content-Type: application/json

Request body:
{
  "categories": ["http_rules", "l4_rules", "agents", "relay_listeners", "certificates", "version_policies"]
}

Response: Same as GET export — gzipped tarball with Content-Disposition header
```

If `categories` is omitted or empty, export everything (backward compatible with existing GET export).

### Existing Endpoints (No Changes)

- `GET /api/system/backup/export` — full export, unchanged
- `POST /api/system/backup/import` — full import, unchanged. The frontend will still use this endpoint; selective filtering happens client-side by only sending selected categories to a new endpoint, or the backend can accept a `categories` filter.

### New Endpoint: Selective Import

```
POST /api/system/backup/import
Content-Type: multipart/form-data

Form fields:
  file: <backup tarball>
  categories: ["http_rules", "l4_rules", "agents"]  (optional JSON array)

Response: Same BackupImportResult structure
```

If `categories` is omitted, import everything (backward compatible). If provided, only import the specified categories.

### New Endpoint: Backup Manifest Preview

```
POST /api/system/backup/preview
Content-Type: multipart/form-data

Form fields:
  file: <backup tarball>

Response:
{
  "manifest": { ... BackupManifest ... },
  "categories": {
    "http_rules": { "count": 12 },
    "l4_rules": { "count": 5 },
    "agents": { "count": 4 },
    "relay_listeners": { "count": 2 },
    "certificates": { "count": 3, "has_materials": true },
    "version_policies": { "count": 1 }
  }
}
```

This endpoint reads the backup, extracts the manifest and category counts, but does **not** perform any import. Used for step 2 preview.

## Component Structure

```
SettingsPage.vue
├── ThemeSection.vue          (existing, minimal changes)
├── DataManagementSection.vue (new, replaces inline data management)
│   ├── SelectiveExport.vue   (checkboxes + export button)
│   └── ImportWizard.vue      (three-step inline wizard)
│       ├── ImportStepUpload.vue
│       ├── ImportStepPreview.vue
│       └── ImportStepResult.vue
├── SystemInfoSection.vue     (extracted from existing)
└── AboutSection.vue          (extracted from existing)
```

## State Management

All state is local to the components (no Pinia store needed):

- `SettingsPage`: theme selection state
- `DataManagementSection`: wizard open/close state, export loading state
- `ImportWizard`: current step (1/2/3), selected file, manifest preview data, import result
- `SelectiveExport`: checked categories, loading state

## Error Handling

- Export errors: toast notification via messageStore
- Import errors:
  - File parse failure: show error in step 1, allow re-selecting file
  - Import API failure: show error in step 3 with retry option
  - Partial import: show the result report with actual counts (backend returns what succeeded/failed)
- Network errors: standard toast notification

## Mobile Responsiveness

- Cards stack full-width on mobile
- Quick action buttons stack vertically on narrow screens
- System info + About cards stack vertically on mobile
- Import wizard maintains full width within card

## Out of Scope

- Scheduled/automatic backups
- Backup history list
- Backup encryption
- Diff/comparison view between current and backup data
- Export specific individual items (only category-level selection)
