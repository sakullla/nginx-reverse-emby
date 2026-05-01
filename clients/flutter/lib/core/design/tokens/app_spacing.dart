// ---------------------------------------------------------------------------
// Design Tokens: Spacing, Radius, Blur
//
// All values follow the 4 px grid system defined in the spec.
// ---------------------------------------------------------------------------

/// Spacing values on the 4 px grid.
abstract final class AppSpacing {
  static const double s4 = 4.0;
  static const double s8 = 8.0;
  static const double s10 = 10.0;
  static const double s12 = 12.0;
  static const double s14 = 14.0;
  static const double s16 = 16.0;
  static const double s20 = 20.0;
}

/// Border-radius tokens.
abstract final class AppRadius {
  static const double small = 6.0;
  static const double medium = 8.0;
  static const double tag = 10.0;
  static const double card = 12.0;
  static const double largeCard = 14.0;
}

/// Backdrop-blur / filter-blur tokens.
abstract final class AppBlur {
  static const double subtle = 10.0;
  static const double standard = 20.0;
  static const double heavy = 40.0;
}
