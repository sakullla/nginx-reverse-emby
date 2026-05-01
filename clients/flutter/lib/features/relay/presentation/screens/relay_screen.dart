import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/components/glass_chip.dart';
import '../../../../core/design/components/glass_search_bar.dart';
import '../../../../core/design/components/glass_toggle.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/network/panel_api_provider.dart';
import '../../../../l10n/app_localizations.dart';
import '../../data/models/relay_models.dart';
import '../providers/relay_provider.dart';

const _relayCertificateSources = ['auto_relay_ca', 'existing_certificate'];
const _relayTrustModes = ['pin_only', 'ca_only', 'pin_or_ca', 'pin_and_ca'];

class RelayScreen extends ConsumerWidget {
  const RelayScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final relayAsync = ref.watch(relayListProvider);
    final filteredRelays = ref.watch(filteredRelayListenersProvider);

    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // -- Filter bar ----
          _FilterBar(
            total: relayAsync.valueOrNull?.length ?? 0,
            loc: AppLocalizations.of(context)!,
          ),
          const SizedBox(height: AppSpacing.s12),

          // -- Content ----
          relayAsync.when(
            data: (_) {
              if (filteredRelays.isEmpty) {
                return _EmptyState(loc: AppLocalizations.of(context)!);
              }
              return _RelayListView(
                relays: filteredRelays,
                loc: AppLocalizations.of(context)!,
              );
            },
            loading: () => const _SkeletonList(),
            error: (err, _) =>
                _ErrorState(error: err, loc: AppLocalizations.of(context)!),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Filter bar: search + protocol filter
// ---------------------------------------------------------------------------

class _FilterBar extends ConsumerWidget {
  const _FilterBar({required this.total, required this.loc});

  final int total;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        Expanded(
          child: Material(
            color: Colors.transparent,
            child: GlassSearchBar(
              hint: loc.hintSearchRelays,
              onChanged: (query) {
                ref.read(relaySearchQueryProvider.notifier).update(query);
              },
            ),
          ),
        ),
        const SizedBox(width: AppSpacing.s8),

        Material(
          color: Colors.transparent,
          child: _ProtocolFilterDropdown(
            value: ref.watch(relayProtocolFilterNotifierProvider),
            loc: loc,
            onChanged: (v) {
              if (v != null) {
                ref
                    .read(relayProtocolFilterNotifierProvider.notifier)
                    .update(v);
              }
            },
          ),
        ),

        const SizedBox(width: AppSpacing.s12),
        GlassButton.primary(
          label: loc.btnNew,
          onPressed: () => showRelayFormDialog(context, ref),
        ),
        const SizedBox(width: AppSpacing.s12),

        Text(
          loc.labelRelayCount(total, total == 1 ? '' : 's'),
          style: AppTypography.metadata.copyWith(color: AppColors.textMuted),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Protocol filter dropdown
// ---------------------------------------------------------------------------

class _ProtocolFilterDropdown extends StatelessWidget {
  const _ProtocolFilterDropdown({
    required this.value,
    required this.loc,
    required this.onChanged,
  });

  final RelayProtocolFilter value;
  final AppLocalizations loc;
  final ValueChanged<RelayProtocolFilter?> onChanged;

  String _label(RelayProtocolFilter f) {
    switch (f) {
      case RelayProtocolFilter.all:
        return loc.filterAllProtocols;
      case RelayProtocolFilter.tcp:
        return 'TCP';
      case RelayProtocolFilter.udp:
        return 'UDP';
      case RelayProtocolFilter.tls:
        return 'TLS';
    }
  }

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: AppBlur.subtle,
          sigmaY: AppBlur.subtle,
        ),
        child: Container(
          height: 36,
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.s8),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<RelayProtocolFilter>(
              value: value,
              items: RelayProtocolFilter.values
                  .map(
                    (f) => DropdownMenuItem(
                      value: f,
                      child: Text(
                        _label(f),
                        style: AppTypography.metadata.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    ),
                  )
                  .toList(),
              onChanged: onChanged,
              dropdownColor: const Color(0xFF1E293B),
              icon: Icon(
                Icons.unfold_more,
                size: 14,
                color: AppColors.textMuted,
              ),
              style: AppTypography.metadata.copyWith(
                color: AppColors.textSecondary,
              ),
              isDense: true,
              underline: const SizedBox.shrink(),
            ),
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Relay list view
// ---------------------------------------------------------------------------

class _RelayListView extends ConsumerWidget {
  const _RelayListView({required this.relays, required this.loc});

  final List<RelayListener> relays;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      children: relays
          .map(
            (relay) => Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.s8),
              child: _RelayCard(relay: relay, loc: loc),
            ),
          )
          .toList(),
    );
  }
}

// ---------------------------------------------------------------------------
// Single relay card
// ---------------------------------------------------------------------------

class _RelayCard extends ConsumerWidget {
  const _RelayCard({required this.relay, required this.loc});

