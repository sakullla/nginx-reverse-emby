import 'package:dio/dio.dart';
import '../exceptions/app_exceptions.dart';
import '../logger/app_logger.dart';

class DioClient {
  late final Dio dio;

  DioClient({required String baseUrl, required String token}) {
    dio = Dio(BaseOptions(
      baseUrl: baseUrl,
      connectTimeout: const Duration(seconds: 10),
      receiveTimeout: const Duration(seconds: 10),
      headers: {'Authorization': 'Bearer $token'},
    ));

    dio.interceptors.addAll([
      LogInterceptor(
        requestBody: true,
        responseBody: true,
        logPrint: (obj) => AppLogger.d(obj.toString()),
      ),
      _ErrorInterceptor(),
    ]);
  }
}

class _ErrorInterceptor extends Interceptor {
  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    final response = err.response;
    final status = response?.statusCode ?? 0;
    final data = response?.data;
    final message = data is Map ? data['error'] ?? data['message'] : null;

    final exception = switch (status) {
      401 => const AuthException('认证失败，请重新连接'),
      403 => const AuthException('权限不足'),
      404 => NotFoundException(message?.toString() ?? '资源不存在'),
      422 => ValidationException(message?.toString() ?? '请求参数错误'),
      >= 500 => ServerException(
          message?.toString() ?? '服务器错误',
          statusCode: status,
        ),
      _ => switch (err.type) {
          DioExceptionType.connectionTimeout ||
          DioExceptionType.receiveTimeout ||
          DioExceptionType.sendTimeout =>
            const NetworkException('连接超时，请检查网络'),
          DioExceptionType.connectionError =>
            const NetworkException('无法连接到服务器'),
          _ => NetworkException('请求失败: ${err.message}'),
        },
    };

    handler.reject(DioException(
      requestOptions: err.requestOptions,
      error: exception,
      response: err.response,
      type: err.type,
    ));
  }
}
