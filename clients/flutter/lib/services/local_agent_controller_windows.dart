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
          appDataDir ??
              Platform.environment['LOCALAPPDATA'] ??
              Directory.current.path,
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
