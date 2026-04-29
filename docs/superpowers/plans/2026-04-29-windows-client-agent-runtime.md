# Windows Client Agent Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build production-mode Windows local agent start/stop support in the Flutter client.

**Architecture:** Extend the local agent controller contract so Runtime UI can pass the registered profile into a platform controller. Implement a Windows controller that manages an installed `%LOCALAPPDATA%\NRE Client\agent\nre-agent.exe` child process with PID/log/data files under local app data. Keep unsupported platforms on the existing stub.

**Tech Stack:** Flutter/Dart, `dart:io` process and filesystem APIs, `flutter_test`.

---

## File Structure

- Modify `clients/flutter/lib/services/local_agent_controller.dart`: shared status snapshot, paths, request types, and controller contract.
- Create `clients/flutter/lib/services/local_agent_controller_factory.dart`: runtime factory that returns the Windows controller on Windows and the unsupported stub elsewhere.
- Modify `clients/flutter/lib/services/local_agent_controller_stub.dart`: implement the richer shared contract for unsupported platforms.
- Create `clients/flutter/lib/services/local_agent_controller_windows.dart`: Windows child-process lifecycle implementation.
- Create `clients/flutter/test/local_agent_controller_windows_test.dart`: unit tests for paths, environment construction, missing binary, stale PID, and start refusal.
- Modify `clients/flutter/lib/app.dart`: inject a controller and pass it to `RuntimeScreen`.
- Modify `clients/flutter/lib/screens/runtime_screen.dart`: replace static content with stateful runtime controls.
- Create `clients/flutter/test/runtime_screen_test.dart`: widget tests for unregistered, missing binary, stopped, running, and error states.
- Modify `clients/flutter/README.md`: document manual Windows agent binary placement and build command.

## Task 1: Controller Contract And Factory

**Files:**
- Modify: `clients/flutter/lib/services/local_agent_controller.dart`
- Modify: `clients/flutter/lib/services/local_agent_controller_stub.dart`
- Create: `clients/flutter/lib/services/local_agent_controller_factory.dart`
- Modify: `clients/flutter/lib/app.dart`

- [ ] **Step 1: Write the failing app injection test**

Append this test to `clients/flutter/test/widget_test.dart`:

```dart
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

testWidgets('client app injects local agent controller into runtime screen', (
  tester,
) async {
  await tester.pumpWidget(
    NreClientApp(localAgentController: FakeLocalAgentController()),
  );

  await tester.tap(find.text('Runtime').last);
  await tester.pumpAndSettle();

  expect(find.text('fake runtime unavailable'), findsOneWidget);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
cd clients\flutter
flutter test test/widget_test.dart
```

Expected: FAIL because `LocalAgentController`, `LocalAgentRuntimeSnapshot`, and `NreClientApp(localAgentController: ...)` do not exist with the planned contract.

- [ ] **Step 3: Implement the shared controller contract**

Replace `clients/flutter/lib/services/local_agent_controller.dart` with:

```dart
import '../core/client_state.dart';

enum LocalAgentControllerStatus { unavailable, stopped, running }

class LocalAgentRuntimeSnapshot {
  const LocalAgentRuntimeSnapshot({
    required this.status,
    required this.binaryPath,
    this.dataDir = '',
    this.logPath = '',
    this.pid,
    this.message = '',
  });

  factory LocalAgentRuntimeSnapshot.unavailable({
    required String message,
    required String binaryPath,
    String dataDir = '',
    String logPath = '',
  }) {
    return LocalAgentRuntimeSnapshot(
      status: LocalAgentControllerStatus.unavailable,
      binaryPath: binaryPath,
      dataDir: dataDir,
      logPath: logPath,
      message: message,
    );
  }

  factory LocalAgentRuntimeSnapshot.stopped({
    required String binaryPath,
    String dataDir = '',
    String logPath = '',
    String message = '',
  }) {
    return LocalAgentRuntimeSnapshot(
      status: LocalAgentControllerStatus.stopped,
      binaryPath: binaryPath,
      dataDir: dataDir,
      logPath: logPath,
      message: message,
    );
  }

  factory LocalAgentRuntimeSnapshot.running({
    required int pid,
    required String binaryPath,
    String dataDir = '',
    String logPath = '',
    String message = '',
  }) {
    return LocalAgentRuntimeSnapshot(
      status: LocalAgentControllerStatus.running,
      pid: pid,
      binaryPath: binaryPath,
      dataDir: dataDir,
      logPath: logPath,
      message: message,
    );
  }

  final LocalAgentControllerStatus status;
  final String binaryPath;
  final String dataDir;
  final String logPath;
  final int? pid;
  final String message;

  bool get canStart => status == LocalAgentControllerStatus.stopped;
  bool get canStop => status == LocalAgentControllerStatus.running;
}

abstract class LocalAgentController {
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile);

  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile);

  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile);

  Future<String> readRecentLogs();
}
```

