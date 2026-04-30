import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'local_agent_controller.dart';
import 'local_agent_controller_factory.dart';

final localAgentControllerProvider = Provider<LocalAgentController>(
  (ref) => createLocalAgentController(),
);