  final RelayListener relay;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final protoColor = _protocolColor(relay.protocol);
    final protoIcon = _protocolIcon(relay.protocol);

    Widget card = GlassCard(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.s16,
        vertical: AppSpacing.s12,
      ),
      child: Row(
        children: [
          // -- Left: protocol icon ----
          Container(
            width: 36,
            height: 36,
            decoration: BoxDecoration(
              color: protoColor.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(AppRadius.medium),
              border: Border.all(color: protoColor.withValues(alpha: 0.2)),
            ),
            child: Icon(protoIcon, size: 18, color: protoColor),
          ),
          const SizedBox(width: AppSpacing.s12),

          // -- Center: listen address + agent ----
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Address + protocol badge
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        relay.listenAddress,
                        style: AppTypography.bodyMedium.copyWith(
                          color: AppColors.textPrimary,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: AppSpacing.s8),
                    GlassChip.accent(
                      label: relay.protocol.toUpperCase(),
                      accentColor: protoColor,
                    ),
                  ],
                ),
                const SizedBox(height: 2),
                // Agent info
                Text(
                  relay.agentName != null && relay.agentName!.isNotEmpty
                      ? loc.labelAgent(relay.agentName!)
                      : loc.descNoAgentAssigned,
                  style: AppTypography.metadataSmall.copyWith(
                    color: AppColors.textMuted,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ),
          ),
          const SizedBox(width: AppSpacing.s8),

          // -- Right: status + toggle + menu ----
          if (relay.enabled)
            GlassChip.success(label: loc.statusActive, showDot: true)
          else
            GlassChip(label: loc.statusDisabled, color: AppColors.textMuted),
          const SizedBox(width: AppSpacing.s8),

          GlassToggle(
            value: relay.enabled,
            onChanged: (v) => _runRelayAction(
              context,
              () =>
                  ref.read(relayListProvider.notifier).toggleRelay(relay.id, v),
            ),
          ),
          const SizedBox(width: AppSpacing.s4),

          _RelayMenu(relay: relay),
        ],
      ),
    );

    // Dim disabled relays
    if (!relay.enabled) {
      card = Opacity(opacity: 0.6, child: card);
    }

    return card;
  }

  Color _protocolColor(String protocol) {
    switch (protocol.toUpperCase()) {
      case 'TCP':
        return const Color(0xFF22D3EE); // cyan
      case 'UDP':
        return const Color(0xFFFBBF24); // amber
      case 'TLS':
        return const Color(0xFF818CF8); // indigo
      default:
        return AppColors.info;
    }
  }

  IconData _protocolIcon(String protocol) {
    switch (protocol.toUpperCase()) {
      case 'TCP':
        return Icons.settings_ethernet;
      case 'UDP':
        return Icons.swap_horiz;
      case 'TLS':
        return Icons.lock_outline;
      default:
        return Icons.sync_alt;
    }
  }
}

// ---------------------------------------------------------------------------
// Popup menu (Edit / Delete)
// ---------------------------------------------------------------------------

class _RelayMenu extends ConsumerWidget {
  const _RelayMenu({required this.relay});

