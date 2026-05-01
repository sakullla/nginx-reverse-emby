import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/components/glass_chip.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../l10n/app_localizations.dart';
import '../../data/models/certificate_models.dart';
import '../providers/certificates_provider.dart';

class CertificatesScreen extends ConsumerWidget {
  const CertificatesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final certsAsync = ref.watch(certificatesListProvider);
    final filteredCerts = ref.watch(filteredCertificatesProvider);
    final statusFilter = ref.watch(certStatusFilterNotifierProvider);

    return SingleChildScrollView(
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // -- Top actions bar ----
          _TopActionsBar(
            totalCerts: certsAsync.valueOrNull?.length ?? 0,
            statusFilter: statusFilter,
            loc: AppLocalizations.of(context)!,
          ),
          const SizedBox(height: AppSpacing.s12),

          // -- Expiry warning banner ----
          if (certsAsync.hasValue) ...[
            ...() {
              final certs = certsAsync.value!;
              final expiringCount = certs
                  .where((c) => c.status == CertStatus.expiring)
                  .length;
              if (expiringCount > 0) {
                return [
                  Padding(
                    padding: const EdgeInsets.only(bottom: AppSpacing.s12),
                    child: _ExpiryWarningBanner(
                      expiringCount: expiringCount,
                      loc: AppLocalizations.of(context)!,
                    ),
                  ),
                ];
              }
              return <Widget>[];
            }(),
          ],

          // -- Content ----
          certsAsync.when(
            data: (_) {
              if (filteredCerts.isEmpty) {
                return _EmptyState(loc: AppLocalizations.of(context)!);
              }
              return _CertificateListView(
                certificates: filteredCerts,
                loc: AppLocalizations.of(context)!,
              );
            },
            loading: () => const _SkeletonList(),
            error: (err, _) =>
                _ErrorState(error: err, loc: AppLocalizations.of(context)!),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Top actions bar: Import, Request, count, filter
// ---------------------------------------------------------------------------

class _TopActionsBar extends ConsumerWidget {
  const _TopActionsBar({
    required this.totalCerts,
    required this.statusFilter,
    required this.loc,
  });

  final int totalCerts;
  final CertStatusFilter statusFilter;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        // Import button
        GlassButton.secondary(
          label: loc.btnImport,
          icon: '↑',
          onPressed: () =>
              _showCertificateFormDialog(context, 'Import certificate'),
        ),
        const SizedBox(width: AppSpacing.s8),

        // Request button
        GlassButton.primary(
          label: loc.btnRequest,
          icon: '+',
          onPressed: () =>
              _showCertificateFormDialog(context, 'Request certificate'),
        ),
        const SizedBox(width: AppSpacing.s12),

        // Certificate count
        Text(
          loc.labelCertificateCount(totalCerts, totalCerts == 1 ? '' : 's'),
          style: AppTypography.metadata.copyWith(color: AppColors.textMuted),
        ),
        const Spacer(),

        // Status filter
        Material(
          color: Colors.transparent,
          child: _StatusFilterDropdown(
            value: statusFilter,
            loc: loc,
            onChanged: (v) {
              if (v != null) {
                ref.read(certStatusFilterNotifierProvider.notifier).update(v);
              }
            },
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Status filter dropdown
// ---------------------------------------------------------------------------

class _StatusFilterDropdown extends StatelessWidget {
  const _StatusFilterDropdown({
    required this.value,
    required this.loc,
    required this.onChanged,
  });

  final CertStatusFilter value;
  final AppLocalizations loc;
  final ValueChanged<CertStatusFilter?> onChanged;

  String _label(CertStatusFilter f) {
    switch (f) {
      case CertStatusFilter.all:
        return loc.filterAllStatus;
      case CertStatusFilter.valid:
        return loc.certStatusValid;
      case CertStatusFilter.expiring:
        return loc.certStatusExpiring;
      case CertStatusFilter.expired:
        return loc.certStatusExpired;
    }
  }

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: AppBlur.subtle,
          sigmaY: AppBlur.subtle,
        ),
        child: Container(
          height: 36,
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.s8),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<CertStatusFilter>(
              value: value,
              items: CertStatusFilter.values
                  .map(
                    (f) => DropdownMenuItem(
                      value: f,
                      child: Text(
                        _label(f),
                        style: AppTypography.metadata.copyWith(
                          color: AppColors.textSecondary,
                        ),
                      ),
                    ),
                  )
                  .toList(),
              onChanged: onChanged,
              dropdownColor: const Color(0xFF1E293B),
              icon: Icon(
                Icons.unfold_more,
                size: 14,
                color: AppColors.textMuted,
              ),
              style: AppTypography.metadata.copyWith(
                color: AppColors.textSecondary,
              ),
              isDense: true,
              underline: const SizedBox.shrink(),
            ),
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Expiry warning banner
// ---------------------------------------------------------------------------

class _ExpiryWarningBanner extends StatelessWidget {
  const _ExpiryWarningBanner({required this.expiringCount, required this.loc});

  final int expiringCount;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return GlassCard(
      accentBorder: true,
      accentColor: AppColors.warning,
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.s16,
        vertical: AppSpacing.s12,
      ),
      child: Row(
        children: [
          Text('⚠', style: TextStyle(fontSize: 16, color: AppColors.warning)),
          const SizedBox(width: AppSpacing.s12),
          Expanded(
            child: Text(
              loc.labelExpiringWarning(
                expiringCount,
                expiringCount == 1 ? '' : 's',
              ),
              style: AppTypography.body.copyWith(color: AppColors.warning),
            ),
          ),
          GestureDetector(
            onTap: () {
              // Could navigate or filter to show only expiring certs
            },
            child: Text(
              loc.labelReview,
              style: AppTypography.bodyMedium.copyWith(
                color: AppColors.warning,
                decoration: TextDecoration.underline,
                decorationColor: AppColors.warning.withValues(alpha: 0.5),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Certificate list view
// ---------------------------------------------------------------------------

class _CertificateListView extends StatelessWidget {
  const _CertificateListView({required this.certificates, required this.loc});

  final List<Certificate> certificates;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: certificates
          .map(
            (cert) => Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.s8),
              child: _CertificateCard(cert: cert, loc: loc),
            ),
          )
          .toList(),
    );
  }
}

// ---------------------------------------------------------------------------
// Single certificate card
// ---------------------------------------------------------------------------

class _CertificateCard extends StatelessWidget {
  const _CertificateCard({required this.cert, required this.loc});

  final Certificate cert;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final isExpiring = cert.status == CertStatus.expiring;

    Widget card = GlassCard(
      accentBorder: isExpiring,
      accentColor: isExpiring ? AppColors.warning : null,
      padding: const EdgeInsets.all(AppSpacing.s16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // -- Header row ----
          _HeaderRow(cert: cert, loc: loc),
          const SizedBox(height: AppSpacing.s12),

          // -- Details row ----
          _DetailsRow(cert: cert, loc: loc),
          const SizedBox(height: AppSpacing.s12),

          // -- Expiry bar ----
          ExpiryBar(progress: cert.lifetimeProgress),
          const SizedBox(height: AppSpacing.s12),

          // -- Associated rules ----
          if (cert.associatedRules.isNotEmpty) ...[
            _AssociatedRulesRow(cert: cert, loc: loc),
            const SizedBox(height: AppSpacing.s12),
          ],

          // -- Action buttons ----
          _ActionRow(cert: cert, loc: loc),
        ],
      ),
    );

    return card;
  }
}

// ---------------------------------------------------------------------------
// Header row: icon, domain + badges, remaining days
// ---------------------------------------------------------------------------

class _HeaderRow extends StatelessWidget {
  const _HeaderRow({required this.cert, required this.loc});

  final Certificate cert;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final statusColor = _statusColor(cert.status);

    return Row(
      children: [
        // Status icon
        Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: statusColor.withValues(alpha: 0.12),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: statusColor.withValues(alpha: 0.2)),
          ),
          child: Icon(
            Icons.verified_user_outlined,
            size: 20,
            color: statusColor,
          ),
        ),
        const SizedBox(width: AppSpacing.s12),

        // Domain + badges
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Flexible(
                    child: Text(
                      cert.domain,
                      style: AppTypography.bodyMedium.copyWith(
                        color: AppColors.textPrimary,
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: AppSpacing.s8),
                  _StatusBadge(status: cert.status, loc: loc),
                  if (cert.isSelfSigned) ...[
                    const SizedBox(width: AppSpacing.s4),
                    GlassChip(
                      label: loc.titleSelfSigned,
                      color: AppColors.textMuted,
                    ),
                  ],
                ],
              ),
            ],
          ),
        ),
        const SizedBox(width: AppSpacing.s12),

        // Remaining days
        Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Text(
              '${cert.remainingDays}',
              style: AppTypography.statNumberSmall.copyWith(
                color: cert.remainingDays < 0
                    ? AppColors.error
                    : cert.remainingDays <= 14
                    ? AppColors.warning
                    : AppColors.textPrimary,
              ),
            ),
            Text(
              cert.remainingDays < 0 ? loc.labelOverdue : loc.labelRemaining,
              style: AppTypography.metadataSmall.copyWith(
                color: AppColors.textMuted,
              ),
            ),
          ],
        ),
      ],
    );
  }

  Color _statusColor(CertStatus status) => switch (status) {
    CertStatus.valid => AppColors.success,
    CertStatus.expiring => AppColors.warning,
    CertStatus.expired => AppColors.error,
  };
}

