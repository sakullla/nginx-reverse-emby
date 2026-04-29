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
