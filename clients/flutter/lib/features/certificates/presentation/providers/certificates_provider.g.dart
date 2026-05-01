// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'certificates_provider.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

String _$filteredCertificatesHash() =>
    r'2cba35f553b1067c92d221aaa5d001bf4c8370a5';

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
String _$certificatesListHash() => r'6c2b2ae49b9dc1131b2508966e4e93f588f58411';

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
