import 'package:flutter/material.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'color_schemes.dart';

part 'theme_controller.g.dart';

class ThemeSettings {
  const ThemeSettings({
    required this.themeMode,
    required this.colorScheme,
  });

  final ThemeMode themeMode;
  final AppColorScheme colorScheme;
}

@riverpod
class ThemeController extends _$ThemeController {
  static const _modeKey = 'theme_mode';
  static const _schemeKey = 'color_scheme';

  @override
  Future<ThemeSettings> build() async {
    final prefs = await SharedPreferences.getInstance();
    final modeIndex = prefs.getInt(_modeKey) ?? 0;
    final schemeIndex = prefs.getInt(_schemeKey) ?? 0;
    return ThemeSettings(
      themeMode: ThemeMode.values[modeIndex.clamp(0, ThemeMode.values.length - 1)],
      colorScheme: AppColorScheme.values[schemeIndex.clamp(0, AppColorScheme.values.length - 1)],
    );
  }

  Future<void> setThemeMode(ThemeMode mode) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_modeKey, mode.index);
    final current = await future;
    state = AsyncData(ThemeSettings(themeMode: mode, colorScheme: current.colorScheme));
  }

  Future<void> setColorScheme(AppColorScheme scheme) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_schemeKey, scheme.index);
    final current = await future;
    state = AsyncData(ThemeSettings(themeMode: current.themeMode, colorScheme: scheme));
  }
}
