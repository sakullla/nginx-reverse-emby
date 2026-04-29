import '../core/client_state.dart';
import 'local_agent_controller.dart';

class UnsupportedLocalAgentController implements LocalAgentController {
  const UnsupportedLocalAgentController();

  static const _message =
      'Local agent runtime is not available on this platform';

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
