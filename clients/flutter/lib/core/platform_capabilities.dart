enum NrePlatform { windows, macos, android }

class PlatformCapabilities {
  const PlatformCapabilities({
    required this.platform,
    required this.canManageLocalAgent,
    required this.canViewRemoteAgents,
    required this.canInstallUpdates,
  });

  final NrePlatform platform;
  final bool canManageLocalAgent;
  final bool canViewRemoteAgents;
  final bool canInstallUpdates;

  static PlatformCapabilities forPlatform(NrePlatform platform) {
    switch (platform) {
      case NrePlatform.windows:
      case NrePlatform.macos:
        return PlatformCapabilities(
          platform: platform,
          canManageLocalAgent: true,
          canViewRemoteAgents: true,
          canInstallUpdates: true,
        );
      case NrePlatform.android:
        return const PlatformCapabilities(
          platform: NrePlatform.android,
          canManageLocalAgent: false,
          canViewRemoteAgents: true,
          canInstallUpdates: false,
        );
    }
  }
}
