import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../../agents/presentation/providers/agents_provider.dart';
import '../../../certificates/data/models/certificate_models.dart';
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
AsyncValue<DashboardSummary> dashboardSummary(DashboardSummaryRef ref) {
  final rulesAsync = ref.watch(rulesListProvider);
  final agentsAsync = ref.watch(agentsListProvider);
  final certificatesAsync = ref.watch(certificatesListProvider);
  final relayListenersAsync = ref.watch(relayListProvider);

  final firstError = [
    rulesAsync,
    agentsAsync,
    certificatesAsync,
    relayListenersAsync,
  ].where((value) => value.hasError).firstOrNull;
  if (firstError != null) {
    return AsyncError(firstError.error!, firstError.stackTrace!);
  }

  if (rulesAsync.isLoading ||
      agentsAsync.isLoading ||
      certificatesAsync.isLoading ||
      relayListenersAsync.isLoading) {
    return const AsyncLoading();
  }

  final rules = rulesAsync.valueOrNull ?? const [];
  final agents = agentsAsync.valueOrNull ?? const [];
  final certificates = certificatesAsync.valueOrNull ?? const [];
  final relayListeners = relayListenersAsync.valueOrNull ?? const [];

  return AsyncData(
    DashboardSummary(
      rulesTotal: rules.length,
      rulesDisabled: rules.where((rule) => !rule.enabled).length,
      agentsTotal: agents.length,
      agentsOnline: agents.where((agent) => agent.isOnline).length,
      certificatesTotal: certificates.length,
      certificatesExpiring: certificates
          .where((certificate) => certificate.status == CertStatus.expiring)
          .length,
      relaysTotal: relayListeners.length,
      relaysActive: relayListeners.where((listener) => listener.enabled).length,
    ),
  );
}
