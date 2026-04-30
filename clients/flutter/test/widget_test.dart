import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/app.dart';

void main() {
  testWidgets('NreClientApp loads with theme', (tester) async {
    await tester.pumpWidget(
      const ProviderScope(child: NreClientApp()),
    );
    // Theme loads asynchronously
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(find.byType(MaterialApp), findsOneWidget);
  });
}
