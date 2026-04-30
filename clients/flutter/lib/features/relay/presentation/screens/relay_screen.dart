import 'package:flutter/material.dart';
import '../../../../shared/widgets/nre_empty_state.dart';

class RelayScreen extends StatelessWidget {
  const RelayScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Relay Listeners')),
      body: const NreEmptyState(
        icon: Icons.sync_alt,
        title: 'No Relay Listeners',
        message: 'No relay listeners configured',
      ),
    );
  }
}
