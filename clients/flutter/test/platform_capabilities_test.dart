import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/platform/platform_capabilities.dart';

void main() {
  test('PlatformCapabilities.current returns valid capabilities', () {
    final capabilities = PlatformCapabilities.current;

    // On desktop platforms (where tests run), should have full management
    expect(capabilities.canViewRemoteAgents, isTrue);
    // canManageLocalAgent depends on the test runner platform
    expect(capabilities.platform, isNotNull);
  });

  test('NrePlatform enum has expected values', () {
    expect(NrePlatform.values, contains(NrePlatform.windows));
    expect(NrePlatform.values, contains(NrePlatform.macos));
    expect(NrePlatform.values, contains(NrePlatform.android));
    expect(NrePlatform.values, contains(NrePlatform.linux));
    expect(NrePlatform.values, contains(NrePlatform.ios));
    expect(NrePlatform.values, contains(NrePlatform.unknown));
  });
}