- [ ] **Step 4: Update unsupported stub**

Replace `clients/flutter/lib/services/local_agent_controller_stub.dart` with:

```dart
import '../core/client_state.dart';
import 'local_agent_controller.dart';

class UnsupportedLocalAgentController implements LocalAgentController {
  const UnsupportedLocalAgentController();

  static const _message = 'Local agent runtime is not available on this platform';

  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.unavailable(message: _message, binaryPath: '');

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.unavailable(message: _message, binaryPath: '');

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.unavailable(message: _message, binaryPath: '');

  @override
  Future<String> readRecentLogs() async => '';
}
```

- [ ] **Step 5: Add factory and compile shell**

Create `clients/flutter/lib/services/local_agent_controller_factory.dart`:

```dart
import 'dart:io';

import 'local_agent_controller.dart';
import 'local_agent_controller_stub.dart';
import 'local_agent_controller_windows.dart';

LocalAgentController createLocalAgentController() {
  if (Platform.isWindows) {
    return WindowsLocalAgentController();
  }
  return const UnsupportedLocalAgentController();
}
```

Create a temporary compile-only shell for `clients/flutter/lib/services/local_agent_controller_windows.dart`:

```dart
import '../core/client_state.dart';
import 'local_agent_controller.dart';

class WindowsLocalAgentController implements LocalAgentController {
  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async =>
      LocalAgentRuntimeSnapshot.unavailable(
        message: 'Windows local agent runtime is not implemented yet',
        binaryPath: '',
      );

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async =>
      status(profile);

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async =>
      status(profile);

  @override
  Future<String> readRecentLogs() async => '';
}
```

- [ ] **Step 6: Inject controller through app**

Modify `clients/flutter/lib/app.dart`:

```dart
import 'services/local_agent_controller.dart';
import 'services/local_agent_controller_factory.dart';
```

Add to `NreClientApp` constructor and fields:

```dart
this.localAgentController,
```

```dart
final LocalAgentController? localAgentController;
```

Pass it into `NreClientHome`:

```dart
localAgentController:
    localAgentController ?? createLocalAgentController(),
```

Add to `NreClientHome` constructor and fields:

```dart
required this.localAgentController,
```

```dart
final LocalAgentController localAgentController;
```

Replace the Runtime screen entry:

```dart
RuntimeScreen(
  state: state,
  controller: widget.localAgentController,
),
```

- [ ] **Step 7: Run test to verify it still fails only because Runtime UI is static**

Run:

```powershell
cd clients\flutter
flutter test test/widget_test.dart
```

Expected: FAIL because the Runtime screen has not rendered the controller snapshot message yet.

- [ ] **Step 8: Commit controller contract and app injection**

After Task 2 makes the test pass, commit both tasks together:

```powershell
git add clients/flutter/lib/services/local_agent_controller.dart clients/flutter/lib/services/local_agent_controller_stub.dart clients/flutter/lib/services/local_agent_controller_factory.dart clients/flutter/lib/services/local_agent_controller_windows.dart clients/flutter/lib/app.dart clients/flutter/test/widget_test.dart
git commit -m "feat(client): wire local agent controller into runtime"
```

## Task 2: Runtime Screen UI

