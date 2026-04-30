import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../../core/client_state.dart' as runtime_state;
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../core/routing/route_names.dart';
import '../../../../shared/widgets/nre_card.dart';
import '../../../../shared/widgets/nre_empty_state.dart';
import '../../../../shared/widgets/nre_status_chip.dart';
import '../../../../services/local_agent_controller.dart';
import '../../../../services/local_agent_controller_provider.dart';
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
            return _DashboardContent(
              profile: state.profile,
              caps: caps,
              scheme: scheme,
              theme: theme,
            );
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
}

class _DashboardContent extends ConsumerStatefulWidget {
  const _DashboardContent({
    required this.profile,
    required this.caps,
    required this.scheme,
    required this.theme,
  });

  final ClientProfile profile;
  final PlatformCapabilities caps;
  final ColorScheme scheme;
  final ThemeData theme;

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
    if (widget.caps.canManageLocalAgent) {
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
    if (!widget.caps.canManageLocalAgent) return;
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
    await _runAgent((controller) => controller.start(_runtimeProfile));
  }

  Future<void> _stopAgent() async {
    await _runAgent((controller) => controller.stop(_runtimeProfile));
  }

  Future<void> _runAgent(
    Future<LocalAgentRuntimeSnapshot> Function(LocalAgentController controller)
    action,
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
    final profile = widget.profile;
    final caps = widget.caps;
    final scheme = widget.scheme;
    final theme = widget.theme;
    final snapshot = _snapshot;
    final isRunning = snapshot?.status == LocalAgentControllerStatus.running;
    final isStopped = snapshot?.status == LocalAgentControllerStatus.stopped;

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
                    if (_agentLoading)
                      const SizedBox.square(
                        dimension: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    else
                      NreStatusChip(
                        label: _agentStatusText(snapshot?.status),
                        type: isRunning
                            ? StatusType.success
                            : isStopped
                            ? StatusType.warning
                            : StatusType.error,
                      ),
                  ],
                ),
                const Divider(),
                Text('PID: ${snapshot?.pid ?? '—'}'),
                Text('Status: ${_agentStatusText(snapshot?.status)}'),
                if (snapshot?.message.isNotEmpty == true)
                  Text('Message: ${snapshot!.message}'),
                if (_agentError.isNotEmpty)
                  Text(
                    'Error: $_agentError',
                    style: TextStyle(color: scheme.error),
                  ),
                const SizedBox(height: 16),
                Row(
                  children: [
                    Expanded(
                      child: FilledButton.icon(
                        onPressed: !_agentLoading && isStopped
                            ? _startAgent
                            : null,
                        icon: const Icon(Icons.play_arrow),
                        label: const Text('Start'),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: FilledButton.tonalIcon(
                        onPressed: !_agentLoading && isRunning
                            ? _stopAgent
                            : null,
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

  String _agentStatusText(LocalAgentControllerStatus? status) {
    return switch (status) {
      LocalAgentControllerStatus.running => 'Running',
      LocalAgentControllerStatus.stopped => 'Stopped',
      LocalAgentControllerStatus.unavailable => 'Unavailable',
      null => 'Unknown',
    };
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
                  style: Theme.of(
                    context,
                  ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
