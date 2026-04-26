import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/app.dart';

void main() {
  testWidgets('client app opens on overview screen', (tester) async {
    await tester.pumpWidget(const NreClientApp());

    expect(find.text('Overview'), findsWidgets);
    expect(find.text('Master'), findsOneWidget);
    expect(find.text('Register'), findsOneWidget);
  });

  testWidgets('navigation preserves register form state', (tester) async {
    await tester.pumpWidget(const NreClientApp());

    await tester.tap(find.text('Register').last);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Client name'),
      'desktop-a',
    );

    await tester.tap(find.text('Runtime').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Register').last);
    await tester.pumpAndSettle();

    expect(find.text('desktop-a'), findsOneWidget);
  });
}
