import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';

void main() {
  test('RelayListener derives listen address from bind hosts and port', () {
    final listener = RelayListener.fromJson({
      'id': 2,
      'agent_id': 'agent-1',
      'agent_name': 'edge-a',
      'name': 'public-tls',
      'listen_port': 8443,
      'bind_hosts': ['0.0.0.0'],
      'enabled': true,
      'tls_mode': 'ca_only',
      'certificate_source': 'existing_certificate',
      'certificate_id': 21,
    });

    expect(listener.id, '2');
    expect(listener.listenAddress, '0.0.0.0:8443');
    expect(listener.agentName, 'edge-a');
    expect(listener.certificateId, '21');
  });
}
