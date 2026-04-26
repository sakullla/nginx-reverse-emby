enum ClientRuntimeStatus {
  unconfigured,
  registered,
  starting,
  running,
  stopped,
  error,
}

class ClientProfile {
  const ClientProfile({
    required this.masterUrl,
    required this.displayName,
    this.agentId = '',
    this.token = '',
  });

  final String masterUrl;
  final String displayName;
  final String agentId;
  final String token;

  bool get isRegistered => agentId.trim().isNotEmpty && token.trim().isNotEmpty;

  ClientProfile copyWith({
    String? masterUrl,
    String? displayName,
    String? agentId,
    String? token,
  }) {
    return ClientProfile(
      masterUrl: masterUrl ?? this.masterUrl,
      displayName: displayName ?? this.displayName,
      agentId: agentId ?? this.agentId,
      token: token ?? this.token,
    );
  }
}

class ClientState {
  const ClientState({
    required this.profile,
    this.runtimeStatus = ClientRuntimeStatus.unconfigured,
    this.lastError = '',
  });

  factory ClientState.empty() {
    return const ClientState(
      profile: ClientProfile(masterUrl: '', displayName: ''),
    );
  }

  final ClientProfile profile;
  final ClientRuntimeStatus runtimeStatus;
  final String lastError;

  ClientState copyWith({
    ClientProfile? profile,
    ClientRuntimeStatus? runtimeStatus,
    String? lastError,
  }) {
    return ClientState(
      profile: profile ?? this.profile,
      runtimeStatus: runtimeStatus ?? this.runtimeStatus,
      lastError: lastError ?? this.lastError,
    );
  }
}

ClientState reduceClientState(
  ClientState state,
  ClientRuntimeStatus status, {
  String error = '',
}) {
  return state.copyWith(runtimeStatus: status, lastError: error);
}
