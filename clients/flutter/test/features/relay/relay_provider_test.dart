import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
import 'package:nre_client/features/relay/presentation/providers/relay_provider.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  late _MockPanelApiClient api;
  late ProviderContainer container;

  setUpAll(() {
    registerFallbackValue(const UpdateRelayListenerRequest(enabled: false));
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
    'toggleRelay rolls back optimistic update when panel API fails',
    () async {
      when(() => api.fetchRelayListeners('local')).thenAnswer(
        (_) async => [
          RelayListener(
            id: '2',
            listenAddress: '0.0.0.0:8443',
            protocol: 'TCP',
          ),
        ],
      );
      when(
        () => api.updateRelayListener('local', '2', any()),
      ).thenThrow(const PanelApiException('failed'));

      final relays = await container.read(relayListProvider.future);
      expect(relays.single.enabled, isTrue);

      await expectLater(
        container.read(relayListProvider.notifier).toggleRelay('2', false),
        throwsA(isA<PanelApiException>()),
      );

      final rolledBack = container.read(relayListProvider).value;
      expect(rolledBack, isNotNull);
      expect(rolledBack!.single.enabled, isTrue);
    },
  );
}
