import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../data/models/certificate_models.dart';
import '../../../../core/network/panel_api_provider.dart';

part 'certificates_provider.g.dart';

// ---------------------------------------------------------------------------
// Filter state
// ---------------------------------------------------------------------------

enum CertStatusFilter { all, active, pending, error, valid, expiring, expired }

@riverpod
class CertStatusFilterNotifier extends _$CertStatusFilterNotifier {
  @override
  CertStatusFilter build() => CertStatusFilter.all;

  void update(CertStatusFilter filter) {
    state = filter;
  }
}

/// Computed filtered list based on status filter.
@riverpod
List<Certificate> filteredCertificates(FilteredCertificatesRef ref) {
  final certsAsync = ref.watch(certificatesListProvider);
  final certs = certsAsync.valueOrNull ?? [];
  final statusFilter = ref.watch(certStatusFilterNotifierProvider);

  return certs.where((cert) {
    if (statusFilter == CertStatusFilter.all) return true;
    switch (statusFilter) {
      case CertStatusFilter.active:
      case CertStatusFilter.pending:
      case CertStatusFilter.error:
        return cert.displayStatus == statusFilter.name;
      case CertStatusFilter.valid:
      case CertStatusFilter.expiring:
      case CertStatusFilter.expired:
        return cert.status.name == statusFilter.name;
      case CertStatusFilter.all:
        return true;
    }
  }).toList();
}

// ---------------------------------------------------------------------------
// Certificates list notifier
// ---------------------------------------------------------------------------

@riverpod
class CertificatesList extends _$CertificatesList {
  @override
  Future<List<Certificate>> build() async {
    final api = ref.read(panelApiClientProvider);
    final agentId = ref.watch(selectedAgentIdProvider);
    return api.fetchCertificates(agentId);
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      return api.fetchCertificates(agentId);
    });
  }

  Future<Certificate> createCertificate(
    CreateCertificateRequest request,
  ) async {
    final previous = state.value ?? [];
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final created = await api.createCertificate(agentId, request);
      state = AsyncData([...previous, created]);
      return created;
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<Certificate> issueCertificate(String id) async {
    final api = ref.read(panelApiClientProvider);
    final agentId = ref.read(selectedAgentIdProvider);
    final updated = await api.issueCertificate(agentId, id);
    final current = state.value ?? [];
    state = AsyncData(
      current.map((cert) => cert.id == id ? updated : cert).toList(),
    );
    return updated;
  }

  Future<void> deleteCertificate(String id) async {
    final previous = state.value ?? [];
    state = AsyncData(previous.where((cert) => cert.id != id).toList());

    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      await api.deleteCertificate(agentId, id);
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }
}
