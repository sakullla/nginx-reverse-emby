import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';
import 'package:nre_client/screens/register_screen.dart';
import 'package:nre_client/services/master_api.dart';

class FakeMasterApi implements MasterApi {
  FakeMasterApi({this.result, this.error, this.delay});

  final RegisterClientResult? result;
  final Object? error;
  final Future<void>? delay;
  RegisterClientRequest? lastRequest;
  MasterApiConfig? lastConfig;

  @override
  Future<RegisterClientResult> register(
    MasterApiConfig config,
    RegisterClientRequest request,
  ) async {
    lastConfig = config;
    lastRequest = request;
    await delay;
    if (error != null) {
      throw error!;
    }
    return result!;
  }
}

void main() {
  testWidgets('register screen validates required fields', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: RegisterScreen()));

    await tester.tap(find.widgetWithText(FilledButton, 'Register'));
    await tester.pump();

    expect(find.text('Master URL is required'), findsOneWidget);
    expect(find.text('Register token is required'), findsOneWidget);
  });

  testWidgets('register screen submits registration and updates state', (
    tester,
  ) async {
    final pendingRegistration = Future<void>.delayed(
      const Duration(milliseconds: 1),
    );
    final api = FakeMasterApi(
      result: const RegisterClientResult(
        agentId: 'agent-1',
        agentToken: 'generated-token',
      ),
      delay: pendingRegistration,
    );
    ClientState? updatedState;

    await tester.pumpWidget(
      MaterialApp(
        home: RegisterScreen(
          api: api,
          initialState: ClientState.empty(),
          onStateChanged: (state) => updatedState = state,
          generateAgentToken: () => 'generated-token',
          platform: 'android',
          version: '1.0.0',
        ),
      ),
    );

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Master URL'),
      ' http://panel.example.com/ ',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Register token'),
      ' register-secret ',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Client name'),
      'phone-a',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Register'));
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    await tester.pumpAndSettle();

    expect(api.lastConfig?.masterUrl, 'http://panel.example.com');
    expect(api.lastConfig?.registerToken, 'register-secret');
    expect(api.lastRequest?.name, 'phone-a');
    expect(api.lastRequest?.agentToken, 'generated-token');
    expect(api.lastRequest?.platform, 'android');
    expect(api.lastRequest?.mode, 'pull');
    expect(find.text('Registered agent agent-1'), findsOneWidget);
    expect(updatedState?.profile.masterUrl, 'http://panel.example.com');
    expect(updatedState?.profile.displayName, 'phone-a');
    expect(updatedState?.profile.agentId, 'agent-1');
    expect(updatedState?.profile.token, 'generated-token');
    expect(updatedState?.runtimeStatus, ClientRuntimeStatus.registered);
  });

  testWidgets('register screen shows registration errors', (tester) async {
    final api = FakeMasterApi(
      error: const MasterApiException('Unauthorized: Invalid token'),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: RegisterScreen(
          api: api,
          initialState: ClientState.empty(),
          generateAgentToken: () => 'generated-token',
        ),
      ),
    );

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Master URL'),
      'http://panel.example.com',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Register token'),
      'bad-token',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Register'));
    await tester.pump();
    await tester.pump();

    expect(find.text('Unauthorized: Invalid token'), findsOneWidget);
  });
}
