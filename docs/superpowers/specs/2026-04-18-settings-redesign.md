# Settings Page Redesign — Design Spec

Date: 2026-04-18

## Summary

Redesign the settings page with a **single-page card layout**. Data management uses only two buttons (导出/导入), with import handled by a clean Modal following industry best practices.

## Page Structure

Single page, max-width 700px (900px on 4K), four card sections stacked vertically:

```
┌─────────────────────────────┐
│  外观主题 (Theme)            │
├─────────────────────────────┤
│  数据管理 (Data Management)  │
│  [导出备份]  [导入备份]       │
├──────────┬──────────────────┤
│ 系统信息   │ 关于             │
└──────────┴──────────────────┘
```

## Module Details

### 1. 外观主题 (Theme)

No change from current behavior. Three theme buttons in a row (二次元/晴空/暗夜), each showing a gradient preview swatch. Active theme gets a checkmark and border highlight.

### 2. 数据管理 (Data Management)

Simple card with a title, one-line description, and two buttons side-by-side:
- **导出备份**: Triggers full backup export immediately. Shows loading state on button, downloads `.tar.gz`.
- **导入备份**: Opens a Modal dialog for the import flow.

#### Import Modal (Three Stages)

A single Modal handles the entire import flow, transitioning between three stages without closing:

**Stage 1 — Upload:**
- Drag-and-drop zone with dashed border (standard pattern from Flatfile, HubSpot, etc.)
- Also supports click-to-browse
- Accepted formats shown below the zone: `.tar.gz`
- File selected → auto-advance to Stage 2

**Stage 2 — Preview & Confirm:**
- Show backup manifest info: source version, architecture, export timestamp
- Show category counts from manifest: HTTP 规则 (12), L4 规则 (5), Agent 节点 (4), 证书 (3), Relay (2), 版本策略 (1)
- Warning banner for destructive action
- Two buttons: "取消" and "确认导入"

**Stage 3 — Result:**
- Three stat blocks in a row with color coding:
  - **已导入** (green) — count
  - **冲突跳过** (yellow/orange) — count
  - **失败** (red) — count
- "完成" button closes the Modal

### 3. 系统信息 (System Info) + 关于 (About)

Two smaller cards displayed side-by-side at the bottom.

**系统信息:**
- 角色 (role)
- 本地 Agent 状态
- Agent ID (if local agent enabled)

**关于:**
- 版本号
- 架构标识
- 项目链接

## API Changes

### New Endpoint: Backup Preview

```
POST /api/system/backup/preview
Content-Type: multipart/form-data

Form fields:
  file: <backup tarball>

Response:
{
  "manifest": {
    "source_app_version": "1.0.0",
    "source_architecture": "go-control-plane",
    "exported_at": "2026-04-18T14:30:00Z",
    "counts": {
      "agents": 4,
      "http_rules": 12,
      "l4_rules": 5,
      "relay_listeners": 2,
      "certificates": 3,
      "version_policies": 1
    }
  }
}
```

Reads the backup, extracts the manifest, does NOT perform import.

### Existing Endpoints (No Changes)

- `GET /api/system/backup/export` — full export, unchanged
- `POST /api/system/backup/import` — full import, unchanged

## Component Structure

```
SettingsPage.vue
├── ThemeSection.vue
├── DataManagementSection.vue
│   └── ImportModal.vue (Modal with 3 stages)
├── SystemInfoSection.vue
└── AboutSection.vue
```

## State Management

All local to components:

- `SettingsPage`: theme selection
- `DataManagementSection`: export loading state, modal open/close
- `ImportModal`: current stage (1/2/3), selected file, manifest data, import result

## Error Handling

- Export failure: toast notification
- Import file parse failure: show error in Stage 1, allow re-upload
- Import API failure: show error in Stage 3 with retry
- Network errors: standard toast

## Mobile Responsiveness

- Cards stack full-width
- Buttons stack vertically on narrow screens
- System info + About stack vertically
- Modal is full-screen on mobile

## Design References

- Smashing Magazine: [Designing An Attractive And Usable Data Importer](https://www.smashingmagazine.com/2020/12/designing-attractive-usable-data-importer-app/)
- SaaSFrame: [Import & Export UI Examples](https://www.saasframe.io/patterns/import-export)
- Key pattern: Crew's two-button Import/Export card, Senja's drag-and-drop upload

## Out of Scope

- Selective/category import/export
- Scheduled/automatic backups
- Backup history
- Backup encryption
- Diff view
