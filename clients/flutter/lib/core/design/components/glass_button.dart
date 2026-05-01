import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';

enum GlassButtonVariant { primary, secondary, danger, warning }

class GlassButton extends StatelessWidget {
  const GlassButton({
    super.key,
    required this.label,
    required this.variant,
    this.icon,
    this.onPressed,
    this.accentStart,
    this.accentEnd,
  });

  final String label;
  final GlassButtonVariant variant;
  final String? icon;
  final VoidCallback? onPressed;
  final Color? accentStart;
  final Color? accentEnd;

  static GlassButton primary({
    Key? key,
    required String label,
    String? icon,
    VoidCallback? onPressed,
    Color? accentStart,
    Color? accentEnd,
  }) {
    return GlassButton(
      key: key,
      label: label,
      variant: GlassButtonVariant.primary,
      icon: icon,
      onPressed: onPressed,
      accentStart: accentStart,
      accentEnd: accentEnd,
    );
  }

  static GlassButton secondary({
    Key? key,
    required String label,
    String? icon,
    VoidCallback? onPressed,
  }) {
    return GlassButton(
      key: key,
      label: label,
      variant: GlassButtonVariant.secondary,
      icon: icon,
      onPressed: onPressed,
    );
  }

  static GlassButton danger({
    Key? key,
    required String label,
    String? icon,
    VoidCallback? onPressed,
  }) {
    return GlassButton(
      key: key,
      label: label,
      variant: GlassButtonVariant.danger,
      icon: icon,
      onPressed: onPressed,
    );
  }

  static GlassButton warning({
    Key? key,
    required String label,
    String? icon,
    VoidCallback? onPressed,
  }) {
    return GlassButton(
      key: key,
      label: label,
      variant: GlassButtonVariant.warning,
      icon: icon,
      onPressed: onPressed,
    );
  }

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onPressed,
      child: MouseRegion(
        cursor: onPressed != null
            ? SystemMouseCursors.click
            : SystemMouseCursors.basic,
        child: Container(
          padding: const EdgeInsets.symmetric(
            vertical: AppSpacing.s8 - 1,
            horizontal: AppSpacing.s14,
          ),
          decoration: _buildDecoration(),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (icon != null) ...[
                Text(icon!, style: const TextStyle(fontSize: 12)),
                const SizedBox(width: 4),
              ],
              Text(
                label,
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w500,
                  color: _textColor,
                  height: 1.4,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  BoxDecoration _buildDecoration() {
    return switch (variant) {
      GlassButtonVariant.primary => BoxDecoration(
          gradient: LinearGradient(
            colors: [
              accentStart ?? AppColors.info,
              accentEnd ?? AppColors.info,
            ],
          ),
          borderRadius: BorderRadius.circular(AppRadius.medium),
        ),
      GlassButtonVariant.secondary => BoxDecoration(
          color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
          borderRadius: BorderRadius.circular(AppRadius.medium),
          border: Border.all(color: AppColors.border),
        ),
      GlassButtonVariant.danger => BoxDecoration(
          color: AppColors.error.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(AppRadius.medium),
          border: Border.all(
              color: AppColors.error.withValues(alpha: 0.2)),
        ),
      GlassButtonVariant.warning => BoxDecoration(
          color: AppColors.warning.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(AppRadius.medium),
          border: Border.all(
              color: AppColors.warning.withValues(alpha: 0.2)),
        ),
    };
  }

  Color get _textColor => switch (variant) {
        GlassButtonVariant.primary => Colors.white,
        GlassButtonVariant.secondary => AppColors.textMuted,
        GlassButtonVariant.danger => AppColors.error,
        GlassButtonVariant.warning => AppColors.warning,
      };
}
