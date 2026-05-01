// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'rules_provider.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

String _$filteredRulesHash() => r'1fcd83914482b96d5e90cbe89b5d3a4dbb0fc404';

/// Computed filtered list based on search query, status filter and type filter.
///
/// Copied from [filteredRules].
@ProviderFor(filteredRules)
final filteredRulesProvider = AutoDisposeProvider<List<HttpProxyRule>>.internal(
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
typedef FilteredRulesRef = AutoDisposeProviderRef<List<HttpProxyRule>>;
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
String _$rulesListHash() => r'77fc1bc2d48e7280cd355e43252d01ec67c121dd';

/// See also [RulesList].
@ProviderFor(RulesList)
final rulesListProvider =
    AutoDisposeAsyncNotifierProvider<RulesList, List<HttpProxyRule>>.internal(
      RulesList.new,
      name: r'rulesListProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$rulesListHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RulesList = AutoDisposeAsyncNotifier<List<HttpProxyRule>>;
// ignore_for_file: type=lint
// ignore_for_file: subtype_of_sealed_class, invalid_use_of_internal_member, invalid_use_of_visible_for_testing_member, deprecated_member_use_from_same_package
