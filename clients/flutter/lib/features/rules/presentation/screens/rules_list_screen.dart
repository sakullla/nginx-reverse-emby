import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/components/glass_chip.dart';
import '../../../../core/design/components/glass_search_bar.dart';
import '../../../../core/design/components/glass_toggle.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../l10n/app_localizations.dart';
import '../../data/models/rule_models.dart';
import '../providers/rules_provider.dart';
import 'rule_form_dialog.dart';

class RulesListScreen extends ConsumerWidget {
  const RulesListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rulesAsync = ref.watch(rulesListProvider);
    final filteredRules = ref.watch(filteredRulesProvider);
    final caps = PlatformCapabilities.current;

    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // -- Search + Filter bar ----
          _FilterBar(caps: caps, loc: AppLocalizations.of(context)!),
          const SizedBox(height: AppSpacing.s12),

          // -- Content ----
          rulesAsync.when(
            data: (_) {
              if (filteredRules.isEmpty) {
                return _EmptyState(loc: AppLocalizations.of(context)!);
              }
              return _RuleListView(rules: filteredRules, caps: caps);
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
// Filter bar: search + status filter + type filter + add button
// ---------------------------------------------------------------------------

class _FilterBar extends ConsumerWidget {
  const _FilterBar({required this.caps, required this.loc});

  final PlatformCapabilities caps;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        // Search bar
        Expanded(
          child: GlassSearchBar(
            hint: loc.hintSearchRules,
            onChanged: (query) {
              ref.read(rulesSearchQueryProvider.notifier).update(query);
            },
          ),
        ),
        const SizedBox(width: AppSpacing.s8),

        // Status filter
        _FilterDropdown<RuleStatusFilter>(
          value: ref.watch(rulesStatusFilterProvider),
          items: RuleStatusFilter.values,
          labelBuilder: (v) => v == RuleStatusFilter.all
              ? loc.filterStatus
              : v.name[0].toUpperCase() + v.name.substring(1),
          onChanged: (v) {
            if (v != null) {
              ref.read(rulesStatusFilterProvider.notifier).update(v);
            }
          },
        ),
        const SizedBox(width: AppSpacing.s8),

        // Type filter
        _FilterDropdown<RuleTypeFilter>(
          value: ref.watch(rulesTypeFilterProvider),
          items: RuleTypeFilter.values,
          labelBuilder: (v) =>
              v == RuleTypeFilter.all ? loc.filterType : v.name.toUpperCase(),
          onChanged: (v) {
            if (v != null) {
              ref.read(rulesTypeFilterProvider.notifier).update(v);
            }
          },
        ),

        // Add button
        if (caps.canEditRules) ...[
          const SizedBox(width: AppSpacing.s8),
          GlassButton.primary(
            label: loc.btnNew,
            onPressed: () => showRuleFormDialog(context),
          ),
        ],
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Glass-styled filter dropdown button
// ---------------------------------------------------------------------------

class _FilterDropdown<T> extends StatelessWidget {
  const _FilterDropdown({
    required this.value,
    required this.items,
    required this.labelBuilder,
    required this.onChanged,
  });

  final T value;
  final List<T> items;
  final String Function(T) labelBuilder;
  final ValueChanged<T?> onChanged;

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
            child: DropdownButton<T>(
              value: value,
              items: items
                  .map(
                    (item) => DropdownMenuItem(
                      value: item,
                      child: Text(
                        labelBuilder(item),
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
// Rule list view
// ---------------------------------------------------------------------------

class _RuleListView extends ConsumerWidget {
  const _RuleListView({required this.rules, required this.caps});

  final List<HttpProxyRule> rules;
  final PlatformCapabilities caps;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Column(
      children: rules
          .map(
            (rule) => Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.s8),
              child: _RuleCard(rule: rule, canEdit: caps.canEditRules),
            ),
          )
          .toList(),
    );
  }
}

// ---------------------------------------------------------------------------
// Single rule card
// ---------------------------------------------------------------------------

class _RuleCard extends ConsumerWidget {
  const _RuleCard({required this.rule, required this.canEdit});

  final HttpProxyRule rule;
  final bool canEdit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final loc = AppLocalizations.of(context)!;
    final ruleType = _ruleType(rule);
    final typeColor = _typeColor(ruleType);
    final typeIcon = _typeIcon(ruleType);

    Widget card = GlassCard(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.s16,
        vertical: AppSpacing.s12,
      ),
      child: Row(
        children: [
          // -- Left: type icon ----
          Container(
            width: 36,
            height: 36,
            decoration: BoxDecoration(
              color: typeColor.withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(AppRadius.medium),
              border: Border.all(color: typeColor.withValues(alpha: 0.2)),
            ),
            child: Icon(typeIcon, size: 18, color: typeColor),
          ),
          const SizedBox(width: AppSpacing.s12),

          // -- Center: domain + target ----
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Domain + type badge
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        rule.frontendUrl,
                        style: AppTypography.bodyMedium.copyWith(
                          color: AppColors.textPrimary,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: AppSpacing.s8),
                    GlassChip.accent(
                      label: ruleType.toUpperCase(),
                      accentColor: typeColor,
                    ),
                  ],
                ),
                const SizedBox(height: 2),
                // Target + updated text
                Text(
                  '-> ${rule.backendUrl}',
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
          if (rule.enabled)
            GlassChip.success(label: loc.statusActive, showDot: true)
          else
            GlassChip(label: loc.statusDisabled, color: AppColors.textMuted),
          const SizedBox(width: AppSpacing.s8),

          GlassToggle(
            value: rule.enabled,
            onChanged: canEdit
                ? (v) => ref
                      .read(rulesListProvider.notifier)
                      .toggleRule(rule.id, v)
                : null,
          ),
          if (canEdit) ...[
            const SizedBox(width: AppSpacing.s4),
            _RuleMenu(rule: rule),
          ],
        ],
      ),
    );

    // Dim disabled rules
    if (!rule.enabled) {
      card = Opacity(opacity: 0.6, child: card);
    }

    return card;
  }

  Color _typeColor(String type) {
    switch (type.toLowerCase()) {
      case 'http':
        return const Color(0xFF22D3EE); // cyan
      case 'https':
        return const Color(0xFF818CF8); // accent (indigo)
      case 'l4':
        return const Color(0xFFA78BFA); // purple
      default:
        return AppColors.info;
    }
  }

  IconData _typeIcon(String type) {
    switch (type.toLowerCase()) {
      case 'http':
        return Icons.language;
      case 'https':
        return Icons.lock_outline;
      case 'l4':
        return Icons.settings_ethernet;
      default:
        return Icons.swap_horiz;
    }
  }

  String _ruleType(HttpProxyRule rule) =>
      rule.frontendUrl.toLowerCase().startsWith('https://') ? 'https' : 'http';
}

// ---------------------------------------------------------------------------
// Popup menu (Edit / Copy / Delete)
// ---------------------------------------------------------------------------

class _RuleMenu extends ConsumerWidget {
  const _RuleMenu({required this.rule});

  final HttpProxyRule rule;

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
                loc.btnViewDetails,
                style: const TextStyle(
                  fontSize: 12,
                  color: AppColors.textPrimary,
                ),
              ),
            ],
          ),
        ),
        PopupMenuItem(
          value: 'copy',
          height: 36,
          child: Row(
            children: [
              const Icon(Icons.copy, size: 16, color: AppColors.textSecondary),
              const SizedBox(width: AppSpacing.s8),
              Text(
                loc.btnCopy,
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
    final loc = AppLocalizations.of(context)!;
    switch (action) {
      case 'edit':
        showRuleFormDialog(
          context,
          mode: RuleFormMode.edit,
          existingRule: rule,
        );
        break;
      case 'copy':
        Clipboard.setData(
          ClipboardData(text: '${rule.frontendUrl} -> ${rule.backendUrl}'),
        );
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(loc.msgRuleCopiedToClipboard),
            duration: const Duration(seconds: 2),
          ),
        );
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
        rule: rule,
        onConfirm: () {
          ref.read(rulesListProvider.notifier).deleteRule(rule.id);
          Navigator.of(ctx).pop();
        },
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Delete confirmation dialog
// ---------------------------------------------------------------------------

class _DeleteConfirmDialog extends StatelessWidget {
  const _DeleteConfirmDialog({required this.rule, required this.onConfirm});

  final HttpProxyRule rule;
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
                  loc.titleDeleteRule,
                  style: AppTypography.title.copyWith(
                    color: AppColors.textPrimary,
                    fontSize: 16,
                  ),
                ),
                const SizedBox(height: AppSpacing.s12),
                Text(
                  loc.descDeleteRuleConfirm(rule.frontendUrl),
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
              Icons.inbox_outlined,
              size: 48,
              color: AppColors.textMuted.withValues(alpha: 0.5),
            ),
            const SizedBox(height: AppSpacing.s12),
            Text(
              loc.titleNoRules,
              style: AppTypography.title.copyWith(color: AppColors.textMuted),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              loc.descCreateFirstRule,
              style: AppTypography.metadata.copyWith(
                color: AppColors.textMuted.withValues(alpha: 0.7),
              ),
            ),
            const SizedBox(height: AppSpacing.s16),
            GlassButton.primary(
              label: loc.btnCreateRule,
              onPressed: () => showRuleFormDialog(context),
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
              loc.failedToLoadRules,
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
                      width: 160,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.06),
                        borderRadius: BorderRadius.circular(4),
                      ),
                    ),
                    const SizedBox(height: 6),
                    Container(
                      height: 8,
                      width: 120,
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
