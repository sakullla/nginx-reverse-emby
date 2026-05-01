# Modern Flutter Client Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite Flutter client UI with Glassmorphism design language, compact sidebar navigation, and 4 switchable accent color themes.

**Architecture:** Feature-first structure with shared design system in `core/design/`. Riverpod state management, GoRouter navigation, Dio networking. Desktop-first with responsive mobile fallback.

**Tech Stack:** Flutter 3.11+, Riverpod 2.6, GoRouter 14.8, Dio 5.8, window_manager 0.4

---

## File Structure

```
lib/
├── app.dart
├── main.dart
├── core/
│   ├── design/
│   │   ├── tokens/
│   │   │   ├── app_colors.dart          # Color constants (status, surface, 4 themes)
│   │   │   ├── app_spacing.dart         # Spacing, radius, blur tokens
│   │   │   └── app_typography.dart      # Text style constants
│   │   ├── components/
│   │   │   ├── glass_card.dart          # Frosted glass card
│   │   │   ├── glass_chip.dart          # Status badge
│   │   │   ├── glass_toggle.dart        # Toggle switch
│   │   │   ├── glass_search_bar.dart    # Search input
│   │   │   ├── glass_button.dart        # Glass button variants
│   │   │   ├── stat_card.dart           # Dashboard stat card
│   │   │   ├── info_grid.dart           # 2x2 info grid
│   │   │   ├── expiry_bar.dart          # Certificate progress bar
│   │   │   └── page_header.dart         # Top bar with title + actions
│   │   └── theme/
│   │       ├── accent_themes.dart       # 4 accent theme definitions
│   │       ├── glass_theme_data.dart    # ThemeData with glassmorphism
│   │       └── theme_controller.dart    # Riverpod theme notifier
│   ├── network/                         # Keep existing, no changes
│   ├── routing/
│   │   ├── route_names.dart
│   │   └── app_router.dart              # Rewrite shell layout
│   └── platform/                        # Keep existing
├── shell/
│   ├── main_shell.dart                  # Shell scaffold
│   ├── sidebar.dart                     # Compact icon+text sidebar
│   └── topbar.dart                      # Page header bar
├── features/
│   ├── auth/                            # Restyle connect screen
│   ├── dashboard/                       # Full redesign
│   ├── rules/                           # Full redesign + CRUD
│   ├── agents/                          # Full redesign
│   ├── certificates/                    # New implementation
│   ├── relay/                           # New implementation
│   └── settings/                        # Restyle
├── l10n/                                # Update ARB files
└── shared/                              # Delete after migration
```

---

### Task 1: Design Tokens

**Files:**
- Create: `lib/core/design/tokens/app_colors.dart`
- Create: `lib/core/design/tokens/app_spacing.dart`
- Create: `lib/core/design/tokens/app_typography.dart`

- [ ] **Step 1:** Create `app_colors.dart` with surface colors, status colors, and 4 accent theme color definitions
- [ ] **Step 2:** Create `app_spacing.dart` with spacing grid, radius, blur constants
- [ ] **Step 3:** Create `app_typography.dart` with text style constants
- [ ] **Step 4:** Commit — `feat(design): add design tokens`

### Task 2: Theme System

**Files:**
- Create: `lib/core/design/theme/accent_themes.dart`
- Create: `lib/core/design/theme/glass_theme_data.dart`
- Create: `lib/core/design/theme/theme_controller.dart`

- [ ] **Step 1:** Create `accent_themes.dart` — 4 AccentTheme definitions (indigo, cyan, rose, emerald) each with primary/secondary gradients and derived colors
- [ ] **Step 2:** Create `glass_theme_data.dart` — Build ThemeData from AccentTheme: dark scheme, card/surface colors, navigation rail/bar themes with glassmorphism styling
- [ ] **Step 3:** Create `theme_controller.dart` — Riverpod Notifier, persist selected theme mode + accent to SharedPreferences, expose current ThemeData
- [ ] **Step 4:** Update `app.dart` to use new theme controller and background gradient
- [ ] **Step 5:** Delete old `core/theme/` files
- [ ] **Step 6:** Commit — `feat(theme): glassmorphism theme system with 4 accent colors`

