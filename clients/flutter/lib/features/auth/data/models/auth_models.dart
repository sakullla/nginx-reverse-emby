class ClientProfile {
  const ClientProfile({
    this.masterUrl = '',
    this.displayName = '',
    this.agentId = '',
    this.token = '',
  });

  final String masterUrl;
  final String displayName;
  final String agentId;
  final String token;

  bool get isRegistered => agentId.isNotEmpty && token.isNotEmpty;

  Map<String, dynamic> toJson() => {
        'masterUrl': masterUrl,
        'displayName': displayName,
        'agentId': agentId,
        'token': token,
      };

  factory ClientProfile.fromJson(Map<String, dynamic> json) => ClientProfile(
        masterUrl: json['masterUrl'] as String? ?? '',
        displayName: json['displayName'] as String? ?? '',
        agentId: json['agentId'] as String? ?? '',
        token: json['token'] as String? ?? '',
      );

  ClientProfile copyWith({
    String? masterUrl,
    String? displayName,
    String? agentId,
    String? token,
  }) =>
      ClientProfile(
        masterUrl: masterUrl ?? this.masterUrl,
        displayName: displayName ?? this.displayName,
        agentId: agentId ?? this.agentId,
        token: token ?? this.token,
      );
}

sealed class AuthState {
  const AuthState();
}

class AuthStateUnauthenticated extends AuthState {
  const AuthStateUnauthenticated();
}

class AuthStateAuthenticated extends AuthState {
  const AuthStateAuthenticated(this.profile);
  final ClientProfile profile;
}

class AuthStateLoading extends AuthState {
  const AuthStateLoading();
}

class AuthStateError extends AuthState {
  const AuthStateError(this.message);
  final String message;
}
