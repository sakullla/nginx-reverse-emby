import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../data/models/rule_models.dart';
import '../../../../core/network/panel_api_provider.dart';

part 'rules_provider.g.dart';

// ---------------------------------------------------------------------------
// Search / filter state
// ---------------------------------------------------------------------------

enum RuleStatusFilter { all, active, disabled }

enum RuleTypeFilter { all, http, https, l4 }

@riverpod
class RulesSearchQuery extends _$RulesSearchQuery {
  @override
  String build() => '';

  void update(String query) {
    state = query;
  }
}

@riverpod
class RulesStatusFilter extends _$RulesStatusFilter {
  @override
  RuleStatusFilter build() => RuleStatusFilter.all;

  void update(RuleStatusFilter filter) {
    state = filter;
  }
}

@riverpod
class RulesTypeFilter extends _$RulesTypeFilter {
  @override
  RuleTypeFilter build() => RuleTypeFilter.all;

  void update(RuleTypeFilter filter) {
    state = filter;
  }
}

/// Computed filtered list based on search query, status filter and type filter.
@riverpod
List<HttpProxyRule> filteredRules(FilteredRulesRef ref) {
  final rulesAsync = ref.watch(rulesListProvider);
  final rules = rulesAsync.valueOrNull ?? [];
  final query = ref.watch(rulesSearchQueryProvider).toLowerCase();
  final statusFilter = ref.watch(rulesStatusFilterProvider);
  final typeFilter = ref.watch(rulesTypeFilterProvider);

  return rules.where((rule) {
    // Search filter
    if (query.isNotEmpty) {
      final matchesSearch =
          rule.frontendUrl.toLowerCase().contains(query) ||
          rule.backendUrl.toLowerCase().contains(query);
      if (!matchesSearch) return false;
    }

    // Status filter
    if (statusFilter == RuleStatusFilter.active && !rule.enabled) return false;
    if (statusFilter == RuleStatusFilter.disabled && rule.enabled) return false;

    // Type filter
    if (typeFilter != RuleTypeFilter.all) {
      final filterType = typeFilter.name.toUpperCase();
      if (filterType != 'HTTP' && filterType != 'HTTPS') return false;
      final isHttps = rule.frontendUrl.toLowerCase().startsWith('https://');
      if (filterType == 'HTTPS' && !isHttps) return false;
      if (filterType == 'HTTP' && isHttps) return false;
    }

    return true;
  }).toList();
}

// ---------------------------------------------------------------------------
// Rules list notifier
// ---------------------------------------------------------------------------

@riverpod
class RulesList extends _$RulesList {
  @override
  Future<List<HttpProxyRule>> build() async {
    final api = ref.read(panelApiClientProvider);
    final agentId = ref.watch(selectedAgentIdProvider);
    return api.fetchRules(agentId);
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      return api.fetchRules(agentId);
    });
  }

  Future<void> toggleRule(String id, bool enabled) async {
    final previous = state.value ?? [];
    final existing = previous.where((rule) => rule.id == id).firstOrNull;
    if (existing == null) return;
    final updated = existing.copyWith(enabled: enabled);
    state = AsyncData(
      previous.map((rule) => rule.id == id ? updated : rule).toList(),
    );

    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final saved = await api.updateRule(
        agentId,
        id,
        UpdateHttpRuleRequest.fromRule(updated),
      );
      final current = state.value ?? [];
      state = AsyncData(
        current.map((rule) => rule.id == id ? saved : rule).toList(),
      );
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<HttpProxyRule> createRule(CreateHttpRuleRequest request) async {
    final previous = state.value ?? [];
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final rule = await api.createRule(agentId, request);
      state = AsyncData([...previous, rule]);
      return rule;
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<HttpProxyRule> updateRule(
    String id,
    UpdateHttpRuleRequest request,
  ) async {
    final previous = state.value ?? [];
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final updatedRule = await api.updateRule(agentId, id, request);
      final current = state.value ?? [];
      state = AsyncData(
        current.map((rule) => rule.id == id ? updatedRule : rule).toList(),
      );
      return updatedRule;
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<void> deleteRule(String id) async {
    final previous = state.value ?? [];
    state = AsyncData(previous.where((rule) => rule.id != id).toList());

    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      await api.deleteRule(agentId, id);
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }
}