### Task 3: Glass Components

**Files:**
- Create: `lib/core/design/components/glass_card.dart`
- Create: `lib/core/design/components/glass_chip.dart`
- Create: `lib/core/design/components/glass_toggle.dart`
- Create: `lib/core/design/components/glass_button.dart`
- Create: `lib/core/design/components/glass_search_bar.dart`
- Create: `lib/core/design/components/stat_card.dart`
- Create: `lib/core/design/components/info_grid.dart`
- Create: `lib/core/design/components/expiry_bar.dart`
- Create: `lib/core/design/components/page_header.dart`

- [ ] **Step 1:** Create `glass_card.dart` — ClipRRect + BackdropFilter + Container with configurable blur, opacity, radius, optional gradient border
- [ ] **Step 2:** Create `glass_chip.dart` — Small badge with accent/status color gradient background, text label
- [ ] **Step 3:** Create `glass_toggle.dart` — Custom toggle with accent gradient when on, glass surface when off
- [ ] **Step 4:** Create `glass_button.dart` — Primary (accent gradient), secondary (glass surface), danger (red glass) variants
- [ ] **Step 5:** Create `glass_search_bar.dart` — Glass surface input with search icon and hint text
- [ ] **Step 6:** Create `stat_card.dart` — GlassCard with label (9px uppercase), large number (22px bold), sub-text
- [ ] **Step 7:** Create `info_grid.dart` — 2x2 grid of small info cells (label + value)
- [ ] **Step 8:** Create `expiry_bar.dart` — Rounded progress bar with status-colored fill
- [ ] **Step 9:** Create `page_header.dart` — Row with title (14px bold) + optional actions slot + connection status badge
- [ ] **Step 10:** Delete old `shared/widgets/` files
- [ ] **Step 11:** Commit — `feat(design): glassmorphism component library`

### Task 4: Shell Layout (Sidebar + Topbar)

**Files:**
- Create: `lib/shell/sidebar.dart`
- Create: `lib/shell/topbar.dart`
- Create: `lib/shell/main_shell.dart`
- Modify: `lib/core/routing/app_router.dart`

- [ ] **Step 1:** Create `sidebar.dart` — 64px wide, vertical icon+text items, active state with accent highlight, Settings at bottom, Logo at top
- [ ] **Step 2:** Create `topbar.dart` — 48px height, PageHeader integration
- [ ] **Step 3:** Create `main_shell.dart` — Row: Sidebar + Expanded(Column: Topbar + Content), background gradient
- [ ] **Step 4:** Rewrite `app_router.dart` — Use MainShell as ShellRoute builder, platform-aware sidebar items, mobile bottom nav fallback
- [ ] **Step 5:** Commit — `feat(shell): compact sidebar with glassmorphism shell`

### Task 5: Connect Wizard Restyle

**Files:**
- Modify: `lib/features/auth/presentation/screens/connect_screen.dart`

- [ ] **Step 1:** Restyle connect screen with dark gradient background, glass cards for each step, accent gradient Next/Back buttons
- [ ] **Step 2:** Commit — `feat(auth): glassmorphism connect wizard`

### Task 6: Dashboard Redesign

**Files:**
- Rewrite: `lib/features/dashboard/presentation/screens/dashboard_screen.dart`

- [ ] **Step 1:** Build status banner — gradient glass card with system health + last sync + log link
- [ ] **Step 2:** Build stats grid — 4 StatCards in a row (Rules, Agents, Certificates, Relay)
- [ ] **Step 3:** Build local agent card — highlighted glass card with PID/Uptime/Version/Sync + Stop/Restart GlassButtons
- [ ] **Step 4:** Build quick actions grid — 2x2 action cards with colored glass backgrounds
- [ ] **Step 5:** Assemble full dashboard screen with PageHeader + scrollable content
- [ ] **Step 6:** Commit — `feat(dashboard): glassmorphism redesign`

### Task 7: Rules Redesign

