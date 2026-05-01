import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../../core/client_state.dart' as runtime_state;
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/components/glass_chip.dart';
import '../../../../core/design/components/info_grid.dart';
import '../../../../core/design/components/stat_card.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../core/routing/route_names.dart';
import '../../../../core/design/theme/accent_themes.dart';
import '../../../../core/design/theme/theme_controller.dart';
import '../../../../l10n/app_localizations.dart';
import '../../../../services/local_agent_controller.dart';
import '../../../../services/local_agent_controller_provider.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../../rules/presentation/providers/rules_provider.dart';

class DashboardScreen extends ConsumerWidget {
  const DashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final authAsync = ref.watch(authNotifierProvider);

    return authAsync.when(
      data: (state) {
        if (state is AuthStateAuthenticated) {
          return _DashboardContent(profile: state.profile);
        }
        return _buildUnauthenticated(context);
      },
      loading: () => _buildLoadingSkeleton(),
      error: (e, _) => _buildErrorCard(context, ref),
    );
  }

  Widget _buildUnauthenticated(BuildContext context) {
    final loc = AppLocalizations.of(context)!;
    return Center(
      child: GlassCard(
        padding: const EdgeInsets.all(AppSpacing.s20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off, size: 48, color: AppColors.textMuted),
            const SizedBox(height: AppSpacing.s12),
            Text(loc.statusNotConnected, style: AppTypography.title),
            const SizedBox(height: AppSpacing.s4),
            Text(
              loc.descPleaseConnectFirst,
              style: AppTypography.metadata.copyWith(color: AppColors.textMuted),
            ),
            const SizedBox(height: AppSpacing.s16),
            GlassButton.primary(
              label: loc.btnConnect,
              onPressed: () => context.go(RouteNames.connect),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildLoadingSkeleton() {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        children: [
          _SkeletonBox(height: 80, borderRadius: AppRadius.card),
          const SizedBox(height: AppSpacing.s12),
          Row(
            children: List.generate(
              4,
              (_) => const Expanded(
                child: Padding(
                  padding: EdgeInsets.symmetric(horizontal: AppSpacing.s4),
                  child: _SkeletonBox(height: 100, borderRadius: AppRadius.card),
                ),
              ),
            ),
          ),
          const SizedBox(height: AppSpacing.s12),
          Row(
            children: List.generate(
              2,
              (_) => const Expanded(
                child: Padding(
                  padding: EdgeInsets.symmetric(horizontal: AppSpacing.s4),
                  child: _SkeletonBox(height: 180, borderRadius: AppRadius.card),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildErrorCard(BuildContext context, WidgetRef ref) {
    final loc = AppLocalizations.of(context)!;
    return Center(
      child: GlassCard(
        padding: const EdgeInsets.all(AppSpacing.s20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, size: 48, color: AppColors.error),
            const SizedBox(height: AppSpacing.s12),
            Text(loc.failedToLoadDashboard, style: AppTypography.title),
            const SizedBox(height: AppSpacing.s16),
            GlassButton.secondary(
              label: loc.btnRetry,
              onPressed: () => ref.invalidate(authNotifierProvider),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Dashboard Content — authenticated, scrollable body (no Scaffold)
// ---------------------------------------------------------------------------

class _DashboardContent extends ConsumerStatefulWidget {
  const _DashboardContent({required this.profile});

  final ClientProfile profile;

  @override
  ConsumerState<_DashboardContent> createState() => _DashboardContentState();
}

class _DashboardContentState extends ConsumerState<_DashboardContent> {
  LocalAgentRuntimeSnapshot? _snapshot;
  var _agentLoading = false;
  String _agentError = '';

  @override
  void initState() {
    super.initState();
    final caps = PlatformCapabilities.current;
    if (caps.canManageLocalAgent) {
      Future.microtask(_refreshAgent);
    }
  }

  @override
  void didUpdateWidget(covariant _DashboardContent oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.profile.agentId != widget.profile.agentId ||
        oldWidget.profile.token != widget.profile.token) {
      _refreshAgent();
    }
  }

  Future<void> _refreshAgent() async {
    final caps = PlatformCapabilities.current;
    if (!caps.canManageLocalAgent) return;
    setState(() {
      _agentLoading = true;
      _agentError = '';
    });
    try {
      final snapshot = await ref
          .read(localAgentControllerProvider)
          .status(_runtimeProfile);
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _agentLoading = false;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _agentError = error.toString();
          _agentLoading = false;
        });
      }
    }
  }

  Future<void> _startAgent() async {
    await _runAgent((c) => c.start(_runtimeProfile));
  }

  Future<void> _stopAgent() async {
    await _runAgent((c) => c.stop(_runtimeProfile));
  }

  Future<void> _runAgent(
    Future<LocalAgentRuntimeSnapshot> Function(LocalAgentController) action,
  ) async {
    setState(() {
      _agentLoading = true;
      _agentError = '';
    });
    try {
      final snapshot = await action(ref.read(localAgentControllerProvider));
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _agentLoading = false;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _agentError = error.toString();
          _agentLoading = false;
        });
      }
    }
  }

  runtime_state.ClientProfile get _runtimeProfile =>
      runtime_state.ClientProfile(
        masterUrl: widget.profile.masterUrl,
        displayName: widget.profile.displayName,
        agentId: widget.profile.agentId,
        token: widget.profile.token,
      );

  @override
  Widget build(BuildContext context) {
    final caps = PlatformCapabilities.current;
    final themeAsync = ref.watch(themeControllerProvider);
    final accent = themeAsync.valueOrNull?.accent ?? AccentThemes.defaults;
    final rulesAsync = ref.watch(rulesListProvider);
    final snapshot = _snapshot;
    final isRunning = snapshot?.status == LocalAgentControllerStatus.running;
    final isStopped = snapshot?.status == LocalAgentControllerStatus.stopped;

    final screenWidth = MediaQuery.sizeOf(context).width;
    final isWide = screenWidth >= 900;

    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // -- 1. Status Banner (only when connected and agent running) ---------
          if (isRunning) ...[
            _StatusBanner(accent: accent, loc: AppLocalizations.of(context)!),
            const SizedBox(height: AppSpacing.s12),
          ],

          // -- 2. Stats Grid -----------------------------------------------------
          _StatsGrid(
            isWide: isWide,
            accent: accent,
            rulesAsync: rulesAsync,
            isRunning: isRunning,
            agentLoading: _agentLoading,
            loc: AppLocalizations.of(context)!,
          ),
          const SizedBox(height: AppSpacing.s12),

          // -- 3. Bottom Row (Local Agent + Quick Actions) -----------------------
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Local Agent card — only when platform supports it
              if (caps.canManageLocalAgent)
                Expanded(
                  child: _LocalAgentCard(
                    snapshot: snapshot,
                    isRunning: isRunning,
                    isStopped: isStopped,
                    agentLoading: _agentLoading,
                    agentError: _agentError,
                    accent: accent,
                    loc: AppLocalizations.of(context)!,
                    onStart: _startAgent,
                    onStop: _stopAgent,
                    onRestart: () async {
                      await _runAgent((c) => c.stop(_runtimeProfile));
                      if (mounted) {
                        await _runAgent((c) => c.start(_runtimeProfile));
                      }
                    },
                  ),
                ),
              if (caps.canManageLocalAgent)
                const SizedBox(width: AppSpacing.s12),

              // Quick Actions
              Expanded(
                child: _QuickActions(caps: caps, accent: accent, loc: AppLocalizations.of(context)!),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Status Banner
// ---------------------------------------------------------------------------

class _StatusBanner extends StatelessWidget {
  const _StatusBanner({required this.accent, required this.loc});

  final AccentColors accent;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.card),
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: AppBlur.standard, sigmaY: AppBlur.standard),
        child: Container(
          padding: const EdgeInsets.all(AppSpacing.s16),
          decoration: BoxDecoration(
            gradient: LinearGradient(
              colors: [
                accent.primaryStart.withValues(alpha: 0.12),
                accent.primaryEnd.withValues(alpha: 0.06),
              ],
            ),
            borderRadius: BorderRadius.circular(AppRadius.card),
            border: Border.all(
              color: accent.primaryStart.withValues(alpha: 0.2),
            ),
          ),
          child: Row(
            children: [
              // Shield icon
              Container(
                width: 42,
                height: 42,
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    colors: [accent.primaryStart, accent.primaryEnd],
                  ),
                  borderRadius: BorderRadius.circular(AppRadius.medium),
                ),
                child: const Icon(
                  Icons.shield_outlined,
                  color: Colors.white,
                  size: 22,
                ),
              ),
              const SizedBox(width: AppSpacing.s12),
              // Title + subtitle
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      loc.descSystemRunningNormal,
                      style: AppTypography.title.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      loc.descAllAgentsOnlineLastSync,
                      style: AppTypography.metadataSmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                  ],
                ),
              ),
              // Action button
              GlassButton.secondary(
                label: loc.btnViewLogs,
                onPressed: () {
                  // Could navigate to a logs screen in the future
                },
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Stats Grid (4 cols desktop, 2 cols mobile)
// ---------------------------------------------------------------------------

class _StatsGrid extends StatelessWidget {
  const _StatsGrid({
    required this.isWide,
    required this.accent,
    required this.rulesAsync,
    required this.isRunning,
    required this.agentLoading,
    required this.loc,
  });

  final bool isWide;
  final AccentColors accent;
  final AsyncValue rulesAsync;
  final bool isRunning;
  final bool agentLoading;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final rulesCount = rulesAsync.valueOrNull?.length ?? 0;
    final disabledCount =
        rulesAsync.valueOrNull?.where((r) => !r.enabled).length ?? 0;

    final cards = [
      StatCard(
        label: loc.navRules,
        value: '$rulesCount',
        subtitle: disabledCount > 0 ? loc.labelDisabledCount(disabledCount) : null,
        accentColor: accent.primaryStart,
      ),
      StatCard(
        label: loc.navAgent,
        value: isRunning ? '1' : '0',
        subtitle: isRunning ? '● ${loc.labelAllOnline}' : loc.labelOffline(0),
        accentColor: accent.primaryStart,
      ),
      StatCard(
        label: loc.navCertificates,
        value: '—',
        subtitle: null,
        accentColor: accent.primaryStart,
      ),
      StatCard(
        label: loc.navRelay,
        value: '—',
        subtitle: '● ${loc.statusActive}',
        accentColor: accent.primaryStart,
      ),
    ];

    if (isWide) {
      // 4 columns
      return Row(
        children: cards
            .map(
              (card) => Expanded(
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: AppSpacing.s4),
                  child: card,
                ),
              ),
            )
            .toList(),
      );
    }

    // 2 columns on mobile (2 rows of 2)
    return Column(
      children: [
        Row(
          children: [
            Expanded(child: Padding(
              padding: const EdgeInsets.only(right: AppSpacing.s4),
              child: cards[0],
            )),
            Expanded(child: Padding(
              padding: const EdgeInsets.only(left: AppSpacing.s4),
              child: cards[1],
            )),
          ],
        ),
        const SizedBox(height: AppSpacing.s8),
        Row(
          children: [
            Expanded(child: Padding(
              padding: const EdgeInsets.only(right: AppSpacing.s4),
              child: cards[2],
            )),
            Expanded(child: Padding(
              padding: const EdgeInsets.only(left: AppSpacing.s4),
              child: cards[3],
            )),
          ],
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Local Agent Card
// ---------------------------------------------------------------------------

class _LocalAgentCard extends StatelessWidget {
  const _LocalAgentCard({
    required this.snapshot,
    required this.isRunning,
    required this.isStopped,
    required this.agentLoading,
    required this.agentError,
    required this.accent,
    required this.loc,
    required this.onStart,
    required this.onStop,
    required this.onRestart,
  });

  final LocalAgentRuntimeSnapshot? snapshot;
  final bool isRunning;
  final bool isStopped;
  final bool agentLoading;
  final String agentError;
  final AccentColors accent;
  final AppLocalizations loc;
  final VoidCallback onStart;
  final VoidCallback onStop;
  final VoidCallback onRestart;

  @override
  Widget build(BuildContext context) {
    return GlassCard(
      accentColor: accent.primaryStart,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header row
          Row(
            children: [
              Text(loc.titleLocalAgent, style: AppTypography.title),
              const SizedBox(width: AppSpacing.s8),
              if (agentLoading)
                const SizedBox.square(
                  dimension: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              else if (isRunning)
                GlassChip.success(label: loc.statusRunning, showDot: true)
              else if (isStopped)
                GlassChip.warning(label: loc.statusStopped, showDot: true)
              else
                GlassChip.error(label: loc.statusUnavailable, showDot: true),
            ],
          ),
          const SizedBox(height: AppSpacing.s12),

          // 2x2 Info grid or not-running message
          if (isRunning && snapshot != null)
            InfoGrid(
              cells: [
                InfoCell(label: loc.labelPid, value: '${snapshot!.pid ?? '—'}'),
                InfoCell(label: loc.metaUptime.toUpperCase(), value: loc.statusActive),
                InfoCell(label: loc.metaVersion.toUpperCase(), value: loc.valueAppVersion.replaceAll('v', '')),
                InfoCell(label: loc.metaLastSync.toUpperCase(), value: loc.metaSync30sAgo),
              ],
            )
          else if (isStopped)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: AppSpacing.s8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    loc.descNotRunning,
                    style: AppTypography.body.copyWith(
                      color: AppColors.textMuted,
                    ),
                  ),
                  if (agentError.isNotEmpty) ...[
                    const SizedBox(height: AppSpacing.s4),
                    Text(
                      agentError,
                      style: AppTypography.metadataSmall.copyWith(
                        color: AppColors.error,
                      ),
                    ),
                  ],
                ],
              ),
            ),

          // Action buttons
          if (isRunning) ...[
            const SizedBox(height: AppSpacing.s12),
            Row(
              children: [
                GlassButton.warning(
                  label: loc.btnRestart,
                  onPressed: agentLoading ? null : onRestart,
                ),
                const SizedBox(width: AppSpacing.s8),
                GlassButton.danger(
                  label: loc.btnStop,
                  onPressed: agentLoading ? null : onStop,
                ),
              ],
            ),
          ] else if (isStopped) ...[
            const SizedBox(height: AppSpacing.s12),
            GlassButton.primary(
              label: loc.btnStart,
              onPressed: agentLoading ? null : onStart,
              accentStart: accent.primaryStart,
              accentEnd: accent.primaryEnd,
            ),
          ],
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Quick Actions (2x2 grid of small glass cards)
// ---------------------------------------------------------------------------

class _QuickActions extends StatelessWidget {
  const _QuickActions({required this.caps, required this.accent, required this.loc});

  final PlatformCapabilities caps;
  final AccentColors accent;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final actions = [
      _QuickActionItem(
        emoji: '📏',
        label: loc.actionNewRule,
        accent: accent,
        onTap: () => context.go(RouteNames.rules),
      ),
      if (caps.canManageCertificates)
        _QuickActionItem(
          emoji: '🔒',
          label: loc.actionAddCertificate,
          accent: accent,
          onTap: () => context.go(RouteNames.certificates),
        ),
      _QuickActionItem(
        emoji: '🤖',
        label: loc.actionAddAgent,
        accent: accent,
        onTap: () => context.go(RouteNames.agents),
      ),
      if (caps.canManageRelay)
        _QuickActionItem(
          emoji: '🔗',
          label: loc.actionNewRelay,
          accent: accent,
          onTap: () => context.go(RouteNames.relay),
        ),
    ];

    // Build 2-column layout
    final rows = <Widget>[];
    for (var i = 0; i < actions.length; i += 2) {
      final first = actions[i];
      final second = i + 1 < actions.length ? actions[i + 1] : null;
      rows.add(
        Padding(
          padding: EdgeInsets.only(
            top: i > 0 ? AppSpacing.s8 : 0,
          ),
          child: Row(
            children: [
              Expanded(child: first),
              const SizedBox(width: AppSpacing.s8),
              Expanded(
                child: second ?? const SizedBox.shrink(),
              ),
            ],
          ),
        ),
      );
    }

    return GlassCard(
      accentColor: accent.primaryStart,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(loc.titleQuickActions, style: AppTypography.title),
          const SizedBox(height: AppSpacing.s12),
          ...rows,
        ],
      ),
    );
  }
}

class _QuickActionItem extends StatelessWidget {
  const _QuickActionItem({
    required this.emoji,
    required this.label,
    required this.accent,
    required this.onTap,
  });

  final String emoji;
  final String label;
  final AccentColors accent;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(AppRadius.medium),
          child: BackdropFilter(
            filter: ImageFilter.blur(
              sigmaX: AppBlur.subtle,
              sigmaY: AppBlur.subtle,
            ),
            child: Container(
              padding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.s12,
                vertical: AppSpacing.s10,
              ),
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  colors: [
                    accent.primaryStart.withValues(alpha: 0.1),
                    accent.primaryEnd.withValues(alpha: 0.05),
                  ],
                ),
                borderRadius: BorderRadius.circular(AppRadius.medium),
                border: Border.all(
                  color: accent.primaryStart.withValues(alpha: 0.15),
                ),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(emoji, style: const TextStyle(fontSize: 16)),
                  const SizedBox(width: AppSpacing.s8),
                  Flexible(
                    child: Text(
                      label,
                      style: AppTypography.metadata.copyWith(
                        fontWeight: FontWeight.w500,
                        color: AppColors.textSecondary,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Skeleton / shimmer placeholder for loading state
// ---------------------------------------------------------------------------

class _SkeletonBox extends StatefulWidget {
  const _SkeletonBox({
    required this.height,
    required this.borderRadius,
  });

  final double height;
  final double borderRadius;

  @override
  State<_SkeletonBox> createState() => _SkeletonBoxState();
}

class _SkeletonBoxState extends State<_SkeletonBox>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1200),
    )..repeat();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, child) {
        final value = (_controller.value * 2).clamp(0.0, 1.0);
        final opacity = value < 0.5
            ? 0.03 + (value * 0.06)
            : 0.09 - ((value - 0.5) * 0.06);
        return Container(
          height: widget.height,
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: opacity),
            borderRadius: BorderRadius.circular(widget.borderRadius),
            border: Border.all(color: AppColors.border),
          ),
        );
      },
    );
  }
}
