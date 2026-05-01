import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../../agents/presentation/providers/agents_provider.dart';
import '../../../certificates/presentation/providers/certificates_provider.dart';
import '../../../relay/presentation/providers/relay_provider.dart';
import '../../../rules/presentation/providers/rules_provider.dart';

part 'dashboard_provider.g.dart';

class DashboardSummary {
  const DashboardSummary({
    required this.rulesTotal,
    required this.rulesDisabled,
    required this.agentsTotal,
    required this.agentsOnline,
    required this.certificatesTotal,
    required this.certificatesExpiring,
    required this.relaysTotal,
    required this.relaysActive,
  });

  final int rulesTotal;
  final int rulesDisabled;
  final int agentsTotal;
  final int agentsOnline;
  final int certificatesTotal;
  final int certificatesExpiring;
  final int relaysTotal;
  final int relaysActive;

  int get rulesActive => rulesTotal - rulesDisabled;
  int get agentsOffline => agentsTotal - agentsOnline;
}

@riverpod
DashboardSummary dashboardSummary(DashboardSummaryRef ref) {
  final rules = ref.watch(rulesListProvider).valueOrNull ?? const [];
  final agents = ref.watch(agentsListProvider).valueOrNull ?? const [];
  final certificates =
      ref.watch(certificatesListProvider).valueOrNull ?? const [];
  final relayListeners = ref.watch(relayListProvider).valueOrNull ?? const [];

  return DashboardSummary(
    rulesTotal: rules.length,
    rulesDisabled: rules.where((rule) => !rule.enabled).length,
    agentsTotal: agents.length,
    agentsOnline: agents.where((agent) => agent.isOnline).length,
    certificatesTotal: certificates.length,
    certificatesExpiring: certificates
        .where((certificate) => certificate.status.name == 'expiring')
        .length,
    relaysTotal: relayListeners.length,
    relaysActive: relayListeners.where((listener) => listener.enabled).length,
  );
}
