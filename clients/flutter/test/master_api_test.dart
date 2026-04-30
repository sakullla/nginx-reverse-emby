import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/services/master_api.dart';

void main() {
  test('normalizeMasterUrl accepts panel-api and join script URLs', () {
    expect(
      normalizeMasterUrl(
        'https://panel.example.com/panel-api/public/join-agent.sh',
      ),
      'https://panel.example.com',
    );
    expect(
      normalizeMasterUrl('https://panel.example.com/panel-api/'),
      'https://panel.example.com',
    );
  });

  test('register posts expected backend request and parses agent id', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      final body = jsonDecode(await utf8.decoder.bind(request).join());

      expect(request.method, 'POST');
      expect(request.uri.path, '/panel-api/agents/register');
      expect(request.headers.contentType?.mimeType, 'application/json');
      expect(request.headers.value('X-Register-Token'), 'register-secret');
      expect(request.headers.value('X-Agent-Token'), 'agent-secret');
      expect(body, {
        'name': 'phone-a',
        'agent_url': '',
        'agent_token': 'agent-secret',
        'version': '1.0.0',
        'platform': 'android',
        'tags': ['mobile', 'edge'],
        'capabilities': ['http_rules'],
        'mode': 'pull',
        'register_token': 'register-secret',
      });

      request.response
        ..statusCode = HttpStatus.ok
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'agent': {'id': 'agent-1'},
          }),
        );
      await request.response.close();
    });

    final api = HttpMasterApi();

    final result = await api.register(
      MasterApiConfig(
        masterUrl: 'http://${server.address.host}:${server.port}/',
        registerToken: 'register-secret',
      ),
      const RegisterClientRequest(
        name: 'phone-a',
        agentToken: 'agent-secret',
        version: '1.0.0',
        platform: 'android',
        tags: ['mobile', 'edge'],
        capabilities: ['http_rules'],
      ),
    );

    expect(captured.uri.path, '/panel-api/agents/register');
    expect(result.agentId, 'agent-1');
    expect(result.agentToken, 'agent-secret');
  });

  test('register reports backend error message', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      await utf8.decoder.bind(request).join();
      request.response
        ..statusCode = HttpStatus.unauthorized
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'error': 'Unauthorized: Invalid token'}));
      await request.response.close();
    });

    final api = HttpMasterApi();

    expect(
      () => api.register(
        MasterApiConfig(
          masterUrl: 'http://${server.address.host}:${server.port}',
          registerToken: 'bad-token',
        ),
        const RegisterClientRequest(
          name: 'phone-a',
          agentToken: 'agent-secret',
          version: '1.0.0',
          platform: 'android',
        ),
      ),
      throwsA(
        isA<MasterApiException>().having(
          (error) => error.message,
          'message',
          'Unauthorized: Invalid token',
        ),
      ),
    );
  });

  test('register reports HTTP status for non-json backend errors', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      await utf8.decoder.bind(request).join();
      request.response
        ..statusCode = HttpStatus.internalServerError
        ..headers.contentType = ContentType.text
        ..write('backend unavailable');
      await request.response.close();
    });

    final api = HttpMasterApi();

    expect(
      () => api.register(
        MasterApiConfig(
          masterUrl: 'http://${server.address.host}:${server.port}',
          registerToken: 'register-secret',
        ),
        const RegisterClientRequest(
          name: 'phone-a',
          agentToken: 'agent-secret',
          version: '1.0.0',
          platform: 'android',
        ),
      ),
      throwsA(
        isA<MasterApiException>().having(
          (error) => error.message,
          'message',
          'Registration failed with HTTP 500',
        ),
      ),
    );
  });

  test(
    'register reports invalid backend response for malformed success',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      server.listen((request) async {
        await utf8.decoder.bind(request).join();
        request.response
          ..statusCode = HttpStatus.ok
          ..headers.contentType = ContentType.text
          ..write('not json');
        await request.response.close();
      });

      final api = HttpMasterApi();

      expect(
        () => api.register(
          MasterApiConfig(
            masterUrl: 'http://${server.address.host}:${server.port}',
            registerToken: 'register-secret',
          ),
          const RegisterClientRequest(
            name: 'phone-a',
            agentToken: 'agent-secret',
            version: '1.0.0',
            platform: 'android',
          ),
        ),
        throwsA(
          isA<MasterApiException>().having(
            (error) => error.message,
            'message',
            startsWith('Invalid backend response:'),
          ),
        ),
      );
    },
  );
}