  final RelayListener relay;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final loc = AppLocalizations.of(context)!;
    return PopupMenuButton<String>(
      icon: Icon(Icons.more_horiz, size: 18, color: AppColors.textMuted),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppRadius.medium),
      ),
      color: const Color(0xFF1E293B),
      onSelected: (action) => _handleAction(context, ref, action),
      itemBuilder: (context) => [
        PopupMenuItem(
          value: 'edit',
          height: 36,
          child: Row(
            children: [
              const Icon(
                Icons.edit_outlined,
                size: 16,
                color: AppColors.textSecondary,
              ),
              const SizedBox(width: AppSpacing.s8),
              Text(
                'Edit',
                style: const TextStyle(
                  fontSize: 12,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
        ),
        PopupMenuItem(
          value: 'delete',
          height: 36,
          child: Row(
            children: [
              const Icon(
                Icons.delete_outline,
                size: 16,
                color: AppColors.error,
              ),
              const SizedBox(width: AppSpacing.s8),
              Text(
                loc.btnDelete,
                style: const TextStyle(fontSize: 12, color: AppColors.error),
              ),
            ],
          ),
        ),
      ],
    );
  }

  void _handleAction(BuildContext context, WidgetRef ref, String action) {
    switch (action) {
      case 'edit':
        showRelayFormDialog(context, ref, existing: relay);
        break;
      case 'delete':
        _confirmDelete(context, ref);
        break;
    }
  }

  void _confirmDelete(BuildContext context, WidgetRef ref) {
    showDialog(
      context: context,
      barrierColor: Colors.black.withValues(alpha: 0.5),
      builder: (ctx) => _DeleteConfirmDialog(
        relay: relay,
        onConfirm: () async {
          final success = await _runRelayAction(
            context,
            () => ref.read(relayListProvider.notifier).deleteRelay(relay.id),
          );
          if (success && ctx.mounted) {
            Navigator.of(ctx).pop();
          }
        },
      ),
    );
  }
}

Future<void> showRelayFormDialog(
  BuildContext context,
  WidgetRef ref, {
  RelayListener? existing,
}) {
  final nameController = TextEditingController(text: existing?.name ?? '');
  final portController = TextEditingController(
    text: existing?.listenPort?.toString() ?? '',
  );
  final hostsController = TextEditingController(
    text: existing?.bindHosts.join(', ') ?? '',
  );
  var certificateSource =
      _relayCertificateSources.contains(existing?.certificateSource)
      ? existing!.certificateSource!
      : 'auto_relay_ca';
  var trustMode = _relayTrustModes.contains(existing?.tlsMode)
      ? existing!.tlsMode!
      : 'pin_or_ca';
  var enabled = existing?.enabled ?? true;

  return showDialog<void>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: const Text('Relay listener'),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                decoration: const InputDecoration(labelText: 'Name'),
              ),
              TextField(
                controller: portController,
                decoration: const InputDecoration(labelText: 'Listen port'),
                keyboardType: TextInputType.number,
              ),
              TextField(
                controller: hostsController,
                decoration: const InputDecoration(labelText: 'Bind hosts'),
              ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Enabled'),
                value: enabled,
                onChanged: (value) => setState(() => enabled = value),
              ),
              DropdownButtonFormField<String>(
                initialValue: certificateSource,
                decoration: const InputDecoration(
                  labelText: 'Certificate source',
                ),
                items: _relayCertificateSources
                    .map(
                      (value) =>
                          DropdownMenuItem(value: value, child: Text(value)),
                    )
                    .toList(),
                onChanged: (value) {
                  if (value != null) {
                    setState(() => certificateSource = value);
                  }
                },
              ),
              DropdownButtonFormField<String>(
                initialValue: trustMode,
                decoration: const InputDecoration(labelText: 'Trust mode'),
                items: _relayTrustModes
                    .map(
                      (value) =>
                          DropdownMenuItem(value: value, child: Text(value)),
                    )
                    .toList(),
                onChanged: (value) {
                  if (value != null) {
                    setState(() => trustMode = value);
                  }
                },
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () async {
              final listenPort = int.tryParse(portController.text.trim());
              if (listenPort == null) return;
              final bindHosts = hostsController.text
                  .split(',')
                  .map((host) => host.trim())
                  .where((host) => host.isNotEmpty)
                  .toList();
              if (existing == null) {
                final agentId = ref.read(selectedAgentIdProvider);
                final success = await _runRelayAction(
                  context,
                  () => ref
                      .read(relayListProvider.notifier)
                      .createRelay(
                        CreateRelayListenerRequest(
                          agentId: agentId,
                          name: nameController.text.trim(),
                          listenPort: listenPort,
                          bindHosts: bindHosts,
                          enabled: enabled,
                          certificateSource: certificateSource,
                          tlsMode: trustMode,
                        ),
                      ),
                );
                if (!success) return;
              } else {
                final success = await _runRelayAction(
                  context,
                  () => ref
                      .read(relayListProvider.notifier)
                      .updateRelay(
                        existing.id,
                        UpdateRelayListenerRequest(
                          name: nameController.text.trim(),
                          listenPort: listenPort,
                          bindHosts: bindHosts,
                          enabled: enabled,
                          certificateSource: certificateSource,
                          tlsMode: trustMode,
                        ),
                      ),
                );
                if (!success) return;
              }
              if (ctx.mounted) {
                Navigator.of(ctx).pop();
              }
            },
            child: const Text('Save'),
          ),
        ],
      ),
    ),
  );
}

// ---------------------------------------------------------------------------
// Delete confirmation dialog
// ---------------------------------------------------------------------------