**Files:**
- Modify: `clients/flutter/lib/screens/runtime_screen.dart`
- Create: `clients/flutter/test/runtime_screen_test.dart`
- Test: `clients/flutter/test/widget_test.dart`

- [ ] **Step 1: Write failing Runtime screen widget tests**

Create `clients/flutter/test/runtime_screen_test.dart`:

```dart
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
        home: RuntimeScreen(
          state: ClientState.empty(),
          controller: controller,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Unavailable'), findsOneWidget);
    expect(
      find.text('Register this client before starting the local agent'),
      findsOneWidget,
    );
    expect(tester.widget<FilledButton>(find.byType(FilledButton)).onPressed, isNull);
    expect(tester.widget<OutlinedButton>(find.byType(OutlinedButton)).onPressed, isNull);
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
cd clients\flutter
flutter test test/runtime_screen_test.dart test/widget_test.dart
```

Expected: FAIL because `RuntimeScreen` does not accept `state` and `controller`, and does not render dynamic runtime state.

- [ ] **Step 3: Implement Runtime screen**

Replace `clients/flutter/lib/screens/runtime_screen.dart` with:

```dart
import 'package:flutter/material.dart';

import '../core/client_state.dart';
import '../services/local_agent_controller.dart';

class RuntimeScreen extends StatefulWidget {
  const RuntimeScreen({
    super.key,
    required this.state,
    required this.controller,
  });

  final ClientState state;
  final LocalAgentController controller;

  @override
  State<RuntimeScreen> createState() => _RuntimeScreenState();
}

class _RuntimeScreenState extends State<RuntimeScreen> {
  LocalAgentRuntimeSnapshot? _snapshot;
  var _busy = false;
  var _error = '';

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void didUpdateWidget(RuntimeScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.state.profile.agentId != widget.state.profile.agentId ||
        oldWidget.state.profile.token != widget.state.profile.token) {
      _refresh();
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      final snapshot = await widget.controller.status(widget.state.profile);
      if (!mounted) return;
      setState(() => _snapshot = snapshot);
    } catch (err) {
      if (!mounted) return;
      setState(() => _error = _cleanError(err));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _start() async {
    await _runAction(() => widget.controller.start(widget.state.profile));
  }

  Future<void> _stop() async {
    await _runAction(() => widget.controller.stop(widget.state.profile));
  }

  Future<void> _runAction(
    Future<LocalAgentRuntimeSnapshot> Function() action,
  ) async {
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      final snapshot = await action();
      if (!mounted) return;
      setState(() => _snapshot = snapshot);
    } catch (err) {
      if (!mounted) return;
      setState(() => _error = _cleanError(err));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Runtime'),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            onPressed: _busy ? null : _refresh,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(
            title: const Text('Local agent'),
            subtitle: Text(_statusLabel(status)),
            trailing: _busy ? const CircularProgressIndicator() : null,
          ),
          if (snapshot?.pid != null)
            ListTile(
              title: const Text('Process'),
              subtitle: Text('PID ${snapshot!.pid}'),
            ),
          if ((snapshot?.binaryPath ?? '').isNotEmpty)
            ListTile(
              title: const Text('Binary'),
              subtitle: Text(snapshot!.binaryPath),
            ),
          if ((snapshot?.dataDir ?? '').isNotEmpty)
            ListTile(
              title: const Text('Data directory'),
              subtitle: Text(snapshot!.dataDir),
            ),
          if ((snapshot?.logPath ?? '').isNotEmpty)
            ListTile(title: const Text('Log'), subtitle: Text(snapshot!.logPath)),
          if ((snapshot?.message ?? '').isNotEmpty)
            ListTile(
              title: const Text('Message'),
              subtitle: Text(snapshot!.message),
            ),
          if (_error.isNotEmpty)
            ListTile(
              title: const Text('Error'),
              subtitle: Text(_error),
            ),
          FilledButton(
            onPressed: !_busy && isStopped ? _start : null,
            child: const Text('Start Agent'),
          ),
          const SizedBox(height: 8),
          OutlinedButton(
            onPressed: !_busy && isRunning ? _stop : null,
            child: const Text('Stop Agent'),
          ),
        ],
      ),
    );
  }
}

String _statusLabel(LocalAgentControllerStatus? status) {
  switch (status) {
    case LocalAgentControllerStatus.running:
      return 'Running';
    case LocalAgentControllerStatus.stopped:
      return 'Stopped';
    case LocalAgentControllerStatus.unavailable:
      return 'Unavailable';
    case null:
      return 'Checking';
  }
}

String _cleanError(Object err) {
  final message = err.toString();
  return message.startsWith('Exception: ')
      ? message.substring('Exception: '.length)
      : message;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```powershell
