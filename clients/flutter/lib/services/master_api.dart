class MasterApiConfig {
  const MasterApiConfig({required this.masterUrl, required this.token});

  final String masterUrl;
  final String token;
}

class RegisterClientRequest {
  const RegisterClientRequest({required this.name, required this.tags});

  final String name;
  final List<String> tags;
}

class RegisterClientResult {
  const RegisterClientResult({required this.agentId, required this.agentToken});

  final String agentId;
  final String agentToken;
}

abstract class MasterApi {
  Future<RegisterClientResult> register(RegisterClientRequest request);
}
