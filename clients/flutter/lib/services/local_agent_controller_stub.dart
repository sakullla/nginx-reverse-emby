import 'local_agent_controller.dart';

class UnsupportedLocalAgentController implements LocalAgentController {
  const UnsupportedLocalAgentController();

  @override
  Future<LocalAgentControllerStatus> status() async =>
      LocalAgentControllerStatus.unavailable;

  @override
  Future<void> start() async {
    throw UnsupportedError(
      'Local agent runtime is not available on this platform',
    );
  }

  @override
  Future<void> stop() async {
    throw UnsupportedError(
      'Local agent runtime is not available on this platform',
    );
  }

  @override
  Future<String> readRecentLogs() async => '';
}