cd clients\flutter
flutter test test/runtime_screen_test.dart test/widget_test.dart
```

Expected: PASS.

- [ ] **Step 5: Commit UI wiring**

Run:

```powershell
git add clients/flutter/lib/screens/runtime_screen.dart clients/flutter/test/runtime_screen_test.dart clients/flutter/test/widget_test.dart clients/flutter/lib/app.dart clients/flutter/lib/services/local_agent_controller.dart clients/flutter/lib/services/local_agent_controller_stub.dart clients/flutter/lib/services/local_agent_controller_factory.dart clients/flutter/lib/services/local_agent_controller_windows.dart
git commit -m "feat(client): add runtime agent controls"
```

## Task 3: Windows Controller

**Files:**
- Modify: `clients/flutter/lib/services/local_agent_controller_windows.dart`
- Test: `clients/flutter/test/local_agent_controller_windows_test.dart`

- [ ] **Step 1: Write failing Windows controller tests**

Create `clients/flutter/test/local_agent_controller_windows_test.dart`:

```dart
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
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
cd clients\flutter
flutter test test/local_agent_controller_windows_test.dart
```

Expected: FAIL because `WindowsLocalAgentController`, `paths`, `buildEnvironment`, and `WindowsProcessManager` are not implemented.

- [ ] **Step 3: Implement Windows controller**

Replace `clients/flutter/lib/services/local_agent_controller_windows.dart` with:

```dart
import 'dart:io';

import '../core/client_state.dart';
import 'local_agent_controller.dart';

class LocalAgentPaths {
  const LocalAgentPaths({
    required this.rootDir,
    required this.binaryPath,
    required this.dataDir,
    required this.runtimeDir,
    required this.pidPath,
    required this.logPath,
  });

  factory LocalAgentPaths.fromAppData(String appDataDir) {
    final root = '$appDataDir\\NRE Client';
    return LocalAgentPaths(
      rootDir: root,
      binaryPath: '$root\\agent\\nre-agent.exe',
      dataDir: '$root\\agent-data',
      runtimeDir: '$root\\runtime',
      pidPath: '$root\\runtime\\nre-agent.pid',
      logPath: '$root\\logs\\nre-agent.log',
    );
  }

  final String rootDir;
  final String binaryPath;
  final String dataDir;
  final String runtimeDir;
  final String pidPath;
  final String logPath;
}

abstract class WindowsProcessManager {
  Future<int> startAgent({
    required String executable,
    required Map<String, String> environment,
    required String logPath,
  });

  Future<bool> isRunning(int pid);

  Future<void> terminate(int pid);
}

class DartWindowsProcessManager implements WindowsProcessManager {
  @override
  Future<int> startAgent({
    required String executable,
    required Map<String, String> environment,
    required String logPath,
  }) async {
    final log = File(logPath);
    await log.parent.create(recursive: true);
    final sink = log.openWrite(mode: FileMode.append);
    try {
      final process = await Process.start(
        executable,
        const [],
        environment: environment,
        mode: ProcessStartMode.detachedWithStdio,
      );
      process.stdout.listen(sink.add);
      process.stderr.listen(sink.add);
      return process.pid;
    } catch (_) {
      await sink.close();
      rethrow;
    }
  }

  @override
  Future<bool> isRunning(int pid) async {
    if (pid <= 0) return false;
    final result = await Process.run('tasklist', [
      '/FI',
      'PID eq $pid',
      '/FO',
      'CSV',
      '/NH',
    ]);
    final output = '${result.stdout}\n${result.stderr}';
    return result.exitCode == 0 && output.contains('"$pid"');
  }

