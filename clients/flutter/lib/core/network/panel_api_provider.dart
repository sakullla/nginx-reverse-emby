import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../features/auth/data/models/auth_models.dart';
import '../../features/auth/presentation/providers/auth_provider.dart';
import 'panel_api_client.dart';

part 'panel_api_provider.g.dart';

@riverpod
PanelApiClient panelApiClient(PanelApiClientRef ref) {
  final authAsync = ref.watch(authNotifierProvider);
  final authState = authAsync.valueOrNull;
  if (authState is AuthStateAuthenticated &&
      authState.profile.hasManagementCredentials) {
    final profile = authState.profile;
    return PanelApiClient(
      baseUrl: profile.masterUrl,
      panelToken: profile.management.panelToken,
    );
  }
  throw const PanelApiException('Management profile is not configured');
}

@riverpod
String selectedAgentId(SelectedAgentIdRef ref) => 'local';