// ---------------------------------------------------------------------------
// Status badge chip
// ---------------------------------------------------------------------------

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.status, required this.loc});

  final CertStatus status;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return switch (status) {
      CertStatus.valid => GlassChip.success(
        label: loc.certStatusValid,
        showDot: true,
      ),
      CertStatus.expiring => GlassChip.warning(
        label: loc.certStatusExpiring,
        showDot: true,
      ),
      CertStatus.expired => GlassChip.error(
        label: loc.certStatusExpired,
        showDot: true,
      ),
    };
  }
}

// ---------------------------------------------------------------------------
// Details row: CA name + issue date
// ---------------------------------------------------------------------------

class _DetailsRow extends StatelessWidget {
  const _DetailsRow({required this.cert, required this.loc});

  final Certificate cert;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final parts = <String>[];
    if (cert.ca != null && cert.ca!.isNotEmpty) parts.add(cert.ca!);
    if (cert.issuedAt != null) {
      parts.add(loc.labelIssued(_formatDate(cert.issuedAt!)));
    }

    if (parts.isEmpty) return const SizedBox.shrink();

    return Text(
      parts.join('  ·  '),
      style: AppTypography.metadata.copyWith(color: AppColors.textMuted),
    );
  }

  String _formatDate(DateTime date) {
    final y = date.year;
    final m = date.month.toString().padLeft(2, '0');
    final d = date.day.toString().padLeft(2, '0');
    return '$y-$m-$d';
  }
}

