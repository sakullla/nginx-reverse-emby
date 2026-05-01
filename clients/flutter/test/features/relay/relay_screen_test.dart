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
    when(() => api.fetchRelayListeners('edge-1')).thenAnswer(
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
    when(() => api.createRelayListener('edge-1', any())).thenAnswer(
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
          selectedAgentIdProvider.overrideWith((ref) => 'edge-1'),
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
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'edge relay');
    await tester.enterText(
      find.widgetWithText(TextField, 'Listen port'),
      '9443',
    );
    await tester.enterText(
      find.widgetWithText(TextField, 'Bind hosts'),
      '0.0.0.0',
    );
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    final createVerification = verify(
      () => api.createRelayListener('edge-1', captureAny()),
    );
    createVerification.called(1);
    final createRequest =
        createVerification.captured.single as CreateRelayListenerRequest;
    expect(createRequest.agentId, 'edge-1');

    await tester.tap(find.byIcon(Icons.more_horiz).last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Edit'));
    await tester.pumpAndSettle();

    expect(find.text('Relay listener'), findsOneWidget);
    expect(find.text('Certificate source'), findsOneWidget);
  });
}
