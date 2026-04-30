import 'dart:async';

import 'package:flutter/foundation.dart'
    show TargetPlatform, defaultTargetPlatform;
import 'package:flutter/material.dart';

import 'core/client_state.dart';
import 'screens/agent_screen.dart';
import 'screens/dashboard_screen.dart';
import 'screens/rules_screen.dart';
import 'screens/settings_screen.dart';
import 'services/client_profile_store.dart';
import 'services/local_agent_controller.dart';
import 'services/local_agent_controller_factory.dart';
import 'services/master_api.dart';

class NreClientApp extends StatelessWidget {
  const NreClientApp({
    super.key,
    this.api = const HttpMasterApi(),
    this.generateAgentToken = defaultAgentTokenGenerator,
    this.profileStore,
    this.localAgentController,
    this.platform,
    this.version = '2.0.0',
    this.enableAutoRefresh = true,
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore? profileStore;
  final LocalAgentController? localAgentController;
  final String? platform;
  final String version;
  final bool enableAutoRefresh;

  @override
  Widget build(BuildContext context) {
    final resolvedPlatform = platform ?? currentClientPlatform();

    return MaterialApp(
      title: 'NRE Client',
      theme: ThemeData(
        useMaterial3: true,
        colorSchemeSeed: Colors.teal,
      ),
      darkTheme: ThemeData(
        useMaterial3: true,
        colorSchemeSeed: Colors.teal,
        brightness: Brightness.dark,
      ),
      home: NreClientHome(
        api: api,
        generateAgentToken: generateAgentToken,
        profileStore: profileStore ?? PathProviderClientProfileStore(),
        localAgentController:
            localAgentController ?? createLocalAgentController(),
        platform: resolvedPlatform,
        version: version,
        enableAutoRefresh: enableAutoRefresh,
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
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore profileStore;
  final LocalAgentController localAgentController;
  final String platform;
  final String version;
  final bool enableAutoRefresh;

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

  @override
  Widget build(BuildContext context) {
    final screens = [
      DashboardScreen(
        state: state,
        controller: widget.localAgentController,
        onNavigateToAgent: () => setState(() => index = 1),
        onNavigateToRegistration: () => setState(() => index = 1),
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
      ),
    ];

    return Scaffold(
      body: IndexedStack(index: index, children: screens),
      bottomNavigationBar: NavigationBar(
        selectedIndex: index,
        onDestinationSelected: (value) => setState(() => index = value),
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.dashboard_outlined),
            selectedIcon: Icon(Icons.dashboard),
            label: 'Dashboard',
          ),
          NavigationDestination(
            icon: Icon(Icons.memory_outlined),
            selectedIcon: Icon(Icons.memory),
            label: 'Agent',
          ),
          NavigationDestination(
            icon: Icon(Icons.rule_outlined),
            selectedIcon: Icon(Icons.rule),
            label: 'Rules',
          ),
          NavigationDestination(
            icon: Icon(Icons.settings_outlined),
            selectedIcon: Icon(Icons.settings),
            label: 'Settings',
          ),
        ],
      ),
    );
  }
}
