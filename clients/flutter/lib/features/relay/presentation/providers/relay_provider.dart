import 'package:dio/dio.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../data/models/relay_models.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/master_api.dart';
import '../../../../core/network/dio_client.dart';

part 'relay_provider.g.dart';

/// Provides the [ApiClient] for relay endpoints.
@riverpod
ApiClient relayApiClient(RelayApiClientRef ref) {
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
// Search / filter state
// ---------------------------------------------------------------------------

@riverpod
class RelaySearchQuery extends _$RelaySearchQuery {
  @override
  String build() => '';

  void update(String query) {
    state = query;
  }
}

enum RelayProtocolFilter { all, tcp, udp, tls }

@riverpod
class RelayProtocolFilterNotifier extends _$RelayProtocolFilterNotifier {
  @override
  RelayProtocolFilter build() => RelayProtocolFilter.all;

  void update(RelayProtocolFilter filter) {
    state = filter;
  }
}

/// Computed filtered list based on search query and protocol filter.
@riverpod
List<RelayListener> filteredRelayListeners(FilteredRelayListenersRef ref) {
  final relayAsync = ref.watch(relayListProvider);
  final relays = relayAsync.valueOrNull ?? [];
  final query = ref.watch(relaySearchQueryProvider).toLowerCase();
  final protocolFilter = ref.watch(relayProtocolFilterNotifierProvider);

  return relays.where((relay) {
    // Search filter
    if (query.isNotEmpty) {
      final matchesSearch =
          relay.listenAddress.toLowerCase().contains(query) ||
          (relay.agentName?.toLowerCase().contains(query) ?? false) ||
          relay.protocol.toLowerCase().contains(query);
      if (!matchesSearch) return false;
    }

    // Protocol filter
    if (protocolFilter != RelayProtocolFilter.all) {
      if (relay.protocol.toUpperCase() != protocolFilter.name.toUpperCase()) {
        return false;
      }
    }

    return true;
  }).toList();
}

// ---------------------------------------------------------------------------
// Relay list notifier
// ---------------------------------------------------------------------------

@riverpod
class RelayList extends _$RelayList {
  @override
  Future<List<RelayListener>> build() async {
    try {
      final api = ref.read(relayApiClientProvider);
      final rawList = await api.getRelayListeners();
      return rawList.map(RelayListener.fromJson).toList();
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
      final api = ref.read(relayApiClientProvider);
      final rawList = await api.getRelayListeners();
      return rawList.map(RelayListener.fromJson).toList();
    });
  }

  Future<void> toggleRelay(String id, bool enabled) async {
    final previous = state.value ?? [];
    // Optimistic update
    state = AsyncData(previous.map((r) {
      if (r.id == id) {
        return r.copyWith(enabled: enabled);
      }
      return r;
    }).toList());

    try {
      final api = ref.read(relayApiClientProvider);
      await api.toggleRelayListener(id, enabled);
    } catch (e) {
      // Revert on failure
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<void> deleteRelay(String id) async {
    final previous = state.value ?? [];
    // Optimistic remove
    state = AsyncData(previous.where((r) => r.id != id).toList());

    try {
      final api = ref.read(relayApiClientProvider);
      await api.deleteRelayListener(id);
    } catch (e) {
      // Revert on failure
      state = AsyncData(previous);
      rethrow;
    }
  }
}
