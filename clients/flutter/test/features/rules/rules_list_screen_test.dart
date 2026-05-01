import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/design/components/glass_toggle.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';
import 'package:nre_client/features/rules/presentation/screens/rules_list_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  setUpAll(() {
    registerFallbackValue(
      const UpdateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );
  });

  testWidgets('rule toggle failure shows feedback', (tester) async {
    final api = _MockPanelApiClient();
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

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          selectedAgentIdProvider.overrideWith((ref) => 'local'),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(body: RulesListScreen()),
        ),
      ),
    );

    await tester.pumpAndSettle();
    await tester.tap(find.byType(GlassToggle).first);
    await tester.pump();

    expect(find.textContaining('failed'), findsOneWidget);
  });
}
