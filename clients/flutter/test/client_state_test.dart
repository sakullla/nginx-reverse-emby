import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';

void main() {
  test('profile is registered only when agent id and token exist', () {
    expect(
      const ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'desktop',
      ).isRegistered,
      isFalse,
    );
    expect(
      const ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'desktop',
        agentId: 'agent-1',
        token: 'secret',
      ).isRegistered,
      isTrue,
    );
  });
}
