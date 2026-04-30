# Flutter Client Complete Redesign — Design Document

**Date:** 2026-04-30  
**Scope:** Feature expansion, UI/UX overhaul, architecture rebuild  
**Platforms:** Windows, macOS, Android (with feature tiering)  
**Style:** Material 3 with anime/二次元 visual theme  

---

## 1. Overview & Goals

### Current State
The existing Flutter client (`clients/flutter/`) has four screens with limited functionality:
- **Dashboard**: Read-only overview of connection and local agent status
- **Agent**: Registration to master + local agent process control + logs
- **Rules**: Read-only rule list
- **Settings**: Basic configuration, theme toggle, data management

State management is scattered across `StatefulWidget`s, API calls are inconsistent (`HttpClient` in RulesScreen, custom `MasterApi` in AgentScreen), and the architecture is flat.

### Target State
Transform the Flutter client into a **full-featured multi-platform management console** with:
- Complete CRUD for rules, certificates, agents, and relay listeners (desktop)
- Read-only + light management for rules and agents (mobile)
- Polished Material 3 UI with anime/二次元 color themes
- Clean architecture with feature-first organization
- Unified state management via Riverpod + AsyncValue
- Comprehensive error handling and testing coverage

### Success Criteria
1. Desktop users can perform all management operations without opening the web panel
2. Mobile users get a lightweight, fast read-only + toggle experience
3. Visual design is cohesive, animated, and reflects the anime theme
4. Architecture supports adding new features with minimal friction
5. Unit test coverage >= 80%, widget tests cover all major screens

---

## 2. Overall Architecture

### 2.1 Directory Structure (Feature-First)

```
lib/
├── main.dart                    # Entry point: platform init, run app
├── app.dart                     # MaterialApp, theme, router root
│
├── core/                        # Infrastructure layer (app-wide)
│   ├── constants/               # API paths, timeouts, version info
│   ├── exceptions/              # Unified exception hierarchy
│   ├── logger/                  # Structured logging (replaces app_logger)
│   ├── platform/                # Platform capability detection
│   │   └── platform_capabilities.dart
│   ├── routing/                 # go_router configuration
│   │   ├── app_router.dart      # Route definitions + ShellRoute
│   │   └── route_names.dart     # Typed route constants
│   ├── theme/                   # Multi-theme system (anime style)
│   │   ├── app_theme.dart       # ThemeData builder
│   │   ├── theme_controller.dart # Riverpod theme state
│   │   ├── color_schemes.dart   # Preset color schemes
│   │   └── theme_preview.dart   # Theme preview widget
│   └── network/                 # Unified network layer
│       ├── dio_client.dart      # Dio config + interceptors
│       ├── api_client.dart      # Abstract API interface
│       ├── master_api.dart      # Concrete API implementation
│       └── models/              # Shared data models (Freezed)
│           └── *.freezed.dart
│
├── features/                    # Feature modules
│   ├── auth/                    # Connection / registration management
│   │   ├── data/
│   │   │   ├── models/
│   │   │   └── repositories/
│   │   └── presentation/
│   │       ├── providers/
│   │       └── screens/
│   ├── dashboard/               # Overview dashboard
│   ├── rules/                   # Proxy rule CRUD
│   ├── certificates/            # SSL certificate management
│   ├── agents/                  # Agent list + local process control
│   ├── relay/                   # Relay listener management
│   └── settings/                # App settings + theme + about
│
└── shared/                      # Shared UI components
    ├── widgets/                 # Generic widgets (NreCard, NreEmptyState, etc.)
    └── extensions/              # Flutter extensions (BuildContext, Theme)
```

Each feature follows the internal structure:
```
feature_name/
├── data/
│   ├── models/                  # Freezed models
│   └── repositories/            # Repository interface + implementation
├── domain/                      # Business logic (optional)
│   └── entities/
└── presentation/
    ├── providers/               # Riverpod providers
    ├── screens/                 # Pages
    └── widgets/                 # Feature-private widgets
```

### 2.2 Dependency Rules

- `core/` must not depend on any `feature/`
- `feature/` modules should not depend on each other; communicate via `core/models` or events
- `shared/` contains pure UI components without business logic
- Data flows: `presentation` -> `providers` -> `repositories` -> `data sources`

---

## 3. Feature Module Design

### 3.1 `auth` — Connection Management

**Responsibility**: Manage the client-to-master connection lifecycle.

