import 'dart:io';

enum NrePlatform { windows, macos, android, linux, ios, unknown }

class PlatformCapabilities {
  const PlatformCapabilities({
    required this.platform,
    required this.canManageLocalAgent,
    required this.canViewRemoteAgents,
    required this.canInstallUpdates,
    required this.canManageCertificates,
    required this.canManageRelay,
    required this.canEditRules,
  });

  final NrePlatform platform;
  final bool canManageLocalAgent;
  final bool canViewRemoteAgents;
  final bool canInstallUpdates;
  final bool canManageCertificates;
  final bool canManageRelay;
  final bool canEditRules;

  static PlatformCapabilities get current {
    final platform = _detectPlatform();
    return switch (platform) {
      NrePlatform.windows || NrePlatform.macos || NrePlatform.linux =>
        PlatformCapabilities(
          platform: platform,
          canManageLocalAgent: true,
          canViewRemoteAgents: true,
          canInstallUpdates: true,
          canManageCertificates: true,
          canManageRelay: true,
          canEditRules: true,
        ),
      NrePlatform.android || NrePlatform.ios => PlatformCapabilities(
          platform: platform,
          canManageLocalAgent: false,
          canViewRemoteAgents: true,
          canInstallUpdates: false,
          canManageCertificates: false,
          canManageRelay: false,
          canEditRules: false,
        ),
      NrePlatform.unknown => PlatformCapabilities(
          platform: platform,
          canManageLocalAgent: false,
          canViewRemoteAgents: true,
          canInstallUpdates: false,
          canManageCertificates: false,
          canManageRelay: false,
          canEditRules: false,
        ),
    };
  }

  static NrePlatform _detectPlatform() {
    if (Platform.isWindows) return NrePlatform.windows;
    if (Platform.isMacOS) return NrePlatform.macos;
    if (Platform.isLinux) return NrePlatform.linux;
    if (Platform.isAndroid) return NrePlatform.android;
    if (Platform.isIOS) return NrePlatform.ios;
    return NrePlatform.unknown;
  }
}
