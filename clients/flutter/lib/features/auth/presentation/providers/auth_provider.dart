import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../../../services/master_api.dart';
import '../../data/models/auth_models.dart';
import '../../data/repositories/auth_repository.dart';

part 'auth_provider.g.dart';

final authRepositoryProvider = Provider((ref) => AuthRepository());

String _generateAgentToken() {
  final random = Random.secure();
  final bytes = List<int>.generate(24, (_) => random.nextInt(256));
  return bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
}

@riverpod
class AuthNotifier extends _$AuthNotifier {
  @override
  Future<AuthState> build() async {
    final repo = ref.read(authRepositoryProvider);
    final profile = await repo.loadProfile();
    return profile.hasAnyCredentials
        ? AuthStateAuthenticated(profile)
        : const AuthStateUnauthenticated();
  }

  Future<void> register({
    required String masterUrl,
    required String registerToken,
    required String name,
  }) async {
    state = const AsyncData(AuthStateLoading());

    try {
      final normalizedUrl = normalizeMasterUrl(masterUrl);
      final uri = Uri.parse('$normalizedUrl/panel-api/agents/register');
      if (uri.scheme != 'http' && uri.scheme != 'https') {
        throw const FormatException('Master URL must use http or https');
      }
      if (uri.host.trim().isEmpty) {
        throw const FormatException('Master URL must include a host');
      }

      final agentToken = _generateAgentToken();
      final trimmedToken = registerToken.trim();

      final client = HttpClient();
      try {
        final request = await client.postUrl(uri);
        request.headers.contentType = ContentType.json;
        request.headers.set('X-Register-Token', trimmedToken);
        request.headers.set('X-Agent-Token', agentToken);
        request.write(
          jsonEncode({
            'name': name.trim(),
            'agent_url': '',
            'agent_token': agentToken,
            'version': '2.1.0',
            'platform': 'windows',
            'tags': <String>[],
            'capabilities': const ['http_rules'],
            'mode': 'pull',
            'register_token': trimmedToken,
          }),
        );

        final response = await request.close();
        final responseText = await utf8.decoder.bind(response).join();

        if (response.statusCode < 200 || response.statusCode >= 300) {
          final payload = _decodeJson(responseText);
          final error = payload['error'] ?? payload['message'];
          throw Exception(
            error is String && error.isNotEmpty
                ? error
                : 'Registration failed with HTTP ${response.statusCode}',
          );
        }

        final payload = _decodeJson(responseText);
        final agent = payload['agent'];
        final agentId = agent is Map ? (agent['id'] as String? ?? '') : '';
        if (payload['ok'] != true || agentId.isEmpty) {
          throw Exception('Registration response did not include an agent id');
        }

        final current = await ref.read(authRepositoryProvider).loadProfile();
        final profile = current.copyWith(
          masterUrl: normalizedUrl,
          displayName: name.trim(),
          activeMode: ConnectionMode.agent,
          agent: AgentProfile(agentId: agentId, agentToken: agentToken),
        );

        await ref.read(authRepositoryProvider).saveProfile(profile);
        state = AsyncData(AuthStateAuthenticated(profile));
      } finally {
        client.close(force: true);
      }
    } catch (e) {
      state = AsyncData(AuthStateError(e.toString()));
    }
  }

  Future<void> connectManagement({
    required String masterUrl,
    required String panelToken,
    required String name,
  }) async {
    state = const AsyncData(AuthStateLoading());
    try {
      final normalizedUrl = normalizeMasterUrl(masterUrl);
      final uri = Uri.parse(normalizedUrl);
      if (uri.scheme != 'http' && uri.scheme != 'https') {
        throw const FormatException('Master URL must use http or https');
      }
      if (uri.host.trim().isEmpty) {
        throw const FormatException('Master URL must include a host');
      }

      final token = panelToken.trim();
      if (token.isEmpty) {
        throw const FormatException('Panel token is required');
      }
      final current = await ref.read(authRepositoryProvider).loadProfile();
      final profile = current.copyWith(
        masterUrl: normalizedUrl,
        displayName: name.trim().isEmpty ? current.displayName : name.trim(),
        activeMode: ConnectionMode.management,
        management: ManagementProfile(panelToken: token),
      );
      await ref.read(authRepositoryProvider).saveProfile(profile);
      state = AsyncData(AuthStateAuthenticated(profile));
    } catch (e) {
      state = AsyncData(AuthStateError(e.toString()));
    }
  }

  Future<void> clearManagement() async {
    final repo = ref.read(authRepositoryProvider);
    final current = await repo.loadProfile();
    final next = current.clearManagement();
    if (next.hasAnyCredentials) {
      await repo.saveProfile(next);
      state = AsyncData(AuthStateAuthenticated(next));
    } else {
      await repo.clearProfile();
      state = const AsyncData(AuthStateUnauthenticated());
    }
  }

  Future<void> clearAgent() async {
    final repo = ref.read(authRepositoryProvider);
    final current = await repo.loadProfile();
    final next = current.clearAgent();
    if (next.hasAnyCredentials) {
      await repo.saveProfile(next);
      state = AsyncData(AuthStateAuthenticated(next));
    } else {
      await repo.clearProfile();
      state = const AsyncData(AuthStateUnauthenticated());
    }
  }

  Future<void> logout() async {
    await ref.read(authRepositoryProvider).clearProfile();
    state = const AsyncData(AuthStateUnauthenticated());
  }

  Map<String, dynamic> _decodeJson(String text) {
    if (text.trim().isEmpty) return const {};
    try {
      final decoded = jsonDecode(text);
      return decoded is Map<String, dynamic> ? decoded : const {};
    } on FormatException {
      return const {};
    }
  }
}
