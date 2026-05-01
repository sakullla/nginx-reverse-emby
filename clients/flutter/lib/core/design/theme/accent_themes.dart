import '../tokens/app_colors.dart';

// ---------------------------------------------------------------------------
// Accent Themes
//
// Named accent theme definitions backed by [AccentColors] from the design
// token layer. Exposes both individual constants and a registry map/list for
// use in the theme picker and theme controller.
// ---------------------------------------------------------------------------

/// Canonical key for each accent theme, matching [AccentThemeRegistry] names.
abstract final class AccentThemeKeys {
  static const String indigo = 'Indigo Purple';
  static const String cyan = 'Cyan Blue';
  static const String rose = 'Rose Pink';
  static const String emerald = 'Emerald Green';
}

/// Convenient typed accessors for the four accent themes.
///
/// Use [AccentThemeRegistry.all] for iteration or
/// [AccentThemeRegistry.byName] for user-persisted look-ups.
abstract final class AccentThemes {
  static AccentColors get indigo => AccentThemeRegistry.indigoPurple;
  static AccentColors get cyan => AccentThemeRegistry.cyanBlue;
  static AccentColors get rose => AccentThemeRegistry.rosePink;
  static AccentColors get emerald => AccentThemeRegistry.emeraldGreen;

  /// Map of theme key -> [AccentColors] for the settings picker.
  static const Map<String, AccentColors> byKey = {
    AccentThemeKeys.indigo: AccentThemeRegistry.indigoPurple,
    AccentThemeKeys.cyan: AccentThemeRegistry.cyanBlue,
    AccentThemeKeys.rose: AccentThemeRegistry.rosePink,
    AccentThemeKeys.emerald: AccentThemeRegistry.emeraldGreen,
  };

  /// All themes in canonical order (same as [AccentThemeRegistry.all]).
  static const List<AccentColors> all = AccentThemeRegistry.all;

  /// Default accent theme.
  static const AccentColors defaults = AccentThemeRegistry.indigoPurple;
}
