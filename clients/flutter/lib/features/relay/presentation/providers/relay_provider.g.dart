// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'relay_provider.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

String _$filteredRelayListenersHash() =>
    r'dda8138e54e662a43ddfede20134683bbbbc445d';

/// Computed filtered list based on search query and protocol filter.
///
/// Copied from [filteredRelayListeners].
@ProviderFor(filteredRelayListeners)
final filteredRelayListenersProvider =
    AutoDisposeProvider<List<RelayListener>>.internal(
      filteredRelayListeners,
      name: r'filteredRelayListenersProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$filteredRelayListenersHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

@Deprecated('Will be removed in 3.0. Use Ref instead')
// ignore: unused_element
typedef FilteredRelayListenersRef = AutoDisposeProviderRef<List<RelayListener>>;
String _$relaySearchQueryHash() => r'35c2b72c5802e0dc5373fe9fedf755efc30830c5';

/// See also [RelaySearchQuery].
@ProviderFor(RelaySearchQuery)
final relaySearchQueryProvider =
    AutoDisposeNotifierProvider<RelaySearchQuery, String>.internal(
      RelaySearchQuery.new,
      name: r'relaySearchQueryProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$relaySearchQueryHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RelaySearchQuery = AutoDisposeNotifier<String>;
String _$relayProtocolFilterNotifierHash() =>
    r'aa697a4bb28561992e3f9530cb0df1bfb832e517';

/// See also [RelayProtocolFilterNotifier].
@ProviderFor(RelayProtocolFilterNotifier)
final relayProtocolFilterNotifierProvider =
    AutoDisposeNotifierProvider<
      RelayProtocolFilterNotifier,
      RelayProtocolFilter
    >.internal(
      RelayProtocolFilterNotifier.new,
      name: r'relayProtocolFilterNotifierProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$relayProtocolFilterNotifierHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RelayProtocolFilterNotifier =
    AutoDisposeNotifier<RelayProtocolFilter>;
String _$relayListHash() => r'8cadb13e366e3b40eded03c922ee21a1f29cc26a';

/// See also [RelayList].
@ProviderFor(RelayList)
final relayListProvider =
    AutoDisposeAsyncNotifierProvider<RelayList, List<RelayListener>>.internal(
      RelayList.new,
      name: r'relayListProvider',
      debugGetCreateSourceHash: const bool.fromEnvironment('dart.vm.product')
          ? null
          : _$relayListHash,
      dependencies: null,
      allTransitiveDependencies: null,
    );

typedef _$RelayList = AutoDisposeAsyncNotifier<List<RelayListener>>;
// ignore_for_file: type=lint
// ignore_for_file: subtype_of_sealed_class, invalid_use_of_internal_member, invalid_use_of_visible_for_testing_member, deprecated_member_use_from_same_package
