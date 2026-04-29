import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';
import 'package:nre_client/services/local_agent_controller.dart';
import 'package:nre_client/services/local_agent_controller_windows.dart';

void main() {
  late Directory tempDir;
  late FakeWindowsProcessManager processes;
  late WindowsLocalAgentController controller;

  setUp(() async {
    tempDir = await Directory.systemTemp.createTemp('nre-client-test-');
    processes = FakeWindowsProcessManager();
    controller = WindowsLocalAgentController(
      appDataDir: tempDir.path,
      processManager: processes,
    );
  });

  tearDown(() async {
    if (await tempDir.exists()) {
      await tempDir.delete(recursive: true);
    }
  });

  test('builds expected environment without exposing register token', () {
    final env = controller.buildEnvironment(
      const ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'windows-test',
        agentId: 'agent-1',
        token: 'agent-secret',
      ),
    );

    expect(env['NRE_MASTER_URL'], 'https://panel.example.com');
    expect(env['NRE_AGENT_ID'], 'agent-1');
    expect(env['NRE_AGENT_NAME'], 'windows-test');
    expect(env['NRE_AGENT_TOKEN'], 'agent-secret');
    expect(env['NRE_DATA_DIR'], endsWith('agent-data'));
    expect(env.containsKey('register_token'), isFalse);
  });

  test('status is unavailable before registration', () async {
    final snapshot = await controller.status(ClientState.empty().profile);

    expect(snapshot.status, LocalAgentControllerStatus.unavailable);
    expect(
      snapshot.message,
      'Register this client before starting the local agent',
    );
  });

  test('status is unavailable when binary is missing', () async {
    final snapshot = await controller.status(_profile());

    expect(snapshot.status, LocalAgentControllerStatus.unavailable);
    expect(snapshot.message, 'Agent binary is not installed');
    expect(snapshot.binaryPath, endsWith(r'agent\nre-agent.exe'));
  });

  test('stale pid file resolves to stopped and is removed', () async {
    await File(controller.paths.binaryPath).create(recursive: true);
    await File(controller.paths.pidPath).create(recursive: true);
    await File(controller.paths.pidPath).writeAsString('1234');
    processes.livePids.clear();

    final snapshot = await controller.status(_profile());

    expect(snapshot.status, LocalAgentControllerStatus.stopped);
    expect(await File(controller.paths.pidPath).exists(), isFalse);
  });

  test('start refuses to run when client is not registered', () async {
    final snapshot = await controller.start(ClientState.empty().profile);

    expect(snapshot.status, LocalAgentControllerStatus.unavailable);
    expect(processes.started, isFalse);
  });

  test('start writes pid and returns running when binary exists', () async {
    await File(controller.paths.binaryPath).create(recursive: true);
    processes.nextPid = 4321;

    final snapshot = await controller.start(_profile());

    expect(snapshot.status, LocalAgentControllerStatus.running);
    expect(snapshot.pid, 4321);
    expect(await File(controller.paths.pidPath).readAsString(), '4321');
    expect(processes.startedEnvironment['NRE_AGENT_ID'], 'agent-1');
  });

  test('stop terminates live process and removes pid file', () async {
    await File(controller.paths.binaryPath).create(recursive: true);
    await File(controller.paths.pidPath).create(recursive: true);
    await File(controller.paths.pidPath).writeAsString('4321');
    processes.livePids.add(4321);

    final snapshot = await controller.stop(_profile());

    expect(snapshot.status, LocalAgentControllerStatus.stopped);
    expect(processes.killedPids, contains(4321));
    expect(await File(controller.paths.pidPath).exists(), isFalse);
  });
}

ClientProfile _profile() {
  return const ClientProfile(
    masterUrl: 'https://panel.example.com',
    displayName: 'windows-test',
    agentId: 'agent-1',
    token: 'agent-secret',
  );
}

class FakeWindowsProcessManager implements WindowsProcessManager {
  var started = false;
  var nextPid = 1000;
  final livePids = <int>{};
  final killedPids = <int>[];
  Map<String, String> startedEnvironment = {};

  @override
  Future<int> startAgent({
    required String executable,
    required Map<String, String> environment,
    required String logPath,
  }) async {
    started = true;
    startedEnvironment = environment;
    livePids.add(nextPid);
    return nextPid;
  }

  @override
  Future<bool> isRunning(int pid) async => livePids.contains(pid);

  @override
  Future<void> terminate(int pid) async {
    killedPids.add(pid);
    livePids.remove(pid);
  }
}
