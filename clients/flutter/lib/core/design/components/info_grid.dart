import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';
import '../tokens/app_typography.dart';

class InfoCell {
  const InfoCell({required this.label, required this.value});
  final String label;
  final String value;
}

class InfoGrid extends StatelessWidget {
  const InfoGrid({super.key, required this.cells});

  final List<InfoCell> cells;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: _buildRows(),
    );
  }

  List<Widget> _buildRows() {
    final rows = <Widget>[];
    for (var i = 0; i < cells.length; i += 2) {
      final first = cells[i];
      final second = i + 1 < cells.length ? cells[i + 1] : null;
      rows.add(
        Padding(
          padding: EdgeInsets.only(
            top: i > 0 ? AppSpacing.s8 : 0,
          ),
          child: Row(
            children: [
              Expanded(child: _InfoCellWidget(cell: first)),
              const SizedBox(width: AppSpacing.s8),
              Expanded(
                child: second != null
                    ? _InfoCellWidget(cell: second)
                    : const SizedBox.shrink(),
              ),
            ],
          ),
        ),
      );
    }
    return rows;
  }
}

class _InfoCellWidget extends StatelessWidget {
  const _InfoCellWidget({required this.cell});
  final InfoCell cell;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.s10,
        vertical: AppSpacing.s8,
      ),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: AppColors.surfaceOpacityInner),
        borderRadius: BorderRadius.circular(AppRadius.medium),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            cell.label,
            style: const TextStyle(
              fontSize: 8,
              fontWeight: FontWeight.w400,
              color: AppColors.textMuted,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 2),
          Text(
            cell.value,
            style: AppTypography.metadata.copyWith(
              fontWeight: FontWeight.w500,
              color: AppColors.textPrimary,
            ),
          ),
        ],
      ),
    );
  }
}