  @override
  Future<void> terminate(int pid) async {
    if (pid <= 0) return;
    await Process.run('taskkill', ['/PID', '$pid', '/T', '/F']);
  }
}

class WindowsLocalAgentController implements LocalAgentController {
  WindowsLocalAgentController({
    String? appDataDir,
    WindowsProcessManager? processManager,
  })  : paths = LocalAgentPaths.fromAppData(
          appDataDir ?? Platform.environment['LOCALAPPDATA'] ?? Directory.current.path,
        ),
        _processManager = processManager ?? DartWindowsProcessManager();

  final LocalAgentPaths paths;
  final WindowsProcessManager _processManager;

  Map<String, String> buildEnvironment(ClientProfile profile) {
    return {
      'NRE_MASTER_URL': profile.masterUrl.trim(),
      'NRE_AGENT_ID': profile.agentId.trim(),
      'NRE_AGENT_NAME': profile.displayName.trim().isEmpty
          ? profile.agentId.trim()
          : profile.displayName.trim(),
      'NRE_AGENT_TOKEN': profile.token.trim(),
      'NRE_DATA_DIR': paths.dataDir,
    };
  }

  @override
  Future<LocalAgentRuntimeSnapshot> status(ClientProfile profile) async {
    final unavailable = _unavailableFor(profile);
    if (unavailable != null) return unavailable;

    if (!await File(paths.binaryPath).exists()) {
      return LocalAgentRuntimeSnapshot.unavailable(
        message: 'Agent binary is not installed',
        binaryPath: paths.binaryPath,
        dataDir: paths.dataDir,
        logPath: paths.logPath,
      );
    }

    final pid = await _readPid();
    if (pid == null) {
      return _stopped();
    }

    if (await _processManager.isRunning(pid)) {
      return LocalAgentRuntimeSnapshot.running(
        pid: pid,
        binaryPath: paths.binaryPath,
        dataDir: paths.dataDir,
        logPath: paths.logPath,
      );
    }

    await _deletePidFile();
    return _stopped();
  }

  @override
  Future<LocalAgentRuntimeSnapshot> start(ClientProfile profile) async {
    final current = await status(profile);
    if (current.status != LocalAgentControllerStatus.stopped) {
      return current;
    }

    await Directory(paths.dataDir).create(recursive: true);
    await Directory(paths.runtimeDir).create(recursive: true);
    await File(paths.logPath).parent.create(recursive: true);
    final pid = await _processManager.startAgent(
      executable: paths.binaryPath,
      environment: buildEnvironment(profile),
      logPath: paths.logPath,
    );
    await File(paths.pidPath).writeAsString('$pid');
    return LocalAgentRuntimeSnapshot.running(
      pid: pid,
      binaryPath: paths.binaryPath,
      dataDir: paths.dataDir,
      logPath: paths.logPath,
    );
  }

  @override
  Future<LocalAgentRuntimeSnapshot> stop(ClientProfile profile) async {
    final pid = await _readPid();
    if (pid != null && await _processManager.isRunning(pid)) {
      await _processManager.terminate(pid);
    }
    await _deletePidFile();
    return _stopped();
  }

  @override
  Future<String> readRecentLogs() async {
    final file = File(paths.logPath);
    if (!await file.exists()) return '';
    final content = await file.readAsString();
    const maxLength = 8000;
    if (content.length <= maxLength) return content;
    return content.substring(content.length - maxLength);
  }

  LocalAgentRuntimeSnapshot? _unavailableFor(ClientProfile profile) {
    if (!profile.isRegistered) {
      return LocalAgentRuntimeSnapshot.unavailable(
        message: 'Register this client before starting the local agent',
        binaryPath: paths.binaryPath,
        dataDir: paths.dataDir,
        logPath: paths.logPath,
      );
    }
    return null;
  }

  LocalAgentRuntimeSnapshot _stopped() {
    return LocalAgentRuntimeSnapshot.stopped(
      binaryPath: paths.binaryPath,
      dataDir: paths.dataDir,
      logPath: paths.logPath,
    );
  }

