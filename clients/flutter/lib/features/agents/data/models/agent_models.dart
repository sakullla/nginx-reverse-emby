String stringId(dynamic value) => value?.toString() ?? '';

List<String> stringList(dynamic value) =>
    value is List ? value.map((item) => item.toString()).toList() : const [];

DateTime? dateTimeOrNull(dynamic value) {
  if (value is DateTime) return value;
  if (value == null) return null;
  return DateTime.tryParse(value.toString());
}

class AgentSummary {
  const AgentSummary({
    required this.id,
    required this.name,
    required this.status,
    this.mode,
    this.platform,
    this.version,
    this.lastSeen,
    this.currentRevision,
    this.targetRevision,
    this.tags = const [],
    this.capabilities = const [],
  });

  final String id;
  final String name;
  final String status;
  final String? mode;
  final String? platform;
  final String? version;
  final DateTime? lastSeen;
  final int? currentRevision;
  final int? targetRevision;
  final List<String> tags;
  final List<String> capabilities;

  bool get isOnline => status.toLowerCase() == 'online';

  bool get hasPendingRevision =>
      currentRevision != null &&
      targetRevision != null &&
      currentRevision != targetRevision;

  factory AgentSummary.fromJson(Map<String, dynamic> json) => AgentSummary(
    id: stringId(json['id']),
    name: json['name']?.toString() ?? '',
    status: json['status']?.toString() ?? '',
    mode: json['mode']?.toString(),
    platform: json['platform']?.toString(),
    version: json['version']?.toString(),
    lastSeen: dateTimeOrNull(json['last_seen_at'] ?? json['last_seen']),
    currentRevision: _intOrNull(json['current_revision']),
    targetRevision: _intOrNull(
      json['desired_revision'] ?? json['target_revision'],
    ),
    tags: stringList(json['tags']),
    capabilities: stringList(json['capabilities']),
  );

  Map<String, dynamic> toJson() => {
    'id': id,
    'name': name,
    'status': status,
    if (mode != null) 'mode': mode,
    if (platform != null) 'platform': platform,
    if (version != null) 'version': version,
    if (lastSeen != null) 'last_seen': lastSeen!.toIso8601String(),
    if (currentRevision != null) 'current_revision': currentRevision,
    if (targetRevision != null) 'target_revision': targetRevision,
    'tags': tags,
    'capabilities': capabilities,
  };
}

int? _intOrNull(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value);
  return null;
}
