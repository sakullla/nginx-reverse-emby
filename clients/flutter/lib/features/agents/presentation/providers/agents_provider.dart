import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../../../core/network/panel_api_provider.dart';
import '../../data/models/agent_models.dart';

part 'agents_provider.g.dart';

@riverpod
class AgentsList extends _$AgentsList {
  @override
  Future<List<AgentSummary>> build() async {
    final api = ref.read(panelApiClientProvider);
    return api.fetchAgents();
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final api = ref.read(panelApiClientProvider);
      return api.fetchAgents();
    });
  }

  Future<void> deleteAgent(String agentId) async {
    final previous = state.value ?? [];
    state = AsyncData(previous.where((agent) => agent.id != agentId).toList());

    try {
      final api = ref.read(panelApiClientProvider);
      await api.deleteAgent(agentId);
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<AgentSummary> renameAgent(String agentId, String name) async {
    final api = ref.read(panelApiClientProvider);
    final updated = await api.renameAgent(agentId, name);
    final current = state.value ?? [];
    state = AsyncData(
      current.map((agent) => agent.id == agentId ? updated : agent).toList(),
    );
    return updated;
  }

  Future<void> applyConfig(String agentId) async {
    final api = ref.read(panelApiClientProvider);
    await api.applyConfig(agentId);
  }
}
