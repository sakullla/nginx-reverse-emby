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

  test('CreateRelayListenerRequest serializes auto trust controls', () {
    final json = const CreateRelayListenerRequest(
      agentId: 'local',
      name: 'public-tls',
      listenPort: 8443,
      bindHosts: ['0.0.0.0'],
      certificateSource: 'auto_relay_ca',
      trustModeSource: 'auto',
      tlsMode: 'pin_and_ca',
    ).toJson();

    expect(json['certificate_source'], 'auto_relay_ca');
    expect(json['trust_mode_source'], 'auto');
    expect(json['tls_mode'], 'pin_and_ca');
  });
}
