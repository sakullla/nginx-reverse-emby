import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/platform/platform_capabilities.dart';
import '../core/routing/route_names.dart';
import 'sidebar.dart';
import 'topbar.dart';

// ---------------------------------------------------------------------------
// Route title mapping
// ---------------------------------------------------------------------------

String _routeTitle(String location) {
  if (location.startsWith(RouteNames.rules)) return '规则';
  if (location.startsWith(RouteNames.certificates)) return '证书';
  if (location.startsWith(RouteNames.agents)) return '代理';
  if (location.startsWith(RouteNames.relay)) return '中继';
  if (location.startsWith(RouteNames.settings)) return '设置';
  return '面板';
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
                GlassTopBar(title: _routeTitle(location)),
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
          GlassTopBar(title: _routeTitle(location)),
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
            const NavigationDestination(
              icon: Icon(Icons.dashboard_rounded),
              label: '面板',
            ),
            const NavigationDestination(
              icon: Icon(Icons.rule_rounded),
              label: '规则',
            ),
            const NavigationDestination(
              icon: Icon(Icons.smart_toy_outlined),
              label: '代理',
            ),
            NavigationDestination(
              icon: const Icon(Icons.settings_outlined),
              label: caps.canManageCertificates ? '更多' : '设置',
            ),
          ],
        ),
      ),
    );
  }
}
