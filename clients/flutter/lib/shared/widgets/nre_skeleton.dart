import 'package:flutter/material.dart';

class NreSkeletonList extends StatelessWidget {
  const NreSkeletonList({super.key, this.itemCount = 6});
  final int itemCount;

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemCount: itemCount,
      separatorBuilder: (_, __) => const SizedBox(height: 12),
      itemBuilder: (_, __) => const NreSkeletonCard(),
    );
  }
}

class NreSkeletonCard extends StatelessWidget {
  const NreSkeletonCard({super.key});

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
      color: Theme.of(context).colorScheme.surfaceContainerHighest,
      child: const Padding(
        padding: EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(height: 12, width: 120, child: _Bone()),
            SizedBox(height: 12),
            SizedBox(height: 8, width: 200, child: _Bone()),
          ],
        ),
      ),
    );
  }
}

class _Bone extends StatelessWidget {
  const _Bone();

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.outlineVariant.withValues(alpha: 0.3),
        borderRadius: BorderRadius.circular(4),
      ),
    );
  }
}