**Screens**:
- **Connection Wizard** (fullscreen, when unauthenticated):
  - Step 1: Master URL input with validation and history dropdown
  - Step 2: Register token input (paste/scan support)
  - Step 3: Client naming
  - Step 4: Success confirmation with connection summary
- **Connection Status Card** (when authenticated):
  - Master URL, Agent ID, health indicator, disconnect button

**Platform**: Identical on desktop and mobile.

**Global Guard**: Non-Dashboard routes show "Please connect first" empty state when unauthenticated. Router auto-redirects to `/connect`.

### 3.2 `dashboard` — Overview Dashboard

**Responsibility**: At-a-glance system status with quick actions.

**Desktop Layout**:
- Statistics card row (2x2 grid):
  - Connection status (colored indicator)
  - Total rules count (large typography + trend icon)
  - Online agents count (local + remote)
  - Certificate count
- Local Agent Control card:
  - Status + PID + quick Start/Stop/Restart buttons
  - Recent log preview (click to expand)
- Quick action grid: shortcuts to Rules, Certificates, Agents
- System info footer: version, platform, last sync time

**Mobile Layout**:
- Same statistics but horizontal scroll cards
- Remove Local Agent Control (not supported on Android)
- Keep quick actions and system info

### 3.3 `rules` — Rule Management (Core Feature)

**Responsibility**: HTTP/L4 proxy rule CRUD.

**Desktop** (full CRUD):
- **List page**: Card list with toolbar (search, filter by type/domain, bulk enable/disable/delete)
  - Each card: domain, target, type tag (HTTP/L4), enable toggle, edit/delete actions
- **Create/Edit page**: Full form (domain/host, target, type selector, enable state, advanced options)
- **Detail page**: Bottom sheet / side panel with full config

**Mobile** (lightweight):
- Read-only list with search
- Single-rule enable/disable toggle only
- No create/edit/delete (desktop-only operations)

### 3.4 `certificates` — Certificate Management

**Responsibility**: SSL certificate upload, view, delete.

**Desktop Only**:
- List: name, domains, expiry date (with warning if near expiry), type
- Upload form: name, certificate content, private key content
- Detail: full content display with copy button

**Mobile**: Hidden from navigation.

### 3.5 `agents` — Agent Management

**Responsibility**: Remote agent list + local agent process control.

**Desktop**:
- **Remote Agents Tab**: Card list from master (name, platform icon, version, online status, last heartbeat)
  - Detail page: full info, rule sync status
- **Local Agent Tab**:
  - Process status (running/stopped/unavailable)
  - Control buttons: Start, Stop, Restart
  - Real-time log viewer (search, auto-scroll, clear, copy)
  - Configuration display

**Mobile**:
- Remote Agents list only (read-only)
- No Local Agent tab (Android cannot manage local agent processes)

### 3.6 `relay` — Relay Listener Management

**Responsibility**: TCP relay listener configuration.

**Desktop Only**:
- List: listen address, target address, status
- Create/Edit form: listen port, target address, enable state

**Mobile**: Hidden from navigation.

### 3.7 `settings` — App Settings

**Responsibility**: Configuration and personalization.

**Sections**:
- **Appearance**: Theme mode (system/light/dark), color scheme selector (Sakura Pink / Electric Cyan / Neon Violet / Cyber Green)
- **Language**: Locale switch (zh/en)
- **Connection**: Current connection info, disconnect action
- **Data**: Export config, clear all data (with confirmation dialog)
- **System** (desktop): Start at login, local storage path, minimize to tray
- **About**: Version, build info, license

### 3.8 Navigation Structure

**Desktop** (7 items, left NavigationRail):
```
Dashboard → Rules → Certificates → Agents → Relay → Settings
```

**Mobile** (4 items, bottom NavigationBar):
```
Dashboard → Rules → Agents → Settings
```
Certificates and Relay are hidden from primary navigation on mobile.

**Deep Linking** (go_router):
- `/dashboard` → Dashboard
- `/rules` → Rules list
- `/rules/:id` → Rule detail
- `/rules/edit/:id` → Rule edit
- `/certificates` → Certificates list
- `/agents` → Agents list
- `/agents/local` → Local agent control (desktop)
- `/relay` → Relay listeners
- `/settings` → Settings
- `/settings/theme` → Theme settings
- `/connect` → Connection wizard (redirects here when unauthenticated)

---

## 4. Theme System (Anime / 二次元 Style)

