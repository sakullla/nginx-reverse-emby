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

  factory ClientProfile.fromJson(Map<String, Object?> json) {
    return ClientProfile(
      masterUrl: _requiredString(json, 'masterUrl'),
      displayName: _requiredString(json, 'displayName'),
      agentId: _requiredString(json, 'agentId'),
      token: _requiredString(json, 'token'),
    );
  }

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

  Map<String, Object?> toJson() {
    return {
      'masterUrl': masterUrl,
      'displayName': displayName,
      'agentId': agentId,
      'token': token,
    };
  }
}

String _requiredString(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is String) {
    return value;
  }
  throw FormatException('Invalid client profile: $key is required');
}

class ClientState {
  const ClientState({
    required this.profile,
    this.runtimeStatus = ClientRuntimeStatus.unconfigured,
    this.lastError = '',
    this.platform = 'unknown',
  });

  factory ClientState.empty() {
    return const ClientState(
      profile: ClientProfile(masterUrl: '', displayName: ''),
    );
  }

  final ClientProfile profile;
  final ClientRuntimeStatus runtimeStatus;
  final String lastError;
  final String platform;

  ClientState copyWith({
    ClientProfile? profile,
    ClientRuntimeStatus? runtimeStatus,
    String? lastError,
    String? platform,
  }) {
    return ClientState(
      profile: profile ?? this.profile,
      runtimeStatus: runtimeStatus ?? this.runtimeStatus,
      lastError: lastError ?? this.lastError,
      platform: platform ?? this.platform,
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
