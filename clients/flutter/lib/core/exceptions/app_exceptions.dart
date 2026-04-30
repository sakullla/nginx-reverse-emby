sealed class AppException implements Exception {
  final String message;
  final String? code;
  const AppException(this.message, {this.code});

  @override
  String toString() => message;
}

class NetworkException extends AppException {
  const NetworkException(super.message, {super.code});
}

class AuthException extends AppException {
  const AuthException(super.message, {super.code});
}

class ValidationException extends AppException {
  const ValidationException(super.message, {super.code});
}

class NotFoundException extends AppException {
  const NotFoundException(super.message, {super.code});
}

class ServerException extends AppException {
  final int statusCode;
  const ServerException(super.message, {required this.statusCode, super.code});
}
