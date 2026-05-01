import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';

class ExpiryBar extends StatefulWidget {
  const ExpiryBar({
    super.key,
    required this.progress,
    this.color,
    this.backgroundColor,
  }) : assert(progress >= 0.0 && progress <= 1.0);

  final double progress;
  final Color? color;
  final Color? backgroundColor;

  @override
  State<ExpiryBar> createState() => _ExpiryBarState();
}

class _ExpiryBarState extends State<ExpiryBar> {
  @override
  void initState() {
    super.initState();
  }

  Color _resolveColor() {
    if (widget.color != null) return widget.color!;
    if (widget.progress > 0.5) return AppColors.success;
    if (widget.progress > 0.2) return AppColors.warning;
    return AppColors.error;
  }

  @override
  Widget build(BuildContext context) {
    final trackColor = widget.backgroundColor ??
        Colors.white.withValues(alpha: AppColors.surfaceOpacityCard);

    return ClipRRect(
      borderRadius: BorderRadius.circular(4),
      child: Container(
        height: 4,
        decoration: BoxDecoration(
          color: trackColor,
          borderRadius: BorderRadius.circular(4),
        ),
        child: AnimatedFractionallySizedBox(
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeInOut,
          alignment: Alignment.centerLeft,
          widthFactor: widget.progress.clamp(0.0, 1.0),
          child: Container(
            decoration: BoxDecoration(
              color: _resolveColor(),
              borderRadius: BorderRadius.circular(4),
            ),
          ),
        ),
      ),
    );
  }
}
