import 'package:flutter/material.dart';

class UpdatesScreen extends StatelessWidget {
  const UpdatesScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Updates')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: const [
          ListTile(
            title: Text('GUI client'),
            subtitle: Text('Current version unknown'),
          ),
          ListTile(
            title: Text('Managed agent'),
            subtitle: Text('No package selected'),
          ),
          ListTile(title: Text('Checksum'), subtitle: Text('-')),
        ],
      ),
    );
  }
}
