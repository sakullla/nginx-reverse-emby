import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';

class GlassToggle extends StatefulWidget {
  const GlassToggle({
    super.key,
    required this.value,
    this.onChanged,
    this.accentStart,
    this.accentEnd,
  });

  final bool value;
  final ValueChanged<bool>? onChanged;
  final Color? accentStart;
  final Color? accentEnd;

  @override
  State<GlassToggle> createState() => _GlassToggleState();
}

class _GlassToggleState extends State<GlassToggle>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 200),
    );
    if (widget.value) _controller.value = 1.0;
  }

  @override
  void didUpdateWidget(GlassToggle oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.value != oldWidget.value) {
      widget.value ? _controller.forward() : _controller.reverse();
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final start = widget.accentStart ?? AppColors.info;
    final end = widget.accentEnd ?? AppColors.info;

    return GestureDetector(
      onTap: () => widget.onChanged?.call(!widget.value),
      child: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          return Container(
            width: 32,
            height: 18,
            decoration: BoxDecoration(
              gradient: widget.value
                  ? LinearGradient(colors: [start, end])
                  : null,
              color: widget.value
                  ? null
                  : Colors.white.withValues(alpha: 0.1),
              borderRadius: BorderRadius.circular(9),
            ),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Align(
                alignment: Alignment(-1 + 2 * _controller.value, 0),
                child: Container(
                  width: 14,
                  height: 14,
                  decoration: BoxDecoration(
                    color:
                        widget.value ? Colors.white : const Color(0xFF94A3B8),
                    shape: BoxShape.circle,
                  ),
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}
