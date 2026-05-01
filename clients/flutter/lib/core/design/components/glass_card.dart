import 'dart:ui';

import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';

class GlassCard extends StatelessWidget {
  const GlassCard({
    super.key,
    required this.child,
    this.borderRadius = AppRadius.card,
    this.blur = AppBlur.standard,
    this.padding = const EdgeInsets.all(AppSpacing.s16),
    this.accentBorder = false,
    this.accentColor,
    this.onTap,
  });

  final Widget child;
  final double borderRadius;
  final double blur;
  final EdgeInsets padding;
  final bool accentBorder;
  final Color? accentColor;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final border = accentBorder && accentColor != null
        ? BorderSide(
            color: accentColor!.withValues(alpha: 0.4), width: 1)
        : const BorderSide(color: AppColors.border);

    return GestureDetector(
      onTap: onTap,
      child: MouseRegion(
        cursor: onTap != null ? SystemMouseCursors.click : MouseCursor.defer,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(borderRadius),
          child: BackdropFilter(
            filter: ImageFilter.blur(sigmaX: blur, sigmaY: blur),
            child: Container(
              padding: padding,
              decoration: BoxDecoration(
                color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
                borderRadius: BorderRadius.circular(borderRadius),
                border: Border.fromBorderSide(border),
              ),
              child: child,
            ),
          ),
        ),
      ),
    );
  }
}
