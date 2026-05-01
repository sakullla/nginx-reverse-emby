import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';

void main() {
  test(
    'ClientProfile stores management and agent credentials independently',
    () {
      final profile = ClientProfile(
        masterUrl: 'https://panel.example.com',
        displayName: 'ops-laptop',
        activeMode: ConnectionMode.management,
        management: const ManagementProfile(panelToken: 'panel-secret'),
        agent: const AgentProfile(
          agentId: 'agent-1',
          agentToken: 'agent-secret',
        ),
      );

      expect(profile.hasManagementCredentials, isTrue);
      expect(profile.hasAgentCredentials, isTrue);
      expect(profile.isRegistered, isTrue);
      expect(profile.management.panelToken, 'panel-secret');
      expect(profile.agent.agentToken, 'agent-secret');
    },
  );

  test('ClientProfile parses legacy agentId token profile', () {
    final profile = ClientProfile.fromJson({
      'masterUrl': 'https://panel.example.com',
      'displayName': 'legacy-client',
      'agentId': 'agent-legacy',
      'token': 'legacy-token',
    });

    expect(profile.activeMode, ConnectionMode.agent);
    expect(profile.agent.agentId, 'agent-legacy');
    expect(profile.agent.agentToken, 'legacy-token');
    expect(profile.hasAgentCredentials, isTrue);
    expect(profile.hasManagementCredentials, isFalse);
  });

  test('clearManagement leaves agent credentials intact', () {
    final profile = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );

    final cleared = profile.clearManagement();

    expect(cleared.hasManagementCredentials, isFalse);
    expect(cleared.hasAgentCredentials, isTrue);
    expect(cleared.activeMode, ConnectionMode.agent);
  });
}
