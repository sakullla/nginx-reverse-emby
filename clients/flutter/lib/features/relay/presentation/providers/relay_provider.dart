import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../data/models/relay_models.dart';
import '../../../../core/network/panel_api_provider.dart';

part 'relay_provider.g.dart';

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
    final api = ref.read(panelApiClientProvider);
    final agentId = ref.watch(selectedAgentIdProvider);
    return api.fetchRelayListeners(agentId);
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      return api.fetchRelayListeners(agentId);
    });
  }

  Future<void> toggleRelay(String id, bool enabled) async {
    final previous = state.value ?? [];
    final updated = previous
        .where((relay) => relay.id == id)
        .firstOrNull
        ?.copyWith(enabled: enabled);
    if (updated == null) return;
    state = AsyncData(
      previous.map((relay) => relay.id == id ? updated : relay).toList(),
    );

    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final saved = await api.updateRelayListener(
        agentId,
        id,
        UpdateRelayListenerRequest(enabled: enabled),
      );
      final current = state.value ?? [];
      state = AsyncData(
        current.map((relay) => relay.id == id ? saved : relay).toList(),
      );
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<RelayListener> createRelay(CreateRelayListenerRequest request) async {
    final previous = state.value ?? [];
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final listener = await api.createRelayListener(agentId, request);
      state = AsyncData([...previous, listener]);
      return listener;
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<RelayListener> updateRelay(
    String id,
    UpdateRelayListenerRequest request,
  ) async {
    final previous = state.value ?? [];
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final listener = await api.updateRelayListener(agentId, id, request);
      final current = state.value ?? [];
      state = AsyncData(
        current.map((relay) => relay.id == id ? listener : relay).toList(),
      );
      return listener;
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<void> deleteRelay(String id) async {
    final previous = state.value ?? [];
    state = AsyncData(previous.where((relay) => relay.id != id).toList());

    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      await api.deleteRelayListener(agentId, id);
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }
}