// ---------------------------------------------------------------------------
// ExpiryBar: progress bar showing certificate lifetime elapsed
// ---------------------------------------------------------------------------

class ExpiryBar extends StatelessWidget {
  const ExpiryBar({super.key, required this.progress});

  /// 0.0 - 1.0 percentage of certificate lifetime elapsed.
  final double progress;

  Color _barColor(double p) {
    if (p > 0.9) return AppColors.error;
    if (p > 0.7) return AppColors.warning;
    return AppColors.success;
  }

  @override
  Widget build(BuildContext context) {
    final color = _barColor(progress);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(3),
          child: SizedBox(
            height: 4,
            child: LayoutBuilder(
              builder: (context, constraints) {
                return Stack(
                  children: [
                    // Background track
                    Container(
                      width: constraints.maxWidth,
                      decoration: BoxDecoration(
                        color: Colors.white.withValues(alpha: 0.06),
                        borderRadius: BorderRadius.circular(3),
                      ),
                    ),
                    // Filled portion
                    Container(
                      width: constraints.maxWidth * progress.clamp(0.0, 1.0),
                      decoration: BoxDecoration(
                        color: color.withValues(alpha: 0.7),
                        borderRadius: BorderRadius.circular(3),
                      ),
                    ),
                  ],
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Associated rules row
// ---------------------------------------------------------------------------

class _AssociatedRulesRow extends StatelessWidget {
  const _AssociatedRulesRow({required this.cert, required this.loc});

  final Certificate cert;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      crossAxisAlignment: WrapCrossAlignment.center,
      spacing: AppSpacing.s4,
      runSpacing: AppSpacing.s4,
      children: [
        Text(
          loc.labelUsedBy,
          style: AppTypography.metadataSmall.copyWith(
            color: AppColors.textMuted,
          ),
        ),
        ...cert.associatedRules.map(
          (domain) =>
              GlassChip.accent(label: domain, accentColor: AppColors.info),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Action buttons row
// ---------------------------------------------------------------------------

class _ActionRow extends ConsumerWidget {
  const _ActionRow({required this.cert, required this.loc});

  final Certificate cert;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isExpiring = cert.status == CertStatus.expiring;

    return Row(
      children: [
        if (isExpiring) ...[
          GlassButton.warning(
            label: loc.btnRenew,
            onPressed: () => ref
                .read(certificatesListProvider.notifier)
                .issueCertificate(cert.id),
          ),
          const SizedBox(width: AppSpacing.s8),
        ],
        GlassButton.secondary(
          label: loc.btnDetails,
          onPressed: () => _showCertificateDetailsDialog(context, cert),
        ),
        const SizedBox(width: AppSpacing.s8),
        GlassButton.danger(
          label: loc.btnDelete,
          onPressed: () => _showDeleteCertificateDialog(context, ref, cert),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.loc});

  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 80),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.security_outlined,
              size: 48,
              color: AppColors.textMuted.withValues(alpha: 0.5),
            ),
            const SizedBox(height: AppSpacing.s12),
            Text(
              loc.titleNoCertificates,
              style: AppTypography.title.copyWith(color: AppColors.textMuted),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              loc.descImportOrRequestCert,
              style: AppTypography.metadata.copyWith(
                color: AppColors.textMuted.withValues(alpha: 0.7),
              ),
            ),
            const SizedBox(height: AppSpacing.s16),
            GlassButton.secondary(
              label: loc.btnImport,
              icon: '↑',
              onPressed: () =>
                  _showCertificateFormDialog(context, 'Import certificate'),
            ),
          ],
        ),
      ),
    );
  }
}

void _showCertificateFormDialog(BuildContext context, String title) {
  final domainController = TextEditingController();
  showDialog<void>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: TextField(
        controller: domainController,
        decoration: const InputDecoration(labelText: 'Domain'),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Cancel'),
        ),
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Save'),
        ),
      ],
    ),
  );
}

