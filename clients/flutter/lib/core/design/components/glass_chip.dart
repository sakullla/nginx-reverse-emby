import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';

class GlassChip extends StatelessWidget {
  const GlassChip({
    super.key,
    required this.label,
    required this.color,
    this.showDot = false,
  });

  final String label;
  final Color color;
  final bool showDot;

  static GlassChip success({
    Key? key,
    required String label,
    bool showDot = false,
  }) {
    return GlassChip(
      key: key,
      label: label,
      color: AppColors.success,
      showDot: showDot,
    );
  }

  static GlassChip warning({
    Key? key,
    required String label,
    bool showDot = false,
  }) {
    return GlassChip(
      key: key,
      label: label,
      color: AppColors.warning,
      showDot: showDot,
    );
  }

  static GlassChip error({
    Key? key,
    required String label,
    bool showDot = false,
  }) {
    return GlassChip(
      key: key,
      label: label,
      color: AppColors.error,
      showDot: showDot,
    );
  }

  static GlassChip info({
    Key? key,
    required String label,
    bool showDot = false,
  }) {
    return GlassChip(
      key: key,
      label: label,
      color: AppColors.info,
      showDot: showDot,
    );
  }

  static GlassChip accent({
    Key? key,
    required String label,
    required Color accentColor,
    bool showDot = false,
  }) {
    return GlassChip(
      key: key,
      label: label,
      color: accentColor,
      showDot: showDot,
    );
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.s8,
        vertical: AppSpacing.s4,
      ),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(AppRadius.small),
        border: Border.all(color: color.withValues(alpha: 0.25), width: 1),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (showDot) ...[
            Container(
              width: 5,
              height: 5,
              decoration: BoxDecoration(
                color: color,
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 4),
          ],
          Text(
            label,
            style: TextStyle(
              fontSize: 9,
              fontWeight: FontWeight.w500,
              color: color,
              height: 1.4,
            ),
          ),
        ],
      ),
    );
  }
}