  Future<int?> _readPid() async {
    final file = File(paths.pidPath);
    if (!await file.exists()) return null;
    final value = (await file.readAsString()).trim();
    return int.tryParse(value);
  }

  Future<void> _deletePidFile() async {
    final file = File(paths.pidPath);
    if (await file.exists()) {
      await file.delete();
    }
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```powershell
cd clients\flutter
flutter test test/local_agent_controller_windows_test.dart
```

Expected: PASS.

- [ ] **Step 5: Run all Flutter tests**

Run:

```powershell
cd clients\flutter
flutter test
```

Expected: PASS.

- [ ] **Step 6: Commit Windows controller**

Run:

```powershell
git add clients/flutter/lib/services/local_agent_controller_windows.dart clients/flutter/test/local_agent_controller_windows_test.dart
git commit -m "feat(client): manage windows agent process"
```

## Task 4: Documentation And Build Verification

**Files:**
- Modify: `clients/flutter/README.md`

- [ ] **Step 1: Write documentation change**

Append this section to `clients/flutter/README.md`:

```markdown
## Windows Local Agent Runtime

The Windows client can start and stop a local `nre-agent.exe` after the client is registered with a control plane.

Build the agent binary locally:

```powershell
cd ..\..\go-agent
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -o ..\clients\flutter\build\agent\nre-agent.exe .\cmd\nre-agent
```

Install it for the client:

```powershell
$installDir = Join-Path $env:LOCALAPPDATA 'NRE Client\agent'
New-Item -ItemType Directory -Force -Path $installDir
Copy-Item .\build\agent\nre-agent.exe (Join-Path $installDir 'nre-agent.exe') -Force
```

After registration, open Runtime and click `Start Agent`. Logs are written under `%LOCALAPPDATA%\NRE Client\logs\nre-agent.log`, and agent data is stored under `%LOCALAPPDATA%\NRE Client\agent-data`.
```

- [ ] **Step 2: Run format**

Run:

```powershell
cd clients\flutter
dart format lib test
```

Expected: files are formatted without errors.

- [ ] **Step 3: Run Flutter tests**

Run:

```powershell
cd clients\flutter
flutter test
```

Expected: PASS.

- [ ] **Step 4: Build Go Windows agent binary**

Run:

```powershell
cd go-agent
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -o ..\clients\flutter\build\agent\nre-agent.exe .\cmd\nre-agent
```

Expected: `clients\flutter\build\agent\nre-agent.exe` exists.

- [ ] **Step 5: Build Flutter Windows client**

Run:

```powershell
cd clients\flutter
flutter build windows
```

Expected: Windows build completes successfully.

- [ ] **Step 6: Commit docs and verification-ready state**

Run:

```powershell
git add clients/flutter/README.md clients/flutter/lib clients/flutter/test
git commit -m "docs(client): document windows local agent runtime"
```

## Manual Test

- [ ] Build `nre-agent.exe` with the command in Task 4.
- [ ] Copy `clients\flutter\build\agent\nre-agent.exe` to `%LOCALAPPDATA%\NRE Client\agent\nre-agent.exe`.
- [ ] Start the Flutter Windows client with `flutter run -d windows`.
- [ ] Register against `https://nre.sakullla.cyou` using the current register token.
- [ ] Open Runtime and click `Start Agent`.
- [ ] Confirm the control plane shows the Windows agent online after one heartbeat interval.
- [ ] Click `Stop Agent`.
- [ ] Confirm `nre-agent.exe` exits in Task Manager.

## Self-Review

Spec coverage:

- Controller contract and factory are covered by Task 1.
- Runtime UI behavior is covered by Task 2.
- Windows process lifecycle, paths, PID handling, log path, and environment are covered by Task 3.
- Manual binary build and install are covered by Task 4.
- Service install and automatic downloads remain excluded as specified future work.

Red-flag scan:

- No unresolved work markers remain in this plan.

Type consistency:

- `LocalAgentRuntimeSnapshot`, `LocalAgentControllerStatus`, `LocalAgentController`, `WindowsLocalAgentController`, and `WindowsProcessManager` are used consistently across tasks.
