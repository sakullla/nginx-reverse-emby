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
