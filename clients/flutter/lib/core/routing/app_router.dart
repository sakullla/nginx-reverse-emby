import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../platform/platform_capabilities.dart';
import 'route_names.dart';
import '../../features/auth/data/models/auth_models.dart';
import '../../features/auth/presentation/providers/auth_provider.dart';
import '../../features/auth/presentation/screens/connect_screen.dart';
import '../../features/dashboard/presentation/screens/dashboard_screen.dart';
import '../../features/rules/presentation/screens/rules_list_screen.dart';
import '../../features/agents/presentation/screens/agents_screen.dart';
import '../../features/certificates/presentation/screens/certificates_screen.dart';
import '../../features/relay/presentation/screens/relay_screen.dart';
import '../../features/settings/presentation/screens/settings_screen.dart';
import '../../shell/main_shell.dart';

final _rootNavigatorKey = GlobalKey<NavigatorState>();
final _shellNavigatorKey = GlobalKey<NavigatorState>();

final routerProvider = Provider<GoRouter>((ref) {
  final caps = PlatformCapabilities.current;

  // Watch auth state so router rebuilds when auth changes
  ref.watch(authNotifierProvider);

  return GoRouter(
    navigatorKey: _rootNavigatorKey,
    initialLocation: RouteNames.dashboard,
    redirect: (context, state) {
      final auth = ref.read(authNotifierProvider);
      final isAuth = auth.value is AuthStateAuthenticated;

      final isConnectRoute = state.matchedLocation == RouteNames.connect;

      if (!isAuth && !isConnectRoute) return RouteNames.connect;
      if (isAuth && isConnectRoute) return RouteNames.dashboard;
      return null;
    },
    routes: [
      GoRoute(
        path: RouteNames.connect,
        builder: (context, state) => const ConnectScreen(),
      ),
      ShellRoute(
        navigatorKey: _shellNavigatorKey,
        builder: (context, state, child) => MainShell(child: child),
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
