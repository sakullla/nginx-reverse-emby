import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';
import 'package:nre_client/features/agents/presentation/providers/agents_provider.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  late _MockPanelApiClient api;
  late ProviderContainer container;

  setUp(() {
    api = _MockPanelApiClient();
    container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWith((ref) => 'local'),
        panelApiClientProvider.overrideWith((ref) => api),
      ],
    );
  });

  tearDown(() {
    container.dispose();
  });

  test('agentsList loads remote agents', () async {
    when(() => api.fetchAgents()).thenAnswer(
      (_) async => const [
        AgentSummary(id: 'agent-1', name: 'edge-a', status: 'online'),
      ],
    );

    final agents = await container.read(agentsListProvider.future);

    expect(agents.single.name, 'edge-a');
  });

  test(
    'deleteAgent rolls back optimistic remove when panel API fails',
    () async {
      when(() => api.fetchAgents()).thenAnswer(
        (_) async => const [
          AgentSummary(id: 'agent-1', name: 'edge-a', status: 'online'),
        ],
      );
      when(
        () => api.deleteAgent('agent-1'),
      ).thenThrow(const PanelApiException('delete failed'));

      final agents = await container.read(agentsListProvider.future);
      expect(agents.single.id, 'agent-1');

      await expectLater(
        container.read(agentsListProvider.notifier).deleteAgent('agent-1'),
        throwsA(isA<PanelApiException>()),
      );

      final rolledBack = container.read(agentsListProvider).value;
      expect(rolledBack, isNotNull);
      expect(rolledBack!.single.id, 'agent-1');
    },
  );
}
