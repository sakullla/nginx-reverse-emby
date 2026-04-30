import 'package:flutter/material.dart';
import '../../../../shared/widgets/nre_empty_state.dart';

class CertificatesScreen extends StatelessWidget {
  const CertificatesScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Certificates')),
      body: const NreEmptyState(
        icon: Icons.security,
        title: 'No Certificates',
        message: 'No SSL certificates configured',
      ),
    );
  }
}
