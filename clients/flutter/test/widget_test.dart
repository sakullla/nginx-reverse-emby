import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/app.dart';

void main() {
  testWidgets('NreClientApp loads with theme', (tester) async {
    await tester.pumpWidget(const NreClientApp());
    // Theme loads asynchronously, so we may see loading first
    await tester.pumpAndSettle();
    expect(find.byType(MaterialApp), findsOneWidget);
  });
}
