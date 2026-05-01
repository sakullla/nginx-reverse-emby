import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/core/network/panel_api_provider.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';
import 'package:nre_client/features/certificates/presentation/screens/certificates_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

class _MockPanelApiClient extends Mock implements PanelApiClient {}

void main() {
  setUpAll(() {
    registerFallbackValue(
      const CreateCertificateRequest(domain: 'fallback.example.com'),
    );
  });

  testWidgets('certificate actions open dialogs and renew via provider', (
    tester,
  ) async {
    final api = _MockPanelApiClient();
    final cert = Certificate(
      id: 'cert-1',
      domain: 'emby.example.com',
      expiresAt: DateTime.now().add(const Duration(days: 2)),
    );
    when(() => api.fetchCertificates('local')).thenAnswer((_) async => [cert]);
    when(() => api.createCertificate('local', any())).thenAnswer(
      (_) async => const Certificate(id: 'cert-2', domain: 'new.example.com'),
    );
    when(() => api.issueCertificate('local', 'cert-1')).thenAnswer(
      (_) async => Certificate(
        id: 'cert-1',
        domain: 'emby.example.com',
        expiresAt: DateTime.now().add(const Duration(days: 90)),
      ),
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          selectedAgentIdProvider.overrideWith((ref) => 'local'),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: CertificatesScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    await tester.tap(find.text('Import'));
    await tester.pumpAndSettle();
    expect(find.text('Import certificate'), findsOneWidget);
    await tester.enterText(find.byType(TextField).first, 'new.example.com');
    await tester.enterText(
      find.widgetWithText(TextField, 'Certificate PEM'),
      '---CERT---',
    );
    await tester.enterText(
      find.widgetWithText(TextField, 'Private key PEM'),
      '---KEY---',
    );
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    final importVerification = verify(
      () => api.createCertificate('local', captureAny()),
    );
    importVerification.called(1);
    final importRequest =
        importVerification.captured.single as CreateCertificateRequest;
    expect(importRequest.domain, 'new.example.com');
    expect(importRequest.certificateType, 'uploaded');
    expect(importRequest.targetAgentIds, ['local']);

    await tester.tap(find.text('Details').first);
    await tester.pumpAndSettle();
    expect(find.text('Certificate details'), findsOneWidget);
    await tester.tap(find.text('Close'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Renew').first);
    await tester.pumpAndSettle();
    verify(() => api.issueCertificate('local', 'cert-1')).called(1);
  });

  testWidgets('certificate import requires certificate and private key pem', (
    tester,
  ) async {
    final api = _MockPanelApiClient();
    when(
      () => api.fetchCertificates('local'),
    ).thenAnswer((_) async => const []);

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          selectedAgentIdProvider.overrideWith((ref) => 'local'),
          panelApiClientProvider.overrideWith((ref) => api),
        ],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: CertificatesScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    await tester.tap(find.text('Import').first);
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'new.example.com');
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();

    expect(
      find.text('Certificate PEM and private key are required'),
      findsOneWidget,
    );
    verifyNever(() => api.createCertificate('local', any()));
  });
}
