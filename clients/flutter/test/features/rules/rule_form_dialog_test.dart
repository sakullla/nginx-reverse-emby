import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/rules/presentation/screens/rule_form_dialog.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('showRuleFormDialog opens HTTP rule fields', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: Builder(
          builder: (context) {
            return TextButton(
              onPressed: () => showRuleFormDialog(context),
              child: const Text('Open rule dialog'),
            );
          },
        ),
      ),
    );

    await tester.tap(find.text('Open rule dialog'));
    await tester.pumpAndSettle();

    expect(find.text('Frontend URL'), findsOneWidget);
    expect(find.text('Backend URL'), findsOneWidget);
    expect(find.text('Enabled'), findsOneWidget);
  });
}
