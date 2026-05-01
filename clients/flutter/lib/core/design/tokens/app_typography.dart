import 'package:flutter/material.dart';

// ---------------------------------------------------------------------------
// Design Tokens: Typography
//
// Text style constants for the glassmorphism design system. Colours are not
// baked into these styles — consumers should apply AppColors text tokens when
// using these styles so that colour stays theme-aware.
// ---------------------------------------------------------------------------

abstract final class AppTypography {
  // -- Title -----------------------------------------------------------------

  static const TextStyle title = TextStyle(
    fontSize: 14.0,
    fontWeight: FontWeight.w600,
    height: 1.4,
  );

  // -- Body ------------------------------------------------------------------

  static const TextStyle body = TextStyle(
    fontSize: 12.0,
    fontWeight: FontWeight.w400,
    height: 1.5,
  );

  static const TextStyle bodyMedium = TextStyle(
    fontSize: 12.0,
    fontWeight: FontWeight.w500,
    height: 1.5,
  );

  // -- Metadata --------------------------------------------------------------

  static const TextStyle metadata = TextStyle(
    fontSize: 11.0,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );

  static const TextStyle metadataSmall = TextStyle(
    fontSize: 10.0,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );

  // -- Label -----------------------------------------------------------------

  static const TextStyle label = TextStyle(
    fontSize: 9.0,
    fontWeight: FontWeight.w500,
    letterSpacing: 0.5,
    height: 1.4,
  );

  // -- Stat numbers ----------------------------------------------------------

  static const TextStyle statNumber = TextStyle(
    fontSize: 24.0,
    fontWeight: FontWeight.w700,
    height: 1.2,
  );

  static const TextStyle statNumberSmall = TextStyle(
    fontSize: 22.0,
    fontWeight: FontWeight.w700,
    height: 1.2,
  );
}