void _showCertificateDetailsDialog(BuildContext context, Certificate cert) {
  showDialog<void>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Certificate details'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(cert.domain),
          if (cert.fingerprint != null)
            Text('Fingerprint: ${cert.fingerprint}'),
          if (cert.scope != null) Text('Scope: ${cert.scope}'),
          if (cert.issuerMode != null) Text('Issuer: ${cert.issuerMode}'),
          if (cert.backendStatus != null) Text('Status: ${cert.backendStatus}'),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Close'),
        ),
      ],
    ),
  );
}

void _showDeleteCertificateDialog(
  BuildContext context,
  WidgetRef ref,
  Certificate cert,
) {
  showDialog<void>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Delete certificate'),
      content: Text('Delete ${cert.domain}?'),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Cancel'),
        ),
        TextButton(
          onPressed: () {
            ref
                .read(certificatesListProvider.notifier)
                .deleteCertificate(cert.id);
            Navigator.of(ctx).pop();
          },
          child: const Text('Delete'),
        ),
      ],
    ),
  );
}

// ---------------------------------------------------------------------------
// Error state
// ---------------------------------------------------------------------------

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.error, required this.loc});

  final Object error;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 60),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.error_outline, size: 48, color: AppColors.error),
            const SizedBox(height: AppSpacing.s12),
            Text(
              loc.failedToLoadCertificates,
              style: AppTypography.title.copyWith(color: AppColors.textPrimary),
            ),
            const SizedBox(height: AppSpacing.s4),
            Text(
              error.toString(),
              style: AppTypography.metadata.copyWith(
                color: AppColors.textMuted,
              ),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Skeleton loading state
// ---------------------------------------------------------------------------

class _SkeletonList extends StatefulWidget {
  const _SkeletonList();

  @override
  State<_SkeletonList> createState() => _SkeletonListState();
}

class _SkeletonListState extends State<_SkeletonList>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1200),
    )..repeat();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: List.generate(
        3,
        (_) => Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.s8),
          child: _SkeletonCard(controller: _controller),
        ),
      ),
    );
  }
}

