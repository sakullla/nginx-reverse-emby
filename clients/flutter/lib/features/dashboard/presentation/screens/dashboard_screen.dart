import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../core/routing/route_names.dart';
import '../../../../shared/widgets/nre_card.dart';
import '../../../../shared/widgets/nre_empty_state.dart';
import '../../../../shared/widgets/nre_status_chip.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';

class DashboardScreen extends ConsumerWidget {
  const DashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final authAsync = ref.watch(authNotifierProvider);
    final caps = PlatformCapabilities.current;

    return Scaffold(
      appBar: AppBar(title: const Text('Dashboard')),
      body: authAsync.when(
        data: (state) {
          if (state is AuthStateAuthenticated) {
            return _buildDashboard(context, state.profile, caps, scheme, theme);
          }
          return NreEmptyState(
            icon: Icons.cloud_off,
            title: 'Not Connected',
            message: 'Please connect to a Master server first',
            action: FilledButton(
              onPressed: () => context.go(RouteNames.connect),
              child: const Text('Connect'),
            ),
          );
        },
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, __) => const Center(child: Text('Error')),
      ),
    );
  }

  Widget _buildDashboard(BuildContext context, ClientProfile profile,
      PlatformCapabilities caps, ColorScheme scheme, ThemeData theme) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _StatCard(
          icon: Icons.check_circle,
          iconColor: Colors.green,
          label: 'Connection Status',
          value: 'Connected',
          scheme: scheme,
        ),
        const SizedBox(height: 12),
        _StatCard(
          icon: Icons.rule,
          iconColor: scheme.primary,
          label: 'Total Rules',
          value: '—',
          scheme: scheme,
        ),
        const SizedBox(height: 12),
        _StatCard(
          icon: Icons.memory,
          iconColor: scheme.secondary,
          label: 'Agents Online',
          value: '—',
          scheme: scheme,
        ),
        if (caps.canManageLocalAgent) ...[
          const SizedBox(height: 16),
          NreCard(
            hasAccentBar: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.play_circle, color: scheme.primary),
                    const SizedBox(width: 8),
                    Text('Local Agent', style: theme.textTheme.titleMedium),
                    const Spacer(),
                    const NreStatusChip(label: 'Stopped', type: StatusType.warning),
                  ],
                ),
                const Divider(),
                const Text('PID: —'),
                const Text('Status: Stopped'),
                const SizedBox(height: 16),
                Row(
                  children: [
                    Expanded(
                      child: FilledButton.icon(
                        onPressed: () {},
                        icon: const Icon(Icons.play_arrow),
                        label: const Text('Start'),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: FilledButton.tonalIcon(
                        onPressed: null,
                        icon: const Icon(Icons.stop),
                        label: const Text('Stop'),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
        const SizedBox(height: 16),
        NreCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Quick Actions', style: theme.textTheme.titleMedium),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  ActionChip(
                    avatar: const Icon(Icons.rule, size: 18),
                    label: const Text('Rules'),
                    onPressed: () => context.go(RouteNames.rules),
                  ),
                  if (caps.canManageCertificates)
                    ActionChip(
                      avatar: const Icon(Icons.security, size: 18),
                      label: const Text('Certificates'),
                      onPressed: () => context.go(RouteNames.certificates),
                    ),
                  ActionChip(
                    avatar: const Icon(Icons.memory, size: 18),
                    label: const Text('Agents'),
                    onPressed: () => context.go(RouteNames.agents),
                  ),
                ],
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _StatCard extends StatelessWidget {
  const _StatCard({
    required this.icon,
    required this.iconColor,
    required this.label,
    required this.value,
    required this.scheme,
  });

  final IconData icon;
  final Color iconColor;
  final String label;
  final String value;
  final ColorScheme scheme;

  @override
  Widget build(BuildContext context) {
    return NreCard(
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: iconColor.withValues(alpha: 0.15),
              borderRadius: BorderRadius.circular(12),
            ),
            child: Icon(icon, color: iconColor),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label, style: TextStyle(color: scheme.outline)),
                Text(
                  value,
                  style: Theme.of(context).textTheme.titleLarge?.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
