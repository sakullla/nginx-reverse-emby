import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/design/theme/accent_themes.dart';
import 'package:nre_client/core/design/theme/glass_theme_data.dart';
import 'package:nre_client/core/design/theme/theme_controller.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';
import 'package:nre_client/features/dashboard/presentation/screens/dashboard_screen.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';
import 'package:nre_client/l10n/app_localizations.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  testWidgets('dashboard stat cards use provider summary values', (
    tester,
  ) async {
    final api = _MockPanelApiClient();
    when(() => api.fetchRules('local')).thenAnswer(
      (_) async => const [
        HttpProxyRule(
          id: '1',
          frontendUrl: 'https://one.example.com',
          backends: [HttpBackend(url: 'http://one:8096')],
          enabled: true,
        ),
      ],
    );
    when(() => api.fetchAgents()).thenAnswer(
      (_) async => const [
        AgentSummary(id: 'edge-1', name: 'edge-1', status: 'online'),
      ],
    );
    when(() => api.fetchCertificates('local')).thenAnswer(
      (_) async => [
        Certificate(
          id: 'cert-1',
          domain: 'expiring.example.com',
          expiresAt: DateTime.now().add(const Duration(days: 2)),
        ),
        const Certificate(id: 'cert-2', domain: 'valid.example.com'),
      ],
    );
    when(() => api.fetchRelayListeners('local')).thenAnswer(
      (_) async => [
        RelayListener(id: 'relay-1', enabled: true),
        RelayListener(id: 'relay-2', enabled: false),
      ],
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authNotifierProvider.overrideWith(() => _AuthNotifierTestDouble()),
          themeControllerProvider.overrideWith(
            () => _ThemeControllerTestDouble(
              ThemeSettings(
                themeMode: ThemeMode.dark,
                accent: AccentThemes.defaults,
                themeData: GlassThemeData.build(AccentThemes.defaults),
              ),
            ),
          ),
          selectedAgentIdProvider.overrideWith((ref) => 'local'),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(body: DashboardScreen()),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('CERTIFICATES'), findsOneWidget);
    expect(find.text('RELAY'), findsOneWidget);
    expect(find.text('2'), findsWidgets);
    expect(find.text('1 certificate expiring within 14 days'), findsOneWidget);
    expect(find.text('● 1 Active'), findsOneWidget);
    expect(find.text('—'), findsNothing);
  });
}

class _AuthNotifierTestDouble extends AuthNotifier {
  @override
  Future<AuthState> build() async {
    return const AuthStateAuthenticated(
      ClientProfile(
        masterUrl: 'https://panel.example.com',
        activeMode: ConnectionMode.management,
        management: ManagementProfile(panelToken: 'panel-secret'),
      ),
    );
  }
}

class _ThemeControllerTestDouble extends ThemeController {
  _ThemeControllerTestDouble(this.settings);

  final ThemeSettings settings;

  @override
  Future<ThemeSettings> build() async => settings;
}
