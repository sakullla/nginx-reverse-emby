import 'package:flutter/material.dart';

import '../core/client_state.dart';

class OverviewScreen extends StatelessWidget {
  const OverviewScreen({super.key, required this.state});

  final ClientState state;

  @override
  Widget build(BuildContext context) {
    final profile = state.profile;
    final masterSubtitle = profile.masterUrl.trim().isEmpty
        ? 'Not configured'
        : profile.masterUrl;
    final runtimeSubtitle = profile.isRegistered
        ? 'Registered: ${profile.agentId}'
        : 'Agent not running';

    return Scaffold(
      appBar: AppBar(title: const Text('Overview')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(title: const Text('Master'), subtitle: Text(masterSubtitle)),
          ListTile(
            title: const Text('Runtime'),
            subtitle: Text(runtimeSubtitle),
          ),
          const ListTile(title: Text('Last sync'), subtitle: Text('-')),
        ],
      ),
    );
  }
}
