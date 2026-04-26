import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/screens/register_screen.dart';

void main() {
  testWidgets('register screen validates required fields', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: RegisterScreen()));

    await tester.tap(find.widgetWithText(FilledButton, 'Register'));
    await tester.pump();

    expect(find.text('Master URL is required'), findsOneWidget);
    expect(find.text('Register token is required'), findsOneWidget);
  });
}