class _DeleteConfirmDialog extends StatelessWidget {
  const _DeleteConfirmDialog({required this.relay, required this.onConfirm});

  final RelayListener relay;
  final VoidCallback onConfirm;

  @override
  Widget build(BuildContext context) {
    final loc = AppLocalizations.of(context)!;
    return Center(
      child: ClipRRect(
        borderRadius: BorderRadius.circular(AppRadius.largeCard),
        child: BackdropFilter(
          filter: ImageFilter.blur(
            sigmaX: AppBlur.heavy,
            sigmaY: AppBlur.heavy,
          ),
          child: Container(
            width: 380,
            padding: const EdgeInsets.all(AppSpacing.s20),
            decoration: BoxDecoration(
              color: const Color(0xFF1E293B).withValues(alpha: 0.95),
              borderRadius: BorderRadius.circular(AppRadius.largeCard),
              border: Border.all(color: AppColors.border),
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  loc.titleDeleteRelay,
                  style: AppTypography.title.copyWith(
                    color: AppColors.textPrimary,
                    fontSize: 16,
                  ),
                ),
                const SizedBox(height: AppSpacing.s12),
                Text(
                  loc.descDeleteRelayConfirm(
                    relay.listenAddress,
                    relay.protocol.toUpperCase(),
                  ),
                  style: AppTypography.body.copyWith(
                    color: AppColors.textSecondary,
                  ),
                ),
                const SizedBox(height: AppSpacing.s20),
                Row(
                  mainAxisAlignment: MainAxisAlignment.end,
                  children: [
                    GlassButton.secondary(
                      label: loc.btnCancel,
                      onPressed: () => Navigator.of(context).pop(),
                    ),
                    const SizedBox(width: AppSpacing.s8),
                    GlassButton.danger(
                      label: loc.btnDelete,
                      onPressed: onConfirm,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

Future<bool> _runRelayAction(
  BuildContext context,
  Future<Object?> Function() action,
) async {
  try {
    await action();
    return true;
  } catch (error) {
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(error.toString()),
          backgroundColor: AppColors.error,
        ),
      );
    }
    return false;
  }
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.loc});

  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 80),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.sync_alt,
              size: 48,
              color: AppColors.textMuted.withValues(alpha: 0.5),
            ),
            const SizedBox(height: AppSpacing.s12),
            Text(
              loc.titleNoRelayListeners,
              style: AppTypography.title.copyWith(color: AppColors.textMuted),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              loc.descRelayListenersAppearHere,
              style: AppTypography.metadata.copyWith(
                color: AppColors.textMuted.withValues(alpha: 0.7),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Error state
// ---------------------------------------------------------------------------

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.error, required this.loc});

  final Object error;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 60),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, size: 48, color: AppColors.error),
            const SizedBox(height: AppSpacing.s12),
            Text(
              loc.failedToLoadRelays,
              style: AppTypography.title.copyWith(color: AppColors.textPrimary),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              error.toString(),
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

// ---------------------------------------------------------------------------
// Skeleton / shimmer placeholder for loading state
// ---------------------------------------------------------------------------

class _SkeletonList extends StatefulWidget {
  const _SkeletonList();

  @override
  State<_SkeletonList> createState() => _SkeletonListState();
}

class _SkeletonListState extends State<_SkeletonList>
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
    return Column(
      children: List.generate(
        4,
        (_) => Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.s8),
          child: _SkeletonCard(controller: _controller),
        ),
      ),
    );
  }
}

class _SkeletonCard extends StatelessWidget {
  const _SkeletonCard({required this.controller});

  final AnimationController controller;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, child) {
        final value = (controller.value * 2).clamp(0.0, 1.0);
        final opacity = value < 0.5
            ? 0.03 + (value * 0.06)
            : 0.09 - ((value - 0.5) * 0.06);
        return Container(
          height: 60,
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s16,
            vertical: AppSpacing.s12,
          ),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: opacity),
            borderRadius: BorderRadius.circular(AppRadius.card),
            border: Border.all(color: AppColors.border),
          ),
          child: Row(
            children: [
              // Icon placeholder
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(
                  color: Colors.white.withValues(alpha: 0.05),
                  borderRadius: BorderRadius.circular(AppRadius.medium),
                ),
              ),
              const SizedBox(width: AppSpacing.s12),
              // Text placeholders
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Container(
                      height: 10,
                      width: 140,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.06),
                        borderRadius: BorderRadius.circular(4),
                      ),
                    ),
                    const SizedBox(height: 6),
                    Container(
                      height: 8,
                      width: 100,
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
