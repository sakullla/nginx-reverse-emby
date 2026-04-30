# Flutter Client Complete Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Flutter client from the ground up with feature-first architecture, anime-themed Material 3 UI, expanded management capabilities, and comprehensive tests.

**Architecture:** Feature-first Clean Architecture (simplified) with `core/` infrastructure, `features/` modules, and `shared/` UI components. Riverpod for state management, go_router for navigation, Dio for networking, Freezed for models.

**Tech Stack:** Flutter 3.11.5+, Material 3, flutter_riverpod, go_router, dio, freezed, shared_preferences, window_manager, tray_manager, mocktail

**Base directory:** `clients/flutter/`
**Branch:** `feature/multi-platform-clients-impl`

---

## File Structure Map

### Phase 1: Foundation — New files

```
lib/
├── main.dart                          # MODIFY — rewrite entry point
├── app.dart                           # MODIFY — rewrite app shell
├── core/
│   ├── constants/
│   │   └── app_constants.dart         # CREATE — API paths, timeouts
│   ├── exceptions/
│   │   └── app_exceptions.dart        # CREATE — exception hierarchy
│   ├── logger/
│   │   └── app_logger.dart            # CREATE — structured logging
│   ├── platform/
│   │   └── platform_capabilities.dart # CREATE — platform detection
│   ├── routing/
│   │   ├── app_router.dart            # CREATE — go_router config
│   │   └── route_names.dart           # CREATE — typed route constants
│   ├── theme/
│   │   ├── app_theme.dart             # CREATE — ThemeData builder
│   │   ├── theme_controller.dart      # CREATE — Riverpod theme state
│   │   ├── color_schemes.dart         # CREATE — anime color presets
│   │   └── theme_preview.dart         # CREATE — theme selector widget
│   └── network/
│       ├── dio_client.dart            # CREATE — Dio config
│       ├── api_client.dart            # CREATE — abstract API
│       ├── master_api.dart            # CREATE — concrete API impl
│       └── models/                    # CREATE — shared models
├── features/
│   ├── auth/
│   │   ├── data/
│   │   │   ├── models/
│   │   │   │   └── auth_models.dart   # CREATE — profile, state models
│   │   │   └── repositories/
│   │   │       └── auth_repository.dart # CREATE — profile persistence
│   │   └── presentation/
│   │       ├── providers/
│   │       │   └── auth_provider.dart # CREATE — auth Riverpod provider
│   │       └── screens/
│   │           └── connect_screen.dart # CREATE — connection wizard
│   ├── dashboard/
│   │   └── presentation/
│   │       ├── providers/
│   │       │   └── dashboard_provider.dart # CREATE
│   │       └── screens/
│   │           └── dashboard_screen.dart   # CREATE
│   ├── rules/
│   │   ├── data/
│   │   │   ├── models/
│   │   │   │   └── rule_models.dart   # CREATE — ProxyRule, etc.
│   │   │   └── repositories/
│   │   │       └── rules_repository.dart # CREATE
│   │   └── presentation/
│   │       ├── providers/
│   │       │   └── rules_provider.dart # CREATE
│   │       └── screens/
│   │           ├── rules_list_screen.dart # CREATE
│   │           └── rule_detail_screen.dart # CREATE
│   ├── agents/
│   │   ├── data/
│   │   │   ├── models/
│   │   │   │   └── agent_models.dart  # CREATE
│   │   │   └── repositories/
│   │   │       └── agents_repository.dart # CREATE
│   │   └── presentation/
│   │       ├── providers/
│   │       │   └── agents_provider.dart # CREATE
│   │       └── screens/
│   │           └── agents_screen.dart   # CREATE
│   ├── certificates/
│   │   └── presentation/
│   │       └── screens/
│   │           └── certificates_screen.dart # CREATE
│   ├── relay/
│   │   └── presentation/
│   │       └── screens/
│   │           └── relay_screen.dart    # CREATE
│   └── settings/
│       └── presentation/
│           └── screens/
│               └── settings_screen.dart # CREATE
└── shared/
    ├── widgets/
    │   ├── nre_card.dart              # CREATE
    │   ├── nre_empty_state.dart       # CREATE
    │   ├── nre_skeleton.dart          # CREATE
    │   ├── nre_status_chip.dart       # CREATE
    │   ├── nre_error_widget.dart      # CREATE
    │   └── animated_list_item.dart    # CREATE
    └── extensions/
        └── build_context_ext.dart     # CREATE
```

### Phase 5: Tests — New files

```
test/
├── core/
│   ├── exceptions/
│   │   └── app_exceptions_test.dart
│   ├── network/
│   │   ├── dio_client_test.dart
│   │   └── master_api_test.dart
│   └── theme/
│       └── theme_controller_test.dart
├── features/
│   ├── auth/
│   │   └── auth_provider_test.dart
│   ├── rules/
│   │   └── rules_provider_test.dart
│   └── agents/
│       └── agents_provider_test.dart
├── shared/
│   └── widgets/
│       └── nre_empty_state_test.dart
└── utils/
    └── test_utils.dart
```

---

## Task 1: Update Dependencies

**Files:**
- Modify: `clients/flutter/pubspec.yaml`

