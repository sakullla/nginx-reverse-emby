import 'package:flutter/material.dart';

class RuntimeScreen extends StatelessWidget {
  const RuntimeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Runtime')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const ListTile(
            title: Text('Local agent'),
            subtitle: Text('Not installed'),
          ),
          const ListTile(title: Text('Startup'), subtitle: Text('Manual')),
          const FilledButton(onPressed: null, child: Text('Start Agent')),
          const SizedBox(height: 8),
          const OutlinedButton(onPressed: null, child: Text('Stop Agent')),
        ],
      ),
    );
  }
}
