import 'dart:async';

import 'package:flutter/foundation.dart'
    show TargetPlatform, defaultTargetPlatform;
import 'package:flutter/material.dart';

import 'core/client_state.dart';
import 'screens/about_screen.dart';
import 'screens/logs_screen.dart';
import 'screens/overview_screen.dart';
import 'screens/register_screen.dart';
import 'screens/runtime_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/updates_screen.dart';
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
    this.version = '1',
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore? profileStore;
  final LocalAgentController? localAgentController;
  final String? platform;
  final String version;

  @override
  Widget build(BuildContext context) {
    final resolvedPlatform = platform ?? currentClientPlatform();

    return MaterialApp(
      title: 'NRE Client',
      theme: ThemeData(useMaterial3: true, colorSchemeSeed: Colors.teal),
      home: NreClientHome(
        api: api,
        generateAgentToken: generateAgentToken,
        profileStore: profileStore ?? PathProviderClientProfileStore(),
        localAgentController:
            localAgentController ?? createLocalAgentController(),
        platform: resolvedPlatform,
        version: version,
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
  });

  final MasterApi api;
  final AgentTokenGenerator generateAgentToken;
  final ClientProfileStore profileStore;
  final LocalAgentController localAgentController;
  final String platform;
  final String version;

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
      );
    });
  }

  void _setStateAndPersist(ClientState nextState) {
    setState(() => state = nextState);
    if (nextState.profile.isRegistered) {
      unawaited(widget.profileStore.save(nextState.profile));
    }
  }

  @override
  Widget build(BuildContext context) {
    final screens = [
      OverviewScreen(state: state),
      RegisterScreen(
        api: widget.api,
        initialState: state,
        onStateChanged: _setStateAndPersist,
        generateAgentToken: widget.generateAgentToken,
        platform: widget.platform,
        version: widget.version,
      ),
      RuntimeScreen(state: state, controller: widget.localAgentController),
      const LogsScreen(),
      const UpdatesScreen(),
      SettingsScreen(state: state),
      const AboutScreen(),
    ];

    return Scaffold(
      body: IndexedStack(index: index, children: screens),
      bottomNavigationBar: NavigationBar(
        selectedIndex: index,
        onDestinationSelected: (value) => setState(() => index = value),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.dashboard), label: 'Overview'),
          NavigationDestination(icon: Icon(Icons.login), label: 'Register'),
          NavigationDestination(icon: Icon(Icons.memory), label: 'Runtime'),
          NavigationDestination(icon: Icon(Icons.article), label: 'Logs'),
          NavigationDestination(
            icon: Icon(Icons.system_update),
            label: 'Updates',
          ),
          NavigationDestination(icon: Icon(Icons.settings), label: 'Settings'),
          NavigationDestination(icon: Icon(Icons.info), label: 'About'),
        ],
      ),
    );
  }
}