**Files:**
- Rewrite: `lib/features/rules/presentation/screens/rules_list_screen.dart`
- Create: `lib/features/rules/presentation/screens/rule_form_dialog.dart`
- Modify: `lib/features/rules/presentation/providers/rules_provider.dart`

- [ ] **Step 1:** Build search + filter bar — GlassSearchBar + two FilterDropdowns (status, type)
- [ ] **Step 2:** Build rule list item — icon + domain + type badge + target + status chip + GlassToggle + overflow menu
- [ ] **Step 3:** Add disabled state styling — opacity 0.6, muted colors
- [ ] **Step 4:** Create rule form dialog — slide-out panel with domain/target/type fields in glass styling
- [ ] **Step 5:** Wire up CRUD — connect to rules_provider for create/edit/delete/toggle with real API calls
- [ ] **Step 6:** Commit — `feat(rules): glassmorphism redesign with CRUD`

### Task 8: Agents Redesign

**Files:**
- Rewrite: `lib/features/agents/presentation/screens/agents_screen.dart`

- [ ] **Step 1:** Build local agent section — highlighted glass card with process info and control buttons
- [ ] **Step 2:** Build remote agent card — grid card with name, location, status badge, InfoGrid, action buttons
- [ ] **Step 3:** Add sync delay color semantics — green/yellow/red based on delay threshold
- [ ] **Step 4:** Add "Update Available" button styling for outdated agents
- [ ] **Step 5:** Commit — `feat(agents): glassmorphism redesign`

### Task 9: Certificates Implementation

**Files:**
- Rewrite: `lib/features/certificates/presentation/screens/certificates_screen.dart`
- Create: `lib/features/certificates/data/models/certificate_models.dart`
- Create: `lib/features/certificates/presentation/providers/certificates_provider.dart`

- [ ] **Step 1:** Create certificate models — Certificate domain model with domain, ca, issueDate, expiryDate, status, associatedRules, isSelfSigned
- [ ] **Step 2:** Create certificates provider — Riverpod AsyncNotifier with list/filter/toggle, connect to API
- [ ] **Step 3:** Build expiry warning banner — amber glass banner showing expiring count
- [ ] **Step 4:** Build certificate card — icon + domain + badges + dates + ExpiryBar + "Used by" tags + action buttons
- [ ] **Step 5:** Add Import/Request top bar buttons
- [ ] **Step 6:** Commit — `feat(certificates): glassmorphism implementation`

### Task 10: Settings Restyle

**Files:**
- Rewrite: `lib/features/settings/presentation/screens/settings_screen.dart`

- [ ] **Step 1:** Build glass section cards for Appearance, Connection, About
- [ ] **Step 2:** Build theme picker — 4 color swatches with active indicator + light/dark/system radio
- [ ] **Step 3:** Build disconnect button with danger GlassButton
- [ ] **Step 4:** Commit — `feat(settings): glassmorphism restyle`

### Task 11: Relay Implementation

**Files:**
- Rewrite: `lib/features/relay/presentation/screens/relay_screen.dart`
- Create: `lib/features/relay/data/models/relay_models.dart`
- Create: `lib/features/relay/presentation/providers/relay_provider.dart`

- [ ] **Step 1:** Create relay models and provider
- [ ] **Step 2:** Build relay list following Rules card pattern — listen address, protocol, status, agent
- [ ] **Step 3:** Commit — `feat(relay): glassmorphism implementation`

### Task 12: Localization & Cleanup

**Files:**
- Modify: `lib/l10n/app_en.arb`
- Modify: `lib/l10n/app_zh.arb`
- Delete: `lib/screens/` (legacy)
- Delete: `lib/shared/` (old widgets, already replaced)
- Delete: `lib/core/platform_capabilities.dart` (duplicate)

- [ ] **Step 1:** Update ARB files with all new UI strings, ensure zh translations are complete
- [ ] **Step 2:** Run `flutter gen-l10n` to regenerate
- [ ] **Step 3:** Delete legacy files
- [ ] **Step 4:** Run `flutter analyze` and fix any issues
- [ ] **Step 5:** Run `flutter build windows` to verify desktop build
- [ ] **Step 6:** Commit — `feat(l10n): update translations and cleanup legacy files`
