import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';

void main() {
  test('HttpProxyRule normalizes backend_url into backends', () {
    final rule = HttpProxyRule.fromJson({
      'id': 7,
      'frontend_url': 'https://emby.example.com',
      'backend_url': 'http://emby:8096',
      'enabled': true,
      'load_balancing': {'strategy': 'round_robin'},
    });

    expect(rule.id, '7');
    expect(rule.frontendUrl, 'https://emby.example.com');
    expect(rule.backendUrl, 'http://emby:8096');
    expect(rule.backends.single.url, 'http://emby:8096');
    expect(rule.loadBalancingStrategy, 'round_robin');
  });

  test('CreateHttpRuleRequest emits backend-compatible payload', () {
    final request = CreateHttpRuleRequest(
      frontendUrl: 'https://emby.example.com',
      backends: const [HttpBackend(url: 'http://emby:8096')],
      enabled: false,
      tags: const ['media'],
      proxyRedirect: true,
      passProxyHeaders: true,
      userAgent: 'NRE-Test',
      customHeaders: const [HttpHeaderEntry(name: 'X-Test', value: 'yes')],
      loadBalancingStrategy: 'adaptive',
      relayLayers: const [
        [1, 2],
      ],
      relayObfs: true,
    );

    expect(request.toJson(), {
      'frontend_url': 'https://emby.example.com',
      'backend_url': 'http://emby:8096',
      'backends': [
        {'url': 'http://emby:8096'},
      ],
      'enabled': false,
      'tags': ['media'],
      'proxy_redirect': true,
      'pass_proxy_headers': true,
      'user_agent': 'NRE-Test',
      'custom_headers': [
        {'name': 'X-Test', 'value': 'yes'},
      ],
      'load_balancing': {'strategy': 'adaptive'},
      'relay_layers': [
        [1, 2],
      ],
      'relay_obfs': true,
    });
  });

  test('HttpBackend and HttpHeaderEntry support copyWith', () {
    final backend = const HttpBackend(url: 'http://emby:8096', weight: 1);
    final header = const HttpHeaderEntry(name: 'X-Test', value: 'yes');

    expect(backend.copyWith(url: 'http://emby:8920').url, 'http://emby:8920');
    expect(header.copyWith(value: 'no').value, 'no');
  });

  test('ProxyRule parses backend-shaped HTTP rule payload', () {
    final rule = ProxyRule.fromJson({
      'id': 7,
      'frontend_url': 'https://emby.example.com',
      'backend_url': 'http://emby:8096',
    });

    expect(rule.id, '7');
    expect(rule.domain, 'https://emby.example.com');
    expect(rule.target, 'http://emby:8096');
    expect(rule.type, 'http');
    expect(rule.enabled, isTrue);
  });
}
