import '../../features/rules/data/models/rule_models.dart';

abstract class ApiClient {
  Future<List<ProxyRule>> getRules();
  Future<ProxyRule> createRule(CreateRuleRequest request);
  Future<ProxyRule> updateRule(String id, UpdateRuleRequest request);
  Future<void> deleteRule(String id);
  Future<void> toggleRule(String id, bool enabled);

  Future<List<Map<String, dynamic>>> getCertificates();
  Future<List<Map<String, dynamic>>> getAgents();
  Future<Map<String, dynamic>> registerAgent(Map<String, dynamic> request);
  Future<void> unregisterAgent(String id);

  Future<Map<String, dynamic>> getLocalAgentStatus();
  Future<Map<String, dynamic>> startLocalAgent();
  Future<Map<String, dynamic>> stopLocalAgent();

  Future<List<Map<String, dynamic>>> getRelayListeners();
  Future<void> toggleRelayListener(String id, bool enabled);
  Future<void> deleteRelayListener(String id);
}
