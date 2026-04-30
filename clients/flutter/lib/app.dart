import 'dart:async';

import 'package:flutter/foundation.dart'
    show TargetPlatform, defaultTargetPlatform;
import 'package:flutter/material.dart';

import 'core/client_state.dart';
import 'l10n/app_localizations.dart';
import 'screens/agent_screen.dart';
import 'screens/dashboard_screen.dart';
import 'screens/rules_screen.dart';
import 'screens/settings_screen.dart';
import 'services/client_profile_store.dart';
import 'services/local_agent_controller.dart';
import 'services/local_agent_controller_factory.dart';
import 'services/master_api.dart';

class NreClientApp extends StatefulWidget {
  const NreClientApp({
    super.key,
    this.api = const HttpMasterApi(),
    this.generateAgentToken = defaultAgentTokenGenerator,
    this.profileStore,
    this.localAgentController,
    this.platform,
    this.version = '2.0.0',
    this.enableAutoRefresh = true,
    this.initialThemeMode = ThemeMode.system,
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore? profileStore;
  final LocalAgentController? localAgentController;
  final String? platform;
  final String version;
  final bool enableAutoRefresh;
  final ThemeMode initialThemeMode;

  @override
  State<NreClientApp> createState() => _NreClientAppState();
}

class _NreClientAppState extends State<NreClientApp> {
  late ThemeMode _themeMode;

  @override
  void initState() {
    super.initState();
    _themeMode = widget.initialThemeMode;
  }

  void _setThemeMode(ThemeMode mode) {
    if (_themeMode != mode) {
      setState(() => _themeMode = mode);
    }
  }

  static final _lightScheme = ColorScheme.fromSeed(
    seedColor: const Color(0xFF006D77),
    brightness: Brightness.light,
  ).copyWith(
    error: const Color(0xFFC1121F),
    errorContainer: const Color(0xFFFFDAD6),
    onErrorContainer: const Color(0xFF410002),
    surfaceContainerHighest: const Color(0xFFEFF1F3),
  );

  static final _darkScheme = ColorScheme.fromSeed(
    seedColor: const Color(0xFF4FD1C5),
    brightness: Brightness.dark,
  ).copyWith(
    error: const Color(0xFFFF6B6B),
    errorContainer: const Color(0xFF93000A),
    onErrorContainer: const Color(0xFFFFDAD6),
    surface: const Color(0xFF0F1419),
    surfaceContainerHighest: const Color(0xFF1E293B),
  );

  @override
  Widget build(BuildContext context) {
    final resolvedPlatform = widget.platform ?? currentClientPlatform();

    return MaterialApp(
      title: 'NRE Client',
      debugShowCheckedModeBanner: false,
      themeMode: _themeMode,
      theme: _buildTheme(_lightScheme, Brightness.light),
      darkTheme: _buildTheme(_darkScheme, Brightness.dark),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: NreClientHome(
        api: widget.api,
        generateAgentToken: widget.generateAgentToken,
        profileStore: widget.profileStore ?? PathProviderClientProfileStore(),
        localAgentController:
            widget.localAgentController ?? createLocalAgentController(),
        platform: resolvedPlatform,
        version: widget.version,
        enableAutoRefresh: widget.enableAutoRefresh,
        themeMode: _themeMode,
        onThemeModeChanged: _setThemeMode,
      ),
    );
  }

  ThemeData _buildTheme(ColorScheme scheme, Brightness brightness) {
    return ThemeData(
      useMaterial3: true,
      colorScheme: scheme,
      scaffoldBackgroundColor: scheme.surface,
      cardTheme: CardThemeData(
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
          side: BorderSide(color: scheme.outlineVariant.withValues(alpha: 0.5)),
        ),
        color: scheme.surfaceContainerHighest,
      ),
      appBarTheme: AppBarTheme(
        centerTitle: false,
        backgroundColor: scheme.surface,
        foregroundColor: scheme.onSurface,
        elevation: 0,
        scrolledUnderElevation: 0.5,
        titleTextStyle: TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: scheme.onSurface,
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        elevation: 1,
        backgroundColor: scheme.surfaceContainerHighest,
        indicatorColor: scheme.primaryContainer,
        labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: scheme.surfaceContainerHighest,
        indicatorColor: scheme.primaryContainer,
        selectedIconTheme: IconThemeData(color: scheme.onPrimaryContainer),
        unselectedIconTheme: IconThemeData(color: scheme.onSurfaceVariant),
        selectedLabelTextStyle: TextStyle(
          color: scheme.onSurface,
          fontWeight: FontWeight.w600,
          fontSize: 12,
        ),
        unselectedLabelTextStyle: TextStyle(
          color: scheme.onSurfaceVariant,
          fontSize: 12,
        ),
      ),
      scrollbarTheme: ScrollbarThemeData(
        thickness: WidgetStateProperty.all(6),
        radius: const Radius.circular(3),
        thumbVisibility: WidgetStateProperty.all(true),
        trackVisibility: WidgetStateProperty.all(false),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainerHighest.withValues(alpha: 0.6),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: scheme.primary, width: 2),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: scheme.error, width: 1),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        backgroundColor: scheme.inverseSurface,
        contentTextStyle: TextStyle(color: scheme.onInverseSurface),
      ),
      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant.withValues(alpha: 0.5),
        space: 1,
      ),
      tabBarTheme: TabBarThemeData(
        indicatorSize: TabBarIndicatorSize.tab,
        dividerColor: scheme.outlineVariant.withValues(alpha: 0.3),
        labelStyle: const TextStyle(fontWeight: FontWeight.w600),
      ),
    );
  }
}

