# Modern Flutter Client Redesign — Design Spec

Date: 2026-05-01

## Overview

Complete rewrite of the Flutter client (`clients/flutter/`) with a Glassmorphism visual language, compact icon+text sidebar navigation, and 4 switchable accent color themes. Desktop-first, mobile as secondary status viewer.

## Design Decisions

| Decision | Choice |
|----------|--------|
| Visual style | Glassmorphism (frosted glass + dark gradient) |
| Navigation | Compact icon+text sidebar (desktop) |
| Color themes | Indigo Purple, Cyan Blue, Rose Pink, Emerald Green |
| Platform priority | Desktop-first (Windows/macOS/Linux), mobile secondary |
| Implementation | Full UI rewrite (keep Riverpod + GoRouter architecture) |

## Visual Language

### Background
- Dark gradient: `#0f172a` → `#1e293b` (135deg)

### Glass Cards
- `background: rgba(255, 255, 255, 0.06)`
- `backdrop-filter: blur(20px)`
- `border: 1px solid rgba(255, 255, 255, 0.08)`
- `border-radius: 12px`

### Accent Colors (4 themes, user-switchable)

| Theme | Primary | Secondary |
|-------|---------|-----------|
| Indigo Purple | `#6366f1` → `#8b5cf6` | `#818cf8` |
| Cyan Blue | `#06b6d4` → `#3b82f6` | `#22d3ee` |
| Rose Pink | `#f43f5e` → `#ec4899` | `#fb7185` |
| Emerald Green | `#10b981` → `#22c55e` | `#34d399` |

### Status Colors
- Success/Online: `#4ade80` (green)
- Warning/Expiring: `#fbbf24` (amber)
- Error/Offline: `#f87171` (red)
- Info: `#818cf8` (accent)

### Design Tokens
- Radius: 6px (small), 8px (medium), 10px (tags), 12px (cards), 14px (large cards)
- Blur: 10px (subtle), 20px (standard), 40px (heavy)
- Spacing: 4px grid (4, 8, 10, 12, 14, 16, 20)
- Surface opacity: 0.03 (disabled), 0.04 (inner), 0.06 (card), 0.08 (border), 0.10 (hover)

### Typography
- Title: 14px weight 600
- Body: 12px weight 400-500
- Metadata: 10-11px weight 400
- Label: 9px uppercase letter-spacing 0.5px
- Stat number: 22-24px weight 700

## Layout

### Desktop (>= 1200px)
- Left sidebar: 64px wide, icon + short text label
- Top bar: 48px, page title + context actions
- Content area: flexible, padded 16-20px

### Sidebar Items
| Icon | Label | Route |
|------|-------|-------|
| 📊 | 面板 | /dashboard |
| 📋 | 规则 | /rules |
| 🤖 | 代理 | /agents |
| 🔐 | 证书 | /certificates |
| 📡 | 中继 | /relay |
| ⚙️ | 设置 | /settings |

- Active item: accent background + border highlight
- Inactive item: muted, hover highlight

### Mobile (< 600px)
- Bottom navigation bar with icons
- Compact card layouts
- Status-view only (no local agent control)

## Pages

### Dashboard (/dashboard)

**Purpose:** System overview at a glance.

**Components:**
1. **Status Banner** — Gradient glass card showing system health, last sync time, link to logs
2. **Stats Grid (4 columns)** — Rules count, Online agents, Certificates, Relay count; each with sub-status text
3. **Local Agent Card** — Running status with PID, Uptime, Version, Sync delay; Stop/Restart buttons
4. **Quick Actions Grid (2x2)** — New Rule, Add Certificate, Add Agent, New Relay; each with accent-colored background

### Rules (/rules)

**Purpose:** Full CRUD management of proxy rules.

**Components:**
1. **Top bar actions** — Rule count + "+ New Rule" button
2. **Search + Filter bar** — Text search, status dropdown (All/Active/Disabled), type dropdown (All/HTTP/HTTPS/L4)
3. **Rule list** — Each item shows:
   - Icon with type-colored background
   - Domain name + type badge (HTTP/HTTPS/L4/WebSocket)
   - Target address + last updated
   - Status chip (Active green / Disabled gray)
   - Toggle switch for quick enable/disable
   - "⋯" menu for Edit/Copy/Delete
4. **Disabled rules** — Reduced opacity (0.6), muted colors

**Interactions:**
- Click rule row → edit detail view
- Toggle → immediate enable/disable with confirmation
- "+ New Rule" → slide-out panel or dialog form
- Search → real-time filter

### Agents (/agents)

**Purpose:** Monitor and control all registered agents.

