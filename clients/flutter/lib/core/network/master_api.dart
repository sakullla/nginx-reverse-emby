import 'package:dio/dio.dart';
import '../../features/rules/data/models/rule_models.dart';
import 'api_client.dart';

class MasterApi implements ApiClient {
  final Dio _dio;

  MasterApi({required Dio dio}) : _dio = dio;

  @override
  Future<List<ProxyRule>> getRules() async {
    final response = await _dio.get('/api/rules');
    final data = response.data;
    List<dynamic> items = [];
    if (data is List) {
      items = data;
    } else if (data is Map) {
      items = data['rules'] ?? data['items'] ?? data['data'] ?? [];
    }
    return items
        .whereType<Map<String, dynamic>>()
        .map(ProxyRule.fromJson)
        .toList();
  }

  @override
  Future<ProxyRule> createRule(CreateRuleRequest request) async {
    final response = await _dio.post('/api/rules', data: request.toJson());
    return ProxyRule.fromJson(response.data as Map<String, dynamic>);
  }

  @override
  Future<ProxyRule> updateRule(String id, UpdateRuleRequest request) async {
    final response = await _dio.put('/api/rules/$id', data: request.toJson());
    return ProxyRule.fromJson(response.data as Map<String, dynamic>);
  }

  @override
  Future<void> deleteRule(String id) async {
    await _dio.delete('/api/rules/$id');
  }

  @override
  Future<void> toggleRule(String id, bool enabled) async {
    await _dio.patch('/api/rules/$id', data: {'enabled': enabled});
  }

  @override
  Future<List<Map<String, dynamic>>> getCertificates() async {
    final response = await _dio.get('/api/certificates');
    return _extractList(response.data);
  }

  @override
  Future<List<Map<String, dynamic>>> getAgents() async {
    final response = await _dio.get('/api/agents');
    return _extractList(response.data);
  }

  @override
  Future<Map<String, dynamic>> registerAgent(Map<String, dynamic> request) async {
    final response = await _dio.post('/panel-api/agents/register', data: request);
    return response.data as Map<String, dynamic>;
  }

  @override
  Future<void> unregisterAgent(String id) async {
    await _dio.delete('/api/agents/$id');
  }

  @override
  Future<Map<String, dynamic>> getLocalAgentStatus() async {
    final response = await _dio.get('/api/local-agent/status');
    return response.data as Map<String, dynamic>;
  }

  @override
  Future<Map<String, dynamic>> startLocalAgent() async {
    final response = await _dio.post('/api/local-agent/start');
    return response.data as Map<String, dynamic>;
  }

  @override
  Future<Map<String, dynamic>> stopLocalAgent() async {
    final response = await _dio.post('/api/local-agent/stop');
    return response.data as Map<String, dynamic>;
  }

  @override
  Future<List<Map<String, dynamic>>> getRelayListeners() async {
    final response = await _dio.get('/api/relay');
    return _extractList(response.data);
  }

  @override
  Future<void> toggleRelayListener(String id, bool enabled) async {
    await _dio.patch('/api/relay/$id', data: {'enabled': enabled});
  }

  @override
  Future<void> deleteRelayListener(String id) async {
    await _dio.delete('/api/relay/$id');
  }

  List<Map<String, dynamic>> _extractList(dynamic data) {
    List<dynamic> items = [];
    if (data is List) {
      items = data;
    } else if (data is Map) {
      items = data['items'] ?? data['data'] ?? [];
    }
    return items.whereType<Map<String, dynamic>>().toList();
  }
}
