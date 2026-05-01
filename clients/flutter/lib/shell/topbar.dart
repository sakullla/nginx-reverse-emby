import 'package:flutter/material.dart';

import '../core/design/components/page_header.dart';

// ---------------------------------------------------------------------------
// GlassTopBar
// ---------------------------------------------------------------------------

class GlassTopBar extends StatelessWidget {
  const GlassTopBar({
    super.key,
    required this.title,
    this.subtitle,
    this.statusBadge,
    this.actions = const [],
  });

  /// Page title displayed on the left.
  final String title;

  /// Optional subtitle beneath the title.
  final String? subtitle;

  /// Optional status badge shown next to the title.
  final Widget? statusBadge;

  /// Optional action buttons on the right.
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      decoration: const BoxDecoration(
        color: Color(0x26000000), // rgba(0,0,0,0.15)
        border: Border(
          bottom: BorderSide(
            color: Color(0x0FFFFFFF), // rgba(255,255,255,0.06)
          ),
        ),
      ),
      child: PageHeader(
        title: title,
        subtitle: subtitle,
        statusBadge: statusBadge,
        actions: actions,
      ),
    );
  }
}
