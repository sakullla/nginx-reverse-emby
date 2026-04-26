import 'package:flutter/material.dart';

class AboutScreen extends StatelessWidget {
  const AboutScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('About')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: const [
          ListTile(title: Text('NRE Client'), subtitle: Text('Flutter GUI')),
          ListTile(
            title: Text('Distribution'),
            subtitle: Text('GitHub Release'),
          ),
          ListTile(
            title: Text('Container policy'),
            subtitle: Text(
              'Client artifacts are not embedded in the control-plane image',
            ),
          ),
        ],
      ),
    );
  }
}
