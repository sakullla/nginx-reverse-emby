import 'package:flutter/material.dart';
import '../tokens/app_colors.dart';
import '../tokens/app_spacing.dart';

// ---------------------------------------------------------------------------
// Glass Theme Data
//
// Builds a complete [ThemeData] for the glassmorphism design system from an
// [AccentColors] instance. The theme is dark-only since glassmorphism requires
// a dark translucent background to be legible.
// ---------------------------------------------------------------------------

abstract final class GlassThemeData {
  /// Background colour (dark navy).
  static const Color _bg = AppColors.bgStart;

  /// Surface colour (slate).
  static const Color _surface = AppColors.bgEnd;

  /// Surface container (lighter slate).
  static const Color _surfaceContainer = Color(0xFF334155);

  /// Builds a dark [ThemeData] configured for glassmorphism using [accent].
  static ThemeData build(AccentColors accent) {
    // Derive a seed-based colour scheme, then override surfaces.
    final baseScheme = ColorScheme.fromSeed(
      seedColor: accent.primaryStart,
      brightness: Brightness.dark,
    );

    final colorScheme = baseScheme.copyWith(
      primary: accent.primaryStart,
      secondary: accent.secondary,
      surface: _surface,
      surfaceContainerHighest: _surface,
      surfaceContainer: _surfaceContainer,
      onSurface: AppColors.textPrimary,
      onSurfaceVariant: AppColors.textSecondary,
    );

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.dark,
      colorScheme: colorScheme,
      scaffoldBackgroundColor: _bg,

      // -- Card: rounded, no traditional elevation ----------------------------
      cardTheme: CardThemeData(
        elevation: 0,
        color: _surface.withValues(alpha: AppColors.surfaceOpacityCard),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppRadius.card),
          side: const BorderSide(color: AppColors.border),
        ),
        margin: EdgeInsets.zero,
      ),

      // -- AppBar: transparent, blends with background -----------------------
      appBarTheme: AppBarTheme(
        centerTitle: false,
        backgroundColor: Colors.transparent,
        foregroundColor: AppColors.textPrimary,
        elevation: 0,
        scrolledUnderElevation: 0,
        titleTextStyle: const TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: AppColors.textPrimary,
        ),
      ),

      // -- Navigation Rail: glass styling ------------------------------------
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: _surface.withValues(alpha: AppColors.surfaceOpacityCard),
        indicatorColor: accent.primaryStart.withValues(alpha: 0.2),
        selectedIconTheme: IconThemeData(color: accent.primaryStart),
        unselectedIconTheme: IconThemeData(color: AppColors.textMuted),
        selectedLabelTextStyle: TextStyle(
          color: accent.primaryStart,
          fontWeight: FontWeight.w600,
          fontSize: 11,
        ),
        unselectedLabelTextStyle: const TextStyle(
          color: AppColors.textMuted,
          fontSize: 11,
        ),
        elevation: 0,
        minWidth: 72,
        minExtendedWidth: 200,
      ),

      // -- Navigation Bar (bottom): glass styling ----------------------------
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: _surface.withValues(alpha: AppColors.surfaceOpacityCard),
        indicatorColor: accent.primaryStart.withValues(alpha: 0.2),
        iconTheme: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return IconThemeData(color: accent.primaryStart);
          }
          return const IconThemeData(color: AppColors.textMuted);
        }),
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return TextStyle(
              color: accent.primaryStart,
              fontWeight: FontWeight.w600,
              fontSize: 11,
            );
          }
          return const TextStyle(
            color: AppColors.textMuted,
            fontSize: 11,
          );
        }),
        elevation: 0,
        height: 64,
      ),

      // -- Buttons -----------------------------------------------------------
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: accent.primaryStart,
          foregroundColor: Colors.white,
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s20,
            vertical: AppSpacing.s14,
          ),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppRadius.medium),
          ),
        ),
      ),

      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: accent.primaryStart,
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s20,
            vertical: AppSpacing.s14,
          ),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppRadius.medium),
          ),
          side: BorderSide(color: accent.primaryStart.withValues(alpha: 0.5)),
        ),
      ),

      // -- Input decoration --------------------------------------------------
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: _surfaceContainer.withValues(alpha: 0.6),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppRadius.medium),
          borderSide: const BorderSide(color: AppColors.border),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppRadius.medium),
          borderSide: const BorderSide(color: AppColors.border),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(AppRadius.medium),
          borderSide: BorderSide(color: accent.primaryStart, width: 2),
        ),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: AppSpacing.s16,
          vertical: AppSpacing.s14,
        ),
        hintStyle: const TextStyle(color: AppColors.textMuted),
      ),

      // -- Snack bar ---------------------------------------------------------
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppRadius.medium),
        ),
        backgroundColor: _surfaceContainer,
        contentTextStyle: const TextStyle(color: AppColors.textPrimary),
      ),

      // -- Divider -----------------------------------------------------------
      dividerTheme: const DividerThemeData(
        color: AppColors.border,
        space: 1,
      ),

      // -- Page transitions --------------------------------------------------
      pageTransitionsTheme: const PageTransitionsTheme(
        builders: {
          TargetPlatform.android: PredictiveBackPageTransitionsBuilder(),
          TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
          TargetPlatform.windows: FadeUpwardsPageTransitionsBuilder(),
          TargetPlatform.macOS: FadeUpwardsPageTransitionsBuilder(),
          TargetPlatform.linux: FadeUpwardsPageTransitionsBuilder(),
        },
      ),
    );
  }
}
