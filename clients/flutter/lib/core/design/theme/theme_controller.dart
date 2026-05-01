import 'package:flutter/material.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../tokens/app_colors.dart';
import 'accent_themes.dart';
import 'glass_theme_data.dart';

part 'theme_controller.g.dart';

// ---------------------------------------------------------------------------
// Theme Controller State
// ---------------------------------------------------------------------------

/// Immutable theme settings persisted across app launches.
class ThemeSettings {
  const ThemeSettings({
    required this.themeMode,
    required this.accent,
    required this.themeData,
  });

  /// Always [ThemeMode.dark] for glassmorphism, but kept for future use.
  final ThemeMode themeMode;

  /// Currently selected accent colours.
  final AccentColors accent;

  /// Fully built [ThemeData] ready for [MaterialApp.theme].
  final ThemeData themeData;
}

// ---------------------------------------------------------------------------
// Theme Controller
// ---------------------------------------------------------------------------

@riverpod
class ThemeController extends _$ThemeController {
  static const _accentKey = 'accent_theme';

  @override
  Future<ThemeSettings> build() async {
    final prefs = await SharedPreferences.getInstance();
    final savedName = prefs.getString(_accentKey);
    final accent = savedName != null
        ? AccentThemeRegistry.byName(savedName)
        : AccentThemes.defaults;
    return ThemeSettings(
      themeMode: ThemeMode.dark,
      accent: accent,
      themeData: GlassThemeData.build(accent),
    );
  }

  /// Persist a new accent theme by [themeName].
  ///
  /// [themeName] should match an entry in [AccentThemeRegistry] (e.g.
  /// `AccentThemeKeys.indigo`).
  Future<void> setAccent(String themeName) async {
    final accent = AccentThemeRegistry.byName(themeName);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_accentKey, themeName);
    state = AsyncData(ThemeSettings(
      themeMode: ThemeMode.dark,
      accent: accent,
      themeData: GlassThemeData.build(accent),
    ));
  }
}
