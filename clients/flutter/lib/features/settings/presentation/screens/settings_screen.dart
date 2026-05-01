import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/design/theme/accent_themes.dart';
import '../../../../core/design/theme/theme_controller.dart';
import '../../../auth/data/models/auth_models.dart';
import '../../../auth/presentation/providers/auth_provider.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final themeAsync = ref.watch(themeControllerProvider);
    final authAsync = ref.watch(authNotifierProvider);

    return themeAsync.when(
      data: (settings) {
        final masterUrl = authAsync.valueOrNull is AuthStateAuthenticated
            ? (authAsync.valueOrNull as AuthStateAuthenticated).profile.masterUrl
            : '';

        return SingleChildScrollView(
          padding: const EdgeInsets.all(AppSpacing.s16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // -- Appearance Section ----
              _SectionTitle(title: 'Appearance'),
              const SizedBox(height: AppSpacing.s8),
              _AppearanceSection(settings: settings),
              const SizedBox(height: AppSpacing.s20),

              // -- Connection Section ----
              _SectionTitle(title: 'Connection'),
              const SizedBox(height: AppSpacing.s8),
              _ConnectionSection(masterUrl: masterUrl),
              const SizedBox(height: AppSpacing.s20),

              // -- About Section ----
              _SectionTitle(title: 'About'),
              const SizedBox(height: AppSpacing.s8),
              const _AboutSection(),
            ],
          ),
        );
      },
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => const Center(child: Text('Error')),
    );
  }
}

// ---------------------------------------------------------------------------
// Section title: uppercase muted label
// ---------------------------------------------------------------------------

class _SectionTitle extends StatelessWidget {
  const _SectionTitle({required this.title});

  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.s4),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w600,
          color: AppColors.textMuted,
          letterSpacing: 0.8,
          height: 1.4,
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Appearance section: theme mode toggles + accent color swatches
// ---------------------------------------------------------------------------

class _AppearanceSection extends ConsumerWidget {
  const _AppearanceSection({required this.settings});

  final ThemeSettings settings;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // -- Theme Mode ----
          Text(
            'Theme Mode',
            style: AppTypography.bodyMedium.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: AppSpacing.s10),
          _ThemeModeToggles(currentMode: settings.themeMode),
          const SizedBox(height: AppSpacing.s20),

          // -- Accent Color ----
          Text(
            'Accent Color',
            style: AppTypography.bodyMedium.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: AppSpacing.s10),
          _AccentSwatches(activeName: settings.accent.name),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Theme mode toggle row: System / Light / Dark (only Dark is functional)
// ---------------------------------------------------------------------------

class _ThemeModeToggles extends StatelessWidget {
  const _ThemeModeToggles({required this.currentMode});

  final ThemeMode currentMode;

  @override
  Widget build(BuildContext context) {
    final modes = [
      (ThemeMode.system, 'System'),
      (ThemeMode.light, 'Light'),
      (ThemeMode.dark, 'Dark'),
    ];

    return Row(
      children: modes.map((entry) {
        final isSelected = currentMode == entry.$1;
        return Padding(
          padding: const EdgeInsets.only(right: AppSpacing.s8),
          child: _ThemeModeButton(
            label: entry.$2,
            isSelected: isSelected,
            // Only Dark is functional for glassmorphism
            onTap: entry.$1 == ThemeMode.dark
                ? () {}
                : null,
          ),
        );
      }).toList(),
    );
  }
}

class _ThemeModeButton extends StatelessWidget {
  const _ThemeModeButton({
    required this.label,
    required this.isSelected,
    this.onTap,
  });

  final String label;
  final bool isSelected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      child: MouseRegion(
        cursor: onTap != null ? SystemMouseCursors.click : SystemMouseCursors.basic,
        child: Container(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s14,
            vertical: AppSpacing.s8,
          ),
          decoration: BoxDecoration(
            gradient: isSelected
                ? LinearGradient(colors: [AppColors.info, AppColors.info])
                : null,
            color: isSelected
                ? null
                : Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(
              color: isSelected
                  ? AppColors.info.withValues(alpha: 0.4)
                  : AppColors.border,
            ),
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w500,
              color: isSelected ? Colors.white : AppColors.textMuted,
              height: 1.4,
            ),
          ),
        ),
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// Accent color swatches: 4 color options in a row
// ---------------------------------------------------------------------------

class _AccentSwatches extends ConsumerWidget {
  const _AccentSwatches({required this.activeName});

  final String activeName;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: AccentThemes.all.map((accent) {
        final isActive = accent.name == activeName;
        return Padding(
          padding: const EdgeInsets.only(right: AppSpacing.s12),
          child: GestureDetector(
            onTap: () =>
                ref.read(themeControllerProvider.notifier).setAccent(accent.name),
            child: MouseRegion(
              cursor: SystemMouseCursors.click,
              child: Container(
                width: 48,
                height: 48,
                decoration: BoxDecoration(
                  gradient: accent.primaryGradient,
                  borderRadius: BorderRadius.circular(12),
                  border: isActive
                      ? Border.all(color: Colors.white, width: 2.5)
                      : Border.all(color: AppColors.border),
                  boxShadow: isActive
                      ? [
                          BoxShadow(
                            color: accent.primaryStart.withValues(alpha: 0.3),
                            blurRadius: 8,
                            spreadRadius: 1,
                          ),
                        ]
                      : null,
                ),
                child: isActive
                    ? const Icon(
                        Icons.check,
                        size: 20,
                        color: Colors.white,
                      )
                    : null,
              ),
            ),
          ),
        );
      }).toList(),
    );
  }
}

// ---------------------------------------------------------------------------
// Connection section: master URL + disconnect
// ---------------------------------------------------------------------------

class _ConnectionSection extends ConsumerWidget {
  const _ConnectionSection({required this.masterUrl});

  final String masterUrl;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Master URL',
            style: AppTypography.bodyMedium.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: AppSpacing.s4),
          Text(
            masterUrl.isNotEmpty ? masterUrl : 'Not connected',
            style: AppTypography.metadata.copyWith(
              color: AppColors.textMuted,
            ),
          ),
          const SizedBox(height: AppSpacing.s16),
          GlassButton.danger(
            label: 'Disconnect',
            onPressed: () => ref.read(authNotifierProvider.notifier).logout(),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------------
// About section: app info
// ---------------------------------------------------------------------------

class _AboutSection extends StatelessWidget {
  const _AboutSection();

  @override
  Widget build(BuildContext context) {
    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _AboutRow(label: 'Application', value: 'NRE Client'),
          const SizedBox(height: AppSpacing.s10),
          _AboutRow(label: 'Version', value: 'v2.1.0'),
        ],
      ),
    );
  }
}

class _AboutRow extends StatelessWidget {
  const _AboutRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(
          label,
          style: AppTypography.body.copyWith(
            color: AppColors.textSecondary,
          ),
        ),
        Text(
          value,
          style: AppTypography.bodyMedium.copyWith(
            color: AppColors.textPrimary,
          ),
        ),
      ],
    );
  }
}
