import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../core/design/theme/accent_themes.dart';
import '../../../../core/design/theme/theme_controller.dart';
import '../../../../l10n/app_localizations.dart';
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
        final profile = authAsync.valueOrNull is AuthStateAuthenticated
            ? (authAsync.valueOrNull as AuthStateAuthenticated).profile
            : null;
        final loc = AppLocalizations.of(context)!;

        return SingleChildScrollView(
          padding: const EdgeInsets.all(AppSpacing.s16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // -- Appearance Section ----
              _SectionTitle(title: loc.titleAppearance),
              const SizedBox(height: AppSpacing.s8),
              _AppearanceSection(settings: settings, loc: loc),
              const SizedBox(height: AppSpacing.s20),

              // -- Connection Section ----
              _SectionTitle(title: loc.titleConnection),
              const SizedBox(height: AppSpacing.s8),
              _ConnectionSection(profile: profile, loc: loc),
              const SizedBox(height: AppSpacing.s20),

              // -- About Section ----
              _SectionTitle(title: loc.titleAbout),
              const SizedBox(height: AppSpacing.s8),
              _AboutSection(loc: loc),
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
  const _AppearanceSection({required this.settings, required this.loc});

  final ThemeSettings settings;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // -- Theme Mode ----
          Text(
            loc.titleThemeMode,
            style: AppTypography.bodyMedium.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: AppSpacing.s10),
          _ThemeModeToggles(currentMode: settings.themeMode, loc: loc),
          const SizedBox(height: AppSpacing.s20),

          // -- Accent Color ----
          Text(
            loc.titleAccentColor,
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
  const _ThemeModeToggles({required this.currentMode, required this.loc});

  final ThemeMode currentMode;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    final modes = [
      (ThemeMode.system, loc.valueThemeSystem),
      (ThemeMode.light, loc.valueThemeLight),
      (ThemeMode.dark, loc.valueThemeDark),
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
            onTap: entry.$1 == ThemeMode.dark ? () {} : null,
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
        cursor: onTap != null
            ? SystemMouseCursors.click
            : SystemMouseCursors.basic,
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
            onTap: () => ref
                .read(themeControllerProvider.notifier)
                .setAccent(accent.name),
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
                    ? const Icon(Icons.check, size: 20, color: Colors.white)
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
  const _ConnectionSection({required this.profile, required this.loc});

  final ClientProfile? profile;
  final AppLocalizations loc;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeMode = switch (profile?.activeMode) {
      ConnectionMode.management => loc.modeManagement,
      ConnectionMode.agent => loc.modeAgent,
      null => loc.statusNotConnected,
    };
    final masterUrl = profile?.masterUrl ?? '';
    final managementConfigured = profile?.management.isConfigured ?? false;
    final agent = profile?.agent ?? const AgentProfile();
    final agentRegistered = agent.isRegistered;

    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _ProfileRow(label: loc.labelActiveMode, value: activeMode),
          const SizedBox(height: AppSpacing.s12),
          _ProfileBlock(
            title: loc.titleManagementProfile,
            rows: [
              _ProfileRow(
                label: loc.labelMasterUrl,
                value: masterUrl.isNotEmpty
                    ? masterUrl
                    : loc.descNotConnectedMaster,
              ),
              _ProfileRow(
                label: loc.labelStatus,
                value: managementConfigured
                    ? loc.labelConfigured
                    : loc.labelNotConfigured,
              ),
            ],
            action: GlassButton.secondary(
              label: loc.btnClear,
              onPressed: managementConfigured
                  ? () => ref
                        .read(authNotifierProvider.notifier)
                        .clearManagement()
                  : null,
            ),
          ),
          const SizedBox(height: AppSpacing.s16),
          _ProfileBlock(
            title: loc.titleAgentProfile,
            rows: [
              _ProfileRow(
                label: loc.labelAgentId,
                value: agentRegistered ? agent.agentId : loc.labelNotRegistered,
              ),
              _ProfileRow(
                label: loc.labelStatus,
                value: agentRegistered
                    ? loc.statusRegistered
                    : loc.labelNotRegistered,
              ),
            ],
            action: GlassButton.secondary(
              label: loc.btnClear,
              onPressed: agentRegistered
                  ? () => ref.read(authNotifierProvider.notifier).clearAgent()
                  : null,
            ),
          ),
        ],
      ),
    );
  }
}

class _ProfileBlock extends StatelessWidget {
  const _ProfileBlock({
    required this.title,
    required this.rows,
    required this.action,
  });

  final String title;
  final List<Widget> rows;
  final Widget action;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                title,
                style: AppTypography.bodyMedium.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
            ),
            action,
          ],
        ),
        const SizedBox(height: AppSpacing.s8),
        ...rows.expand((row) => [row, const SizedBox(height: AppSpacing.s4)]),
      ],
    );
  }
}

class _ProfileRow extends StatelessWidget {
  const _ProfileRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 120,
          child: Text(
            label,
            style: AppTypography.metadata.copyWith(color: AppColors.textMuted),
          ),
        ),
        Expanded(
          child: Text(
            value,
            style: AppTypography.bodyMedium.copyWith(
              color: AppColors.textPrimary,
            ),
          ),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------------
// About section: app info
// ---------------------------------------------------------------------------

class _AboutSection extends StatelessWidget {
  const _AboutSection({required this.loc});

  final AppLocalizations loc;

  @override
  Widget build(BuildContext context) {
    return GlassCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _AboutRow(label: loc.labelApplication, value: loc.valueAppName),
          const SizedBox(height: AppSpacing.s10),
          _AboutRow(label: loc.labelVersion, value: loc.valueAppVersion),
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
          style: AppTypography.body.copyWith(color: AppColors.textSecondary),
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
