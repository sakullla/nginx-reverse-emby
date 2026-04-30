import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../shared/widgets/nre_card.dart';
import '../../../../shared/widgets/nre_empty_state.dart';
import '../../../../shared/widgets/nre_error_widget.dart';
import '../../../../shared/widgets/nre_skeleton.dart';
import '../../data/models/rule_models.dart';
import '../providers/rules_provider.dart';

class RulesListScreen extends ConsumerWidget {
  const RulesListScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rulesAsync = ref.watch(rulesListProvider);
    final caps = PlatformCapabilities.current;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Rules'),
        actions: [
          IconButton(
            onPressed: () => ref.read(rulesListProvider.notifier).refresh(),
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: rulesAsync.when(
        data: (rules) => rules.isEmpty
            ? const NreEmptyState(
                icon: Icons.inbox,
                title: 'No Rules',
                message: 'No proxy rules configured yet',
              )
            : ListView.separated(
                padding: const EdgeInsets.all(16),
                itemCount: rules.length,
                separatorBuilder: (_, __) => const SizedBox(height: 12),
                itemBuilder: (_, index) => _RuleCard(
                  rule: rules[index],
                  canEdit: caps.canEditRules,
                ),
              ),
        loading: () => const NreSkeletonList(itemCount: 4),
        error: (err, _) => NreErrorWidget(
          error: err,
          onRetry: () => ref.read(rulesListProvider.notifier).refresh(),
        ),
      ),
      floatingActionButton: caps.canEditRules
          ? FloatingActionButton(
              onPressed: () {},
              child: const Icon(Icons.add),
            )
          : null,
    );
  }
}

class _RuleCard extends ConsumerWidget {
  const _RuleCard({required this.rule, required this.canEdit});

  final ProxyRule rule;
  final bool canEdit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;

    return NreCard(
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  rule.domain,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 4),
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: scheme.surfaceContainerHighest,
                        borderRadius: BorderRadius.circular(6),
                      ),
                      child: Text(
                        'Target: ${rule.target}',
                        style: theme.textTheme.bodySmall,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: scheme.primaryContainer,
                        borderRadius: BorderRadius.circular(6),
                      ),
                      child: Text(
                        rule.type.toUpperCase(),
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: scheme.onPrimaryContainer,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
          Switch(
            value: rule.enabled,
            onChanged: canEdit
                ? (value) => ref.read(rulesListProvider.notifier).toggleRule(rule.id, value)
                : null,
          ),
        ],
      ),
    );
  }
}
