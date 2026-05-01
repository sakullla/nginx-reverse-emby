String stringId(dynamic value) => value?.toString() ?? '';

List<String> stringList(dynamic value) =>
    value is List ? value.map((item) => item.toString()).toList() : const [];

/// A relay listener managed by the control plane.
class RelayListener {
  RelayListener({
    required this.id,
    String? listenAddress,
    this.protocol = 'TCP',
    this.enabled = true,
    this.agentId,
    this.agentName,
    this.name,
    this.listenPort,
    this.bindHosts = const [],
    this.tlsMode,
    this.certificateSource,
    this.certificateId,
  }) : listenAddress =
           listenAddress ??
           (bindHosts.length == 1 && listenPort != null
               ? '${bindHosts.first}:$listenPort'
               : '');

  final String id;
  final String listenAddress; // e.g. "0.0.0.0:443"
  final String protocol; // TCP, UDP, TLS
  final bool enabled;
  final String? agentId; // associated agent
  final String? agentName;
  final String? name;
  final int? listenPort;
  final List<String> bindHosts;
  final String? tlsMode;
  final String? certificateSource;
  final String? certificateId;

  factory RelayListener.fromJson(Map<String, dynamic> json) {
    final bindHosts = stringList(json['bind_hosts']);
    final listenPort = _intOrNull(json['listen_port']);
    return RelayListener(
      id: stringId(json['id']),
      listenAddress:
          json['listen_address'] as String? ??
          json['listenAddress'] as String? ??
          _listenAddress(bindHosts, listenPort),
      protocol: json['protocol'] as String? ?? 'TCP',
      enabled: json['enabled'] as bool? ?? true,
      agentId: json['agent_id'] as String? ?? json['agentId'] as String?,
      agentName: json['agent_name'] as String? ?? json['agentName'] as String?,
      name: json['name']?.toString(),
      listenPort: listenPort,
      bindHosts: bindHosts,
      tlsMode: json['tls_mode']?.toString(),
      certificateSource: json['certificate_source']?.toString(),
      certificateId: json['certificate_id']?.toString(),
    );
  }

  Map<String, dynamic> toJson() => {
    'id': id,
    'listen_address': listenAddress,
    'protocol': protocol,
    'enabled': enabled,
    if (agentId != null) 'agent_id': agentId,
    if (agentName != null) 'agent_name': agentName,
    if (name != null) 'name': name,
    if (listenPort != null) 'listen_port': listenPort,
    'bind_hosts': bindHosts,
    if (tlsMode != null) 'tls_mode': tlsMode,
    if (certificateSource != null) 'certificate_source': certificateSource,
    if (certificateId != null) 'certificate_id': certificateId,
  };

  RelayListener copyWith({
    String? id,
    String? listenAddress,
    String? protocol,
    bool? enabled,
    String? agentId,
    String? agentName,
    String? name,
    int? listenPort,
    List<String>? bindHosts,
    String? tlsMode,
    String? certificateSource,
    String? certificateId,
  }) => RelayListener(
    id: id ?? this.id,
    listenAddress: listenAddress ?? this.listenAddress,
    protocol: protocol ?? this.protocol,
    enabled: enabled ?? this.enabled,
    agentId: agentId ?? this.agentId,
    agentName: agentName ?? this.agentName,
    name: name ?? this.name,
    listenPort: listenPort ?? this.listenPort,
    bindHosts: bindHosts ?? this.bindHosts,
    tlsMode: tlsMode ?? this.tlsMode,
    certificateSource: certificateSource ?? this.certificateSource,
    certificateId: certificateId ?? this.certificateId,
  );
}

class CreateRelayListenerRequest {
  const CreateRelayListenerRequest({
    required this.agentId,
    required this.name,
    required this.listenPort,
    this.bindHosts = const [],
    this.enabled = true,
    this.tlsMode,
    this.certificateSource,
    this.certificateId,
  });

  final String agentId;
  final String name;
  final int listenPort;
  final List<String> bindHosts;
  final bool enabled;
  final String? tlsMode;
  final String? certificateSource;
  final String? certificateId;

  Map<String, dynamic> toJson() => {
    'agent_id': agentId,
    'name': name,
    'listen_port': listenPort,
    'bind_hosts': bindHosts,
    'enabled': enabled,
    if (tlsMode != null) 'tls_mode': tlsMode,
    if (certificateSource != null) 'certificate_source': certificateSource,
    if (certificateId != null) 'certificate_id': certificateId,
  };
}

class UpdateRelayListenerRequest {
  const UpdateRelayListenerRequest({
    this.name,
    this.listenPort,
    this.bindHosts,
    this.enabled,
    this.tlsMode,
    this.certificateSource,
    this.certificateId,
  });

  final String? name;
  final int? listenPort;
  final List<String>? bindHosts;
  final bool? enabled;
  final String? tlsMode;
  final String? certificateSource;
  final String? certificateId;

  Map<String, dynamic> toJson() => {
    if (name != null) 'name': name,
    if (listenPort != null) 'listen_port': listenPort,
    if (bindHosts != null) 'bind_hosts': bindHosts,
    if (enabled != null) 'enabled': enabled,
    if (tlsMode != null) 'tls_mode': tlsMode,
    if (certificateSource != null) 'certificate_source': certificateSource,
    if (certificateId != null) 'certificate_id': certificateId,
  };
}

String _listenAddress(List<String> bindHosts, int? listenPort) {
  if (bindHosts.isEmpty || listenPort == null) return '';
  return '${bindHosts.first}:$listenPort';
}

int? _intOrNull(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value);
  return null;
}
