import 'package:flutter/material.dart';

enum AppColorScheme { sakuraPink, electricCyan, neonViolet, cyberGreen }

extension AppColorSchemeColors on AppColorScheme {
  Color get primaryLight => switch (this) {
    AppColorScheme.sakuraPink => const Color(0xFFEC407A),
    AppColorScheme.electricCyan => const Color(0xFF00BCD4),
    AppColorScheme.neonViolet => const Color(0xFF7C4DFF),
    AppColorScheme.cyberGreen => const Color(0xFF00E676),
  };

  Color get primaryDark => switch (this) {
    AppColorScheme.sakuraPink => const Color(0xFFF48FB1),
    AppColorScheme.electricCyan => const Color(0xFF00E5FF),
    AppColorScheme.neonViolet => const Color(0xFFB388FF),
    AppColorScheme.cyberGreen => const Color(0xFF69F0AE),
  };

  Color get secondary => switch (this) {
    AppColorScheme.sakuraPink => const Color(0xFFFF80AB),
    AppColorScheme.electricCyan => const Color(0xFF18FFFF),
    AppColorScheme.neonViolet => const Color(0xFFE040FB),
    AppColorScheme.cyberGreen => const Color(0xFF76FF03),
  };

  String get displayName => switch (this) {
    AppColorScheme.sakuraPink => 'Sakura Pink',
    AppColorScheme.electricCyan => 'Electric Cyan',
    AppColorScheme.neonViolet => 'Neon Violet',
    AppColorScheme.cyberGreen => 'Cyber Green',
  };
}
