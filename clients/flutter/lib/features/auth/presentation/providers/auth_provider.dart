import 'package:riverpod_annotation/riverpod_annotation.dart';
import '../../data/models/auth_models.dart';
import '../../data/repositories/auth_repository.dart';

part 'auth_provider.g.dart';

final authRepositoryProvider = Provider((ref) => AuthRepository());

@riverpod
class AuthNotifier extends _$AuthNotifier {
  @override
  Future<AuthState> build() async {
    final repo = ref.read(authRepositoryProvider);
    final profile = await repo.loadProfile();
    return profile.isRegistered
        ? AuthStateAuthenticated(profile)
        : const AuthStateUnauthenticated();
  }

  Future<void> register({
    required String masterUrl,
    required String registerToken,
    required String name,
  }) async {
    state = const AsyncData(AuthStateLoading());

    try {
      final profile = ClientProfile(
        masterUrl: masterUrl,
        displayName: name,
        agentId: 'agent-${DateTime.now().millisecondsSinceEpoch}',
        token: 'tok-${DateTime.now().millisecondsSinceEpoch}',
      );

      await ref.read(authRepositoryProvider).saveProfile(profile);
      state = AsyncData(AuthStateAuthenticated(profile));
    } catch (e) {
      state = AsyncData(AuthStateError(e.toString()));
    }
  }

  Future<void> logout() async {
    await ref.read(authRepositoryProvider).clearProfile();
    state = const AsyncData(AuthStateUnauthenticated());
  }
}