### 4.1 Color Presets

| Theme | Primary (Light) | Primary (Dark) | Secondary | Vibe |
|-------|----------------|----------------|-----------|------|
| **Sakura Pink** | `#EC407A` | `#F48FB1` | `#FF80AB` | Warm, energetic |
| **Electric Cyan** | `#00BCD4` | `#00E5FF` | `#18FFFF` | Tech, crisp |
| **Neon Violet** | `#7C4DFF` | `#B388FF` | `#E040FB` | Mystic, cool |
| **Cyber Green** | `#00E676` | `#69F0AE` | `#76FF03` | Fresh, vibrant |

### 4.2 Anime Style Adjustments

Applied on top of standard Material 3:

- **Cards**: Border radius 20px (larger than default), subtle colored shadow (`primary.withAlpha(38)`), optional 4px gradient accent bar at top
- **Buttons**: Large border radius (16px), generous padding, desktop hover scale effect (1.02x)
- **Status chips**: Capsule shape with gradient background, high saturation, thin border
- **Loading indicators**: Theme-colored `CircularProgressIndicator` with optional glow effect
- **Dark mode background**: Deep blue-gray (`#0F172A`) instead of pure black for depth
- **Light mode background**: Off-white (`#FAFAFA`) with subtle surface layers
- **Empty states**: Theme-colored circular icon background (never gray), large icons
- **Input focus**: Colored glow border via `BoxShadow`

### 4.3 Theme Architecture

```dart
// core/theme/theme_controller.dart
@riverpod
class ThemeController extends _$ThemeController {
  @override
  ThemeSettings build() => _loadFromPrefs();

  void setThemeMode(ThemeMode mode) { ... }
  void setColorScheme(AppColorScheme scheme) { ... }
}

// core/theme/app_theme.dart
ThemeData buildTheme(AppColorScheme scheme, Brightness brightness) {
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme.toColorScheme(brightness),
    cardTheme: CardThemeData(
      elevation: 2,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
      shadowColor: scheme.primary.withValues(alpha: 0.15),
    ),
    // ... additional component themes
  );
}
```

Theme settings are persisted to local storage and restored on app launch.

---

## 5. Data Layer & State Management

### 5.1 Unified Network Layer

All API calls go through a single `ApiClient` backed by Dio:

```dart
// core/network/dio_client.dart
class DioClient {
  final Dio _dio;

  DioClient({required String baseUrl, required String token}) {
    _dio = Dio(BaseOptions(
      baseUrl: baseUrl,
      connectTimeout: const Duration(seconds: 10),
      receiveTimeout: const Duration(seconds: 10),
      headers: {'Authorization': 'Bearer $token'},
    ));

    _dio.interceptors.addAll([
      LogInterceptor(requestBody: true, responseBody: true),
      ErrorInterceptor(),
      ConnectivityInterceptor(),
    ]);
  }
}

// core/network/api_client.dart — abstract interface
abstract class ApiClient {
  Future<List<ProxyRule>> getRules();
  Future<ProxyRule> createRule(CreateRuleRequest request);
  Future<ProxyRule> updateRule(String id, UpdateRuleRequest request);
  Future<void> deleteRule(String id);
  Future<void> toggleRule(String id, bool enabled);

  Future<List<Certificate>> getCertificates();
  Future<List<Agent>> getAgents();
  Future<Agent> registerAgent(RegisterRequest request);
  Future<void> unregisterAgent(String id);

  Future<LocalAgentStatus> getLocalAgentStatus();
  Future<LocalAgentStatus> startLocalAgent();
  Future<LocalAgentStatus> stopLocalAgent();
}
```

All data models use Freezed for immutability and JSON serialization.

### 5.2 Riverpod State Management Pattern

Each feature uses `AsyncNotifier` for list/state data:

```dart
// features/rules/presentation/providers/rules_provider.dart
@riverpod
class RulesList extends _$RulesList {
  @override
  Future<List<ProxyRule>> build() async {
    final api = ref.watch(apiClientProvider);
    return api.getRules();
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(
      () => ref.read(apiClientProvider).getRules(),
    );
  }

  Future<void> toggleRule(String id, bool enabled) async {
    // Optimistic update
    final previous = state.value ?? [];
    state = AsyncData(previous.map((r) =>
      r.id == id ? r.copyWith(enabled: enabled) : r
    ).toList());

    try {
      await ref.read(apiClientProvider).toggleRule(id, enabled);
    } catch (e) {
      state = AsyncData(previous); // Rollback on failure
      rethrow;
    }
  }
}
```

