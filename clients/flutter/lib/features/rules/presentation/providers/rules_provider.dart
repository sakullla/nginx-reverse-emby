import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../data/models/rule_models.dart';

part 'rules_provider.g.dart';

@riverpod
class RulesList extends _$RulesList {
  @override
  Future<List<ProxyRule>> build() async {
    await Future.delayed(const Duration(milliseconds: 500));
    return const [
      ProxyRule(id: '1', domain: 'example.com', target: 'localhost:8080', type: 'http', enabled: true),
      ProxyRule(id: '2', domain: 'api.local', target: 'localhost:3000', type: 'http', enabled: false),
    ];
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() async {
      await Future.delayed(const Duration(milliseconds: 500));
      return const [
        ProxyRule(id: '1', domain: 'example.com', target: 'localhost:8080', type: 'http', enabled: true),
        ProxyRule(id: '2', domain: 'api.local', target: 'localhost:3000', type: 'http', enabled: false),
      ];
    });
  }

  Future<void> toggleRule(String id, bool enabled) async {
    final previous = state.value ?? [];
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
      await Future.delayed(const Duration(milliseconds: 200));
    } catch (e) {
      state = AsyncData(previous);
      rethrow;
    }
  }
}
