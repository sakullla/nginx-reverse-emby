import 'dart:convert';
import 'dart:io';

class MasterApiConfig {
  const MasterApiConfig({required this.masterUrl, required this.registerToken});

  final String masterUrl;
  final String registerToken;
}

class RegisterClientRequest {
  const RegisterClientRequest({
    required this.name,
    required this.agentToken,
    required this.version,
    required this.platform,
    this.agentUrl = '',
    this.tags = const [],
    this.capabilities = const ['http_rules'],
    this.mode = 'pull',
  });

  final String name;
  final String agentUrl;
  final String agentToken;
  final String version;
  final String platform;
  final List<String> tags;
  final List<String> capabilities;
  final String mode;
}

class RegisterClientResult {
  const RegisterClientResult({required this.agentId, required this.agentToken});

  final String agentId;
  final String agentToken;
}

class MasterApiException implements Exception {
  const MasterApiException(this.message);

  final String message;

  @override
  String toString() => message;
}

abstract class MasterApi {
  Future<RegisterClientResult> register(
    MasterApiConfig config,
    RegisterClientRequest request,
  );
}

class HttpMasterApi implements MasterApi {
  const HttpMasterApi();

  @override
  Future<RegisterClientResult> register(
    MasterApiConfig config,
    RegisterClientRequest request,
  ) async {
    final masterUrl = normalizeMasterUrl(config.masterUrl);
    final registerToken = config.registerToken.trim();
    final uri = Uri.parse('$masterUrl/panel-api/agents/register');
    if (uri.scheme != 'http' && uri.scheme != 'https') {
      throw const MasterApiException('Master URL must use http or https');
    }
    if (uri.host.trim().isEmpty) {
      throw const MasterApiException('Master URL must include a host');
    }

    final client = HttpClient();
    try {
      final httpRequest = await client.postUrl(uri);
      httpRequest.headers.contentType = ContentType.json;
      httpRequest.headers.set('X-Register-Token', registerToken);
      httpRequest.headers.set('X-Agent-Token', request.agentToken);
      httpRequest.write(
        jsonEncode({
          'name': request.name,
          'agent_url': request.agentUrl,
          'agent_token': request.agentToken,
          'version': request.version,
          'platform': request.platform,
          'tags': request.tags,
          'capabilities': request.capabilities,
          'mode': request.mode,
          'register_token': registerToken,
        }),
      );

      final response = await httpRequest.close();
      final responseText = await utf8.decoder.bind(response).join();
      if (response.statusCode < 200 || response.statusCode >= 300) {
        final payload = _decodeErrorPayload(responseText);
        throw MasterApiException(_extractError(payload, response.statusCode));
      }
      final payload = _decodePayload(responseText);
      final agent = payload['agent'];
      final agentId = agent is Map ? (agent['id'] as String? ?? '') : '';
      if (payload['ok'] != true || agentId.trim().isEmpty) {
        throw const MasterApiException(
          'Registration response did not include an agent id',
        );
      }
      return RegisterClientResult(
        agentId: agentId.trim(),
        agentToken: request.agentToken,
      );
    } on FormatException catch (error) {
      throw MasterApiException('Invalid backend response: ${error.message}');
    } on SocketException catch (error) {
      throw MasterApiException('Registration failed: ${error.message}');
    } on HttpException catch (error) {
      throw MasterApiException('Registration failed: ${error.message}');
    } finally {
      client.close(force: true);
    }
  }

  Map<String, Object?> _decodeErrorPayload(String responseText) {
    try {
      return _decodePayload(responseText);
    } on FormatException {
      return const {};
    }
  }

  Map<String, Object?> _decodePayload(String responseText) {
    if (responseText.trim().isEmpty) {
      return const {};
    }
    final decoded = jsonDecode(responseText);
    if (decoded is Map<String, Object?>) {
      return decoded;
    }
    return const {};
  }

  String _extractError(Map<String, Object?> payload, int statusCode) {
    final error = payload['error'] ?? payload['message'];
    if (error is String && error.trim().isNotEmpty) {
      return error.trim();
    }
    return 'Registration failed with HTTP $statusCode';
  }
}

String normalizeMasterUrl(String value) {
  var normalized = value.trim();
  while (normalized.endsWith('/')) {
    normalized = normalized.substring(0, normalized.length - 1);
  }
  if (normalized.endsWith('/panel-api/public/join-agent.sh')) {
    normalized = normalized.substring(
      0,
      normalized.length - '/panel-api/public/join-agent.sh'.length,
    );
  }
  if (normalized.endsWith('/panel-api')) {
    normalized = normalized.substring(
      0,
      normalized.length - '/panel-api'.length,
    );
  }
  return normalized;
}
