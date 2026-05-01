import 'package:flutter/material.dart';

// ---------------------------------------------------------------------------
// Design Tokens: Colors
//
// Surface colors support the glassmorphism design language. Accent themes are
// defined as const instances of [AccentColors] in [AccentThemeRegistry].
// ---------------------------------------------------------------------------

/// Surface and glassmorphism colours shared across all accent themes.
abstract final class AppColors {
  // -- Background gradient ---------------------------------------------------

  /// Gradient start colour (dark navy).
  static const Color bgStart = Color(0xFF0F172A);

  /// Gradient end colour (lighter slate).
  static const Color bgEnd = Color(0xFF1E293B);

  /// Convenience [LinearGradient] matching the spec (135 deg).
  static const LinearGradient backgroundGradient = LinearGradient(
    begin: Alignment(-0.5, -0.5), // ~135 deg in Alignment space
    end: Alignment(0.5, 0.5),
    colors: [bgStart, bgEnd],
  );

  // -- Surface opacities (apply over white) ----------------------------------

  static const double surfaceOpacityDisabled = 0.03;
  static const double surfaceOpacityInner = 0.04;
  static const double surfaceOpacityCard = 0.06;
  static const double surfaceOpacityBorder = 0.08;
  static const double surfaceOpacityHover = 0.10;

  // -- Borders ---------------------------------------------------------------

  static const Color border = Color(0x14FFFFFF); // rgba(255,255,255,0.08)

  // -- Status colours --------------------------------------------------------

  static const Color success = Color(0xFF4ADE80);
  static const Color warning = Color(0xFFFBBF24);
  static const Color error = Color(0xFFF87171);
  static const Color info = Color(0xFF818CF8);

  // -- Common text colours ---------------------------------------------------

  static const Color textPrimary = Color(0xFFFFFFFF);
  static const Color textSecondary = Color(0xB3FFFFFF); // 70% white
  static const Color textMuted = Color(0x80FFFFFF); // 50% white
}

// ---------------------------------------------------------------------------
// Accent theme colour definitions
// ---------------------------------------------------------------------------

/// An immutable palette for a single accent theme.
///
/// Use [AccentThemeRegistry.all] to iterate every theme, or
/// [AccentThemeRegistry.byName] for a specific one.
class AccentColors {
  const AccentColors({
    required this.name,
    required this.primaryStart,
    required this.primaryEnd,
    required this.secondary,
  });

  final String name;
  final Color primaryStart;
  final Color primaryEnd;
  final Color secondary;

  /// Linear gradient from [primaryStart] to [primaryEnd].
  LinearGradient get primaryGradient => LinearGradient(
        colors: [primaryStart, primaryEnd],
      );
}

// -- Registry ---------------------------------------------------------------

/// Registry of all accent themes, indexed by name for look-up.
abstract final class AccentThemeRegistry {
  static const AccentColors indigoPurple = AccentColors(
    name: 'Indigo Purple',
    primaryStart: Color(0xFF6366F1),
    primaryEnd: Color(0xFF8B5CF6),
    secondary: Color(0xFF818CF8),
  );

  static const AccentColors cyanBlue = AccentColors(
    name: 'Cyan Blue',
    primaryStart: Color(0xFF06B6D4),
    primaryEnd: Color(0xFF3B82F6),
    secondary: Color(0xFF22D3EE),
  );

  static const AccentColors rosePink = AccentColors(
    name: 'Rose Pink',
    primaryStart: Color(0xFFF43F5E),
    primaryEnd: Color(0xFFEC4899),
    secondary: Color(0xFFFB7185),
  );

  static const AccentColors emeraldGreen = AccentColors(
    name: 'Emerald Green',
    primaryStart: Color(0xFF10B981),
    primaryEnd: Color(0xFF22C55E),
    secondary: Color(0xFF34D399),
  );

  /// All accent themes in canonical order.
  static const List<AccentColors> all = [
    indigoPurple,
    cyanBlue,
    rosePink,
    emeraldGreen,
  ];

  /// Returns the accent theme matching [name], or the first theme as fallback.
  static AccentColors byName(String name) =>
      all.firstWhere((a) => a.name == name, orElse: () => all.first);
}
