import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';
import 'package:nre_client/features/certificates/presentation/providers/certificates_provider.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  test('certificatesList loads selected agent certificates', () async {
    final api = _MockPanelApiClient();
    when(() => api.fetchCertificates('local')).thenAnswer(
      (_) async => const [Certificate(id: '21', domain: 'emby.example.com')],
    );
    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWith((ref) => 'local'),
        panelApiClientProvider.overrideWith((ref) => api),
      ],
    );
    addTearDown(container.dispose);

    final certificates = await container.read(certificatesListProvider.future);

    expect(certificates.single.domain, 'emby.example.com');
  });

  test('filteredCertificates uses backend status when present', () async {
    final api = _MockPanelApiClient();
    when(() => api.fetchCertificates('local')).thenAnswer(
      (_) async => const [
        Certificate(
          id: '21',
          domain: 'emby.example.com',
          backendStatus: 'active',
        ),
        Certificate(
          id: '22',
          domain: 'pending.example.com',
          backendStatus: 'pending',
        ),
      ],
    );
    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWith((ref) => 'local'),
        panelApiClientProvider.overrideWith((ref) => api),
      ],
    );
    addTearDown(container.dispose);

    await container.read(certificatesListProvider.future);
    container
        .read(certStatusFilterNotifierProvider.notifier)
        .update(CertStatusFilter.active);

    final filtered = container.read(filteredCertificatesProvider);

    expect(filtered.map((cert) => cert.domain), ['emby.example.com']);
  });
}
