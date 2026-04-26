import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/app.dart';
import 'package:nre_client/services/master_api.dart';

class FakeMasterApi implements MasterApi {
  RegisterClientRequest? lastRequest;
  MasterApiConfig? lastConfig;

  @override
  Future<RegisterClientResult> register(
    MasterApiConfig config,
    RegisterClientRequest request,
  ) async {
    lastConfig = config;
    lastRequest = request;
    return RegisterClientResult(
      agentId: 'agent-1',
      agentToken: request.agentToken,
    );
  }
}

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

  testWidgets('client app stores registration state for overview', (
    tester,
  ) async {
    final api = FakeMasterApi();

    await tester.pumpWidget(
      NreClientApp(
        api: api,
        generateAgentToken: () => 'generated-token',
        platform: 'android',
        version: '1.0.0',
      ),
    );

    await tester.tap(find.text('Register').last);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Master URL'),
      'http://panel.example.com',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Register token'),
      'register-secret',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Client name'),
      'phone-a',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Register'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Overview').last);
    await tester.pumpAndSettle();

    expect(api.lastRequest?.agentToken, 'generated-token');
    expect(find.text('http://panel.example.com'), findsOneWidget);
    expect(find.text('Registered: agent-1'), findsOneWidget);
  });
}
