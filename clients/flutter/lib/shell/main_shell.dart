import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/platform/platform_capabilities.dart';
import '../core/routing/route_names.dart';
import '../l10n/app_localizations.dart';
import 'sidebar.dart';
import 'topbar.dart';

// ---------------------------------------------------------------------------
// Route title mapping
// ---------------------------------------------------------------------------

String _routeTitle(BuildContext context, String location) {
  final loc = AppLocalizations.of(context);
  if (loc == null) return 'Dashboard';
  if (location.startsWith(RouteNames.rules)) return loc.navRules;
  if (location.startsWith(RouteNames.certificates)) return loc.navCertificates;
  if (location.startsWith(RouteNames.agents)) return loc.navAgent;
  if (location.startsWith(RouteNames.relay)) return loc.navRelay;
  if (location.startsWith(RouteNames.settings)) return loc.navSettings;
  return loc.navDashboard;
}

// ---------------------------------------------------------------------------
// MainShell
// ---------------------------------------------------------------------------

class MainShell extends ConsumerWidget {
  const MainShell({super.key, required this.child});

  /// The routed page content from ShellRoute.
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final isDesktop = constraints.maxWidth >= 600;
        if (isDesktop) {
          return _DesktopShell(child: child);
        }
        return _MobileShell(child: child);
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Desktop layout: sidebar + topbar + content
// ---------------------------------------------------------------------------

class _DesktopShell extends StatelessWidget {
  const _DesktopShell({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final location = GoRouterState.of(context).matchedLocation;

    return Scaffold(
      backgroundColor: Colors.transparent,
      body: Row(
        children: [
          const GlassSidebar(),
          Expanded(
            child: Column(
              children: [
                GlassTopBar(title: _routeTitle(context, location)),
                Expanded(child: child),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Mobile layout: content + bottom navigation bar
// ---------------------------------------------------------------------------

class _MobileShell extends StatelessWidget {
  const _MobileShell({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final location = GoRouterState.of(context).matchedLocation;
    final caps = PlatformCapabilities.current;
    final loc = AppLocalizations.of(context);

    final routes = [
      RouteNames.dashboard,
      RouteNames.rules,
      RouteNames.agents,
      RouteNames.settings,
    ];

    int selectedIndex = 0;
    if (location.startsWith(RouteNames.rules)) {
      selectedIndex = 1;
    } else if (location.startsWith(RouteNames.agents)) {
      selectedIndex = 2;
    } else if (location.startsWith(RouteNames.settings)) {
      selectedIndex = 3;
    }

    return Scaffold(
      backgroundColor: Colors.transparent,
      body: Column(
        children: [
          GlassTopBar(title: _routeTitle(context, location)),
          Expanded(child: child),
        ],
      ),
      bottomNavigationBar: Container(
        decoration: const BoxDecoration(
          color: Color(0x33000000), // rgba(0,0,0,0.2)
          border: Border(
            top: BorderSide(
              color: Color(0x0FFFFFFF), // rgba(255,255,255,0.06)
            ),
          ),
        ),
        child: NavigationBar(
          backgroundColor: Colors.transparent,
          elevation: 0,
          selectedIndex: selectedIndex,
          onDestinationSelected: (index) {
            context.go(routes[index]);
          },
          destinations: [
            NavigationDestination(
              icon: const Icon(Icons.dashboard_rounded),
              label: loc?.navDashboard ?? 'Dashboard',
            ),
            NavigationDestination(
              icon: const Icon(Icons.rule_rounded),
              label: loc?.navRules ?? 'Rules',
            ),
            NavigationDestination(
              icon: const Icon(Icons.smart_toy_outlined),
              label: loc?.navAgent ?? 'Agents',
            ),
            NavigationDestination(
              icon: const Icon(Icons.settings_outlined),
              label: caps.canManageCertificates ? (loc?.navMore ?? 'More') : (loc?.navSettings ?? 'Settings'),
            ),
          ],
        ),
      ),
    );
  }
}
