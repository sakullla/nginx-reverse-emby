import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';
import 'package:nre_client/screens/runtime_screen.dart';
import 'package:nre_client/services/local_agent_controller.dart';

class FakeLocalAgentController implements LocalAgentController {
  FakeLocalAgentController(this.snapshot);

  LocalAgentRuntimeSnapshot snapshot;
  int startCalls = 0;
  int stopCalls = 0;

  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async =>
      snapshot;

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async {
    startCalls++;
    return snapshot = LocalAgentRuntimeSnapshot.running(
      pid: 42,
      binaryPath: snapshot.binaryPath,
      dataDir: snapshot.dataDir,
      logPath: snapshot.logPath,
    );
  }

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async {
    stopCalls++;
    return snapshot = LocalAgentRuntimeSnapshot.stopped(
      binaryPath: snapshot.binaryPath,
      dataDir: snapshot.dataDir,
      logPath: snapshot.logPath,
    );
  }

  @override
  Future<String> readRecentLogs() async => '';
}

ClientState registeredState() {
  return const ClientState(
    profile: ClientProfile(
      masterUrl: 'https://panel.example.com',
      displayName: 'windows-test',
      agentId: 'agent-1',
      token: 'agent-secret',
    ),
    runtimeStatus: ClientRuntimeStatus.registered,
  );
}

void main() {
  testWidgets('runtime screen disables controls before registration', (
    tester,
  ) async {
    final controller = FakeLocalAgentController(
      LocalAgentRuntimeSnapshot.unavailable(
        message: 'Register this client before starting the local agent',
        binaryPath: r'C:\Users\me\AppData\Local\NRE Client\agent\nre-agent.exe',
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: RuntimeScreen(state: ClientState.empty(), controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Unavailable'), findsOneWidget);
    expect(
      find.text('Register this client before starting the local agent'),
      findsOneWidget,
    );
    expect(
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
      isNull,
    );
    expect(
      tester.widget<OutlinedButton>(find.byType(OutlinedButton)).onPressed,
      isNull,
    );
  });

  testWidgets('runtime screen shows missing binary path', (tester) async {
    final controller = FakeLocalAgentController(
      LocalAgentRuntimeSnapshot.unavailable(
        message: 'Agent binary is not installed',
        binaryPath: r'C:\Users\me\AppData\Local\NRE Client\agent\nre-agent.exe',
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: RuntimeScreen(state: registeredState(), controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Agent binary is not installed'), findsOneWidget);
    expect(
      find.text(r'C:\Users\me\AppData\Local\NRE Client\agent\nre-agent.exe'),
      findsOneWidget,
    );
  });

  testWidgets('runtime screen starts stopped agent', (tester) async {
    final controller = FakeLocalAgentController(
      LocalAgentRuntimeSnapshot.stopped(
        binaryPath: r'C:\nre-agent.exe',
        dataDir: r'C:\nre-data',
        logPath: r'C:\nre-agent.log',
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: RuntimeScreen(state: registeredState(), controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Start Agent'));
    await tester.pumpAndSettle();

    expect(controller.startCalls, 1);
    expect(find.text('Running'), findsOneWidget);
    expect(find.text('PID 42'), findsOneWidget);
  });

  testWidgets('runtime screen stops running agent', (tester) async {
    final controller = FakeLocalAgentController(
      LocalAgentRuntimeSnapshot.running(
        pid: 42,
        binaryPath: r'C:\nre-agent.exe',
      ),
    );

    await tester.pumpWidget(
      MaterialApp(
        home: RuntimeScreen(state: registeredState(), controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(OutlinedButton, 'Stop Agent'));
    await tester.pumpAndSettle();

    expect(controller.stopCalls, 1);
    expect(find.text('Stopped'), findsOneWidget);
  });

  testWidgets('runtime screen displays controller action errors', (
    tester,
  ) async {
    final controller = _ThrowingLocalAgentController();

    await tester.pumpWidget(
      MaterialApp(
        home: RuntimeScreen(state: registeredState(), controller: controller),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Start Agent'));
    await tester.pumpAndSettle();

    expect(find.text('Start failed'), findsOneWidget);
  });
}

class _ThrowingLocalAgentController implements LocalAgentController {
  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.stopped(binaryPath: r'C:\nre-agent.exe');

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async {
    throw Exception('Start failed');
  }

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.stopped(binaryPath: r'C:\nre-agent.exe');

  @override
  Future<String> readRecentLogs() async => '';
}
