import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';

void main() {
  test('base URL ending in panel-api is not double-appended', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true, 'agents': []}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}/panel-api',
      panelToken: 'panel-secret',
    );

    await api.fetchAgents();

    expect(captured.uri.path, '/panel-api/agents');
  });

  test('fetchAgents sends X-Panel-Token and parses agents envelope', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'agents': [
              {'id': 'agent-1', 'name': 'edge-a', 'status': 'online'},
            ],
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final agents = await api.fetchAgents();

    expect(captured.uri.path, '/panel-api/agents');
    expect(captured.headers.value('X-Panel-Token'), 'panel-secret');
    expect(agents.single.id, 'agent-1');
  });

  test(
    'fetchRules gets selected agent rules and parses rules envelope',
    () async {
      late HttpRequest captured;
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      server.listen((request) async {
        captured = request;
        request.response
          ..headers.contentType = ContentType.json
          ..write(
            jsonEncode({
              'ok': true,
              'rules': [
                {
                  'id': 4,
                  'frontend_url': 'https://emby.example.com',
                  'backend_url': 'http://emby:8096',
                  'enabled': true,
                },
              ],
            }),
          );
        await request.response.close();
      });

      final api = PanelApiClient(
        baseUrl: 'http://${server.address.host}:${server.port}',
        panelToken: 'panel-secret',
      );

      final rules = await api.fetchRules('agent-1');

      expect(captured.method, 'GET');
      expect(captured.uri.path, '/panel-api/agents/agent-1/rules');
      expect(rules.single.id, '4');
      expect(rules.single.backendUrl, 'http://emby:8096');
    },
  );

  test('createRule posts normalized payload to selected agent', () async {
    late Map<String, dynamic> body;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      expect(request.method, 'POST');
      expect(request.uri.path, '/panel-api/agents/local/rules');
      body =
          jsonDecode(await utf8.decoder.bind(request).join())
              as Map<String, dynamic>;
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'rule': {
              'id': 9,
              'frontend_url': body['frontend_url'],
              'backend_url': body['backend_url'],
              'enabled': body['enabled'],
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final rule = await api.createRule(
      'local',
      const CreateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );

    expect(body['backend_url'], 'http://emby:8096');
    expect(rule.id, '9');
  });

  test('updateRule puts normalized payload to selected rule', () async {
    late HttpRequest captured;
    late Map<String, dynamic> body;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      body =
          jsonDecode(await utf8.decoder.bind(request).join())
              as Map<String, dynamic>;
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'rule': {
              'id': 9,
              'frontend_url': body['frontend_url'],
              'backend_url': body['backend_url'],
              'enabled': body['enabled'],
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final rule = await api.updateRule(
      'local',
      '9',
      const UpdateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
        enabled: false,
      ),
    );

    expect(captured.method, 'PUT');
    expect(captured.uri.path, '/panel-api/agents/local/rules/9');
    expect(body['backend_url'], 'http://emby:8096');
    expect(rule.enabled, isFalse);
  });

  test('deleteRule deletes selected rule', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.deleteRule('local', '9');

    expect(captured.method, 'DELETE');
    expect(captured.uri.path, '/panel-api/agents/local/rules/9');
  });

  test('applyConfig posts to selected agent apply endpoint', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.applyConfig('local');

    expect(captured.method, 'POST');
    expect(captured.uri.path, '/panel-api/agents/local/apply');
  });

  test(
    'fetchCertificates gets selected agent certificates and parses envelope',
    () async {
      late HttpRequest captured;
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      server.listen((request) async {
        captured = request;
        request.response
          ..headers.contentType = ContentType.json
          ..write(
            jsonEncode({
              'ok': true,
              'certificates': [
                {'id': 21, 'domain': 'emby.example.com'},
              ],
            }),
          );
        await request.response.close();
      });

      final api = PanelApiClient(
        baseUrl: 'http://${server.address.host}:${server.port}',
        panelToken: 'panel-secret',
      );

      final certificates = await api.fetchCertificates('local');

      expect(captured.method, 'GET');
      expect(captured.uri.path, '/panel-api/agents/local/certificates');
      expect(certificates.single.id, '21');
    },
  );

  test(
    'fetchRelayListeners gets selected agent listeners and parses envelope',
    () async {
      late HttpRequest captured;
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(server.close);
      server.listen((request) async {
        captured = request;
        request.response
          ..headers.contentType = ContentType.json
          ..write(
            jsonEncode({
              'ok': true,
              'listeners': [
                {
                  'id': 2,
                  'agent_id': 'local',
                  'listen_port': 8443,
                  'bind_hosts': ['0.0.0.0'],
                },
              ],
            }),
          );
        await request.response.close();
      });

      final api = PanelApiClient(
        baseUrl: 'http://${server.address.host}:${server.port}',
        panelToken: 'panel-secret',
      );

      final listeners = await api.fetchRelayListeners('local');

      expect(captured.method, 'GET');
      expect(captured.uri.path, '/panel-api/agents/local/relay-listeners');
      expect(listeners.single.id, '2');
      expect(listeners.single.listenAddress, '0.0.0.0:8443');
    },
  );

  test('throws PanelApiException with backend message', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      request.response
        ..statusCode = HttpStatus.forbidden
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'error': 'panel token denied'}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'bad-token',
    );

    expect(
      api.fetchAgents,
      throwsA(
        isA<PanelApiException>().having(
          (e) => e.message,
          'message',
          'panel token denied',
        ),
      ),
    );
  });
}
