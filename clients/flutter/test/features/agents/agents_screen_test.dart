import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/core/client_state.dart' as runtime_state;
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';
import 'package:nre_client/features/agents/presentation/screens/agents_screen.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:nre_client/l10n/app_localizations.dart';
import 'package:nre_client/services/local_agent_controller.dart';
import 'package:nre_client/services/local_agent_controller_provider.dart';

import 'package:mocktail/mocktail.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  testWidgets('local agent start button invokes controller', (tester) async {
    final controller = FakeLocalAgentController(
      snapshot: LocalAgentRuntimeSnapshot.stopped(
        binaryPath: r'C:\NRE Client\agent\nre-agent.exe',
        dataDir: r'C:\NRE Client\agent-data',
        logPath: r'C:\NRE Client\logs\nre-agent.log',
      ),
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authNotifierProvider.overrideWith(() => AuthNotifierTestDouble()),
          localAgentControllerProvider.overrideWithValue(controller),
          panelApiClientProvider.overrideWith((ref) => _remoteApi([])),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: AgentsScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();
    await tester.tap(find.text('LOCAL AGENT'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Start'));
    await tester.pumpAndSettle();

    expect(controller.startCalls, 1);
    expect(find.textContaining('PID: 4321'), findsOneWidget);
    expect(find.text('Running'), findsOneWidget);
  });

  testWidgets('remote agents render and can be searched', (tester) async {
    final api = _remoteApi(const [
      AgentSummary(
        id: 'agent-1',
        name: 'edge-a',
        status: 'online',
        platform: 'linux/amd64',
        version: 'v1.2.3',
        mode: 'remote',
        currentRevision: 2,
        targetRevision: 3,
      ),
    ]);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authNotifierProvider.overrideWith(() => AuthNotifierTestDouble()),
          localAgentControllerProvider.overrideWithValue(
            FakeLocalAgentController(
              snapshot: LocalAgentRuntimeSnapshot.stopped(
                binaryPath: r'C:\NRE Client\agent\nre-agent.exe',
                dataDir: r'C:\NRE Client\agent-data',
                logPath: r'C:\NRE Client\logs\nre-agent.log',
              ),
            ),
          ),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: AgentsScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('edge-a'), findsOneWidget);
    expect(find.textContaining('linux/amd64'), findsOneWidget);

    await tester.enterText(find.byType(TextField).last, 'missing');
    await tester.pumpAndSettle();

    expect(find.text('No remote agents'), findsOneWidget);
  });
}

_MockPanelApiClient _remoteApi(List<AgentSummary> agents) {
  final api = _MockPanelApiClient();
  when(api.fetchAgents).thenAnswer((_) async => agents);
  return api;
}

class AuthNotifierTestDouble extends AuthNotifier {
  @override
  Future<AuthState> build() async {
    return const AuthStateAuthenticated(
      ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'windows-test',
        agentId: 'agent-1',
        token: 'agent-secret',
      ),
    );
  }
}

class FakeLocalAgentController implements LocalAgentController {
  FakeLocalAgentController({required this.snapshot});

  LocalAgentRuntimeSnapshot snapshot;
  var startCalls = 0;

  @override
  Future<String> readRecentLogs() async => '';

  @override
  Future<LocalAgentRuntimeSnapshot> start(
    runtime_state.ClientProfile profile,
  ) async {
    startCalls++;
    snapshot = LocalAgentRuntimeSnapshot.running(
      pid: 4321,
      binaryPath: r'C:\NRE Client\agent\nre-agent.exe',
      dataDir: r'C:\NRE Client\agent-data',
      logPath: r'C:\NRE Client\logs\nre-agent.log',
    );
    return snapshot;
  }

  @override
  Future<LocalAgentRuntimeSnapshot> status(
    runtime_state.ClientProfile profile,
  ) async {
    return snapshot;
  }

  @override
  Future<LocalAgentRuntimeSnapshot> stop(
    runtime_state.ClientProfile profile,
  ) async {
    snapshot = LocalAgentRuntimeSnapshot.stopped(
      binaryPath: r'C:\NRE Client\agent\nre-agent.exe',
      dataDir: r'C:\NRE Client\agent-data',
      logPath: r'C:\NRE Client\logs\nre-agent.log',
    );
    return snapshot;
  }
}
