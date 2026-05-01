/// A relay listener managed by the control plane.
class RelayListener {
  const RelayListener({
    required this.id,
    required this.listenAddress,
    required this.protocol,
    this.enabled = true,
    this.agentId,
    this.agentName,
  });

  final String id;
  final String listenAddress; // e.g. "0.0.0.0:443"
  final String protocol; // TCP, UDP, TLS
  final bool enabled;
  final String? agentId; // associated agent
  final String? agentName;

  factory RelayListener.fromJson(Map<String, dynamic> json) => RelayListener(
        id: json['id'] as String? ?? '',
        listenAddress: json['listen_address'] as String? ??
            json['listenAddress'] as String? ??
            '',
        protocol: json['protocol'] as String? ?? 'TCP',
        enabled: json['enabled'] as bool? ?? true,
        agentId: json['agent_id'] as String? ?? json['agentId'] as String?,
        agentName: json['agent_name'] as String? ?? json['agentName'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'listen_address': listenAddress,
        'protocol': protocol,
        'enabled': enabled,
        if (agentId != null) 'agent_id': agentId,
        if (agentName != null) 'agent_name': agentName,
      };

  RelayListener copyWith({
    String? id,
    String? listenAddress,
    String? protocol,
    bool? enabled,
    String? agentId,
    String? agentName,
  }) =>
      RelayListener(
        id: id ?? this.id,
        listenAddress: listenAddress ?? this.listenAddress,
        protocol: protocol ?? this.protocol,
        enabled: enabled ?? this.enabled,
        agentId: agentId ?? this.agentId,
        agentName: agentName ?? this.agentName,
      );
}
