// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'rules_provider.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

String _$apiClientHash() => r'231c0983cd389b4da6887210a37165392b66370b';

/// Provides the [ApiClient] based on the current auth profile.
///
/// Copied from [apiClient].
@ProviderFor(apiClient)
final apiClientProvider = AutoDisposeProvider<ApiClient>.internal(
  apiClient,
  name: r'apiClientProvider',
  debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
      ? null
      : _$apiClientHash,
  dependencies: null,
  allTransitiveDependencies: null,
);

@Deprecated('Will be removed in 3.0. Use Ref instead')
// ignore: unused_element
typedef ApiClientRef = AutoDisposeProviderRef<ApiClient>;
String _$filteredRulesHash() => r'ff1e038fbb7b064dced9db188dadf4faf63871af';

/// Computed filtered list based on search query, status filter and type filter.
///
/// Copied from [filteredRules].
@ProviderFor(filteredRules)
final filteredRulesProvider = AutoDisposeProvider<List<ProxyRule>>.internal(
  filteredRules,
  name: r'filteredRulesProvider',
  debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
      ? null
      : _$filteredRulesHash,
  dependencies: null,
  allTransitiveDependencies: null,
);

@Deprecated('Will be removed in 3.0. Use Ref instead')
// ignore: unused_element
typedef FilteredRulesRef = AutoDisposeProviderRef<List<ProxyRule>>;
String _$rulesSearchQueryHash() => r'a2a1a358fbde070867ec7a74a558bc6c8b264baa';

/// See also [RulesSearchQuery].
@ProviderFor(RulesSearchQuery)
final rulesSearchQueryProvider =
    AutoDisposeNotifierProvider<RulesSearchQuery, String>.internal(
      RulesSearchQuery.new,
      name: r'rulesSearchQueryProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$rulesSearchQueryHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RulesSearchQuery = AutoDisposeNotifier<String>;
String _$rulesStatusFilterHash() => r'ca92ea3a6f1cd1768c6ab5f0c606e2bd5caa9010';

/// See also [RulesStatusFilter].
@ProviderFor(RulesStatusFilter)
final rulesStatusFilterProvider =
    AutoDisposeNotifierProvider<RulesStatusFilter, RuleStatusFilter>.internal(
      RulesStatusFilter.new,
      name: r'rulesStatusFilterProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$rulesStatusFilterHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RulesStatusFilter = AutoDisposeNotifier<RuleStatusFilter>;
String _$rulesTypeFilterHash() => r'd795332a4567b09ae5b877129b2929b4944bc997';

/// See also [RulesTypeFilter].
@ProviderFor(RulesTypeFilter)
final rulesTypeFilterProvider =
    AutoDisposeNotifierProvider<RulesTypeFilter, RuleTypeFilter>.internal(
      RulesTypeFilter.new,
      name: r'rulesTypeFilterProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$rulesTypeFilterHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RulesTypeFilter = AutoDisposeNotifier<RuleTypeFilter>;
String _$rulesListHash() => r'b18eeb684efd6cc4918ff09b948f3e6a8eec0d02';

/// See also [RulesList].
@ProviderFor(RulesList)
final rulesListProvider =
    AutoDisposeAsyncNotifierProvider<RulesList, List<ProxyRule>>.internal(
      RulesList.new,
      name: r'rulesListProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$rulesListHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RulesList = AutoDisposeAsyncNotifier<List<ProxyRule>>;
// ignore_for_file: type=lint
// ignore_for_file: subtype_of_sealed_class, invalid_use_of_internal_member, invalid_use_of_visible_for_testing_member, deprecated_member_use_from_same_package
