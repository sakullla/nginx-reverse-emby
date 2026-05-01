import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/client_state.dart' as runtime_state;
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/components/glass_chip.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../l10n/app_localizations.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../../../services/local_agent_controller.dart';
import '../../../../services/local_agent_controller_provider.dart';

class AgentsScreen extends ConsumerWidget {
  const AgentsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final caps = PlatformCapabilities.current;
    final loc = AppLocalizations.of(context)!;

    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // -- Local Agent Section --
          if (caps.canManageLocalAgent) ...[
            _SectionHeader(title: loc.titleLocalAgent),
            const SizedBox(height: AppSpacing.s12),
            const _LocalAgentCard(),
            const SizedBox(height: AppSpacing.s20),
          ],

          // -- Remote Agents Section --
          _SectionHeader(
            title: loc.titleRemoteAgents,
            trailing: GlassChip(
              label: loc.labelRegisteredCount(0),
              color: AppColors.textMuted,
            ),
          ),
          const SizedBox(height: AppSpacing.s12),
          const _RemoteAgentsEmptyState(),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Section header: uppercase label + optional trailing widget + divider
// ---------------------------------------------------------------------------

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.title,
    this.trailing,
  });

  final String title;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Text(
          title.toUpperCase(),
          style: AppTypography.label.copyWith(color: AppColors.textMuted),
        ),
        const SizedBox(width: AppSpacing.s8),
        const Expanded(
          child: Divider(
            color: AppColors.border,
            thickness: 1,
            height: 1,
          ),
        ),
        if (trailing != null) ...[
          const SizedBox(width: AppSpacing.s8),
          trailing!,
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Local agent card — highlighted gradient glass card with process control
// ---------------------------------------------------------------------------

class _LocalAgentCard extends ConsumerStatefulWidget {
  const _LocalAgentCard();

  @override
  ConsumerState<_LocalAgentCard> createState() => _LocalAgentCardState();
}

class _LocalAgentCardState extends ConsumerState<_LocalAgentCard> {
  LocalAgentRuntimeSnapshot? _snapshot;
  var _loading = true;
  String _error = '';

  @override
  void initState() {
    super.initState();
    Future.microtask(_refresh);
  }

  Future<void> _refresh() async {
    final profile = _profile();
    if (profile == null) {
      setState(() {
        _loading = false;
        _snapshot = null;
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final snapshot = await ref
          .read(localAgentControllerProvider)
          .status(profile);
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _loading = false;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _error = error.toString();
          _loading = false;
        });
      }
    }
  }

  Future<void> _start() async {
    await _run((controller, profile) => controller.start(profile));
  }

  Future<void> _stop() async {
    await _run((controller, profile) => controller.stop(profile));
  }

  Future<void> _restart() async {
    final profile = _profile();
    if (profile == null) return;
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final controller = ref.read(localAgentControllerProvider);
      if (_snapshot?.canStop == true) {
        await controller.stop(profile);
      }
      final snapshot = await controller.start(profile);
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _loading = false;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _error = error.toString();
          _loading = false;
        });
      }
    }
  }

  Future<void> _run(
    Future<LocalAgentRuntimeSnapshot> Function(
      LocalAgentController controller,
      runtime_state.ClientProfile profile,
    )
    action,
  ) async {
    final profile = _profile();
    if (profile == null) return;
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final snapshot = await action(
        ref.read(localAgentControllerProvider),
        profile,
      );
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _loading = false;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _error = error.toString();
          _loading = false;
        });
      }
    }
  }

  runtime_state.ClientProfile? _profile() {
    final auth = ref.read(authNotifierProvider).value;
    if (auth is! AuthStateAuthenticated) return null;
    final profile = auth.profile;
    return runtime_state.ClientProfile(
      masterUrl: profile.masterUrl,
      displayName: profile.displayName,
      agentId: profile.agentId,
      token: profile.token,
    );
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authNotifierProvider);
    final loc = AppLocalizations.of(context)!;
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    // Auto-refresh when profile becomes available without a snapshot
    if (auth.value is AuthStateAuthenticated &&
        !_loading &&
        snapshot == null &&
        _error.isEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _refresh();
      });
    }

    if (_loading && snapshot == null) {
      return const _LocalAgentSkeleton();
    }

    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.card),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: AppBlur.standard,
          sigmaY: AppBlur.standard,
        ),
        child: Container(
          padding: const EdgeInsets.all(AppSpacing.s16),
          decoration: BoxDecoration(
            gradient: LinearGradient(
              colors: [
                AppColors.info.withValues(alpha: 0.08),
                AppColors.info.withValues(alpha: 0.02),
              ],
              begin: Alignment.centerLeft,
              end: Alignment.centerRight,
            ),
            borderRadius: BorderRadius.circular(AppRadius.card),
            border: Border.all(
              color: AppColors.info.withValues(alpha: 0.15),
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // -- Header row --
              Row(
                children: [
                  // Gradient icon
                  Container(
                    width: 44,
                    height: 44,
                    decoration: BoxDecoration(
                      gradient: const LinearGradient(
                        colors: [AppColors.info, Color(0xFF8B5CF6)],
                      ),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: const Center(
                      child: Text(
                        '🖥️',
                        style: TextStyle(fontSize: 22),
                      ),
                    ),
                  ),
                  const SizedBox(width: AppSpacing.s12),

                  // Center info
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        // Title + status chip
                        Row(
                          children: [
                            Text(
                              loc.navAgent,
                              style: AppTypography.title.copyWith(
                                color: AppColors.textPrimary,
                              ),
                            ),
                            const SizedBox(width: AppSpacing.s8),
                            if (_loading)
                              const SizedBox.square(
                                dimension: 14,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: AppColors.textMuted,
                                ),
                              )
                            else
                              _buildStatusChip(context, status),
                          ],
                        ),
                        const SizedBox(height: 4),

                        // Metadata row
                        _buildMetadataRow(context, snapshot),
                      ],
                    ),
                  ),

                  const SizedBox(width: AppSpacing.s12),

                  // Action buttons
                  if (_loading && snapshot != null)
                    const SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppColors.info,
                      ),
                    )
                  else
                    _buildActionButtons(context, isRunning, isStopped, status),
                ],
              ),

              // Error message
              if (_error.isNotEmpty) ...[
                const SizedBox(height: AppSpacing.s12),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.s10,
                    vertical: AppSpacing.s8,
                  ),
                  decoration: BoxDecoration(
                    color: AppColors.error.withValues(alpha: 0.08),
                    borderRadius: BorderRadius.circular(AppRadius.medium),
                    border: Border.all(
                      color: AppColors.error.withValues(alpha: 0.15),
                    ),
                  ),
                  child: Row(
                    children: [
                      const Icon(
                        Icons.error_outline,
                        size: 14,
                        color: AppColors.error,
                      ),
                      const SizedBox(width: AppSpacing.s8),
                      Expanded(
                        child: Text(
                          _error,
                          style: AppTypography.metadataSmall.copyWith(
                            color: AppColors.error,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildStatusChip(BuildContext context, LocalAgentControllerStatus? status) {
    final loc = AppLocalizations.of(context)!;
    return switch (status) {
      LocalAgentControllerStatus.running => GlassChip.success(
          label: loc.statusRunning,
          showDot: true,
        ),
      LocalAgentControllerStatus.stopped => GlassChip.warning(
          label: loc.statusStopped,
        ),
      LocalAgentControllerStatus.unavailable => GlassChip.error(
          label: loc.statusUnavailable,
        ),
      null => GlassChip(
          label: loc.statusUnknown,
          color: AppColors.textMuted,
        ),
    };
  }

  Widget _buildMetadataRow(BuildContext context, LocalAgentRuntimeSnapshot? snapshot) {
    final loc = AppLocalizations.of(context)!;
    final items = <String>[
      '${loc.labelPid}: ${snapshot?.pid?.toString() ?? '—'}',
      '${loc.metaUptime}: —',
      '${loc.metaVersion}: —',
      '${loc.metaLastSync}: —',
    ];

    return Text(
      items.join('  ·  '),
      style: AppTypography.metadataSmall.copyWith(
        color: AppColors.textMuted,
      ),
      overflow: TextOverflow.ellipsis,
    );
  }

  Widget _buildActionButtons(
    BuildContext context,
    bool isRunning,
    bool isStopped,
    LocalAgentControllerStatus? status,
  ) {
    final loc = AppLocalizations.of(context)!;
    if (status == LocalAgentControllerStatus.unavailable) {
      return Text(
        loc.descNotAvailable,
        style: AppTypography.metadata.copyWith(
          color: AppColors.textMuted,
        ),
      );
    }

    if (isRunning) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          GlassButton.warning(
            label: loc.btnRestart,
            onPressed: _loading ? null : _restart,
          ),
          const SizedBox(width: AppSpacing.s8),
          GlassButton.danger(
            label: loc.btnStop,
            onPressed: _loading ? null : _stop,
          ),
        ],
      );
    }

    if (isStopped) {
      return GlassButton.primary(
        label: loc.btnStart,
        onPressed: _loading ? null : _start,
      );
    }

    return const SizedBox.shrink();
  }
}

// ---------------------------------------------------------------------------
// Skeleton loading state for local agent
// ---------------------------------------------------------------------------

class _LocalAgentSkeleton extends StatefulWidget {
  const _LocalAgentSkeleton();

  @override
  State<_LocalAgentSkeleton> createState() => _LocalAgentSkeletonState();
}

class _LocalAgentSkeletonState extends State<_LocalAgentSkeleton>
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
        final value = (controller: _controller, value: _controller.value);
        final pulse = value.value < 0.5
            ? 0.03 + (value.value * 0.06)
            : 0.09 - ((value.value - 0.5) * 0.06);
        return Container(
          height: 100,
          padding: const EdgeInsets.all(AppSpacing.s16),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: pulse),
            borderRadius: BorderRadius.circular(AppRadius.card),
            border: Border.all(color: AppColors.border),
          ),
          child: Row(
            children: [
              // Icon placeholder
              Container(
                width: 44,
                height: 44,
                decoration: BoxDecoration(
                  color: Colors.white.withValues(alpha: 0.05),
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              const SizedBox(width: AppSpacing.s12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Container(
                      height: 12,
                      width: 140,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.06),
                        borderRadius: BorderRadius.circular(4),
                      ),
                    ),
                    const SizedBox(height: 8),
                    Container(
                      height: 8,
                      width: 220,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.04),
                        borderRadius: BorderRadius.circular(4),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

// ---------------------------------------------------------------------------
// Remote agents empty state
// ---------------------------------------------------------------------------

class _RemoteAgentsEmptyState extends StatelessWidget {
  const _RemoteAgentsEmptyState();

  @override
  Widget build(BuildContext context) {
    final loc = AppLocalizations.of(context)!;
    return GlassCard(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 48),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 56,
              height: 56,
              decoration: BoxDecoration(
                color: AppColors.info.withValues(alpha: 0.08),
                shape: BoxShape.circle,
                border: Border.all(
                  color: AppColors.info.withValues(alpha: 0.15),
                ),
              ),
              child: const Icon(
                Icons.devices_outlined,
                size: 28,
                color: AppColors.textMuted,
              ),
            ),
            const SizedBox(height: AppSpacing.s16),
            Text(
              loc.titleNoRemoteAgents,
              style: AppTypography.title.copyWith(
                color: AppColors.textPrimary,
              ),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              loc.descRemoteAgentsAppearHere,
              style: AppTypography.metadata.copyWith(
                color: AppColors.textMuted,
              ),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}
