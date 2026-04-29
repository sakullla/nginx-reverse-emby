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
