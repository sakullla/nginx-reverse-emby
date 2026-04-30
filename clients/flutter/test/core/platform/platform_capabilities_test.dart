import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/platform/platform_capabilities.dart';

void main() {
  test('current returns valid capabilities', () {
    final caps = PlatformCapabilities.current;
    expect(caps.platform, isNot(NrePlatform.unknown));
    expect(caps.canViewRemoteAgents, isTrue);
  });

  test('desktop platforms have full capabilities', () {
    const desktop = PlatformCapabilities(
      platform: NrePlatform.windows,
      canManageLocalAgent: true,
      canViewRemoteAgents: true,
      canInstallUpdates: true,
      canManageCertificates: true,
      canManageRelay: true,
      canEditRules: true,
    );
    expect(desktop.canManageLocalAgent, isTrue);
    expect(desktop.canEditRules, isTrue);
  });

  test('mobile platforms have restricted capabilities', () {
    const mobile = PlatformCapabilities(
      platform: NrePlatform.android,
      canManageLocalAgent: false,
      canViewRemoteAgents: true,
      canInstallUpdates: false,
      canManageCertificates: false,
      canManageRelay: false,
      canEditRules: false,
    );
    expect(mobile.canManageLocalAgent, isFalse);
    expect(mobile.canViewRemoteAgents, isTrue);
  });
}
