// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'certificates_provider.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

String _$certApiClientHash() => r'2f7b25fc0371bdf31da64a77a7f87bf6411c0c1c';

/// Re-use the same [ApiClient] provider pattern as rules.
///
/// Copied from [certApiClient].
@ProviderFor(certApiClient)
final certApiClientProvider = AutoDisposeProvider<ApiClient>.internal(
  certApiClient,
  name: r'certApiClientProvider',
  debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
      ? null
      : _$certApiClientHash,
  dependencies: null,
  allTransitiveDependencies: null,
);

@Deprecated('Will be removed in 3.0. Use Ref instead')
// ignore: unused_element
typedef CertApiClientRef = AutoDisposeProviderRef<ApiClient>;
String _$filteredCertificatesHash() =>
    r'467786f9d08ff1e99ce43f099fc5b94a6ed74acf';

/// Computed filtered list based on status filter.
///
/// Copied from [filteredCertificates].
@ProviderFor(filteredCertificates)
final filteredCertificatesProvider =
    AutoDisposeProvider<List<Certificate>>.internal(
      filteredCertificates,
      name: r'filteredCertificatesProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$filteredCertificatesHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

@Deprecated('Will be removed in 3.0. Use Ref instead')
// ignore: unused_element
typedef FilteredCertificatesRef = AutoDisposeProviderRef<List<Certificate>>;
String _$certStatusFilterNotifierHash() =>
    r'26ddbcb6cac17665f6327f42af33debe0d2096c8';

/// See also [CertStatusFilterNotifier].
@ProviderFor(CertStatusFilterNotifier)
final certStatusFilterNotifierProvider =
    AutoDisposeNotifierProvider<
      CertStatusFilterNotifier,
      CertStatusFilter
    >.internal(
      CertStatusFilterNotifier.new,
      name: r'certStatusFilterNotifierProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$certStatusFilterNotifierHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$CertStatusFilterNotifier = AutoDisposeNotifier<CertStatusFilter>;
String _$certificatesListHash() => r'a42b2647cc23aa0aeee490336d155e5e8d9ab416';

/// See also [CertificatesList].
@ProviderFor(CertificatesList)
final certificatesListProvider =
    AutoDisposeAsyncNotifierProvider<
      CertificatesList,
      List<Certificate>
    >.internal(
      CertificatesList.new,
      name: r'certificatesListProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$certificatesListHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$CertificatesList = AutoDisposeAsyncNotifier<List<Certificate>>;
// ignore_for_file: type=lint
// ignore_for_file: subtype_of_sealed_class, invalid_use_of_internal_member, invalid_use_of_visible_for_testing_member, deprecated_member_use_from_same_package
