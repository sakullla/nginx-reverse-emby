import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:nre_client/features/auth/presentation/screens/connect_screen.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('shows management and agent modes', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authNotifierProvider.overrideWith(() => AuthNotifierTestDouble()),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConnectScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('Management'), findsOneWidget);
    expect(find.text('Agent'), findsOneWidget);

    await tester.tap(find.text('Management'));
    await tester.pumpAndSettle();

    expect(find.text('Panel token'), findsOneWidget);
  });
}

class AuthNotifierTestDouble extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthStateUnauthenticated();
}
