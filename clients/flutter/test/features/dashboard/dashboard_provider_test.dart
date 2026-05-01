import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';
import 'package:nre_client/features/dashboard/presentation/providers/dashboard_provider.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
import 'package:nre_client/features/relay/presentation/providers/relay_provider.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';
import 'package:nre_client/features/agents/presentation/providers/agents_provider.dart';
import 'package:nre_client/features/certificates/presentation/providers/certificates_provider.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  test('DashboardSummary counts disabled rules and active relays', () {
    final summary = DashboardSummary(
      rulesTotal: 3,
      rulesDisabled: 1,
      agentsTotal: 2,
      agentsOnline: 1,
      certificatesTotal: 4,
      certificatesExpiring: 2,
      relaysTotal: 5,
      relaysActive: 3,
    );

    expect(summary.rulesActive, 2);
    expect(summary.agentsOffline, 1);
  });

  test('dashboardSummary aggregates feature providers', () async {
    final api = _MockPanelApiClient();
    when(() => api.fetchRules('local')).thenAnswer(
      (_) async => const [
        HttpProxyRule(
          id: '1',
          frontendUrl: 'https://one.example.com',
          backends: [HttpBackend(url: 'http://one:8096')],
          enabled: true,
        ),
        HttpProxyRule(
          id: '2',
          frontendUrl: 'https://two.example.com',
          backends: [HttpBackend(url: 'http://two:8096')],
          enabled: false,
        ),
      ],
    );
    when(() => api.fetchAgents()).thenAnswer(
      (_) async => const [
        AgentSummary(id: 'edge-1', name: 'edge-1', status: 'online'),
        AgentSummary(id: 'edge-2', name: 'edge-2', status: 'offline'),
      ],
    );
    when(() => api.fetchCertificates('local')).thenAnswer(
      (_) async => [
        Certificate(
          id: 'cert-1',
          domain: 'expiring.example.com',
          expiresAt: DateTime.now().add(const Duration(days: 2)),
        ),
      ],
    );
    when(() => api.fetchRelayListeners('local')).thenAnswer(
      (_) async => [
        RelayListener(id: 'relay-1', enabled: true),
        RelayListener(id: 'relay-2', enabled: false),
      ],
    );

    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWith((ref) => 'local'),
        panelApiClientProvider.overrideWith((ref) => api),
      ],
    );
    addTearDown(container.dispose);

    await Future.wait([
      container.read(rulesListProvider.future),
      container.read(agentsListProvider.future),
      container.read(certificatesListProvider.future),
      container.read(relayListProvider.future),
    ]);
    final summary = container.read(dashboardSummaryProvider);

    expect(summary.rulesTotal, 2);
    expect(summary.rulesDisabled, 1);
    expect(summary.agentsTotal, 2);
    expect(summary.agentsOnline, 1);
    expect(summary.certificatesTotal, 1);
    expect(summary.certificatesExpiring, 1);
    expect(summary.relaysTotal, 2);
    expect(summary.relaysActive, 1);
  });
}
