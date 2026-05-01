import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';

void main() {
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
