import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/platform/platform_capabilities.dart';
import '../../../../shared/widgets/nre_card.dart';
import '../../../../shared/widgets/nre_empty_state.dart';

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

class _LocalAgentTab extends StatelessWidget {
  const _LocalAgentTab();

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
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
                  Text('Local Agent Process', style: Theme.of(context).textTheme.titleMedium),
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
    );
  }
}
