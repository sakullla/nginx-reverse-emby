enum ConnectionMode { management, agent }

class ManagementProfile {
  const ManagementProfile({this.panelToken = ''});

  final String panelToken;

  bool get isConfigured => panelToken.trim().isNotEmpty;

  Map<String, dynamic> toJson() => {'panelToken': panelToken};

  factory ManagementProfile.fromJson(Map<String, dynamic>? json) {
    if (json == null) return const ManagementProfile();
    return ManagementProfile(panelToken: json['panelToken'] as String? ?? '');
  }
}

class AgentProfile {
  const AgentProfile({this.agentId = '', this.agentToken = ''});

  final String agentId;
  final String agentToken;

  bool get isRegistered =>
      agentId.trim().isNotEmpty && agentToken.trim().isNotEmpty;

  Map<String, dynamic> toJson() => {
    'agentId': agentId,
    'agentToken': agentToken,
  };

  factory AgentProfile.fromJson(Map<String, dynamic>? json) {
    if (json == null) return const AgentProfile();
    return AgentProfile(
      agentId: json['agentId'] as String? ?? '',
      agentToken: json['agentToken'] as String? ?? '',
    );
  }
}

class ClientProfile {
  const ClientProfile({
    this.masterUrl = '',
    this.displayName = '',
    this.activeMode = ConnectionMode.agent,
    this.management = const ManagementProfile(),
    AgentProfile? agent,
    String agentId = '',
    String token = '',
  }) : _agent = agent,
       _legacyAgentId = agentId,
       _legacyToken = token;

  final String masterUrl;
  final String displayName;
  final ConnectionMode activeMode;
  final ManagementProfile management;
  final AgentProfile? _agent;
  final String _legacyAgentId;
  final String _legacyToken;

  AgentProfile get agent =>
      _agent ?? AgentProfile(agentId: _legacyAgentId, agentToken: _legacyToken);
  String get agentId => agent.agentId;
  String get token => agent.agentToken;
  bool get isRegistered => agent.isRegistered || management.isConfigured;
  bool get hasAgentCredentials => agent.isRegistered;
  bool get hasManagementCredentials => management.isConfigured;

  Map<String, dynamic> toJson() => {
    'masterUrl': masterUrl,
    'displayName': displayName,
    'activeMode': activeMode.name,
    'management': management.toJson(),
    'agent': agent.toJson(),
  };

  factory ClientProfile.fromJson(Map<String, dynamic> json) {
    final legacyAgentId = json['agentId'] as String? ?? '';
    final legacyToken = json['token'] as String? ?? '';
    final agent = json['agent'] is Map<String, dynamic>
        ? AgentProfile.fromJson(json['agent'] as Map<String, dynamic>)
        : AgentProfile(agentId: legacyAgentId, agentToken: legacyToken);
    final management = ManagementProfile.fromJson(
      json['management'] is Map<String, dynamic>
          ? json['management'] as Map<String, dynamic>
          : null,
    );
    final parsedMode = ConnectionMode.values
        .where((mode) => mode.name == json['activeMode'])
        .firstOrNull;
    final fallbackMode = management.isConfigured
        ? ConnectionMode.management
        : ConnectionMode.agent;
    return ClientProfile(
      masterUrl: json['masterUrl'] as String? ?? '',
      displayName: json['displayName'] as String? ?? '',
      activeMode: parsedMode ?? fallbackMode,
      management: management,
      agent: agent,
    );
  }

  ClientProfile copyWith({
    String? masterUrl,
    String? displayName,
    ConnectionMode? activeMode,
    ManagementProfile? management,
    AgentProfile? agent,
  }) => ClientProfile(
    masterUrl: masterUrl ?? this.masterUrl,
    displayName: displayName ?? this.displayName,
    activeMode: activeMode ?? this.activeMode,
    management: management ?? this.management,
    agent: agent ?? this.agent,
  );

  ClientProfile clearManagement() {
    final nextMode = agent.isRegistered
        ? ConnectionMode.agent
        : ConnectionMode.management;
    return copyWith(
      activeMode: nextMode,
      management: const ManagementProfile(),
    );
  }

  ClientProfile clearAgent() {
    final nextMode = management.isConfigured
        ? ConnectionMode.management
        : ConnectionMode.agent;
    return copyWith(activeMode: nextMode, agent: const AgentProfile());
  }
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