**Components:**
1. **Local Agent Section** — Highlighted card at top:
   - Machine icon with gradient background
   - "This Machine" label + Running status badge
   - PID, Uptime, Version, Last sync metadata
   - Restart (amber) / Stop (red) action buttons
2. **Remote Agents Section** — Grid of cards (2 columns):
   - Agent icon + name + location
   - Online/Offline status badge
   - 2x2 info grid: Rules count, Sync delay, Version, Uptime
   - Action buttons: Details / Logs / Update Available (when version behind)

**Sync delay semantics:**
- Green (< 30s): normal
- Yellow (30s - 5min): degraded
- Red (> 5min or offline): alert

**Interactions:**
- Details → expand panel with agent's rules, config, logs
- "Update Available" → trigger agent update
- Stop/Restart → control local agent process

### Certificates (/certificates)

**Purpose:** Manage TLS certificates, track expiry, link to rules.

**Components:**
1. **Top bar actions** — Count + Import button + Request button (ACME)
2. **Expiry warning banner** — Shows count of certs expiring within 14 days; links to review
3. **Certificate cards:**
   - Icon with status-colored background (green=valid, amber=expiring)
   - Domain + status badge (Valid/Expiring/Self-signed)
   - CA name + issue date
   - Remaining days with number + "remaining" label
   - Progress bar showing time elapsed (accent color matches status)
   - "Used by" tags listing associated rules
   - Action buttons: Renew (expiring only) / Details
4. **Expiring certs** — Yellow border, Renew button prominent

**Interactions:**
- Import → file picker for .pem/.crt/.key upload
- Request → ACME enrollment form
- Renew → trigger renewal for Let's Encrypt certs
- Details → full cert info (chain, SANs, fingerprint)

## Architecture

### Directory Structure
```
lib/
├── app.dart                     # MaterialApp + theme injection
├── main.dart                    # Entry point
├── core/
│   ├── design/
│   │   ├── tokens/              # Color tokens, spacing, radius, blur
│   │   ├── components/          # GlassCard, GlassChip, GlassToggle, etc.
│   │   └── theme/               # 4 theme definitions + ThemeController
│   ├── network/                 # API client (Dio)
│   ├── routing/                 # GoRouter with sidebar shell
│   └── platform/                # PlatformCapabilities
├── features/
│   ├── auth/                    # Connect wizard + registration
│   ├── dashboard/               # Dashboard screen + providers
│   ├── rules/                   # Rules CRUD + providers
│   ├── agents/                  # Agent management + providers
│   ├── certificates/            # Certificate management + providers
│   ├── relay/                   # Relay listeners + providers
│   └── settings/                # Theme picker, disconnect, app info
├── l10n/                        # Localization (en, zh)
└── shell/                       # MainShell (sidebar + topbar + content slot)
```

### State Management
- Riverpod with code generation (`@riverpod`)
- AsyncValue for loading/error states
- SharedPreferences for theme persistence

### Routing
- GoRouter with ShellRoute for authenticated layout
- Route guard redirects to `/connect` if unauthenticated
- Platform-aware routes (mobile omits Certificates, Relay)

### Key Components (to build)
- `GlassCard` — Frosted glass card with configurable blur, border, radius
- `GlassChip` — Status badge with gradient background
- `GlassToggle` — Toggle switch with accent gradient
- `StatCard` — Dashboard stat with label + large number + sub-text
- `InfoGrid` — 2x2 info grid for agent details
- `ExpiryBar` — Progress bar for certificate expiry
- `SearchBar` — Glassmorphism search input
- `FilterDropdown` — Glassmorphism dropdown button

## Additional Pages

These pages adopt the glassmorphism styling but follow established patterns:

### Connect Wizard (/connect)
- Keep current 3-step flow (Master URL → Register Token → Client Name)
- Restyle with glassmorphism cards and accent gradient buttons
- Dark gradient background consistent with main app

### Settings (/settings)
- Theme picker: 4 accent color swatches + light/dark/system toggle
- Disconnect button
- App info (version, build)
- Glass card sections

### Relay (/relay)
- Same card-list pattern as Rules
- Each relay shows: listen address, protocol, status, associated agent
- Create/Edit/Delete actions

## Localization
- Maintain existing en/zh support
- All new UI strings must have l10n keys
- Remove all hardcoded strings

## Migration Notes
- Delete legacy `screens/` directory
- Delete legacy duplicate state models
- Consolidate network layer to single Dio-based API client
- Keep existing `pubspec.yaml` dependencies (Riverpod, GoRouter, Dio)
- Add `window_manager` for desktop window management
