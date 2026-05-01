import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
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

  test('injected Dio still gets normalized panel api base and token', () async {
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
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
      dio: Dio(),
    );

    await api.fetchAgents();

    expect(captured.uri.path, '/panel-api/agents');
    expect(captured.headers.value('X-Panel-Token'), 'panel-secret');
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

  test('fetchRules encodes agent id path segment', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true, 'rules': []}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.fetchRules('agent/a b');

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/rules');
  });

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

  test('createRule encodes agent id path segment', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      await utf8.decoder.bind(request).join();
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'rule': {
              'id': 9,
              'frontend_url': 'https://emby.example.com',
              'backend_url': 'http://emby:8096',
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.createRule(
      'agent/a b',
      const CreateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/rules');
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

  test('updateRule encodes agent and rule id path segments', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      await utf8.decoder.bind(request).join();
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'rule': {
              'id': 'rule/1',
              'frontend_url': 'https://emby.example.com',
              'backend_url': 'http://emby:8096',
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.updateRule(
      'agent/a b',
      'rule/1',
      const UpdateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/rules/rule%2F1');
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

  test('deleteRule encodes agent and rule id path segments', () async {
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

    await api.deleteRule('agent/a b', 'rule/1');

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/rules/rule%2F1');
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

  test('applyConfig encodes agent id path segment', () async {
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

    await api.applyConfig('agent/a b');

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/apply');
  });

  test('renameAgent patches selected agent', () async {
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
            'agent': {
              'id': 'agent-1',
              'name': body['name'],
              'status': 'online',
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final agent = await api.renameAgent('agent-1', 'edge-a');

    expect(captured.method, 'PATCH');
    expect(captured.uri.path, '/panel-api/agents/agent-1');
    expect(agent.name, 'edge-a');
  });

  test('deleteAgent deletes selected agent', () async {
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

    await api.deleteAgent('agent-1');

    expect(captured.method, 'DELETE');
    expect(captured.uri.path, '/panel-api/agents/agent-1');
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

  test('fetchCertificates encodes agent id path segment', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true, 'certificates': []}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.fetchCertificates('agent/a b');

    expect(captured.uri.path, '/panel-api/agents/agent%2Fa%20b/certificates');
  });

  test('issueCertificate posts selected certificate issue endpoint', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      await utf8.decoder.bind(request).join();
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'ok': true,
            'certificate': {'id': 'cert-1', 'domain': 'emby.example.com'},
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final certificate = await api.issueCertificate('local', 'cert-1');

    expect(captured.method, 'POST');
    expect(
      captured.uri.path,
      '/panel-api/agents/local/certificates/cert-1/issue',
    );
    expect(certificate.id, 'cert-1');
  });

  test('deleteCertificate deletes selected certificate', () async {
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

    await api.deleteCertificate('local', 'cert-1');

    expect(captured.method, 'DELETE');
    expect(captured.uri.path, '/panel-api/agents/local/certificates/cert-1');
  });

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

  test('fetchRelayListeners encodes agent id path segment', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true, 'listeners': []}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    await api.fetchRelayListeners('agent/a b');

    expect(
      captured.uri.path,
      '/panel-api/agents/agent%2Fa%20b/relay-listeners',
    );
  });

  test('createRelayListener posts selected agent listener', () async {
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
            'listener': {
              'id': 'listener-1',
              'agent_id': body['agent_id'],
              'name': body['name'],
              'listen_port': body['listen_port'],
              'bind_hosts': body['bind_hosts'],
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final listener = await api.createRelayListener(
      'local',
      const CreateRelayListenerRequest(
        agentId: 'local',
        name: 'public-tls',
        listenPort: 8443,
        bindHosts: ['0.0.0.0'],
      ),
    );

    expect(captured.method, 'POST');
    expect(captured.uri.path, '/panel-api/agents/local/relay-listeners');
    expect(listener.listenAddress, '0.0.0.0:8443');
  });

  test('updateRelayListener puts selected listener', () async {
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
            'listener': {
              'id': 'listener-1',
              'name': body['name'],
              'listen_port': body['listen_port'],
              'bind_hosts': body['bind_hosts'],
            },
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final listener = await api.updateRelayListener(
      'local',
      'listener-1',
      const UpdateRelayListenerRequest(
        name: 'public-tls',
        listenPort: 9443,
        bindHosts: ['127.0.0.1'],
      ),
    );

    expect(captured.method, 'PUT');
    expect(
      captured.uri.path,
      '/panel-api/agents/local/relay-listeners/listener-1',
    );
    expect(listener.listenAddress, '127.0.0.1:9443');
  });

  test('deleteRelayListener deletes selected listener', () async {
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

    await api.deleteRelayListener('local', 'listener-1');

    expect(captured.method, 'DELETE');
    expect(
      captured.uri.path,
      '/panel-api/agents/local/relay-listeners/listener-1',
    );
  });

  test('throws PanelApiException when rules envelope is missing', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'ok': true}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    expect(
      () => api.fetchRules('local'),
      throwsA(
        isA<PanelApiException>().having(
          (error) => error.message,
          'message',
          'Backend response missing rules',
        ),
      ),
    );
  });

  test('throws PanelApiException for non-map success response', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      request.response
        ..headers.contentType = ContentType.text
        ..write('not json');
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    expect(
      api.fetchAgents,
      throwsA(
        isA<PanelApiException>().having(
          (error) => error.message,
          'message',
          'Invalid backend response',
        ),
      ),
    );
  });

  test('throws PanelApiException for malformed list entries', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      request.response
        ..headers.contentType = ContentType.json
        ..write(
          jsonEncode({
            'agents': ['bad'],
          }),
        );
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    expect(
      () => api.fetchAgents(),
      throwsA(
        isA<PanelApiException>().having(
          (error) => error.message,
          'message',
          'Backend response missing agents',
        ),
      ),
    );
  });

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
