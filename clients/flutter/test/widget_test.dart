import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/app.dart';
import 'package:nre_client/core/client_state.dart';
import 'package:nre_client/services/client_profile_store.dart';
import 'package:nre_client/services/local_agent_controller.dart';
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

class FakeLocalAgentController implements LocalAgentController {
  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.unavailable(
        message: 'fake runtime unavailable',
        binaryPath: 'C:\\fake\\nre-agent.exe',
      );

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.stopped(binaryPath: 'C:\\fake\\nre-agent.exe');

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.stopped(binaryPath: 'C:\\fake\\nre-agent.exe');

  @override
  Future<String> readRecentLogs() async => '';
}

class FakeClientProfileStore implements ClientProfileStore {
  FakeClientProfileStore([
    this.profile = const ClientProfile(masterUrl: '', displayName: ''),
  ]);

  ClientProfile profile;
  ClientProfile? savedProfile;

  @override
  Future<ClientProfile> load() async => profile;

  @override
  Future<void> save(ClientProfile profile) async {
    savedProfile = profile;
    this.profile = profile;
  }
}

void main() {
  testWidgets('client app opens on overview screen', (tester) async {
    await tester.pumpWidget(
      NreClientApp(
        profileStore: FakeClientProfileStore(),
        localAgentController: FakeLocalAgentController(),
      ),
    );

    expect(find.text('Overview'), findsWidgets);
    expect(find.text('Master'), findsOneWidget);
    expect(find.text('Register'), findsOneWidget);
  });

  testWidgets('navigation preserves register form state', (tester) async {
    await tester.pumpWidget(
      NreClientApp(
        profileStore: FakeClientProfileStore(),
        localAgentController: FakeLocalAgentController(),
      ),
    );

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
    final profileStore = FakeClientProfileStore();

    await tester.pumpWidget(
      NreClientApp(
        api: api,
        generateAgentToken: () => 'generated-token',
        profileStore: profileStore,
        localAgentController: FakeLocalAgentController(),
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
    expect(profileStore.savedProfile?.agentId, 'agent-1');
    expect(profileStore.savedProfile?.token, 'generated-token');
    expect(find.text('http://panel.example.com'), findsOneWidget);
    expect(find.text('Registered: agent-1'), findsOneWidget);
  });

  testWidgets('client app restores saved registration state on startup', (
    tester,
  ) async {
    final profileStore = FakeClientProfileStore(
      const ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'desktop-a',
        agentId: 'agent-1',
        token: 'agent-secret',
      ),
    );

    await tester.pumpWidget(
      NreClientApp(
        profileStore: profileStore,
        localAgentController: FakeLocalAgentController(),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('https://panel.example.com'), findsOneWidget);
    expect(find.text('Registered: agent-1'), findsOneWidget);
  });

  testWidgets('client app injects local agent controller into runtime screen', (
    tester,
  ) async {
    await tester.pumpWidget(
      NreClientApp(
        profileStore: FakeClientProfileStore(),
        localAgentController: FakeLocalAgentController(),
      ),
    );

    await tester.tap(find.text('Runtime').last);
    await tester.pumpAndSettle();

    expect(find.text('fake runtime unavailable'), findsOneWidget);
  });
}
