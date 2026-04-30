import 'package:logger/logger.dart';

final appLogger = Logger(
  printer: PrettyPrinter(
    methodCount: 0,
    errorMethodCount: 5,
    lineLength: 80,
    colors: true,
    printEmojis: false,
    dateTimeFormat: DateTimeFormat.dateAndTime,
  ),
);
