import 'package:flutter/material.dart';

class OverviewScreen extends StatelessWidget {
  const OverviewScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Overview')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: const [
          ListTile(title: Text('Master'), subtitle: Text('Not configured')),
          ListTile(title: Text('Runtime'), subtitle: Text('Agent not running')),
          ListTile(title: Text('Last sync'), subtitle: Text('-')),
        ],
      ),
    );
  }
}
