import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/data/repositories/auth_repository.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class MockAuthRepository extends Mock implements AuthRepository {}

void main() {
  setUpAll(() {
    registerFallbackValue(const ClientProfile());
  });

  test(
    'connectManagement saves panel token profile without agent credentials',
    () async {
      final repo = MockAuthRepository();
      when(repo.loadProfile).thenAnswer((_) async => const ClientProfile());
      when(() => repo.saveProfile(any())).thenAnswer((_) async {});
      final container = ProviderContainer(
        overrides: [authRepositoryProvider.overrideWithValue(repo)],
      );
      addTearDown(container.dispose);

      await container.read(authNotifierProvider.future);
      await container
          .read(authNotifierProvider.notifier)
          .connectManagement(
            masterUrl: 'https://panel.example.com/panel-api/',
            panelToken: 'panel-secret',
            name: 'ops-laptop',
          );

      final state = container.read(authNotifierProvider).value;
      expect(state, isA<AuthStateAuthenticated>());
      final profile = (state as AuthStateAuthenticated).profile;
      expect(profile.masterUrl, 'https://panel.example.com');
      expect(profile.activeMode, ConnectionMode.management);
      expect(profile.management.panelToken, 'panel-secret');
      expect(profile.hasAgentCredentials, isFalse);
      final saved = verify(() => repo.saveProfile(captureAny())).captured;
      expect(saved, hasLength(1));
      final savedProfile = saved.single as ClientProfile;
      expect(savedProfile.masterUrl, 'https://panel.example.com');
      expect(savedProfile.displayName, 'ops-laptop');
      expect(savedProfile.activeMode, ConnectionMode.management);
      expect(savedProfile.management.panelToken, 'panel-secret');
    },
  );

  test('build authenticates management-only saved profile', () async {
    final repo = MockAuthRepository();
    final loaded = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
    );
    when(repo.loadProfile).thenAnswer((_) async => loaded);
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    final state = await container.read(authNotifierProvider.future);

    expect(state, isA<AuthStateAuthenticated>());
    final profile = (state as AuthStateAuthenticated).profile;
    expect(profile.hasManagementCredentials, isTrue);
    expect(profile.isRegistered, isFalse);
  });

  test('connectManagement rejects invalid URL and does not save', () async {
    final repo = MockAuthRepository();
    when(repo.loadProfile).thenAnswer((_) async => const ClientProfile());
    when(() => repo.saveProfile(any())).thenAnswer((_) async {});
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    await container.read(authNotifierProvider.future);
    await container
        .read(authNotifierProvider.notifier)
        .connectManagement(
          masterUrl: 'panel.example.com',
          panelToken: 'panel-secret',
          name: 'ops-laptop',
        );

    final state = container.read(authNotifierProvider).value;
    expect(state, isA<AuthStateError>());
    verifyNever(() => repo.saveProfile(any()));
  });

  test('clearManagement preserves agent profile', () async {
    final repo = MockAuthRepository();
    final loaded = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );
    when(repo.loadProfile).thenAnswer((_) async => loaded);
    when(() => repo.saveProfile(any())).thenAnswer((_) async {});
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    await container.read(authNotifierProvider.future);
    await container.read(authNotifierProvider.notifier).clearManagement();

    final state = container.read(authNotifierProvider).value;
    final profile = (state as AuthStateAuthenticated).profile;
    expect(profile.hasManagementCredentials, isFalse);
    expect(profile.agent.agentId, 'agent-1');
    expect(profile.activeMode, ConnectionMode.agent);
  });

  test('clearAgent preserves management profile', () async {
    final repo = MockAuthRepository();
    final loaded = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.agent,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );
    when(repo.loadProfile).thenAnswer((_) async => loaded);
    when(() => repo.saveProfile(any())).thenAnswer((_) async {});
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    await container.read(authNotifierProvider.future);
    await container.read(authNotifierProvider.notifier).clearAgent();

    final state = container.read(authNotifierProvider).value;
    final profile = (state as AuthStateAuthenticated).profile;
    expect(profile.hasAgentCredentials, isFalse);
    expect(profile.management.panelToken, 'panel-secret');
    expect(profile.activeMode, ConnectionMode.management);
  });
}
