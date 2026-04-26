import 'package:flutter/material.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: const [
          ListTile(title: Text('Master URL'), subtitle: Text('Not configured')),
          ListTile(title: Text('Data directory'), subtitle: Text('Default')),
          SwitchListTile(
            value: false,
            onChanged: null,
            title: Text('Start at login'),
          ),
        ],
      ),
    );
  }
}
