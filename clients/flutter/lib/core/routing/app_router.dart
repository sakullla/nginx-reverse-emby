import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../platform/platform_capabilities.dart';
import 'route_names.dart';
import '../../features/auth/presentation/screens/connect_screen.dart';
import '../../features/dashboard/presentation/screens/dashboard_screen.dart';
import '../../features/rules/presentation/screens/rules_list_screen.dart';
import '../../features/agents/presentation/screens/agents_screen.dart';
import '../../features/certificates/presentation/screens/certificates_screen.dart';
import '../../features/relay/presentation/screens/relay_screen.dart';
import '../../features/settings/presentation/screens/settings_screen.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();
final _shellNavigatorKey = GlobalKey<NavigatorState>();

final routerProvider = Provider<GoRouter>((ref) {
  final caps = PlatformCapabilities.current;

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: RouteNames.dashboard,
    redirect: (context, state) {
      // TODO: Add auth guard once auth provider is built
      return null;
    },
    routes: [
      GoRoute(
        path: RouteNames.connect,
        builder: (context, state) => const ConnectScreen(),
      ),
      ShellRoute(
        navigatorKey: _shellNavigatorKey,
        builder: (context, state, child) => AppShell(child: child),
        routes: [
          GoRoute(
            path: RouteNames.dashboard,
            builder: (context, state) => const DashboardScreen(),
          ),
          GoRoute(
            path: RouteNames.rules,
            builder: (context, state) => const RulesListScreen(),
          ),
          if (caps.canManageCertificates)
            GoRoute(
              path: RouteNames.certificates,
              builder: (context, state) => const CertificatesScreen(),
            ),
          GoRoute(
            path: RouteNames.agents,
            builder: (context, state) => const AgentsScreen(),
          ),
          if (caps.canManageRelay)
            GoRoute(
              path: RouteNames.relay,
              builder: (context, state) => const RelayScreen(),
            ),
          GoRoute(
            path: RouteNames.settings,
            builder: (context, state) => const SettingsScreen(),
          ),
        ],
      ),
    ],
  );
});

class AppShell extends StatelessWidget {
  const AppShell({super.key, required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final isDesktop = constraints.maxWidth >= 600;
        if (isDesktop) {
          return Scaffold(
            body: Row(
              children: [
                _DesktopNavigation(),
                const VerticalDivider(thickness: 1, width: 1),
                Expanded(child: child),
              ],
            ),
          );
        }
        return Scaffold(
          body: child,
          bottomNavigationBar: _MobileNavigation(),
        );
      },
    );
  }
}

class _DesktopNavigation extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final caps = PlatformCapabilities.current;
    final location = GoRouterState.of(context).matchedLocation;

    final destinations = [
      const NavigationRailDestination(
        icon: Icon(Icons.dashboard_outlined),
        selectedIcon: Icon(Icons.dashboard),
        label: Text('Dashboard'),
      ),
      const NavigationRailDestination(
        icon: Icon(Icons.rule_outlined),
        selectedIcon: Icon(Icons.rule),
        label: Text('Rules'),
      ),
      if (caps.canManageCertificates)
        const NavigationRailDestination(
          icon: Icon(Icons.security_outlined),
          selectedIcon: Icon(Icons.security),
          label: Text('Certificates'),
        ),
      const NavigationRailDestination(
        icon: Icon(Icons.memory_outlined),
        selectedIcon: Icon(Icons.memory),
        label: Text('Agents'),
      ),
      if (caps.canManageRelay)
        const NavigationRailDestination(
          icon: Icon(Icons.sync_alt_outlined),
          selectedIcon: Icon(Icons.sync_alt),
          label: Text('Relay'),
        ),
      const NavigationRailDestination(
        icon: Icon(Icons.settings_outlined),
        selectedIcon: Icon(Icons.settings),
        label: Text('Settings'),
      ),
    ];

    int selectedIndex = 0;
    if (location.startsWith('/rules')) {
      selectedIndex = 1;
    } else if (location.startsWith('/certificates')) {
      selectedIndex = 2;
    } else if (location.startsWith('/agents')) {
      selectedIndex = caps.canManageCertificates ? 3 : 2;
    } else if (location.startsWith('/relay')) {
      selectedIndex = caps.canManageCertificates ? 4 : 3;
    } else if (location.startsWith('/settings')) {
      selectedIndex = destinations.length - 1;
    }

    return NavigationRail(
      selectedIndex: selectedIndex,
      onDestinationSelected: (index) {
        final routes = [
          RouteNames.dashboard,
          RouteNames.rules,
          if (caps.canManageCertificates) RouteNames.certificates,
          RouteNames.agents,
          if (caps.canManageRelay) RouteNames.relay,
          RouteNames.settings,
        ];
        context.go(routes[index]);
      },
      labelType: NavigationRailLabelType.all,
      destinations: destinations,
    );
  }
}

class _MobileNavigation extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final location = GoRouterState.of(context).matchedLocation;

    int selectedIndex = 0;
    if (location.startsWith('/rules')) {
      selectedIndex = 1;
    } else if (location.startsWith('/agents')) {
      selectedIndex = 2;
    } else if (location.startsWith('/settings')) {
      selectedIndex = 3;
    }

    return NavigationBar(
      selectedIndex: selectedIndex,
      onDestinationSelected: (index) {
        final routes = [
          RouteNames.dashboard,
          RouteNames.rules,
          RouteNames.agents,
          RouteNames.settings,
        ];
        context.go(routes[index]);
      },
      destinations: const [
        NavigationDestination(
          icon: Icon(Icons.dashboard_outlined),
          selectedIcon: Icon(Icons.dashboard),
          label: 'Dashboard',
        ),
        NavigationDestination(
          icon: Icon(Icons.rule_outlined),
          selectedIcon: Icon(Icons.rule),
          label: 'Rules',
        ),
        NavigationDestination(
          icon: Icon(Icons.memory_outlined),
          selectedIcon: Icon(Icons.memory),
          label: 'Agents',
        ),
        NavigationDestination(
          icon: Icon(Icons.settings_outlined),
          selectedIcon: Icon(Icons.settings),
          label: 'Settings',
        ),
      ],
    );
  }
}