UI layer consumes via `AsyncValue` pattern:

```dart
final rulesAsync = ref.watch(rulesListProvider);

return rulesAsync.when(
  data: (rules) => rules.isEmpty
    ? NreEmptyState(title: 'No Rules', message: '...')
    : RulesListView(rules: rules),
  loading: () => const NreSkeletonList(itemCount: 6),
  error: (err, _) => NreErrorWidget(
    error: err,
    onRetry: () => ref.read(rulesListProvider.notifier).refresh(),
  ),
);
```

### 5.3 Auth State & Route Guards

```dart
// core/auth/auth_provider.dart
@riverpod
class AuthState extends _$AuthState {
  @override
  AuthStatus build() {
    final profile = ref.read(storageProvider).loadProfile();
    return profile.isRegistered
      ? AuthStatus.authenticated(profile)
      : const AuthStatus.unauthenticated();
  }

  Future<void> register(...) async { ... }
  Future<void> unregister() async { ... }
  Future<void> checkHealth() async { ... }
}

// Router auto-redirects unauthenticated users
final routerProvider = Provider<GoRouter>((ref) {
  final auth = ref.watch(authStateProvider);
  return GoRouter(
    redirect: (context, state) {
      if (auth.isUnauthenticated && state.matchedLocation != '/connect') {
        return '/connect';
      }
      return null;
    },
    // ... routes
  );
});
```

### 5.4 Local Storage

```dart
abstract class AppStorage {
  Future<String?> getString(String key);
  Future<void> setString(String key, String value);
  Future<void> remove(String key);
}
```

Persisted data:
- Connection profile (Master URL, Agent ID, Token)
- Theme settings (mode, color scheme)
- Language setting
- Desktop window size/position

---

## 6. UI/UX Design Specification

### 6.1 Shared Component Library

| Component | Description |
|-----------|-------------|
| `NreCard` | Anime-styled card with optional gradient accent bar, colored shadow, 20px radius |
| `NreEmptyState` | Centered layout with theme-colored icon circle, title, message, optional action button |
| `NreSkeletonList` | Shimmer-style loading list with configurable item count |
| `NreSkeletonCard` | Single skeleton card for grid layouts |
| `NreStatusChip` | Capsule-shaped status badge with gradient background |
| `NreErrorWidget` | Error display with retry action, themed colors |
| `NreAppBar` | Consistent app bar with title, actions, adaptive elevation |
| `NreSearchBar` | Themed search input with clear button |
| `NreConfirmDialog` | Confirmation dialog with destructive action styling |
| `AnimatedListItem` | Fade + slide entrance animation for list items |
| `HoverScaleCard` | Desktop-only hover scale effect (1.0x → 1.02x) |

### 6.2 Animation Spec

| Animation | Duration | Curve | Trigger |
|-----------|----------|-------|---------|
| Page transition (mobile) | 300ms | Cupertino/EaseOut | Route push/pop |
| Page transition (desktop) | 200ms | EaseOutCubic | Route change |
| List item entrance | 300ms | EaseOutCubic | First build |
| Card hover scale | 200ms | EaseOutCubic | Mouse enter/leave |
| Button press | 100ms | EaseInOut | Tap |
| Snackbar entrance | 250ms | EaseOut | Show |
| Skeleton shimmer | 1500ms | Linear | Continuous |
| Theme switch | 300ms | EaseInOut | Toggle |

### 6.3 Responsive Breakpoints

```dart
class Breakpoints {
  static bool isMobile(BuildContext context) =>
      MediaQuery.of(context).size.width < 600;
  static bool isTablet(BuildContext context) =>
      MediaQuery.of(context).size.width >= 600 &&
      MediaQuery.of(context).size.width < 1200;
  static bool isDesktop(BuildContext context) =>
      MediaQuery.of(context).size.width >= 1200;
}
```

| Breakpoint | Layout |
|------------|--------|
| < 600px | Bottom nav, single column, full-screen forms |
| 600-1200px | Collapsible rail, 2-column grids |
| >= 1200px | Expanded rail, multi-column, side panels |

### 6.4 Empty States

