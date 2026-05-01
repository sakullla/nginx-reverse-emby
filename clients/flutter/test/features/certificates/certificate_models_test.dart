import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';

void main() {
  test('Certificate parses backend certificate metadata', () {
    final cert = Certificate.fromJson({
      'id': 21,
      'domain': 'emby.example.com',
      'scope': 'domain',
      'issuer_mode': 'local_http01',
      'certificate_type': 'acme',
      'status': 'active',
      'expires_at': '2026-06-01T00:00:00Z',
      'issued_at': '2026-03-01T00:00:00Z',
      'self_signed': false,
      'fingerprint': 'abc',
      'target_agent_ids': ['local'],
    });

    expect(cert.id, '21');
    expect(cert.domain, 'emby.example.com');
    expect(cert.issuerMode, 'local_http01');
    expect(cert.targetAgentIds, ['local']);
  });
}
