import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../../agents/presentation/providers/agents_provider.dart';
import '../../../certificates/presentation/providers/certificates_provider.dart';
import '../../../relay/presentation/providers/relay_provider.dart';
import '../../../rules/presentation/providers/rules_provider.dart';

part 'dashboard_provider.g.dart';

class DashboardSummary {
  const DashboardSummary({
    required this.rulesCount,
    required this.disabledRulesCount,
    required this.agentsCount,
    required this.onlineAgentsCount,
    required this.certificatesCount,
    required this.relayListenersCount,
    required this.enabledRelayListenersCount,
  });

  final int rulesCount;
  final int disabledRulesCount;
  final int agentsCount;
  final int onlineAgentsCount;
  final int certificatesCount;
  final int relayListenersCount;
  final int enabledRelayListenersCount;
}

@riverpod
DashboardSummary dashboardSummary(DashboardSummaryRef ref) {
  final rules = ref.watch(rulesListProvider).valueOrNull ?? const [];
  final agents = ref.watch(agentsListProvider).valueOrNull ?? const [];
  final certificates =
      ref.watch(certificatesListProvider).valueOrNull ?? const [];
  final relayListeners = ref.watch(relayListProvider).valueOrNull ?? const [];

  return DashboardSummary(
    rulesCount: rules.length,
    disabledRulesCount: rules.where((rule) => !rule.enabled).length,
    agentsCount: agents.length,
    onlineAgentsCount: agents.where((agent) => agent.isOnline).length,
    certificatesCount: certificates.length,
    relayListenersCount: relayListeners.length,
    enabledRelayListenersCount: relayListeners
        .where((listener) => listener.enabled)
        .length,
  );
}
