enum LocalAgentControllerStatus { unavailable, stopped, running }

abstract class LocalAgentController {
  Future<LocalAgentControllerStatus> status();

  Future<void> start();

  Future<void> stop();

  Future<String> readRecentLogs();
}