- [ ] **Step 1: Update pubspec.yaml dependencies**

  Replace the `dependencies:` and `dev_dependencies:` sections:

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

  dev_dependencies:
    flutter_test:
      sdk: flutter
    flutter_lints: ^6.0.0
    build_runner: ^2.4.15
    freezed: ^3.0.0
    json_serializable: ^6.9.4
    riverpod_generator: ^2.6.5
    mocktail: ^1.0.0
  ```

- [ ] **Step 2: Install dependencies**

  ```bash
  cd clients/flutter && flutter pub get
  ```

  Expected: Packages resolve successfully.

- [ ] **Step 3: Commit**

  ```bash
  cd clients/flutter && git add pubspec.yaml pubspec.lock && git commit -m "chore(deps): add riverpod, go_router, dio, freezed, shared_preferences"
  ```

---

## Task 2: Core — Exceptions + Logger

**Files:**
- Create: `clients/flutter/lib/core/exceptions/app_exceptions.dart`
- Create: `clients/flutter/lib/core/logger/app_logger.dart`

- [ ] **Step 1: Write exception hierarchy**

  ```dart
  // lib/core/exceptions/app_exceptions.dart
  sealed class AppException implements Exception {
    final String message;
    final String? code;
    const AppException(this.message, {this.code});

    @override
    String toString() => message;
  }

  class NetworkException extends AppException {
    const NetworkException(super.message, {super.code});
  }

  class AuthException extends AppException {
    const AuthException(super.message, {super.code});
  }

  class ValidationException extends AppException {
    const ValidationException(super.message, {super.code});
  }

  class NotFoundException extends AppException {
    const NotFoundException(super.message, {super.code});
  }

  class ServerException extends AppException {
    final int statusCode;
    const ServerException(super.message, {required this.statusCode, super.code});
  }
  ```

- [ ] **Step 2: Write logger**

  ```dart
  // lib/core/logger/app_logger.dart
  import 'package:logger/logger.dart';

  final _logger = Logger(
    printer: PrettyPrinter(
      methodCount: 2,
      errorMethodCount: 8,
      lineLength: 120,
      colors: true,
      printEmojis: true,
    ),
  );

  class AppLogger {
    static void d(String message) => _logger.d(message);
    static void i(String message) => _logger.i(message);
    static void w(String message) => _logger.w(message);
    static void e(String message, {Object? error, StackTrace? stackTrace}) =>
        _logger.e(message, error: error, stackTrace: stackTrace);
  }
  ```

- [ ] **Step 3: Write tests for exceptions**

  ```dart
  // test/core/exceptions/app_exceptions_test.dart
  import 'package:flutter_test/flutter_test.dart';
  import 'package:nre_client/core/exceptions/app_exceptions.dart';

  void main() {
    test('NetworkException stores message', () {
      const e = NetworkException('timeout');
      expect(e.message, 'timeout');
      expect(e.toString(), 'timeout');
    });

    test('ServerException stores statusCode', () {
      const e = ServerException('fail', statusCode: 500);
      expect(e.statusCode, 500);
    });
  }
  ```

- [ ] **Step 4: Run tests**

  ```bash
  cd clients/flutter && flutter test test/core/exceptions/app_exceptions_test.dart
  ```

  Expected: 2 tests pass.

- [ ] **Step 5: Commit**

  ```bash
  cd clients/flutter && git add lib/core/exceptions/ lib/core/logger/ test/core/exceptions/ && git commit -m "feat(core): add exception hierarchy and logger"
  ```

---

## Task 3: Core — Platform Capabilities

**Files:**
- Create: `clients/flutter/lib/core/platform/platform_capabilities.dart`
- Modify: `clients/flutter/lib/core/constants/app_constants.dart`

- [ ] **Step 1: Write platform capabilities**

  ```dart
  // lib/core/platform/platform_capabilities.dart
  import 'dart:io';

  enum NrePlatform { windows, macos, android, linux, ios, unknown }

  class PlatformCapabilities {
    const PlatformCapabilities({
      required this.platform,
      required this.canManageLocalAgent,
      required this.canViewRemoteAgents,
      required this.canInstallUpdates,
      required this.canManageCertificates,
      required this.canManageRelay,
      required this.canEditRules,
    });

    final NrePlatform platform;
    final bool canManageLocalAgent;
    final bool canViewRemoteAgents;
    final bool canInstallUpdates;
    final bool canManageCertificates;
    final bool canManageRelay;
    final bool canEditRules;

    static PlatformCapabilities get current {
      final platform = _detectPlatform();
      return switch (platform) {
        NrePlatform.windows || NrePlatform.macos || NrePlatform.linux =>
          PlatformCapabilities(
            platform: platform,
            canManageLocalAgent: true,
            canViewRemoteAgents: true,
            canInstallUpdates: true,
            canManageCertificates: true,
            canManageRelay: true,
            canEditRules: true,
          ),
        NrePlatform.android || NrePlatform.ios => PlatformCapabilities(
            platform: platform,
            canManageLocalAgent: false,
            canViewRemoteAgents: true,
            canInstallUpdates: false,
            canManageCertificates: false,
            canManageRelay: false,
            canEditRules: false,
          ),
        NrePlatform.unknown => PlatformCapabilities(
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

    static NrePlatform _detectPlatform() {
      if (Platform.isWindows) return NrePlatform.windows;
      if (Platform.isMacOS) return NrePlatform.macos;
      if (Platform.isLinux) return NrePlatform.linux;
      if (Platform.isAndroid) return NrePlatform.android;
      if (Platform.isIOS) return NrePlatform.ios;
      return NrePlatform.unknown;
    }
  }
  ```

- [ ] **Step 2: Write constants**

  ```dart
  // lib/core/constants/app_constants.dart
  class AppConstants {
    static const String appName = 'NRE Client';
    static const String appVersion = '2.1.0';
    static const Duration apiTimeout = Duration(seconds: 10);
    static const Duration connectTimeout = Duration(seconds: 10);
    static const Duration receiveTimeout = Duration(seconds: 10);
  }
  ```

- [ ] **Step 3: Write tests for platform capabilities**

  ```dart
  // test/core/platform/platform_capabilities_test.dart
  import 'package:flutter_test/flutter_test.dart';
  import 'package:nre_client/core/platform/platform_capabilities.dart';

  void main() {
    test('current returns valid capabilities', () {
      final caps = PlatformCapabilities.current;
      expect(caps.platform, isNot(NrePlatform.unknown));
      expect(caps.canViewRemoteAgents, isTrue);
    });

    test('desktop platforms have full capabilities', () {
      const desktop = PlatformCapabilities(
        platform: NrePlatform.windows,
        canManageLocalAgent: true,
        canViewRemoteAgents: true,
        canInstallUpdates: true,
        canManageCertificates: true,
        canManageRelay: true,
        canEditRules: true,
      );
      expect(desktop.canManageLocalAgent, isTrue);
      expect(desktop.canEditRules, isTrue);
    });

    test('mobile platforms have restricted capabilities', () {
      const mobile = PlatformCapabilities(
        platform: NrePlatform.android,
        canManageLocalAgent: false,
        canViewRemoteAgents: true,
        canInstallUpdates: false,
        canManageCertificates: false,
        canManageRelay: false,
        canEditRules: false,
      );
      expect(mobile.canManageLocalAgent, isFalse);
      expect(mobile.canViewRemoteAgents, isTrue);
    });
  }
  ```

- [ ] **Step 4: Run tests**

  ```bash
  cd clients/flutter && flutter test test/core/platform/platform_capabilities_test.dart
  ```

  Expected: 3 tests pass.

- [ ] **Step 5: Commit**

  ```bash
  cd clients/flutter && git add lib/core/platform/ lib/core/constants/ test/core/platform/ && git commit -m "feat(core): add platform capabilities and app constants"
  ```

---

## Task 4: Core — Anime Theme System

**Files:**
- Create: `clients/flutter/lib/core/theme/color_schemes.dart`
- Create: `clients/flutter/lib/core/theme/app_theme.dart`
- Create: `clients/flutter/lib/core/theme/theme_controller.dart`

- [ ] **Step 1: Write color schemes**

  ```dart
  // lib/core/theme/color_schemes.dart
  import 'package:flutter/material.dart';

  enum AppColorScheme { sakuraPink, electricCyan, neonViolet, cyberGreen }

  extension AppColorSchemeColors on AppColorScheme {
    Color get primaryLight => switch (this) {
      AppColorScheme.sakuraPink => const Color(0xFFEC407A),
      AppColorScheme.electricCyan => const Color(0xFF00BCD4),
      AppColorScheme.neonViolet => const Color(0xFF7C4DFF),
      AppColorScheme.cyberGreen => const Color(0xFF00E676),
    };

    Color get primaryDark => switch (this) {
      AppColorScheme.sakuraPink => const Color(0xFFF48FB1),
      AppColorScheme.electricCyan => const Color(0xFF00E5FF),
      AppColorScheme.neonViolet => const Color(0xFFB388FF),
      AppColorScheme.cyberGreen => const Color(0xFF69F0AE),
    };

    Color get secondary => switch (this) {
      AppColorScheme.sakuraPink => const Color(0xFFFF80AB),
      AppColorScheme.electricCyan => const Color(0xFF18FFFF),
      AppColorScheme.neonViolet => const Color(0xFFE040FB),
      AppColorScheme.cyberGreen => const Color(0xFF76FF03),
    };

    String get displayName => switch (this) {
      AppColorScheme.sakuraPink => 'Sakura Pink',
      AppColorScheme.electricCyan => 'Electric Cyan',
      AppColorScheme.neonViolet => 'Neon Violet',
      AppColorScheme.cyberGreen => 'Cyber Green',
    };
  }
  ```

- [ ] **Step 2: Write app theme builder**

  ```dart
  // lib/core/theme/app_theme.dart
  import 'package:flutter/material.dart';
  import 'color_schemes.dart';

  class AppTheme {
    static ThemeData buildTheme(AppColorScheme scheme, ThemeMode mode) {
      final brightness = mode == ThemeMode.dark ? Brightness.dark : Brightness.light;
      final isDark = brightness == Brightness.dark;
      final seedColor = isDark ? scheme.primaryDark : scheme.primaryLight;

      final colorScheme = ColorScheme.fromSeed(
        seedColor: seedColor,
        brightness: brightness,
      ).copyWith(
        surface: isDark ? const Color(0xFF0F172A) : const Color(0xFFFAFAFA),
        surfaceContainerHighest: isDark ? const Color(0xFF1E293B) : const Color(0xFFF1F5F9),
        surfaceContainer: isDark ? const Color(0xFF334155) : const Color(0xFFE2E8F0),
      );

      return ThemeData(
        useMaterial3: true,
        colorScheme: colorScheme,
        scaffoldBackgroundColor: colorScheme.surface,
        cardTheme: CardThemeData(
          elevation: 2,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
          shadowColor: colorScheme.primary.withValues(alpha: 0.15),
        ),
        appBarTheme: AppBarTheme(
          centerTitle: false,
          backgroundColor: colorScheme.surface,
          foregroundColor: colorScheme.onSurface,
          elevation: 0,
          scrolledUnderElevation: 0.5,
          titleTextStyle: TextStyle(
            fontSize: 20,
            fontWeight: FontWeight.w600,
            color: colorScheme.onSurface,
          ),
        ),
        navigationBarTheme: NavigationBarThemeData(
          elevation: 1,
          backgroundColor: colorScheme.surfaceContainerHighest,
          indicatorColor: colorScheme.primaryContainer,
        ),
        navigationRailTheme: NavigationRailThemeData(
          backgroundColor: colorScheme.surfaceContainerHighest,
          indicatorColor: colorScheme.primaryContainer,
          selectedIconTheme: IconThemeData(color: colorScheme.onPrimaryContainer),
          unselectedIconTheme: IconThemeData(color: colorScheme.onSurfaceVariant),
        ),
        filledButtonTheme: FilledButtonThemeData(
          style: FilledButton.styleFrom(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 14),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
          ),
        ),
        outlinedButtonTheme: OutlinedButtonThemeData(
          style: OutlinedButton.styleFrom(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 14),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
          ),
        ),
        inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: colorScheme.surfaceContainerHighest.withValues(alpha: 0.6),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: BorderSide(color: colorScheme.outlineVariant),
          ),
          focusedBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(12),
            borderSide: BorderSide(color: colorScheme.primary, width: 2),
          ),
          contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        ),
        snackBarTheme: SnackBarThemeData(
          behavior: SnackBarBehavior.floating,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
          backgroundColor: colorScheme.inverseSurface,
          contentTextStyle: TextStyle(color: colorScheme.onInverseSurface),
        ),
        pageTransitionsTheme: const PageTransitionsTheme(
          builders: {
            TargetPlatform.android: PredictiveBackPageTransitionsBuilder(),
            TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
            TargetPlatform.windows: FadeUpwardsPageTransitionsBuilder(),
            TargetPlatform.macOS: FadeUpwardsPageTransitionsBuilder(),
            TargetPlatform.linux: FadeUpwardsPageTransitionsBuilder(),
          },
        ),
      );
    }
  }
  ```

- [ ] **Step 3: Write theme controller**

  ```dart
  // lib/core/theme/theme_controller.dart
  import 'package:flutter/material.dart';
  import 'package:riverpod_annotation/riverpod_annotation.dart';
  import 'package:shared_preferences/shared_preferences.dart';
  import 'color_schemes.dart';

  part 'theme_controller.g.dart';

  class ThemeSettings {
    const ThemeSettings({
      required this.themeMode,
      required this.colorScheme,
    });

    final ThemeMode themeMode;
    final AppColorScheme colorScheme;
  }

  @riverpod
  class ThemeController extends _$ThemeController {
    static const _modeKey = 'theme_mode';
    static const _schemeKey = 'color_scheme';

    @override
    Future<ThemeSettings> build() async {
      final prefs = await SharedPreferences.getInstance();
      final modeIndex = prefs.getInt(_modeKey) ?? 0;
      final schemeIndex = prefs.getInt(_schemeKey) ?? 0;
      return ThemeSettings(
        themeMode: ThemeMode.values[modeIndex.clamp(0, ThemeMode.values.length - 1)],
        colorScheme: AppColorScheme.values[schemeIndex.clamp(0, AppColorScheme.values.length - 1)],
      );
    }

    Future<void> setThemeMode(ThemeMode mode) async {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setInt(_modeKey, mode.index);
      final current = await future;
      state = AsyncData(ThemeSettings(themeMode: mode, colorScheme: current.colorScheme));
    }

    Future<void> setColorScheme(AppColorScheme scheme) async {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setInt(_schemeKey, scheme.index);
      final current = await future;
      state = AsyncData(ThemeSettings(themeMode: current.themeMode, colorScheme: scheme));
    }
  }
  ```

- [ ] **Step 4: Generate Riverpod code**

  ```bash
  cd clients/flutter && flutter pub run build_runner build --delete-conflicting-outputs
  ```

  Expected: `theme_controller.g.dart` generated successfully.

- [ ] **Step 5: Commit**

  ```bash
  cd clients/flutter && git add lib/core/theme/ && git commit -m "feat(core): add anime theme system with 4 color presets"
  ```

---

## Task 5: Core — Shared Widgets

**Files:**
- Create: `clients/flutter/lib/shared/widgets/nre_card.dart`
- Create: `clients/flutter/lib/shared/widgets/nre_empty_state.dart`
- Create: `clients/flutter/lib/shared/widgets/nre_skeleton.dart`
- Create: `clients/flutter/lib/shared/widgets/nre_status_chip.dart`
- Create: `clients/flutter/lib/shared/widgets/nre_error_widget.dart`

- [ ] **Step 1: Write NreCard**

  ```dart
  // lib/shared/widgets/nre_card.dart
  import 'package:flutter/material.dart';

  class NreCard extends StatelessWidget {
    const NreCard({
      super.key,
      required this.child,
      this.accentColor,
      this.hasAccentBar = false,
      this.onTap,
      this.padding = const EdgeInsets.all(16),
    });

    final Widget child;
    final Color? accentColor;
    final bool hasAccentBar;
    final VoidCallback? onTap;
    final EdgeInsets padding;

    @override
    Widget build(BuildContext context) {
      final scheme = Theme.of(context).colorScheme;
      final card = Card(
        elevation: 2,
        shadowColor: scheme.primary.withValues(alpha: 0.15),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
        clipBehavior: Clip.antiAlias,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (hasAccentBar)
              Container(
                height: 4,
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    colors: [
                      accentColor ?? scheme.primary,
                      (accentColor ?? scheme.primary).withValues(alpha: 0.6),
                    ],
                  ),
                  borderRadius: const BorderRadius.vertical(
                    top: Radius.circular(20),
                  ),
                ),
              ),
            Padding(padding: padding, child: child),
          ],
        ),
      );

      if (onTap != null) {
        return InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(20),
          child: card,
        );
      }
      return card;
    }
  }
  ```

- [ ] **Step 2: Write NreEmptyState**

  ```dart
  // lib/shared/widgets/nre_empty_state.dart
  import 'package:flutter/material.dart';

  class NreEmptyState extends StatelessWidget {
    const NreEmptyState({
      super.key,
      required this.icon,
      required this.title,
      this.message,
      this.action,
    });

    final IconData icon;
    final String title;
    final String? message;
    final Widget? action;

    @override
    Widget build(BuildContext context) {
      final theme = Theme.of(context);
      final scheme = theme.colorScheme;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 360),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Container(
                  padding: const EdgeInsets.all(24),
                  decoration: BoxDecoration(
                    color: scheme.primaryContainer,
                    shape: BoxShape.circle,
                  ),
                  child: Icon(icon, size: 48, color: scheme.primary),
                ),
                const SizedBox(height: 20),
                Text(
                  title,
                  style: theme.textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
                ),
                if (message != null) ...[
                  const SizedBox(height: 8),
                  Text(
                    message!,
                    textAlign: TextAlign.center,
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: scheme.outline,
                    ),
                  ),
                ],
                if (action != null) ...[
                  const SizedBox(height: 20),
                  action!,
                ],
              ],
            ),
          ),
        ),
      );
    }
  }
  ```

- [ ] **Step 3: Write NreSkeleton + NreStatusChip + NreErrorWidget**

  ```dart
  // lib/shared/widgets/nre_skeleton.dart
  import 'package:flutter/material.dart';

  class NreSkeletonList extends StatelessWidget {
    const NreSkeletonList({super.key, this.itemCount = 6});
    final int itemCount;

    @override
    Widget build(BuildContext context) {
      return ListView.separated(
        padding: const EdgeInsets.all(16),
        itemCount: itemCount,
        separatorBuilder: (_, __) => const SizedBox(height: 12),
        itemBuilder: (_, __) => const NreSkeletonCard(),
      );
    }
  }

  class NreSkeletonCard extends StatelessWidget {
    const NreSkeletonCard({super.key});

    @override
    Widget build(BuildContext context) {
      return Card(
        elevation: 0,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        child: const Padding(
          padding: EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(height: 12, width: 120, child: _Bone()),
              SizedBox(height: 12),
              SizedBox(height: 8, width: 200, child: _Bone()),
            ],
          ),
        ),
      );
    }
  }

  class _Bone extends StatelessWidget {
    const _Bone();

    @override
    Widget build(BuildContext context) {
      return Container(
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.outlineVariant.withValues(alpha: 0.3),
          borderRadius: BorderRadius.circular(4),
        ),
      );
    }
  }
  ```

  ```dart
  // lib/shared/widgets/nre_status_chip.dart
  import 'package:flutter/material.dart';

  enum StatusType { success, warning, error, info }

  class NreStatusChip extends StatelessWidget {
    const NreStatusChip({super.key, required this.label, required this.type});

    final String label;
    final StatusType type;

    @override
    Widget build(BuildContext context) {
      final colors = _resolveColors(context);
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          gradient: LinearGradient(
            colors: [colors.background, colors.background.withValues(alpha: 0.7)],
          ),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: colors.foreground.withValues(alpha: 0.3),
            width: 1,
          ),
        ),
        child: Text(
          label,
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: colors.foreground,
          ),
        ),
      );
    }

    _StatusColors _resolveColors(BuildContext context) {
      final scheme = Theme.of(context).colorScheme;
      return switch (type) {
        StatusType.success => _StatusColors(
            background: Colors.green.withValues(alpha: 0.15),
            foreground: Colors.green,
          ),
        StatusType.warning => _StatusColors(
            background: Colors.orange.withValues(alpha: 0.15),
            foreground: Colors.orange,
          ),
        StatusType.error => _StatusColors(
            background: scheme.errorContainer,
            foreground: scheme.error,
          ),
        StatusType.info => _StatusColors(
            background: scheme.primaryContainer,
            foreground: scheme.primary,
          ),
      };
    }
  }

  class _StatusColors {
    final Color background;
    final Color foreground;
    _StatusColors({required this.background, required this.foreground});
  }
  ```

  ```dart
  // lib/shared/widgets/nre_error_widget.dart
  import 'package:flutter/material.dart';

  class NreErrorWidget extends StatelessWidget {
    const NreErrorWidget({
      super.key,
      required this.error,
      this.title = '加载失败',
      this.onRetry,
    });

    final Object error;
    final String title;
    final VoidCallback? onRetry;

    @override
    Widget build(BuildContext context) {
      final theme = Theme.of(context);
      final scheme = theme.colorScheme;
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 360),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(Icons.error_outline, size: 48, color: scheme.error),
                const SizedBox(height: 16),
                Text(title, style: theme.textTheme.titleMedium),
                const SizedBox(height: 8),
                Text(
                  error.toString(),
                  textAlign: TextAlign.center,
                  style: theme.textTheme.bodySmall?.copyWith(color: scheme.outline),
                ),
                if (onRetry != null) ...[
                  const SizedBox(height: 16),
                  FilledButton.icon(
                    onPressed: onRetry,
                    icon: const Icon(Icons.refresh),
                    label: const Text('重试'),
                  ),
                ],
              ],
            ),
          ),
        ),
      );
    }
  }
  ```

- [ ] **Step 4: Commit**

  ```bash
  cd clients/flutter && git add lib/shared/widgets/ && git commit -m "feat(shared): add anime-styled shared widget library"
  ```

---

## Task 6: Core — Network Layer (Dio + API Client)

**Files:**
- Create: `clients/flutter/lib/core/network/dio_client.dart`
- Create: `clients/flutter/lib/core/network/api_client.dart`
- Create: `clients/flutter/lib/core/network/master_api.dart`

- [ ] **Step 1: Write Dio client with interceptors**

  ```dart
  // lib/core/network/dio_client.dart
  import 'package:dio/dio.dart';
  import '../exceptions/app_exceptions.dart';
  import '../logger/app_logger.dart';

  class DioClient {
    late final Dio dio;

    DioClient({required String baseUrl, required String token}) {
      dio = Dio(BaseOptions(
        baseUrl: baseUrl,
        connectTimeout: const Duration(seconds: 10),
        receiveTimeout: const Duration(seconds: 10),
        headers: {'Authorization': 'Bearer $token'},
      ));

      dio.interceptors.addAll([
        LogInterceptor(
          requestBody: true,
          responseBody: true,
          logPrint: (obj) => AppLogger.d(obj.toString()),
        ),
        _ErrorInterceptor(),
      ]);
    }
  }

  class _ErrorInterceptor extends Interceptor {
    @override
    void onError(DioException err, ErrorInterceptorHandler handler) {
      final response = err.response;
      final status = response?.statusCode ?? 0;
      final data = response?.data;
      final message = data is Map ? data['error'] ?? data['message'] : null;

      final exception = switch (status) {
        401 => const AuthException('认证失败，请重新连接'),
        403 => const AuthException('权限不足'),
        404 => NotFoundException(message?.toString() ?? '资源不存在'),
        422 => ValidationException(message?.toString() ?? '请求参数错误'),
        >= 500 => ServerException(
            message?.toString() ?? '服务器错误',
            statusCode: status,
          ),
        _ => switch (err.type) {
            DioExceptionType.connectionTimeout ||
            DioExceptionType.receiveTimeout ||
            DioExceptionType.sendTimeout =>
              const NetworkException('连接超时，请检查网络'),
            DioExceptionType.connectionError =>
              const NetworkException('无法连接到服务器'),
            _ => NetworkException('请求失败: ${err.message}'),
          },
      };

      handler.reject(exception as DioException);
    }
  }
  ```

- [ ] **Step 2: Write abstract API client**

  ```dart
  // lib/core/network/api_client.dart
  import '../../features/rules/data/models/rule_models.dart';

  abstract class ApiClient {
    Future<List<ProxyRule>> getRules();
    Future<ProxyRule> createRule(CreateRuleRequest request);
    Future<ProxyRule> updateRule(String id, UpdateRuleRequest request);
    Future<void> deleteRule(String id);
    Future<void> toggleRule(String id, bool enabled);

    Future<List<Map<String, dynamic>>> getCertificates();
    Future<List<Map<String, dynamic>>> getAgents();
    Future<Map<String, dynamic>> registerAgent(Map<String, dynamic> request);
    Future<void> unregisterAgent(String id);

    Future<Map<String, dynamic>> getLocalAgentStatus();
    Future<Map<String, dynamic>> startLocalAgent();
    Future<Map<String, dynamic>> stopLocalAgent();

    Future<List<Map<String, dynamic>>> getRelayListeners();
  }
  ```

- [ ] **Step 3: Write Master API implementation**

  ```dart
  // lib/core/network/master_api.dart
  import 'package:dio/dio.dart';
  import 'api_client.dart';

  class MasterApi implements ApiClient {
    final Dio _dio;

    MasterApi({required Dio dio}) : _dio = dio;

    @override
    Future<List<ProxyRule>> getRules() async {
      final response = await _dio.get('/api/rules');
      final data = response.data;
      List<dynamic> items = [];
      if (data is List) {
        items = data;
      } else if (data is Map) {
        items = data['rules'] ?? data['items'] ?? data['data'] ?? [];
      }
      return items
          .whereType<Map<String, dynamic>>()
          .map(ProxyRule.fromJson)
          .toList();
    }

    @override
    Future<ProxyRule> createRule(CreateRuleRequest request) async {
      final response = await _dio.post('/api/rules', data: request.toJson());
      return ProxyRule.fromJson(response.data as Map<String, dynamic>);
    }

    @override
    Future<ProxyRule> updateRule(String id, UpdateRuleRequest request) async {
      final response = await _dio.put('/api/rules/$id', data: request.toJson());
      return ProxyRule.fromJson(response.data as Map<String, dynamic>);
    }

    @override
    Future<void> deleteRule(String id) async {
      await _dio.delete('/api/rules/$id');
    }

    @override
    Future<void> toggleRule(String id, bool enabled) async {
      await _dio.patch('/api/rules/$id', data: {'enabled': enabled});
    }

    @override
    Future<List<Map<String, dynamic>>> getCertificates() async {
      final response = await _dio.get('/api/certificates');
      return _extractList(response.data);
    }

    @override
    Future<List<Map<String, dynamic>>> getAgents() async {
      final response = await _dio.get('/api/agents');
      return _extractList(response.data);
    }

    @override
    Future<Map<String, dynamic>> registerAgent(Map<String, dynamic> request) async {
      final response = await _dio.post('/panel-api/agents/register', data: request);
      return response.data as Map<String, dynamic>;
    }

    @override
    Future<void> unregisterAgent(String id) async {
      await _dio.delete('/api/agents/$id');
    }

    @override
    Future<Map<String, dynamic>> getLocalAgentStatus() async {
      final response = await _dio.get('/api/local-agent/status');
      return response.data as Map<String, dynamic>;
    }

    @override
    Future<Map<String, dynamic>> startLocalAgent() async {
      final response = await _dio.post('/api/local-agent/start');
      return response.data as Map<String, dynamic>;
    }

    @override
    Future<Map<String, dynamic>> stopLocalAgent() async {
      final response = await _dio.post('/api/local-agent/stop');
      return response.data as Map<String, dynamic>;
    }

    @override
    Future<List<Map<String, dynamic>>> getRelayListeners() async {
      final response = await _dio.get('/api/relay');
      return _extractList(response.data);
    }

    List<Map<String, dynamic>> _extractList(dynamic data) {
      List<dynamic> items = [];
      if (data is List) {
        items = data;
      } else if (data is Map) {
        items = data['items'] ?? data['data'] ?? [];
      }
      return items.whereType<Map<String, dynamic>>().toList();
    }
  }
  ```

- [ ] **Step 4: Write network tests**

  ```dart
  // test/core/network/master_api_test.dart
  import 'package:dio/dio.dart';
  import 'package:flutter_test/flutter_test.dart';
  import 'package:mocktail/mocktail.dart';
  import 'package:nre_client/core/network/master_api.dart';
  import 'package:nre_client/features/rules/data/models/rule_models.dart';

  class MockDio extends Mock implements Dio {}
  class FakeRequestOptions extends Fake implements RequestOptions {}

  void main() {
    late MockDio mockDio;
    late MasterApi api;

    setUpAll(() {
      registerFallbackValue(FakeRequestOptions());
    });

    setUp(() {
      mockDio = MockDio();
      api = MasterApi(dio: mockDio);
    });

    test('getRules returns list of ProxyRule', () async {
      when(() => mockDio.get('/api/rules')).thenAnswer(
        (_) async => Response(
          data: [
            {'id': '1', 'domain': 'example.com', 'target': 'localhost:8080', 'type': 'http', 'enabled': true},
          ],
          statusCode: 200,
          requestOptions: RequestOptions(),
        ),
      );

      final rules = await api.getRules();
      expect(rules, hasLength(1));
      expect(rules.first.domain, 'example.com');
    });

    test('getRules throws on 500', () async {
      when(() => mockDio.get('/api/rules')).thenThrow(
        DioException(
          response: Response(statusCode: 500, requestOptions: RequestOptions()),
          requestOptions: RequestOptions(),
        ),
      );

      expect(() => api.getRules(), throwsException);
    });
  }
  ```

- [ ] **Step 5: Run tests**

  ```bash
  cd clients/flutter && flutter test test/core/network/master_api_test.dart
  ```

  Expected: 2 tests pass.

- [ ] **Step 6: Commit**

  ```bash
  cd clients/flutter && git add lib/core/network/ test/core/network/ && git commit -m "feat(core): add Dio network layer with interceptors and API client"
  ```

---

## Task 7: Core — Routing (go_router + ShellRoute)

**Files:**
- Create: `clients/flutter/lib/core/routing/route_names.dart`
- Create: `clients/flutter/lib/core/routing/app_router.dart`
- Modify: `clients/flutter/lib/app.dart`

- [ ] **Step 1: Write route names**

  ```dart
  // lib/core/routing/route_names.dart
  class RouteNames {
    static const String connect = '/connect';
    static const String dashboard = '/dashboard';
    static const String rules = '/rules';
    static const String ruleDetail = '/rules/:id';
    static const String ruleEdit = '/rules/edit/:id';
    static const String certificates = '/certificates';
    static const String agents = '/agents';
    static const String relay = '/relay';
    static const String settings = '/settings';
    static const String settingsTheme = '/settings/theme';
  }
  ```

- [ ] **Step 2: Write app router with ShellRoute**

  ```dart
  // lib/core/routing/app_router.dart
  import 'package:flutter/material.dart';
  import 'package:go_router/go_router.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import '../platform/platform_capabilities.dart';
  import 'route_names.dart';
  import '../../features/auth/presentation/screens/connect_screen.dart';
  import '../../features/dashboard/presentation/screens/dashboard_screen.dart';
  import '../../features/rules/presentation/screens/rules_list_screen.dart';
  import '../../features/agents/presentation/screens/agents_screen.dart';
  import '../../features/certificates/presentation/screens/certificates_screen.dart';
  import '../../features/relay/presentation/screens/relay_screen.dart';
  import '../../features/settings/presentation/screens/settings_screen.dart';

  final _rootNavigatorKey = GlobalKey<NavigatorState>();
  final _shellNavigatorKey = GlobalKey<NavigatorState>();

  final routerProvider = Provider<GoRouter>((ref) {
    final caps = PlatformCapabilities.current;

    return GoRouter(
      navigatorKey: _rootNavigatorKey,
      initialLocation: RouteNames.dashboard,
      redirect: (context, state) {
        // TODO: Add auth guard once auth provider is built
        return null;
      },
      routes: [
        GoRoute(
          path: RouteNames.connect,
          builder: (context, state) => const ConnectScreen(),
        ),
        ShellRoute(
          navigatorKey: _shellNavigatorKey,
          builder: (context, state, child) => AppShell(child: child),
          routes: [
            GoRoute(
              path: RouteNames.dashboard,
              builder: (context, state) => const DashboardScreen(),
            ),
            GoRoute(
              path: RouteNames.rules,
              builder: (context, state) => const RulesListScreen(),
            ),
            if (caps.canManageCertificates)
              GoRoute(
                path: RouteNames.certificates,
                builder: (context, state) => const CertificatesScreen(),
              ),
            GoRoute(
              path: RouteNames.agents,
              builder: (context, state) => const AgentsScreen(),
            ),
            if (caps.canManageRelay)
              GoRoute(
                path: RouteNames.relay,
                builder: (context, state) => const RelayScreen(),
              ),
            GoRoute(
              path: RouteNames.settings,
              builder: (context, state) => const SettingsScreen(),
            ),
          ],
        ),
      ],
    );
  });

  class AppShell extends StatelessWidget {
    const AppShell({super.key, required this.child});
    final Widget child;

    @override
    Widget build(BuildContext context) {
      return LayoutBuilder(
        builder: (context, constraints) {
          final isDesktop = constraints.maxWidth >= 600;
          if (isDesktop) {
            return Scaffold(
              body: Row(
                children: [
                  _DesktopNavigation(),
                  const VerticalDivider(thickness: 1, width: 1),
                  Expanded(child: child),
                ],
              ),
            );
          }
          return Scaffold(
            body: child,
            bottomNavigationBar: _MobileNavigation(),
          );
        },
      );
    }
  }

  class _DesktopNavigation extends ConsumerWidget {
    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final caps = PlatformCapabilities.current;
      final location = GoRouterState.of(context).matchedLocation;

      final destinations = [
        const NavigationRailDestination(
          icon: Icon(Icons.dashboard_outlined),
          selectedIcon: Icon(Icons.dashboard),
          label: Text('Dashboard'),
        ),
        const NavigationRailDestination(
          icon: Icon(Icons.rule_outlined),
          selectedIcon: Icon(Icons.rule),
          label: Text('Rules'),
        ),
        if (caps.canManageCertificates)
          const NavigationRailDestination(
            icon: Icon(Icons.security_outlined),
            selectedIcon: Icon(Icons.security),
            label: Text('Certificates'),
          ),
        const NavigationRailDestination(
          icon: Icon(Icons.memory_outlined),
          selectedIcon: Icon(Icons.memory),
          label: Text('Agents'),
        ),
        if (caps.canManageRelay)
          const NavigationRailDestination(
            icon: Icon(Icons.sync_alt_outlined),
            selectedIcon: Icon(Icons.sync_alt),
            label: Text('Relay'),
          ),
        const NavigationRailDestination(
          icon: Icon(Icons.settings_outlined),
          selectedIcon: Icon(Icons.settings),
          label: Text('Settings'),
        ),
      ];

      int selectedIndex = 0;
      if (location.startsWith('/rules')) selectedIndex = 1;
      else if (location.startsWith('/certificates')) selectedIndex = 2;
      else if (location.startsWith('/agents')) selectedIndex = caps.canManageCertificates ? 3 : 2;
      else if (location.startsWith('/relay')) selectedIndex = caps.canManageCertificates ? 4 : 3;
      else if (location.startsWith('/settings')) selectedIndex = destinations.length - 1;

      return NavigationRail(
        selectedIndex: selectedIndex,
        onDestinationSelected: (index) {
          final routes = [
            RouteNames.dashboard,
            RouteNames.rules,
            if (caps.canManageCertificates) RouteNames.certificates,
            RouteNames.agents,
            if (caps.canManageRelay) RouteNames.relay,
            RouteNames.settings,
          ];
          context.go(routes[index]);
        },
        labelType: NavigationRailLabelType.all,
        destinations: destinations,
      );
    }
  }

  class _MobileNavigation extends ConsumerWidget {
    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final location = GoRouterState.of(context).matchedLocation;

      int selectedIndex = 0;
      if (location.startsWith('/rules')) selectedIndex = 1;
      else if (location.startsWith('/agents')) selectedIndex = 2;
      else if (location.startsWith('/settings')) selectedIndex = 3;

      return NavigationBar(
        selectedIndex: selectedIndex,
        onDestinationSelected: (index) {
          final routes = [
            RouteNames.dashboard,
            RouteNames.rules,
            RouteNames.agents,
            RouteNames.settings,
          ];
          context.go(routes[index]);
        },
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.dashboard_outlined),
            selectedIcon: Icon(Icons.dashboard),
            label: 'Dashboard',
          ),
          NavigationDestination(
            icon: Icon(Icons.rule_outlined),
            selectedIcon: Icon(Icons.rule),
            label: 'Rules',
          ),
          NavigationDestination(
            icon: Icon(Icons.memory_outlined),
            selectedIcon: Icon(Icons.memory),
            label: 'Agents',
          ),
          NavigationDestination(
            icon: Icon(Icons.settings_outlined),
            selectedIcon: Icon(Icons.settings),
            label: 'Settings',
          ),
        ],
      );
    }
  }
  ```

- [ ] **Step 3: Rewrite app.dart**

  ```dart
  // lib/app.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import 'core/routing/app_router.dart';
  import 'core/theme/app_theme.dart';
  import 'core/theme/theme_controller.dart';

  class NreClientApp extends ConsumerWidget {
    const NreClientApp({super.key});

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final router = ref.watch(routerProvider);
      final themeAsync = ref.watch(themeControllerProvider);

      return themeAsync.when(
        data: (settings) => MaterialApp.router(
          title: 'NRE Client',
          debugShowCheckedModeBanner: false,
          themeMode: settings.themeMode,
          theme: AppTheme.buildTheme(settings.colorScheme, ThemeMode.light),
          darkTheme: AppTheme.buildTheme(settings.colorScheme, ThemeMode.dark),
          routerConfig: router,
          localizationsDelegates: const [],
          supportedLocales: const [Locale('en'), Locale('zh')],
        ),
        loading: () => const MaterialApp(
          home: Scaffold(body: Center(child: CircularProgressIndicator())),
        ),
        error: (_, __) => const MaterialApp(
          home: Scaffold(body: Center(child: Text('Failed to load theme'))),
        ),
      );
    }
  }
  ```

- [ ] **Step 4: Rewrite main.dart**

  ```dart
  // lib/main.dart
  import 'dart:io';
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import 'package:window_manager/window_manager.dart';
  import 'package:tray_manager/tray_manager.dart' as tray;
  import 'app.dart';

  Future<void> main() async {
    WidgetsFlutterBinding.ensureInitialized();

    if (_isDesktop) {
      await windowManager.ensureInitialized();
      await windowManager.waitUntilReadyToShow(
        const WindowOptions(
          size: Size(1024, 700),
          minimumSize: Size(720, 520),
          center: true,
          title: 'NRE Client',
          backgroundColor: Colors.transparent,
          skipTaskbar: false,
        ),
        () async {
          await windowManager.show();
          await windowManager.focus();
        },
      );

      await _setupTray();
      windowManager.addListener(_WindowCloseHandler());
    }

    runApp(const ProviderScope(child: NreClientApp()));
  }

  bool get _isDesktop =>
      Platform.isWindows || Platform.isMacOS || Platform.isLinux;

  Future<void> _setupTray() async {
    try {
      await tray.trayManager.setToolTip('NRE Client');
      await tray.trayManager.setContextMenu(
        tray.Menu(
          items: [
            tray.MenuItem(key: 'show', label: 'Show'),
            tray.MenuItem.separator(),
            tray.MenuItem(key: 'quit', label: 'Quit'),
          ],
        ),
      );
      tray.trayManager.addListener(_TrayHandler());
    } catch (_) {
      // Tray may not be available
    }
  }

  class _TrayHandler extends tray.TrayListener {
    @override
    void onTrayIconMouseDown() => tray.trayManager.popUpContextMenu();

    @override
    void onTrayMenuItemClick(tray.MenuItem menuItem) async {
      switch (menuItem.key) {
        case 'show':
          await windowManager.show();
          await windowManager.focus();
        case 'quit':
          await tray.trayManager.destroy();
          await windowManager.close();
      }
    }
  }

  class _WindowCloseHandler extends WindowListener {
    @override
    void onWindowClose() async => await windowManager.hide();
  }
  ```

- [ ] **Step 5: Create stub screens to verify compilation**

  Create minimal stub files for each screen so the app compiles:
  - `lib/features/auth/presentation/screens/connect_screen.dart`
  - `lib/features/dashboard/presentation/screens/dashboard_screen.dart`
  - `lib/features/rules/presentation/screens/rules_list_screen.dart`
  - `lib/features/agents/presentation/screens/agents_screen.dart`
  - `lib/features/certificates/presentation/screens/certificates_screen.dart`
  - `lib/features/relay/presentation/screens/relay_screen.dart`
  - `lib/features/settings/presentation/screens/settings_screen.dart`

  Each stub is a simple `StatelessWidget` with `Scaffold(body: Center(child: Text('Screen Name')))`.

- [ ] **Step 6: Verify app builds**

  ```bash
  cd clients/flutter && flutter build apk --debug 2>&1 | tail -20
  ```

  Expected: Build succeeds (or shows only warnings, no errors).

- [ ] **Step 7: Commit**

  ```bash
  cd clients/flutter && git add lib/ && git commit -m "feat(core): add go_router navigation with adaptive shell layout"
  ```

---

## Task 8: Feature — Auth (Connection Wizard)

**Files:**
- Create: `clients/flutter/lib/features/auth/data/models/auth_models.dart`
- Create: `clients/flutter/lib/features/auth/data/repositories/auth_repository.dart`
- Create: `clients/flutter/lib/features/auth/presentation/providers/auth_provider.dart`
- Create: `clients/flutter/lib/features/auth/presentation/screens/connect_screen.dart`

- [ ] **Step 1: Write auth models**

  ```dart
  // lib/features/auth/data/models/auth_models.dart
  import 'package:freezed_annotation/freezed_annotation.dart';

  part 'auth_models.freezed.dart';
  part 'auth_models.g.dart';

  @freezed
  class ClientProfile with _$ClientProfile {
    const factory ClientProfile({
      @Default('') String masterUrl,
      @Default('') String displayName,
      @Default('') String agentId,
      @Default('') String token,
    }) = _ClientProfile;

    factory ClientProfile.fromJson(Map<String, Object?> json) =>
        _$ClientProfileFromJson(json);

    const ClientProfile._();

    bool get isRegistered => agentId.isNotEmpty && token.isNotEmpty;
  }

  @freezed
  class AuthState with _$AuthState {
    const factory AuthState.unauthenticated() = _Unauthenticated;
    const factory AuthState.authenticated(ClientProfile profile) = _Authenticated;
    const factory AuthState.loading() = _Loading;
    const factory AuthState.error(String message) = _Error;
  }
  ```

- [ ] **Step 2: Generate Freezed code**

  ```bash
  cd clients/flutter && flutter pub run build_runner build --delete-conflicting-outputs
  ```

- [ ] **Step 3: Write auth repository**

  ```dart
  // lib/features/auth/data/repositories/auth_repository.dart
  import 'dart:convert';
  import 'package:shared_preferences/shared_preferences.dart';
  import '../models/auth_models.dart';

  class AuthRepository {
    static const _profileKey = 'client_profile';

    Future<ClientProfile> loadProfile() async {
      final prefs = await SharedPreferences.getInstance();
      final json = prefs.getString(_profileKey);
      if (json == null) return const ClientProfile();
      try {
        return ClientProfile.fromJson(
          jsonDecode(json) as Map<String, dynamic>,
        );
      } catch (_) {
        return const ClientProfile();
      }
    }

    Future<void> saveProfile(ClientProfile profile) async {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_profileKey, jsonEncode(profile.toJson()));
    }

    Future<void> clearProfile() async {
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_profileKey);
    }
  }
  ```

- [ ] **Step 4: Write auth provider**

  ```dart
  // lib/features/auth/presentation/providers/auth_provider.dart
  import 'package:riverpod_annotation/riverpod_annotation.dart';
  import '../../../../core/network/api_client.dart';
  import '../../data/models/auth_models.dart';
  import '../../data/repositories/auth_repository.dart';

  part 'auth_provider.g.dart';

  final authRepositoryProvider = Provider((ref) => AuthRepository());

  @riverpod
  class AuthNotifier extends _$AuthNotifier {
    @override
    Future<AuthState> build() async {
      final repo = ref.read(authRepositoryProvider);
      final profile = await repo.loadProfile();
      return profile.isRegistered
          ? AuthState.authenticated(profile)
          : const AuthState.unauthenticated();
    }

    Future<void> register({
      required String masterUrl,
      required String registerToken,
      required String name,
    }) async {
      state = const AsyncData(AuthState.loading());

      try {
        // TODO: Use actual API client once integrated
        // final api = ref.read(apiClientProvider);
        // final result = await api.registerAgent({...});

        // Stub: simulate success
        final profile = ClientProfile(
          masterUrl: masterUrl,
          displayName: name,
          agentId: 'agent-${DateTime.now().millisecondsSinceEpoch}',
          token: 'tok-${DateTime.now().millisecondsSinceEpoch}',
        );

        await ref.read(authRepositoryProvider).saveProfile(profile);
        state = AsyncData(AuthState.authenticated(profile));
      } catch (e) {
        state = AsyncData(AuthState.error(e.toString()));
      }
    }

    Future<void> logout() async {
      await ref.read(authRepositoryProvider).clearProfile();
      state = const AsyncData(AuthState.unauthenticated());
    }
  }
  ```

- [ ] **Step 5: Generate Riverpod code**

  ```bash
  cd clients/flutter && flutter pub run build_runner build --delete-conflicting-outputs
  ```

- [ ] **Step 6: Write connect screen**

  ```dart
  // lib/features/auth/presentation/screens/connect_screen.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import '../providers/auth_provider.dart';

  class ConnectScreen extends ConsumerStatefulWidget {
    const ConnectScreen({super.key});

    @override
    ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
  }

  class _ConnectScreenState extends ConsumerState<ConnectScreen> {
    final _formKey = GlobalKey<FormState>();
    final _urlController = TextEditingController();
    final _tokenController = TextEditingController();
    final _nameController = TextEditingController(text: 'nre-client');
    var _step = 0;

    @override
    void dispose() {
      _urlController.dispose();
      _tokenController.dispose();
      _nameController.dispose();
      super.dispose();
    }

    @override
    Widget build(BuildContext context) {
      final theme = Theme.of(context);
      final authAsync = ref.watch(authNotifierProvider);

      return Scaffold(
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 480),
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Card(
                child: Padding(
                  padding: const EdgeInsets.all(24),
                  child: Form(
                    key: _formKey,
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '连接到 Master',
                          style: theme.textTheme.headlineSmall?.copyWith(
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                        const SizedBox(height: 8),
                        Text(
                          'Step ${_step + 1} of 3',
                          style: theme.textTheme.bodyMedium?.copyWith(
                            color: theme.colorScheme.outline,
                          ),
                        ),
                        const SizedBox(height: 24),
                        if (_step == 0) ...[
                          TextFormField(
                            controller: _urlController,
                            decoration: const InputDecoration(
                              labelText: 'Master URL',
                              hintText: 'https://your-server.com',
                              prefixIcon: Icon(Icons.link),
                            ),
                            validator: (v) => v == null || v.isEmpty ? '请输入 URL' : null,
                          ),
                        ] else if (_step == 1) ...[
                          TextFormField(
                            controller: _tokenController,
                            decoration: const InputDecoration(
                              labelText: 'Register Token',
                              hintText: '从服务器获取的注册令牌',
                              prefixIcon: Icon(Icons.key),
                            ),
                            obscureText: true,
                            validator: (v) => v == null || v.isEmpty ? '请输入 Token' : null,
                          ),
                        ] else ...[
                          TextFormField(
                            controller: _nameController,
                            decoration: const InputDecoration(
                              labelText: '客户端名称',
                              hintText: 'nre-client',
                              prefixIcon: Icon(Icons.badge),
                            ),
                          ),
                        ],
                        if (authAsync.value is AuthStateError) ...[
                          const SizedBox(height: 16),
                          Text(
                            (authAsync.value as AuthStateError).message,
                            style: TextStyle(color: theme.colorScheme.error),
                          ),
                        ],
                        const SizedBox(height: 24),
                        Row(
                          children: [
                            if (_step > 0)
                              OutlinedButton(
                                onPressed: () => setState(() => _step--),
                                child: const Text('上一步'),
                              ),
                            const Spacer(),
                            FilledButton(
                              onPressed: authAsync.isLoading ? null : _onNext,
                              child: authAsync.isLoading
                                  ? const SizedBox.square(
                                      dimension: 18,
                                      child: CircularProgressIndicator(strokeWidth: 2),
                                    )
                                  : Text(_step < 2 ? '下一步' : '连接'),
                            ),
                          ],
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    }

    void _onNext() {
      if (!(_formKey.currentState?.validate() ?? false)) return;
      if (_step < 2) {
        setState(() => _step++);
      } else {
        ref.read(authNotifierProvider.notifier).register(
          masterUrl: _urlController.text.trim(),
          registerToken: _tokenController.text.trim(),
          name: _nameController.text.trim(),
        );
      }
    }
  }
  ```

- [ ] **Step 7: Commit**

  ```bash
  cd clients/flutter && git add lib/features/auth/ && git commit -m "feat(auth): add connection wizard with profile persistence"
  ```

---

## Task 9: Feature — Dashboard

**Files:**
- Create: `clients/flutter/lib/features/dashboard/presentation/screens/dashboard_screen.dart`
- Modify: `clients/flutter/lib/core/routing/app_router.dart` — Add auth guard redirect

- [ ] **Step 1: Write dashboard screen**

  ```dart
  // lib/features/dashboard/presentation/screens/dashboard_screen.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import 'package:go_router/go_router.dart';
  import '../../../../core/platform/platform_capabilities.dart';
  import '../../../../core/routing/route_names.dart';
  import '../../../../shared/widgets/nre_card.dart';
  import '../../../../shared/widgets/nre_empty_state.dart';
  import '../../../../shared/widgets/nre_status_chip.dart';
  import '../../../auth/presentation/providers/auth_provider.dart';

  class DashboardScreen extends ConsumerWidget {
    const DashboardScreen({super.key});

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final theme = Theme.of(context);
      final scheme = theme.colorScheme;
      final authAsync = ref.watch(authNotifierProvider);
      final caps = PlatformCapabilities.current;

      return Scaffold(
        appBar: AppBar(title: const Text('Dashboard')),
        body: authAsync.when(
          data: (state) => state.maybeWhen(
            authenticated: (profile) => _buildDashboard(context, profile, caps, scheme, theme),
            orElse: () => NreEmptyState(
              icon: Icons.cloud_off,
              title: '未连接',
              message: '请先连接到 Master 服务器',
              action: FilledButton(
                onPressed: () => context.go(RouteNames.connect),
                child: const Text('去连接'),
              ),
            ),
          ),
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, __) => const Center(child: Text('Error')),
        ),
      );
    }

    Widget _buildDashboard(BuildContext context, ClientProfile profile,
        PlatformCapabilities caps, ColorScheme scheme, ThemeData theme) {
      return ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _StatCard(
            icon: Icons.check_circle,
            iconColor: Colors.green,
            label: '连接状态',
            value: '已连接',
            scheme: scheme,
          ),
          const SizedBox(height: 12),
          _StatCard(
            icon: Icons.rule,
            iconColor: scheme.primary,
            label: '规则总数',
            value: '—',
            scheme: scheme,
          ),
          const SizedBox(height: 12),
          _StatCard(
            icon: Icons.memory,
            iconColor: scheme.secondary,
            label: 'Agent 在线',
            value: '—',
            scheme: scheme,
          ),
          if (caps.canManageLocalAgent) ...[
            const SizedBox(height: 16),
            NreCard(
              hasAccentBar: true,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Icon(Icons.play_circle, color: scheme.primary),
                      const SizedBox(width: 8),
                      Text('本地 Agent', style: theme.textTheme.titleMedium),
                      const Spacer(),
                      const NreStatusChip(label: '已停止', type: StatusType.warning),
                    ],
                  ),
                  const SizedBox(height: 16),
                  Row(
                    children: [
                      Expanded(
                        child: FilledButton.icon(
                          onPressed: () {},
                          icon: const Icon(Icons.play_arrow),
                          label: const Text('启动'),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: FilledButton.tonalIcon(
                          onPressed: null,
                          icon: const Icon(Icons.stop),
                          label: const Text('停止'),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ],
          const SizedBox(height: 16),
          NreCard(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('快捷操作', style: theme.textTheme.titleMedium),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    ActionChip(
                      avatar: const Icon(Icons.rule, size: 18),
                      label: const Text('规则'),
                      onPressed: () => context.go(RouteNames.rules),
                    ),
                    if (caps.canManageCertificates)
                      ActionChip(
                        avatar: const Icon(Icons.security, size: 18),
                        label: const Text('证书'),
                        onPressed: () => context.go(RouteNames.certificates),
                      ),
                    ActionChip(
                      avatar: const Icon(Icons.memory, size: 18),
                      label: const Text('Agents'),
                      onPressed: () => context.go(RouteNames.agents),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      );
    }
  }

  class _StatCard extends StatelessWidget {
    const _StatCard({
      required this.icon,
      required this.iconColor,
      required this.label,
      required this.value,
      required this.scheme,
    });

    final IconData icon;
    final Color iconColor;
    final String label;
    final String value;
    final ColorScheme scheme;

    @override
    Widget build(BuildContext context) {
      return NreCard(
        child: Row(
          children: [
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: iconColor.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(12),
              ),
              child: Icon(icon, color: iconColor),
            ),
            const SizedBox(width: 16),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(label, style: TextStyle(color: scheme.outline)),
                  Text(
                    value,
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      );
    }
  }
  ```

- [ ] **Step 2: Add auth guard to router**

  In `lib/core/routing/app_router.dart`, update the `redirect` callback:

  ```dart
  redirect: (context, state) {
    final auth = ref.read(authNotifierProvider);
    final isAuth = auth.value?.maybeWhen(
      authenticated: (_) => true,
      orElse: () => false,
    ) ?? false;

    final isConnectRoute = state.matchedLocation == RouteNames.connect;

    if (!isAuth && !isConnectRoute) return RouteNames.connect;
    if (isAuth && isConnectRoute) return RouteNames.dashboard;
    return null;
  },
  ```

- [ ] **Step 3: Commit**

  ```bash
  cd clients/flutter && git add lib/ && git commit -m "feat(dashboard): add overview dashboard with stat cards and quick actions"
  ```

---

## Task 10: Feature — Rules (List + Read-Only)

**Files:**
- Create: `clients/flutter/lib/features/rules/data/models/rule_models.dart`
- Create: `clients/flutter/lib/features/rules/presentation/providers/rules_provider.dart`
- Create: `clients/flutter/lib/features/rules/presentation/screens/rules_list_screen.dart`

- [ ] **Step 1: Write rule models**

  ```dart
  // lib/features/rules/data/models/rule_models.dart
  import 'package:freezed_annotation/freezed_annotation.dart';

  part 'rule_models.freezed.dart';
  part 'rule_models.g.dart';

  @freezed
  class ProxyRule with _$ProxyRule {
    const factory ProxyRule({
      required String id,
      required String domain,
      required String target,
      @Default('http') String type,
      @Default(true) bool enabled,
    }) = _ProxyRule;

    factory ProxyRule.fromJson(Map<String, Object?> json) =>
        _$ProxyRuleFromJson(json);
  }

  @freezed
  class CreateRuleRequest with _$CreateRuleRequest {
    const factory CreateRuleRequest({
      required String domain,
      required String target,
      @Default('http') String type,
      @Default(true) bool enabled,
    }) = _CreateRuleRequest;

    factory CreateRuleRequest.fromJson(Map<String, Object?> json) =>
        _$CreateRuleRequestFromJson(json);
  }

  @freezed
  class UpdateRuleRequest with _$UpdateRuleRequest {
    const factory UpdateRuleRequest({
      String? domain,
      String? target,
      String? type,
      bool? enabled,
    }) = _UpdateRuleRequest;

    factory UpdateRuleRequest.fromJson(Map<String, Object?> json) =>
        _$UpdateRuleRequestFromJson(json);
  }
  ```

- [ ] **Step 2: Generate Freezed code**

  ```bash
  cd clients/flutter && flutter pub run build_runner build --delete-conflicting-outputs
  ```

- [ ] **Step 3: Write rules provider**

  ```dart
  // lib/features/rules/presentation/providers/rules_provider.dart
  import 'package:riverpod_annotation/riverpod_annotation.dart';
  import '../../../../core/network/api_client.dart';
  import '../../data/models/rule_models.dart';

  part 'rules_provider.g.dart';

  @riverpod
  class RulesList extends _$RulesList {
    @override
    Future<List<ProxyRule>> build() async {
      // TODO: Integrate with real API client
      // final api = ref.watch(apiClientProvider);
      // return api.getRules();

      // Stub data for now
      await Future.delayed(const Duration(milliseconds: 500));
      return [
        const ProxyRule(id: '1', domain: 'example.com', target: 'localhost:8080', type: 'http', enabled: true),
        const ProxyRule(id: '2', domain: 'api.local', target: 'localhost:3000', type: 'http', enabled: false),
      ];
    }

    Future<void> refresh() async {
      state = const AsyncLoading();
      state = await AsyncValue.guard(() async {
        await Future.delayed(const Duration(milliseconds: 500));
        return [
          const ProxyRule(id: '1', domain: 'example.com', target: 'localhost:8080', type: 'http', enabled: true),
          const ProxyRule(id: '2', domain: 'api.local', target: 'localhost:3000', type: 'http', enabled: false),
        ];
      });
    }

    Future<void> toggleRule(String id, bool enabled) async {
      final previous = state.value ?? [];
      state = AsyncData(previous.map((r) =>
        r.id == id ? r.copyWith(enabled: enabled) : r
      ).toList());

      try {
        // TODO: await ref.read(apiClientProvider).toggleRule(id, enabled);
        await Future.delayed(const Duration(milliseconds: 200));
      } catch (e) {
        state = AsyncData(previous);
        rethrow;
      }
    }
  }
  ```

- [ ] **Step 4: Generate Riverpod code**

  ```bash
  cd clients/flutter && flutter pub run build_runner build --delete-conflicting-outputs
  ```

- [ ] **Step 5: Write rules list screen**

  ```dart
  // lib/features/rules/presentation/screens/rules_list_screen.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import '../../../../core/platform/platform_capabilities.dart';
  import '../../../../shared/widgets/nre_card.dart';
  import '../../../../shared/widgets/nre_empty_state.dart';
  import '../../../../shared/widgets/nre_error_widget.dart';
  import '../../../../shared/widgets/nre_skeleton_list.dart';
  import '../../../../shared/widgets/nre_status_chip.dart';
  import '../../data/models/rule_models.dart';
  import '../providers/rules_provider.dart';

  class RulesListScreen extends ConsumerWidget {
    const RulesListScreen({super.key});

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final rulesAsync = ref.watch(rulesListProvider);
      final caps = PlatformCapabilities.current;

      return Scaffold(
        appBar: AppBar(
          title: const Text('规则'),
          actions: [
            IconButton(
              onPressed: () => ref.read(rulesListProvider.notifier).refresh(),
              icon: const Icon(Icons.refresh),
            ),
          ],
        ),
        body: rulesAsync.when(
          data: (rules) => rules.isEmpty
              ? const NreEmptyState(
                  icon: Icons.inbox,
                  title: '暂无规则',
                  message: '当前没有配置代理规则',
                )
              : ListView.separated(
                  padding: const EdgeInsets.all(16),
                  itemCount: rules.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 12),
                  itemBuilder: (_, index) => _RuleCard(
                    rule: rules[index],
                    canEdit: caps.canEditRules,
                  ),
                ),
          loading: () => const NreSkeletonList(itemCount: 4),
          error: (err, _) => NreErrorWidget(
            error: err,
            onRetry: () => ref.read(rulesListProvider.notifier).refresh(),
          ),
        ),
        floatingActionButton: caps.canEditRules
            ? FloatingActionButton(
                onPressed: () {},
                child: const Icon(Icons.add),
              )
            : null,
      );
    }
  }

  class _RuleCard extends ConsumerWidget {
    const _RuleCard({required this.rule, required this.canEdit});

    final ProxyRule rule;
    final bool canEdit;

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final theme = Theme.of(context);
      final scheme = theme.colorScheme;

      return NreCard(
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    rule.domain,
                    style: theme.textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                        decoration: BoxDecoration(
                          color: scheme.surfaceContainerHighest,
                          borderRadius: BorderRadius.circular(6),
                        ),
                        child: Text(
                          'Target: ${rule.target}',
                          style: theme.textTheme.bodySmall,
                        ),
                      ),
                      const SizedBox(width: 8),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                        decoration: BoxDecoration(
                          color: scheme.primaryContainer,
                          borderRadius: BorderRadius.circular(6),
                        ),
                        child: Text(
                          rule.type.toUpperCase(),
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: scheme.onPrimaryContainer,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            Switch(
              value: rule.enabled,
              onChanged: canEdit
                  ? (value) => ref.read(rulesListProvider.notifier).toggleRule(rule.id, value)
                  : null,
            ),
          ],
        ),
      );
    }
  }
  ```

- [ ] **Step 6: Commit**

  ```bash
  cd clients/flutter && git add lib/features/rules/ && git commit -m "feat(rules): add rules list with toggle and platform-aware editing"
  ```

---

## Task 11: Feature — Agents, Certificates, Relay, Settings (Stub Screens)

**Files:**
- Create: `clients/flutter/lib/features/agents/presentation/screens/agents_screen.dart`
- Create: `clients/flutter/lib/features/certificates/presentation/screens/certificates_screen.dart`
- Create: `clients/flutter/lib/features/relay/presentation/screens/relay_screen.dart`
- Create: `clients/flutter/lib/features/settings/presentation/screens/settings_screen.dart`

- [ ] **Step 1: Write Agents screen**

  ```dart
  // lib/features/agents/presentation/screens/agents_screen.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import '../../../../core/platform/platform_capabilities.dart';
  import '../../../../shared/widgets/nre_card.dart';
  import '../../../../shared/widgets/nre_empty_state.dart';

  class AgentsScreen extends ConsumerWidget {
    const AgentsScreen({super.key});

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final caps = PlatformCapabilities.current;

      return DefaultTabController(
        length: caps.canManageLocalAgent ? 2 : 1,
        child: Scaffold(
          appBar: AppBar(
            title: const Text('Agents'),
            bottom: caps.canManageLocalAgent
                ? const TabBar(
                    tabs: [
                      Tab(icon: Icon(Icons.devices), text: '远程'),
                      Tab(icon: Icon(Icons.computer), text: '本地'),
                    ],
                  )
                : null,
          ),
          body: TabBarView(
            children: [
              const _RemoteAgentsTab(),
              if (caps.canManageLocalAgent) const _LocalAgentTab(),
            ],
          ),
        ),
      );
    }
  }

  class _RemoteAgentsTab extends StatelessWidget {
    const _RemoteAgentsTab();

    @override
    Widget build(BuildContext context) {
      return const NreEmptyState(
        icon: Icons.devices,
        title: '暂无 Agent',
        message: '未找到远程 Agent',
      );
    }
  }

  class _LocalAgentTab extends StatelessWidget {
    const _LocalAgentTab();

    @override
    Widget build(BuildContext context) {
      final scheme = Theme.of(context).colorScheme;
      return ListView(
        padding: const EdgeInsets.all(16),
        children: [
          NreCard(
            hasAccentBar: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.memory, color: scheme.primary),
                    const SizedBox(width: 8),
                    Text('本地 Agent 进程', style: Theme.of(context).textTheme.titleMedium),
                  ],
                ),
                const Divider(),
                const Text('PID: —'),
                const Text('状态: 已停止'),
                const SizedBox(height: 16),
                Row(
                  children: [
                    Expanded(
                      child: FilledButton.icon(
                        onPressed: () {},
                        icon: const Icon(Icons.play_arrow),
                        label: const Text('启动'),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: FilledButton.tonalIcon(
                        onPressed: null,
                        icon: const Icon(Icons.stop),
                        label: const Text('停止'),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      );
    }
  }
  ```

- [ ] **Step 2: Write Certificates screen**

  ```dart
  // lib/features/certificates/presentation/screens/certificates_screen.dart
  import 'package:flutter/material.dart';
  import '../../../../shared/widgets/nre_empty_state.dart';

  class CertificatesScreen extends StatelessWidget {
    const CertificatesScreen({super.key});

    @override
    Widget build(BuildContext context) {
      return Scaffold(
        appBar: AppBar(title: const Text('证书')),
        body: const NreEmptyState(
          icon: Icons.security,
          title: '暂无证书',
          message: '当前没有配置 SSL 证书',
        ),
      );
    }
  }
  ```

- [ ] **Step 3: Write Relay screen**

  ```dart
  // lib/features/relay/presentation/screens/relay_screen.dart
  import 'package:flutter/material.dart';
  import '../../../../shared/widgets/nre_empty_state.dart';

  class RelayScreen extends StatelessWidget {
    const RelayScreen({super.key});

    @override
    Widget build(BuildContext context) {
      return Scaffold(
        appBar: AppBar(title: const Text('中继监听')),
        body: const NreEmptyState(
          icon: Icons.sync_alt,
          title: '暂无中继监听',
          message: '当前没有配置中继监听器',
        ),
      );
    }
  }
  ```

- [ ] **Step 4: Write Settings screen**

  ```dart
  // lib/features/settings/presentation/screens/settings_screen.dart
  import 'package:flutter/material.dart';
  import 'package:flutter_riverpod/flutter_riverpod.dart';
  import '../../../../core/theme/color_schemes.dart';
  import '../../../../core/theme/theme_controller.dart';
  import '../../../auth/presentation/providers/auth_provider.dart';

  class SettingsScreen extends ConsumerWidget {
    const SettingsScreen({super.key});

    @override
    Widget build(BuildContext context, WidgetRef ref) {
      final themeAsync = ref.watch(themeControllerProvider);

      return Scaffold(
        appBar: AppBar(title: const Text('设置')),
        body: themeAsync.when(
          data: (settings) => ListView(
            children: [
              _SectionHeader(title: '外观', icon: Icons.palette),
              ListTile(
                leading: const Icon(Icons.dark_mode),
                title: const Text('主题模式'),
                trailing: DropdownButton<ThemeMode>(
                  value: settings.themeMode,
                  underline: const SizedBox.shrink(),
                  items: ThemeMode.values.map((mode) => DropdownMenuItem(
                    value: mode,
                    child: Text(switch (mode) {
                      ThemeMode.system => '跟随系统',
                      ThemeMode.light => '浅色',
                      ThemeMode.dark => '深色',
                    }),
                  )).toList(),
                  onChanged: (mode) {
                    if (mode != null) {
                      ref.read(themeControllerProvider.notifier).setThemeMode(mode);
                    }
                  },
                ),
              ),
              ListTile(
                leading: const Icon(Icons.color_lens),
                title: const Text('主题色'),
                trailing: Wrap(
                  spacing: 8,
                  children: AppColorScheme.values.map((scheme) => InkWell(
                    onTap: () => ref.read(themeControllerProvider.notifier).setColorScheme(scheme),
                    child: Container(
                      width: 28,
                      height: 28,
                      decoration: BoxDecoration(
                        color: scheme.primaryLight,
                        shape: BoxShape.circle,
                        border: settings.colorScheme == scheme
                            ? Border.all(color: Colors.white, width: 2)
                            : null,
                      ),
                    ),
                  )).toList(),
                ),
              ),
              const Divider(),
              _SectionHeader(title: '连接', icon: Icons.link),
              ListTile(
                leading: const Icon(Icons.logout),
                title: const Text('断开连接', style: TextStyle(color: Colors.red)),
                onTap: () => ref.read(authNotifierProvider.notifier).logout(),
              ),
              const Divider(),
              _SectionHeader(title: '关于', icon: Icons.info),
              const ListTile(
                leading: Icon(Icons.app_shortcut),
                title: Text('应用'),
                subtitle: Text('NRE Client'),
              ),
              const ListTile(
                leading: Icon(Icons.tag),
                title: Text('版本'),
                subtitle: Text('2.1.0'),
              ),
            ],
          ),
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, __) => const Center(child: Text('Error')),
        ),
      );
    }
  }

  class _SectionHeader extends StatelessWidget {
    const _SectionHeader({required this.title, required this.icon});
    final String title;
    final IconData icon;

    @override
    Widget build(BuildContext context) {
      final scheme = Theme.of(context).colorScheme;
      return Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
        child: Row(
          children: [
            Icon(icon, size: 18, color: scheme.primary),
            const SizedBox(width: 8),
            Text(
              title,
              style: TextStyle(
                color: scheme.primary,
                fontWeight: FontWeight.bold,
              ),
            ),
          ],
        ),
      );
    }
  }
  ```

- [ ] **Step 5: Commit**

  ```bash
  cd clients/flutter && git add lib/features/ && git commit -m "feat(features): add agents, certificates, relay, and settings screens"
  ```

---

## Task 12: Shared Extensions + Animation Widgets

**Files:**
- Create: `clients/flutter/lib/shared/extensions/build_context_ext.dart`
- Create: `clients/flutter/lib/shared/widgets/animated_list_item.dart`

- [ ] **Step 1: Write BuildContext extensions**

  ```dart
  // lib/shared/extensions/build_context_ext.dart
  import 'package:flutter/material.dart';

  extension BuildContextExt on BuildContext {
    ThemeData get theme => Theme.of(this);
    ColorScheme get colorScheme => Theme.of(this).colorScheme;
    MediaQueryData get mediaQuery => MediaQuery.of(this);
    Size get screenSize => MediaQuery.of(this).size;
    bool get isMobile => MediaQuery.of(this).size.width < 600;
    bool get isDesktop => MediaQuery.of(this).size.width >= 1200;

    void showSnack(String message, {bool isError = false}) {
      ScaffoldMessenger.of(this).showSnackBar(
        SnackBar(
          content: Text(message),
          backgroundColor: isError ? colorScheme.errorContainer : null,
          behavior: SnackBarBehavior.floating,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
          duration: const Duration(seconds: 3),
        ),
      );
    }
  }
  ```

- [ ] **Step 2: Write animated list item**

  ```dart
  // lib/shared/widgets/animated_list_item.dart
  import 'package:flutter/material.dart';

  class AnimatedListItem extends StatelessWidget {
    const AnimatedListItem({
      super.key,
      required this.index,
      required this.child,
    });

    final int index;
    final Widget child;

    @override
    Widget build(BuildContext context) {
      return TweenAnimationBuilder<double>(
        tween: Tween(begin: 0, end: 1),
        duration: Duration(milliseconds: 300 + (index * 50).clamp(0, 300)),
        curve: Curves.easeOutCubic,
        builder: (context, value, _) {
          return Opacity(
            opacity: value,
            child: Transform.translate(
              offset: Offset(0, (1 - value) * 20),
              child: child,
            ),
          );
        },
      );
    }
  }
  ```

- [ ] **Step 3: Commit**

  ```bash
  cd clients/flutter && git add lib/shared/ && git commit -m "feat(shared): add context extensions and animated list item"
  ```

---

## Task 13: Verify Complete Build

- [ ] **Step 1: Run Flutter analyze**

  ```bash
  cd clients/flutter && flutter analyze
  ```

  Expected: No errors (warnings are acceptable).

- [ ] **Step 2: Run all tests**

  ```bash
  cd clients/flutter && flutter test
  ```

  Expected: All existing + new tests pass.

- [ ] **Step 3: Build debug APK to verify**

  ```bash
  cd clients/flutter && flutter build apk --debug
  ```

  Expected: Build succeeds.

- [ ] **Step 4: Final commit**

  ```bash
  cd clients/flutter && git commit -m "feat(flutter): complete redesign foundation with anime theme and feature architecture" --allow-empty
  ```

---

## Self-Review Checklist

**1. Spec coverage:**
- Feature-first directory structure: Tasks 1-7 ✓
- Anime theme system: Tasks 4 ✓
- go_router navigation with ShellRoute: Task 7 ✓
- Auth connection wizard: Task 8 ✓
- Dashboard overview: Task 9 ✓
- Rules list + toggle: Task 10 ✓
- Agents/Certificates/Relay/Settings stubs: Task 11 ✓
- Shared widgets (NreCard, NreEmptyState, etc.): Task 5 ✓
- Dio + API client: Task 6 ✓
- Platform capabilities: Task 3 ✓
- Error handling (exception hierarchy): Task 2 ✓
- Responsive breakpoints: Task 7 (AppShell) ✓

**2. Placeholder scan:**
- No "TBD" or "TODO" in non-code context ✓
- All test steps have actual test code ✓
- All implementation steps have actual code ✓
- Commands have expected outputs ✓

**3. Type consistency:**
- `ProxyRule` model consistent across Tasks 6, 10 ✓
- `AuthState` consistent across Tasks 8, 9 ✓
- `ThemeSettings` consistent across Tasks 4, 11 ✓
- Route names consistent across Tasks 7, 8, 9, 10 ✓

**Gaps identified:**
- Rule CRUD form (create/edit) is not implemented — planned for Phase 3 but not in this foundation plan
- Certificate/Relay full CRUD — planned for Phase 3
- Real API integration (currently stubbed) — requires backend alignment
- Widget tests for screens — planned for Phase 5
- Integration tests — planned for Phase 5
- Local agent controller integration — uses existing Windows controller from old code

These gaps are intentional — this plan covers the **foundation + auth + dashboard + rules list** (Phase 1-2). Extended CRUD and testing will be in follow-up plans.

---

*Plan version: 1.0*  
*Based on design: docs/superpowers/specs/2026-04-30-flutter-client-redesign-design.md*
