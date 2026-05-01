import 'package:dio/dio.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../data/models/rule_models.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/master_api.dart';
import '../../../../core/network/dio_client.dart';

part 'rules_provider.g.dart';

/// Provides the [ApiClient] based on the current auth profile.
@riverpod
ApiClient apiClient(ApiClientRef ref) {
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
List<ProxyRule> filteredRules(FilteredRulesRef ref) {
  final rulesAsync = ref.watch(rulesListProvider);
  final rules = rulesAsync.valueOrNull ?? [];
  final query = ref.watch(rulesSearchQueryProvider).toLowerCase();
  final statusFilter = ref.watch(rulesStatusFilterProvider);
  final typeFilter = ref.watch(rulesTypeFilterProvider);

  return rules.where((rule) {
    // Search filter
    if (query.isNotEmpty) {
      final matchesSearch =
          rule.domain.toLowerCase().contains(query) ||
          rule.target.toLowerCase().contains(query);
      if (!matchesSearch) return false;
    }

    // Status filter
    if (statusFilter == RuleStatusFilter.active && !rule.enabled) return false;
    if (statusFilter == RuleStatusFilter.disabled && rule.enabled) return false;

    // Type filter
    if (typeFilter != RuleTypeFilter.all) {
      final filterType = typeFilter.name.toUpperCase();
      if (rule.type.toUpperCase() != filterType) return false;
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
  Future<List<ProxyRule>> build() async {
    try {
      final api = ref.read(apiClientProvider);
      return api.getRules();
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
      final api = ref.read(apiClientProvider);
      return api.getRules();
    });
  }

  Future<void> toggleRule(String id, bool enabled) async {
    final previous = state.value ?? [];
    // Optimistic update
    state = AsyncData(previous.map((r) {
      if (r.id == id) {
        return ProxyRule(
          id: r.id,
          domain: r.domain,
          target: r.target,
          type: r.type,
          enabled: enabled,
        );
      }
      return r;
    }).toList());

    try {
      final api = ref.read(apiClientProvider);
      await api.toggleRule(id, enabled);
    } catch (e) {
      // Revert on failure
      state = AsyncData(previous);
      rethrow;
    }
  }

  Future<ProxyRule> createRule(CreateRuleRequest request) async {
    final api = ref.read(apiClientProvider);
    final newRule = await api.createRule(request);
    // Append to current list
    final current = state.value ?? [];
    state = AsyncData([...current, newRule]);
    return newRule;
  }

  Future<ProxyRule> updateRule(String id, UpdateRuleRequest request) async {
    final api = ref.read(apiClientProvider);
    final updatedRule = await api.updateRule(id, request);
    // Replace in current list
    final current = state.value ?? [];
    state = AsyncData(current.map((r) => r.id == id ? updatedRule : r).toList());
    return updatedRule;
  }

  Future<void> deleteRule(String id) async {
    final previous = state.value ?? [];
    // Optimistic remove
    state = AsyncData(previous.where((r) => r.id != id).toList());

    try {
      final api = ref.read(apiClientProvider);
      await api.deleteRule(id);
    } catch (e) {
      // Revert on failure
      state = AsyncData(previous);
      rethrow;
    }
  }
}