| Scenario | Icon | Title | Action |
|----------|------|-------|--------|
| Not connected | `cloud_off` | "未连接到 Master" | "去连接" button |
| No rules | `inbox` | "暂无规则" | "创建规则" (desktop only) |
| No agents | `devices` | "暂无 Agent" | — |
| No certificates | `security` | "暂无证书" | "上传证书" (desktop only) |
| Error | `error_outline` | "加载失败" | "重试" button |
| Search empty | `search_off` | "未找到结果" | "清除搜索" |

---

## 7. Error Handling & Edge Cases

### 7.1 Unified Exception Hierarchy

```dart
sealed class AppException implements Exception {
  final String message;
  final String? code;
  const AppException(this.message, {this.code});
}

class NetworkException extends AppException { ... }
class AuthException extends AppException { ... }
class ValidationException extends AppException { ... }
class NotFoundException extends AppException { ... }
class ServerException extends AppException {
  final int statusCode;
  ...
}
```

### 7.2 HTTP Error Mapping

| HTTP Status | Exception | User Message |
|-------------|-----------|--------------|
| 401 | `AuthException` | "认证失败，请重新连接" |
| 403 | `AuthException` | "权限不足" |
| 404 | `NotFoundException` | "资源不存在" |
| 422 | `ValidationException` | "请求参数错误" |
| >= 500 | `ServerException` | "服务器错误" |
| Timeout | `NetworkException` | "连接超时，请检查网络" |
| No connection | `NetworkException` | "无法连接到服务器" |

### 7.3 Edge Case Handling

| Scenario | Strategy |
|----------|----------|
| First launch offline | Connection wizard shows offline hint; allows input but blocks registration |
| Network drops during use | Global connection indicator turns red; disables actions; shows reconnect button |
| Token expires | Auto-logout, redirect to connection wizard, preserve Master URL |
| Master server upgraded | Show version mismatch warning with update suggestion |
| Local agent binary missing | Show "Not Installed" status with download guidance |
| Rule edit conflict | Fetch latest before save; show merge prompt if changed |
| Mobile accesses desktop feature | Route interception; show "Desktop only" empty state |
| App killed while agent running | On restart, detect orphaned process and reconnect |

### 7.4 Global Error Boundary

```dart
class NreErrorBoundary extends StatelessWidget {
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return ErrorBoundary(
      onError: (error, stack) {
        Logger.e('UI Error', error: error, stackTrace: stack);
      },
      fallbackBuilder: (context, error) => Scaffold(
        body: NreErrorWidget(
          error: error,
          title: '应用出现错误',
          onRetry: () => Navigator.of(context).pushReplacement(
            MaterialPageRoute(builder: (_) => const NreClientApp()),
          ),
        ),
      ),
      child: child,
    );
  }
}
```

---

## 8. Testing Strategy

### 8.1 Test Pyramid

```
      ▲
     / \     E2E / Integration Tests
    /   \    — Full user flows: register → view rules → start agent
   /─────\
  /       \  Widget Tests
 /         \ — Screen rendering, interactions, state changes
/───────────\
/             \  Unit Tests
/               \ — Providers, Repositories, Models, Utils
─────────────────
```

### 8.2 Unit Tests (Target: >= 80% coverage)

Focus areas:
- API layer: request/response serialization, error mapping
- Providers: state transitions, optimistic updates, rollback
- Models: Freezed serialization round-trip
- Utils: URL normalization, token generation, validation

Tools: `mocktail`, `riverpod_test`

### 8.3 Widget Tests (Target: >= 60% coverage)

Focus areas:
- Each screen renders correctly in loading/data/error/empty states
- User interactions trigger correct provider calls
- Navigation works as expected
- Theme switching applies correctly

Tools: `flutter_test`, `golden_toolkit` (for UI regression)

### 8.4 Integration Tests

Core flows to cover:
1. Fresh install → connection wizard → registration → dashboard
2. View rules list → toggle rule → verify state update
3. Start local agent → verify status change → view logs → stop agent
4. Theme switch → verify color change across screens
5. Logout → verify redirect to connection wizard

### 8.5 Test Utilities

```dart
// test/utils/test_utils.dart
ProviderContainer createContainer({List<Override>? overrides}) {
  return ProviderContainer(overrides: overrides);
}

// Pumps app with mocked dependencies
Future<void> pumpApp(WidgetTester tester, {List<Override>? overrides}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: overrides,
      child: const NreClientApp(),
    ),
  );
  await tester.pumpAndSettle();
}
```

---

## 9. Platform Adaptation

### 9.1 Platform Capability Matrix

