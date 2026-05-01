import 'package:dio/dio.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../data/models/certificate_models.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/master_api.dart';
import '../../../../core/network/dio_client.dart';

part 'certificates_provider.g.dart';

/// Re-use the same [ApiClient] provider pattern as rules.
@riverpod
ApiClient certApiClient(CertApiClientRef ref) {
  final authAsync = ref.watch(authNotifierProvider);
  final authState = authAsync.valueOrNull;
  if (authState is AuthStateAuthenticated) {
    final clientProfile = authState.profile;
    final dioClient = DioClient(
      baseUrl: clientProfile.masterUrl,
      token: clientProfile.token,
    );
    return MasterApi(dio: dioClient.dio);
  }
  throw StateError('Not authenticated');
}

// ---------------------------------------------------------------------------
// Filter state
// ---------------------------------------------------------------------------

enum CertStatusFilter { all, valid, expiring, expired }

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
    return cert.status.name == statusFilter.name;
  }).toList();
}

// ---------------------------------------------------------------------------
// Certificates list notifier
// ---------------------------------------------------------------------------

@riverpod
class CertificatesList extends _$CertificatesList {
  @override
  Future<List<Certificate>> build() async {
    try {
      final api = ref.read(certApiClientProvider);
      final rawList = await api.getCertificates();
      return rawList.map(Certificate.fromJson).toList();
    } on StateError {
      // Not authenticated yet — return empty list
      return [];
    } on DioException {
      return [];
    }
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final api = ref.read(certApiClientProvider);
      final rawList = await api.getCertificates();
      return rawList.map(Certificate.fromJson).toList();
    });
  }
}