class _SkeletonCard extends StatelessWidget {
  const _SkeletonCard({required this.controller});

  final AnimationController controller;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: controller,
      builder: (context, child) {
        final value = (controller.value * 2).clamp(0.0, 1.0);
        final opacity = value < 0.5
            ? 0.03 + (value * 0.06)
            : 0.09 - ((value - 0.5) * 0.06);
        return Container(
          padding: const EdgeInsets.all(AppSpacing.s16),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: opacity),
            borderRadius: BorderRadius.circular(AppRadius.card),
            border: Border.all(color: AppColors.border),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Header row skeleton
              Row(
                children: [
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: Colors.white.withValues(alpha: 0.05),
                      borderRadius: BorderRadius.circular(AppRadius.medium),
                    ),
                  ),
                  const SizedBox(width: AppSpacing.s12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Container(
                          height: 10,
                          width: 180,
                          decoration: BoxDecoration(
                            color: Colors.white.withValues(alpha: 0.06),
                            borderRadius: BorderRadius.circular(4),
                          ),
                        ),
                        const SizedBox(height: 6),
                        Container(
                          height: 8,
                          width: 100,
                          decoration: BoxDecoration(
                            color: Colors.white.withValues(alpha: 0.04),
                            borderRadius: BorderRadius.circular(4),
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: AppSpacing.s12),
                  Container(
                    height: 24,
                    width: 36,
                    decoration: BoxDecoration(
                      color: Colors.white.withValues(alpha: 0.05),
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: AppSpacing.s12),
              // Expiry bar skeleton
              Container(
                height: 4,
                decoration: BoxDecoration(
                  color: Colors.white.withValues(alpha: 0.04),
                  borderRadius: BorderRadius.circular(3),
                ),
              ),
              const SizedBox(height: AppSpacing.s12),
              // Action buttons skeleton
              Row(
                children: [
                  Container(
                    height: 24,
                    width: 60,
                    decoration: BoxDecoration(
                      color: Colors.white.withValues(alpha: 0.05),
                      borderRadius: BorderRadius.circular(AppRadius.medium),
                    ),
                  ),
                  const SizedBox(width: AppSpacing.s8),
                  Container(
                    height: 24,
                    width: 60,
                    decoration: BoxDecoration(
                      color: Colors.white.withValues(alpha: 0.05),
                      borderRadius: BorderRadius.circular(AppRadius.medium),
                    ),
                  ),
                ],
              ),
            ],
          ),
        );
      },
    );
  }
}