| Feature | Windows | macOS | Android |
|---------|---------|-------|---------|
| Dashboard overview | Yes | Yes | Yes (simplified) |
| Rule list/view | Yes | Yes | Yes |
| Rule CRUD | Yes | Yes | Read-only + toggle |
| Certificate CRUD | Yes | Yes | No |
| Agent list (remote) | Yes | Yes | Yes |
| Local agent control | Yes | Yes | No |
| Relay listener CRUD | Yes | Yes | No |
| Theme switching | Yes | Yes | Yes |
| System tray | Yes | Yes | No |
| Start at login | Yes | Yes | No |
| Window management | Yes | Yes | No |

### 9.2 Platform Detection

```dart
enum NrePlatform { windows, macos, android }

class PlatformCapabilities {
  final NrePlatform platform;
  final bool canManageLocalAgent;
  final bool canViewRemoteAgents;
  final bool canInstallUpdates;
  final bool canManageCertificates;
  final bool canManageRelay;
  final bool canEditRules;

  static PlatformCapabilities forPlatform(NrePlatform platform) {
    return switch (platform) {
      NrePlatform.windows || NrePlatform.macos => PlatformCapabilities(
        platform: platform,
        canManageLocalAgent: true,
        canViewRemoteAgents: true,
        canInstallUpdates: true,
        canManageCertificates: true,
        canManageRelay: true,
        canEditRules: true,
      ),
      NrePlatform.android => PlatformCapabilities(
        platform: platform,
        canManageLocalAgent: false,
        canViewRemoteAgents: true,
        canInstallUpdates: false,
        canManageCertificates: false,
        canManageRelay: false,
        canEditRules: false,
      ),
    };
  }
}
```

### 9.3 Desktop Window Management

Using `window_manager`:
- Initial size: 1024x700, minimum: 720x520
- Center on first launch; restore position on subsequent launches
- Close button minimizes to system tray (not quit)
- System tray menu: Show / Quit

---

## 10. Implementation Priority

### Phase 1: Foundation (Week 1-2)
1. Set up new directory structure
2. Configure dependencies (Freezed, Riverpod, go_router, Dio)
3. Build core layer: theme system, network layer, routing, storage
4. Set up testing infrastructure

### Phase 2: Auth + Dashboard (Week 2-3)
1. Connection wizard screen
2. Auth state management + route guards
3. Dashboard overview with statistics
4. Local agent status card (desktop)

### Phase 3: Core Features (Week 3-5)
1. Rules CRUD (desktop) / read-only (mobile)
2. Agents list + local agent control
3. Certificates (desktop)
4. Relay listeners (desktop)

### Phase 4: Polish (Week 5-6)
1. Settings screen (theme, language, data)
2. Animations and transitions
3. Error handling refinement
4. Desktop window / tray integration

### Phase 5: Testing & QA (Week 6-7)
1. Unit tests for all providers and repositories
2. Widget tests for all screens
3. Integration tests for core flows
4. Cross-platform manual testing

---

## 11. Appendix

### 11.1 Dependencies

```yaml
dependencies:
  flutter:
    sdk: flutter
  cupertino_icons: ^1.0.8
  flutter_localizations:
    sdk: flutter
  intl: ^0.20.0

  # State management
  flutter_riverpod: ^2.6.1
  riverpod_annotation: ^2.6.1

  # Navigation
  go_router: ^14.8.1

  # Network
  dio: ^5.8.0

  # Models
  freezed_annotation: ^3.0.0
  json_annotation: ^4.9.0

  # Storage
  shared_preferences: ^2.3.0
  path_provider: ^2.1.5

  # Desktop
  window_manager: ^0.4.3
  tray_manager: ^0.3.2

  # UI
  gap: ^3.0.1
  lucide_icons: ^0.257.0  # Alternative icon set

dev_dependencies:
  flutter_test:
    sdk: flutter
  flutter_lints: ^6.0.0
  build_runner: ^2.4.15
  freezed: ^3.0.0
  json_serializable: ^6.9.4
  riverpod_generator: ^2.6.5
  mocktail: ^1.0.0
  golden_toolkit: ^0.15.0
```

### 11.2 Related Documents

- `CLAUDE.md` — Project overview and commands
- Existing Flutter client: `clients/flutter/lib/`
- Backend API: `panel/backend-go/internal/`

---

*Document version: 1.0*  
*Author: Claude Code (Brainstorming Session)*  
*Status: Awaiting implementation plan*
