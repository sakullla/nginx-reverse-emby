import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/client_state.dart' as runtime_state;
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../shared/widgets/nre_card.dart';
import '../../../../shared/widgets/nre_empty_state.dart';
import '../../../../shared/widgets/nre_status_chip.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';
import '../../../../services/local_agent_controller.dart';
import '../../../../services/local_agent_controller_provider.dart';

class AgentsScreen extends ConsumerWidget {
  const AgentsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final caps = PlatformCapabilities.current;

    return DefaultTabController(
      length: caps.canManageLocalAgent ? 2 : 1,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Agents'),
          bottom: caps.canManageLocalAgent
              ? const TabBar(
                  tabs: [
                    Tab(icon: Icon(Icons.devices), text: 'Remote'),
                    Tab(icon: Icon(Icons.computer), text: 'Local'),
                  ],
                )
              : null,
        ),
        body: TabBarView(
          children: [
            const _RemoteAgentsTab(),
            if (caps.canManageLocalAgent) const _LocalAgentTab(),
          ],
        ),
      ),
    );
  }
}

class _RemoteAgentsTab extends StatelessWidget {
  const _RemoteAgentsTab();

  @override
  Widget build(BuildContext context) {
    return const NreEmptyState(
      icon: Icons.devices,
      title: 'No Agents',
      message: 'No remote agents found',
    );
  }
}

class _LocalAgentTab extends ConsumerStatefulWidget {
  const _LocalAgentTab();

  @override
  ConsumerState<_LocalAgentTab> createState() => _LocalAgentTabState();
}

class _LocalAgentTabState extends ConsumerState<_LocalAgentTab> {
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
    if (auth is! AuthStateAuthenticated) {
      return null;
    }
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
    final scheme = Theme.of(context).colorScheme;
    final auth = ref.watch(authNotifierProvider);
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    if (auth.value is AuthStateAuthenticated &&
        !_loading &&
        snapshot == null &&
        _error.isEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _refresh();
      });
    }

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        NreCard(
          hasAccentBar: true,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.memory, color: scheme.primary),
                  const SizedBox(width: 8),
                  Text(
                    'Local Agent Process',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const Spacer(),
                  if (_loading)
                    const SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  else
                    NreStatusChip(
                      label: _statusText(status),
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
              Text('Status: ${_statusText(status)}'),
              if (snapshot?.message.isNotEmpty == true)
                Text('Message: ${snapshot!.message}'),
              if (_error.isNotEmpty)
                Text('Error: $_error', style: TextStyle(color: scheme.error)),
              if (snapshot != null) ...[
                Text('Binary: ${snapshot.binaryPath}'),
                Text('Data: ${snapshot.dataDir}'),
                Text('Logs: ${snapshot.logPath}'),
              ],
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: FilledButton.icon(
                      onPressed: !_loading && isStopped ? _start : null,
                      icon: const Icon(Icons.play_arrow),
                      label: const Text('Start'),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: FilledButton.tonalIcon(
                      onPressed: !_loading && isRunning ? _stop : null,
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
    );
  }

  String _statusText(LocalAgentControllerStatus? status) {
    return switch (status) {
      LocalAgentControllerStatus.running => 'Running',
      LocalAgentControllerStatus.stopped => 'Stopped',
      LocalAgentControllerStatus.unavailable => 'Unavailable',
      null => 'Unknown',
    };
  }
}
