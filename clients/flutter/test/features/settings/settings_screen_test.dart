import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/core/design/theme/accent_themes.dart';
import 'package:nre_client/core/design/theme/glass_theme_data.dart';
import 'package:nre_client/core/design/theme/theme_controller.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:nre_client/features/settings/presentation/screens/settings_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('shows management and agent profiles', (tester) async {
    final accent = AccentThemes.defaults;

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authNotifierProvider.overrideWith(() => AuthNotifierTestDouble()),
          themeControllerProvider.overrideWith(
            () => ThemeControllerTestDouble(
              ThemeSettings(
                themeMode: ThemeMode.dark,
                accent: accent,
                themeData: GlassThemeData.build(accent),
              ),
            ),
          ),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: SettingsScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('Management profile'), findsOneWidget);
    expect(find.text('Agent profile'), findsOneWidget);
    expect(find.text('https://panel.example.com'), findsOneWidget);
  });
}

class AuthNotifierTestDouble extends AuthNotifier {
  @override
  Future<AuthState> build() async {
    return const AuthStateAuthenticated(
      ClientProfile(
        masterUrl: 'https://panel.example.com',
        activeMode: ConnectionMode.management,
        management: ManagementProfile(panelToken: 'panel-secret'),
        agent: AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
      ),
    );
  }
}

class ThemeControllerTestDouble extends ThemeController {
  ThemeControllerTestDouble(this.settings);

  final ThemeSettings settings;

  @override
  Future<ThemeSettings> build() async => settings;
}
