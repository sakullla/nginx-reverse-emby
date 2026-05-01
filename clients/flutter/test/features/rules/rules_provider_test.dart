import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  late _MockPanelApiClient api;
  late ProviderContainer container;

  setUpAll(() {
    registerFallbackValue(
      const UpdateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );
  });

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

  test(
    'toggleRule rolls back optimistic update when panel API fails',
    () async {
      when(() => api.fetchRules('local')).thenAnswer(
        (_) async => const [
          HttpProxyRule(
            id: '1',
            frontendUrl: 'https://emby.example.com',
            backends: [HttpBackend(url: 'http://emby:8096')],
            enabled: true,
          ),
        ],
      );
      when(
        () => api.updateRule('local', '1', any()),
      ).thenThrow(const PanelApiException('failed'));

      final rules = await container.read(rulesListProvider.future);
      expect(rules.single.enabled, isTrue);

      await expectLater(
        container.read(rulesListProvider.notifier).toggleRule('1', false),
        throwsA(isA<PanelApiException>()),
      );

      final rolledBack = container.read(rulesListProvider).value;
      expect(rolledBack, isNotNull);
      expect(rolledBack!.single.enabled, isTrue);
    },
  );
}
