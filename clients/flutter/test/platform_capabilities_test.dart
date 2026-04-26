import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/platform_capabilities.dart';

void main() {
  test('desktop platforms can manage local agent', () {
    expect(
      PlatformCapabilities.forPlatform(NrePlatform.windows).canManageLocalAgent,
      isTrue,
    );
    expect(
      PlatformCapabilities.forPlatform(NrePlatform.macos).canManageLocalAgent,
      isTrue,
    );
  });

  test('android is light management mode', () {
    final capabilities = PlatformCapabilities.forPlatform(NrePlatform.android);

    expect(capabilities.canManageLocalAgent, isFalse);
    expect(capabilities.canViewRemoteAgents, isTrue);
  });
}
