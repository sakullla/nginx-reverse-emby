import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
import 'package:nre_client/features/relay/presentation/screens/relay_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  setUpAll(() {
    registerFallbackValue(
      const CreateRelayListenerRequest(
        agentId: 'local',
        name: 'fallback',
        listenPort: 443,
      ),
    );
    registerFallbackValue(const UpdateRelayListenerRequest(name: 'fallback'));
  });

  testWidgets('relay screen opens new and edit forms', (tester) async {
    final api = _MockPanelApiClient();
    when(() => api.fetchRelayListeners('local')).thenAnswer(
      (_) async => [
        RelayListener(
          id: 'relay-1',
          name: 'public-tls',
          listenPort: 8443,
          bindHosts: const ['0.0.0.0'],
          protocol: 'TLS',
          certificateSource: 'managed',
          tlsMode: 'strict',
        ),
      ],
    );
    when(() => api.createRelayListener('local', any())).thenAnswer(
      (_) async => RelayListener(
        id: 'relay-2',
        name: 'created',
        listenPort: 9443,
        bindHosts: const ['0.0.0.0'],
      ),
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          selectedAgentIdProvider.overrideWith((ref) => 'local'),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: RelayScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    await tester.tap(find.text('+ New'));
    await tester.pumpAndSettle();
    expect(find.text('Relay listener'), findsOneWidget);
    expect(find.text('Listen port'), findsOneWidget);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Edit'));
    await tester.pumpAndSettle();

    expect(find.text('Relay listener'), findsOneWidget);
    expect(find.text('Certificate source'), findsOneWidget);
  });
}
