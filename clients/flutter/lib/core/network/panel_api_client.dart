import 'package:dio/dio.dart';

import '../../features/agents/data/models/agent_models.dart';
import '../../features/certificates/data/models/certificate_models.dart';
import '../../features/relay/data/models/relay_models.dart';
import '../../features/rules/data/models/rule_models.dart';

class PanelApiException implements Exception {
  const PanelApiException(this.message, {this.statusCode});

  final String message;
  final int? statusCode;

  @override
  String toString() => 'PanelApiException: $message';
}

class PanelApiClient {
  PanelApiClient({
    required String baseUrl,
    required String panelToken,
    Dio? dio,
  }) : _dio =
           dio ??
           Dio(
             BaseOptions(
               baseUrl: _normalizeBaseUrl(baseUrl),
               connectTimeout: const Duration(seconds: 10),
               receiveTimeout: const Duration(seconds: 10),
               headers: {'X-Panel-Token': panelToken},
             ),
           );

  final Dio _dio;

  static final Options _longRunningOptions = Options(
    sendTimeout: const Duration(seconds: 30),
    receiveTimeout: const Duration(minutes: 2),
  );

  Future<List<AgentSummary>> fetchAgents() async {
    final data = await _requestMap(() => _dio.get('/agents'));
    return _extractList(data, 'agents').map(AgentSummary.fromJson).toList();
  }

  Future<List<HttpProxyRule>> fetchRules(String agentId) async {
    final data = await _requestMap(() => _dio.get('/agents/$agentId/rules'));
    return _extractList(data, 'rules').map(HttpProxyRule.fromJson).toList();
  }

  Future<HttpProxyRule> createRule(
    String agentId,
    CreateHttpRuleRequest request,
  ) async {
    final data = await _requestMap(
      () => _dio.post(
        '/agents/$agentId/rules',
        data: request.toJson(),
        options: _longRunningOptions,
      ),
    );
    return HttpProxyRule.fromJson(_extractMap(data, 'rule'));
  }

  Future<HttpProxyRule> updateRule(
    String agentId,
    String id,
    UpdateHttpRuleRequest request,
  ) async {
    final data = await _requestMap(
      () => _dio.put(
        '/agents/$agentId/rules/$id',
        data: request.toJson(),
        options: _longRunningOptions,
      ),
    );
    return HttpProxyRule.fromJson(_extractMap(data, 'rule'));
  }

  Future<void> deleteRule(String agentId, String id) async {
    await _requestMap(
      () => _dio.delete(
        '/agents/$agentId/rules/$id',
        options: _longRunningOptions,
      ),
    );
  }

  Future<void> applyConfig(String agentId) async {
    await _requestMap(
      () => _dio.post('/agents/$agentId/apply', options: _longRunningOptions),
    );
  }

  Future<List<Certificate>> fetchCertificates(String agentId) async {
    final data = await _requestMap(
      () => _dio.get('/agents/$agentId/certificates'),
    );
    return _extractList(
      data,
      'certificates',
    ).map(Certificate.fromJson).toList();
  }

  Future<List<RelayListener>> fetchRelayListeners(String agentId) async {
    final data = await _requestMap(
      () => _dio.get('/agents/$agentId/relay-listeners'),
    );
    return _extractList(data, 'listeners').map(RelayListener.fromJson).toList();
  }

  Future<Map<String, dynamic>> _requestMap(
    Future<Response<dynamic>> Function() request,
  ) async {
    try {
      final response = await request();
      final data = response.data;
      if (data is Map<String, dynamic>) return data;
      if (data is Map) return Map<String, dynamic>.from(data);
      return const {};
    } on DioException catch (error) {
      throw _exceptionFromDio(error);
    }
  }

  static PanelApiException _exceptionFromDio(DioException error) {
    final response = error.response;
    final data = response?.data;
    final message = data is Map
        ? (data['error'] ?? data['message'])?.toString()
        : null;
    return PanelApiException(
      message ?? 'Panel API request failed with HTTP ${response?.statusCode}',
      statusCode: response?.statusCode,
    );
  }

  static List<Map<String, dynamic>> _extractList(
    Map<String, dynamic> data,
    String key,
  ) {
    final value = data[key] ?? data['items'] ?? data['data'];
    if (value is! List) return const [];
    return value
        .whereType<Map>()
        .map((item) => Map<String, dynamic>.from(item))
        .toList();
  }

  static Map<String, dynamic> _extractMap(
    Map<String, dynamic> data,
    String key,
  ) {
    final value = data[key] ?? data['data'];
    if (value is Map<String, dynamic>) return value;
    if (value is Map) return Map<String, dynamic>.from(value);
    return data;
  }
}

String _normalizeBaseUrl(String value) {
  var baseUrl = value.trim();
  while (baseUrl.endsWith('/')) {
    baseUrl = baseUrl.substring(0, baseUrl.length - 1);
  }
  if (baseUrl.endsWith('/panel-api')) return baseUrl;
  return '$baseUrl/panel-api';
}
