import 'dart:ui';

import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';

class GlassSearchBar extends StatefulWidget {
  const GlassSearchBar({
    super.key,
    this.hint = 'Search...',
    this.onChanged,
    this.controller,
  });

  final String hint;
  final ValueChanged<String>? onChanged;
  final TextEditingController? controller;

  @override
  State<GlassSearchBar> createState() => _GlassSearchBarState();
}

class _GlassSearchBarState extends State<GlassSearchBar> {
  late final TextEditingController _controller;

  @override
  void initState() {
    super.initState();
    _controller = widget.controller ?? TextEditingController();
  }

  @override
  void dispose() {
    if (widget.controller == null) _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(
            sigmaX: AppBlur.standard, sigmaY: AppBlur.standard),
        child: Container(
          height: 36,
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: TextField(
            controller: _controller,
            onChanged: widget.onChanged,
            style: const TextStyle(
              fontSize: 12,
              color: AppColors.textPrimary,
            ),
            decoration: InputDecoration(
              hintText: widget.hint,
              hintStyle: const TextStyle(
                fontSize: 12,
                color: AppColors.textMuted,
              ),
              prefixIcon: Padding(
                padding: const EdgeInsets.only(
                  left: AppSpacing.s12,
                  right: AppSpacing.s8,
                ),
                child: Icon(
                  Icons.search,
                  size: 16,
                  color: AppColors.textMuted.withValues(alpha: 0.6),
                ),
              ),
              prefixIconConstraints: const BoxConstraints(
                minWidth: 36,
                minHeight: 36,
              ),
              border: InputBorder.none,
              enabledBorder: InputBorder.none,
              focusedBorder: InputBorder.none,
              contentPadding: const EdgeInsets.symmetric(vertical: 10),
              isDense: true,
            ),
          ),
        ),
      ),
    );
  }
}
