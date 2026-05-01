import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';

void main() {
  test('AgentSummary parses status and revision fields', () {
    final agent = AgentSummary.fromJson({
      'id': 'agent-1',
      'name': 'edge-a',
      'status': 'online',
      'mode': 'pull',
      'platform': 'windows',
      'version': '2.1.0',
      'last_seen': '2026-05-01T10:00:00Z',
      'current_revision': 3,
      'target_revision': 5,
      'tags': ['edge'],
      'capabilities': ['http_rules'],
    });

    expect(agent.id, 'agent-1');
    expect(agent.isOnline, isTrue);
    expect(agent.hasPendingRevision, isTrue);
    expect(agent.tags, ['edge']);
  });
}
