import 'package:logger/logger.dart';

final _logger = Logger(
  printer: PrettyPrinter(
    methodCount: 2,
    errorMethodCount: 8,
    lineLength: 120,
    colors: true,
    printEmojis: true,
  ),
);

class AppLogger {
  static void d(String message) => _logger.d(message);
  static void i(String message) => _logger.i(message);
  static void w(String message) => _logger.w(message);
  static void e(String message, {Object? error, StackTrace? stackTrace}) =>
      _logger.e(message, error: error, stackTrace: stackTrace);
}
