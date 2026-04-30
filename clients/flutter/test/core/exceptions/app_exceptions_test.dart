import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/exceptions/app_exceptions.dart';

void main() {
  test('NetworkException stores message', () {
    const e = NetworkException('timeout');
    expect(e.message, 'timeout');
    expect(e.toString(), 'timeout');
  });

  test('ServerException stores statusCode', () {
    const e = ServerException('fail', statusCode: 500);
    expect(e.statusCode, 500);
  });
}
