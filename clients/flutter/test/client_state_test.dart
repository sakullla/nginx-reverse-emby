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

  test('profile serializes to and from json', () {
    const profile = ClientProfile(
      masterUrl: 'https://panel.example.com',
      displayName: 'desktop',
      agentId: 'agent-1',
      token: 'secret',
    );

    final restored = ClientProfile.fromJson(profile.toJson());

    expect(restored.masterUrl, 'https://panel.example.com');
    expect(restored.displayName, 'desktop');
    expect(restored.agentId, 'agent-1');
    expect(restored.token, 'secret');
  });

  test('profile json rejects missing required string fields', () {
    expect(
      () => ClientProfile.fromJson({
        'masterUrl': 'https://panel.example.com',
        'displayName': 'desktop',
        'agentId': 'agent-1',
      }),
      throwsA(isA<FormatException>()),
    );
  });
}
