import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/master_api.dart';

class MockDio extends Mock implements Dio {}

class FakeRequestOptions extends Fake implements RequestOptions {}

void main() {
  late MockDio mockDio;
  late MasterApi api;

  setUpAll(() {
    registerFallbackValue(FakeRequestOptions());
  });

  setUp(() {
    mockDio = MockDio();
    api = MasterApi(dio: mockDio);
  });

  test('getRules returns list of ProxyRule', () async {
    when(() => mockDio.get('/api/rules')).thenAnswer(
      (_) async => Response(
        data: [
          {
            'id': '1',
            'domain': 'example.com',
            'target': 'localhost:8080',
            'type': 'http',
            'enabled': true,
          },
        ],
        statusCode: 200,
        requestOptions: RequestOptions(),
      ),
    );

    final rules = await api.getRules();
    expect(rules, hasLength(1));
    expect(rules.first.domain, 'example.com');
  });

  test('getRules accepts backend-shaped HTTP rule response', () async {
    when(() => mockDio.get('/api/rules')).thenAnswer(
      (_) async => Response(
        data: {
          'rules': [
            {
              'id': 7,
              'frontend_url': 'https://emby.example.com',
              'backend_url': 'http://emby:8096',
              'enabled': false,
            },
          ],
        },
        statusCode: 200,
        requestOptions: RequestOptions(),
      ),
    );

    final rules = await api.getRules();

    expect(rules.single.id, '7');
    expect(rules.single.domain, 'https://emby.example.com');
    expect(rules.single.target, 'http://emby:8096');
    expect(rules.single.type, 'http');
    expect(rules.single.enabled, isFalse);
  });

  test('getRules throws on 500', () async {
    when(() => mockDio.get('/api/rules')).thenThrow(
      DioException(
        response: Response(statusCode: 500, requestOptions: RequestOptions()),
        requestOptions: RequestOptions(),
      ),
    );

    expect(() => api.getRules(), throwsException);
  });
}
