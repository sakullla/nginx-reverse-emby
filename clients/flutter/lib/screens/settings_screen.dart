import 'package:flutter/material.dart';

import '../core/client_state.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key, required this.state});

  final ClientState state;

  @override
  Widget build(BuildContext context) {
    final masterUrl = state.profile.masterUrl.trim();

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(
            title: const Text('Master URL'),
            subtitle: Text(masterUrl.isEmpty ? 'Not configured' : masterUrl),
          ),
          const ListTile(
            title: Text('Data directory'),
            subtitle: Text('Default'),
          ),
          const SwitchListTile(
            value: false,
            onChanged: null,
            title: Text('Start at login'),
          ),
        ],
      ),
    );
  }
}