String currentClientPlatform() {
  switch (defaultTargetPlatform) {
    case TargetPlatform.android:
      return 'android';
    case TargetPlatform.windows:
      return 'windows';
    case TargetPlatform.macOS:
      return 'darwin';
    case TargetPlatform.linux:
      return 'linux';
    case TargetPlatform.iOS:
      return 'ios';
    case TargetPlatform.fuchsia:
      return 'fuchsia';
  }
}

class NreClientHome extends StatefulWidget {
  const NreClientHome({
    super.key,
    required this.api,
    required this.generateAgentToken,
    required this.profileStore,
    required this.localAgentController,
    required this.platform,
    required this.version,
    this.enableAutoRefresh = true,
    this.themeMode = ThemeMode.system,
    this.onThemeModeChanged,
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore profileStore;
  final LocalAgentController localAgentController;
  final String platform;
  final String version;
  final bool enableAutoRefresh;
  final ThemeMode themeMode;
  final ValueChanged<ThemeMode>? onThemeModeChanged;

  @override
  State<NreClientHome> createState() => _NreClientHomeState();
}

class _NreClientHomeState extends State<NreClientHome> {
  int index = 0;
  ClientState state = ClientState.empty();

  @override
  void initState() {
    super.initState();
    _loadProfile();
  }

  Future<void> _loadProfile() async {
    final profile = await widget.profileStore.load();
    if (!mounted || !profile.isRegistered) {
      return;
    }
    setState(() {
      state = state.copyWith(
        profile: profile,
        runtimeStatus: ClientRuntimeStatus.registered,
        platform: widget.platform,
      );
    });
  }

  void _setStateAndPersist(ClientState nextState) {
    setState(() => state = nextState.copyWith(platform: widget.platform));
    if (nextState.profile.isRegistered) {
      unawaited(widget.profileStore.save(nextState.profile));
    }
  }

  void _clearProfile() {
    final empty = ClientState.empty().copyWith(platform: widget.platform);
    setState(() => state = empty);
    unawaited(widget.profileStore.save(empty.profile));
  }

  void _setIndex(int value) => setState(() => index = value);

  List<Widget> get _screens => [
    DashboardScreen(
      state: state,
      controller: widget.localAgentController,
      onNavigateToAgent: () => _setIndex(1),
      onNavigateToRegistration: () => _setIndex(1),
    ),
    AgentScreen(
      api: widget.api,
      initialState: state,
      onStateChanged: _setStateAndPersist,
      generateAgentToken: widget.generateAgentToken,
      platform: widget.platform,
      version: widget.version,
      enableAutoRefresh: widget.enableAutoRefresh,
      controller: widget.localAgentController,
    ),
    RulesScreen(state: state),
    SettingsScreen(
      state: state,
      onClearProfile: _clearProfile,
      themeMode: widget.themeMode,
      onThemeModeChanged: widget.onThemeModeChanged,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final screens = _screens;

    return LayoutBuilder(
      builder: (context, constraints) {
        final isDesktop = constraints.maxWidth >= 600;
        if (isDesktop) {
          return Scaffold(
            body: Row(
              children: [
                NavigationRail(
                  selectedIndex: index,
                  onDestinationSelected: _setIndex,
                  labelType: NavigationRailLabelType.all,
                  destinations: [
                    NavigationRailDestination(
                      icon: const Icon(Icons.dashboard_outlined),
                      selectedIcon: const Icon(Icons.dashboard),
                      label: Text(l10n.navDashboard),
                    ),
                    NavigationRailDestination(
                      icon: const Icon(Icons.memory_outlined),
                      selectedIcon: const Icon(Icons.memory),
                      label: Text(l10n.navAgent),
                    ),
                    NavigationRailDestination(
                      icon: const Icon(Icons.rule_outlined),
                      selectedIcon: const Icon(Icons.rule),
                      label: Text(l10n.navRules),
                    ),
                    NavigationRailDestination(
                      icon: const Icon(Icons.settings_outlined),
                      selectedIcon: const Icon(Icons.settings),
                      label: Text(l10n.navSettings),
                    ),
                  ],
                ),
                const VerticalDivider(thickness: 1, width: 1),
                Expanded(
                  child: IndexedStack(index: index, children: screens),
                ),
              ],
            ),
          );
        }
        return Scaffold(
          body: IndexedStack(index: index, children: screens),
          bottomNavigationBar: NavigationBar(
            selectedIndex: index,
            onDestinationSelected: _setIndex,
            destinations: [
              NavigationDestination(
                icon: const Icon(Icons.dashboard_outlined),
                selectedIcon: const Icon(Icons.dashboard),
                label: l10n.navDashboard,
              ),
              NavigationDestination(
                icon: const Icon(Icons.memory_outlined),
                selectedIcon: const Icon(Icons.memory),
                label: l10n.navAgent,
              ),
              NavigationDestination(
                icon: const Icon(Icons.rule_outlined),
                selectedIcon: const Icon(Icons.rule),
                label: l10n.navRules,
              ),
              NavigationDestination(
                icon: const Icon(Icons.settings_outlined),
                selectedIcon: const Icon(Icons.settings),
                label: l10n.navSettings,
              ),
            ],
          ),
        );
      },
    );
  }
}
