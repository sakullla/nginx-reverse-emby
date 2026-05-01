import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/design/theme/accent_themes.dart';
import '../core/design/theme/theme_controller.dart';
import '../core/design/tokens/app_colors.dart';
import '../core/design/tokens/app_spacing.dart';
import '../core/design/tokens/app_typography.dart';
import '../core/platform/platform_capabilities.dart';
import '../core/routing/route_names.dart';
import '../l10n/app_localizations.dart';

// ---------------------------------------------------------------------------
// Navigation item descriptor
// ---------------------------------------------------------------------------

class _NavItem {
  const _NavItem({
    required this.icon,
    required this.label,
    required this.route,
  });

  final IconData icon;
  final String label;
  final String route;
}

// ---------------------------------------------------------------------------
// GlassSidebar
// ---------------------------------------------------------------------------

class GlassSidebar extends ConsumerWidget {
  const GlassSidebar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final accent = ref.watch(
      themeControllerProvider.select((s) => s.value?.accent ?? AccentThemes.defaults),
    );
    final caps = PlatformCapabilities.current;
    final location = GoRouterState.of(context).matchedLocation;

    final loc = AppLocalizations.of(context)!;

    // Build the platform-aware navigation items.
    final items = [
      _NavItem(
        icon: Icons.dashboard_rounded,
        label: loc.navDashboard,
        route: RouteNames.dashboard,
      ),
      _NavItem(
        icon: Icons.rule_rounded,
        label: loc.navRules,
        route: RouteNames.rules,
      ),
      _NavItem(
        icon: Icons.smart_toy_outlined,
        label: loc.navAgent,
        route: RouteNames.agents,
      ),
      if (caps.canManageCertificates)
        _NavItem(
          icon: Icons.verified_user_outlined,
          label: loc.navCertificates,
          route: RouteNames.certificates,
        ),
      if (caps.canManageRelay)
        _NavItem(
          icon: Icons.settings_ethernet_outlined,
          label: loc.navRelay,
          route: RouteNames.relay,
        ),
    ];

    return Container(
      width: 64,
      decoration: const BoxDecoration(
        color: Color(0x33000000), // rgba(0,0,0,0.2)
        border: Border(
          right: BorderSide(
            color: Color(0x0FFFFFFF), // rgba(255,255,255,0.06)
          ),
        ),
      ),
      child: Column(
        children: [
          // -- Logo -----------------------------------------------------------
          const SizedBox(height: AppSpacing.s12),
          _LogoIcon(accent: accent),
          const SizedBox(height: AppSpacing.s12),
          const Divider(
            height: 1,
            thickness: 1,
            color: Color(0x0FFFFFFF),
          ),

          // -- Navigation items -----------------------------------------------
          Expanded(
            child: SingleChildScrollView(
              padding: const EdgeInsets.symmetric(vertical: AppSpacing.s8),
              child: Column(
                children: [
                  for (final item in items)
                    _SidebarItem(
                      icon: item.icon,
                      label: item.label,
                      route: item.route,
                      isActive: _isActive(location, item.route),
                      accent: accent,
                    ),
                ],
              ),
            ),
          ),

          // -- Settings (bottom) -----------------------------------------------
          const Divider(
            height: 1,
            thickness: 1,
            color: Color(0x0FFFFFFF),
          ),
          _SidebarItem(
            icon: Icons.settings_outlined,
            label: loc.navSettings,
            route: RouteNames.settings,
            isActive: location == RouteNames.settings,
            accent: accent,
          ),
          const SizedBox(height: AppSpacing.s8),
        ],
      ),
    );
  }

  /// Whether [route] matches the current [location].
  bool _isActive(String location, String route) {
    if (route == RouteNames.dashboard) return location == route;
    return location.startsWith(route);
  }
}

// ---------------------------------------------------------------------------
// Logo icon
// ---------------------------------------------------------------------------

class _LogoIcon extends StatelessWidget {
  const _LogoIcon({required this.accent});
  final AccentColors accent;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 32,
      height: 32,
      decoration: BoxDecoration(
        gradient: accent.primaryGradient,
        borderRadius: BorderRadius.circular(8),
      ),
      alignment: Alignment.center,
      child: const Text(
        'N',
        style: TextStyle(
          color: AppColors.textPrimary,
          fontSize: 16,
          fontWeight: FontWeight.w700,
          height: 1.0,
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Single sidebar item
// ---------------------------------------------------------------------------

class _SidebarItem extends StatelessWidget {
  const _SidebarItem({
    required this.icon,
    required this.label,
    required this.route,
    required this.isActive,
    required this.accent,
  });

  final IconData icon;
  final String label;
  final String route;
  final bool isActive;
  final AccentColors accent;

  @override
  Widget build(BuildContext context) {
    return _SidebarItemHover(
      isActive: isActive,
      accent: accent,
      onTap: () => context.go(route),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const SizedBox(height: AppSpacing.s8),
          SizedBox(
            width: 40,
            height: 40,
            child: Icon(
              icon,
              size: 20,
              color: isActive ? accent.primaryStart : AppColors.textMuted,
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: AppTypography.metadataSmall.copyWith(
              color: isActive ? accent.primaryStart : AppColors.textMuted,
              fontWeight: isActive ? FontWeight.w600 : FontWeight.w400,
            ),
          ),
          const SizedBox(height: AppSpacing.s4),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Hover wrapper for sidebar items
// ---------------------------------------------------------------------------

class _SidebarItemHover extends StatefulWidget {
  const _SidebarItemHover({
    required this.isActive,
    required this.accent,
    required this.onTap,
    required this.child,
  });

  final bool isActive;
  final AccentColors accent;
  final VoidCallback onTap;
  final Widget child;

  @override
  State<_SidebarItemHover> createState() => _SidebarItemHoverState();
}

class _SidebarItemHoverState extends State<_SidebarItemHover> {
  bool _hovering = false;

  @override
  Widget build(BuildContext context) {
    Color? bg;
    BoxBorder? border;

    if (widget.isActive) {
      bg = widget.accent.primaryStart.withValues(alpha: 0.15);
      border = Border(
        left: BorderSide(color: widget.accent.primaryStart, width: 2),
      );
    } else if (_hovering) {
      bg = const Color(0x1AFFFFFF); // rgba(255,255,255,0.10)
    }

    return GestureDetector(
      onTap: widget.onTap,
      child: MouseRegion(
        onEnter: (_) => setState(() => _hovering = true),
        onExit: (_) => setState(() => _hovering = false),
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          decoration: BoxDecoration(
            color: bg,
            border: border,
          ),
          child: widget.child,
        ),
      ),
    );
  }
}
