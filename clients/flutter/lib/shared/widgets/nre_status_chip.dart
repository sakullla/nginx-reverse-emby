import 'package:flutter/material.dart';

enum StatusType { success, warning, error, info }

class NreStatusChip extends StatelessWidget {
  const NreStatusChip({super.key, required this.label, required this.type});

  final String label;
  final StatusType type;

  @override
  Widget build(BuildContext context) {
    final colors = _resolveColors(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: BoxDecoration(
        gradient: LinearGradient(
          colors: [colors.background, colors.background.withValues(alpha: 0.7)],
        ),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(
          color: colors.foreground.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: colors.foreground,
        ),
      ),
    );
  }

  _StatusColors _resolveColors(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return switch (type) {
      StatusType.success => _StatusColors(
          background: Colors.green.withValues(alpha: 0.15),
          foreground: Colors.green,
        ),
      StatusType.warning => _StatusColors(
          background: Colors.orange.withValues(alpha: 0.15),
          foreground: Colors.orange,
        ),
      StatusType.error => _StatusColors(
          background: scheme.errorContainer,
          foreground: scheme.error,
        ),
      StatusType.info => _StatusColors(
          background: scheme.primaryContainer,
          foreground: scheme.primary,
        ),
    };
  }
}

class _StatusColors {
  final Color background;
  final Color foreground;
  _StatusColors({required this.background, required this.foreground});
}
