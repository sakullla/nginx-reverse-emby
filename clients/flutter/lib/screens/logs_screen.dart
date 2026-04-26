import 'package:flutter/material.dart';

class LogsScreen extends StatelessWidget {
  const LogsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      appBar: _LogsAppBar(),
      body: Padding(
        padding: EdgeInsets.all(16),
        child: SelectableText('No logs available.'),
      ),
    );
  }
}

class _LogsAppBar extends StatelessWidget implements PreferredSizeWidget {
  const _LogsAppBar();

  @override
  Size get preferredSize => const Size.fromHeight(kToolbarHeight);

  @override
  Widget build(BuildContext context) {
    return AppBar(title: const Text('Logs'));
  }
}
