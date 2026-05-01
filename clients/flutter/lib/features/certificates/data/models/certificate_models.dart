String stringId(dynamic value) => value?.toString() ?? '';

List<String> stringList(dynamic value) =>
    value is List ? value.map((item) => item.toString()).toList() : const [];

DateTime? dateTimeOrNull(dynamic value) =>
    value is String ? DateTime.tryParse(value) : null;

/// Certificate status derived from the expiry date.
enum CertStatus { valid, expiring, expired }

/// A TLS certificate managed by the control plane.
class Certificate {
  const Certificate({
    required this.id,
    required this.domain,
    this.ca,
    this.issuedAt,
    this.expiresAt,
    this.isSelfSigned = false,
    this.associatedRules = const [],
    this.fingerprint,
    this.scope,
    this.issuerMode,
    this.certificateType,
    this.backendStatus,
    this.targetAgentIds = const [],
  });

  final String id;
  final String domain;
  final String? ca; // e.g. "Let's Encrypt", "Self-signed"
  final DateTime? issuedAt;
  final DateTime? expiresAt;
  final bool isSelfSigned;
  final List<String> associatedRules; // domain names using this cert
  final String? fingerprint;
  final String? scope;
  final String? issuerMode;
  final String? certificateType;
  final String? backendStatus;
  final List<String> targetAgentIds;

  String get displayStatus => backendStatus?.trim().isNotEmpty == true
      ? backendStatus!.trim()
      : status.name;

  bool get canIssue => certificateType != 'uploaded';

  /// Derive status from [expiresAt].
  CertStatus get status {
    if (expiresAt == null) return CertStatus.valid;
    final now = DateTime.now();
    if (now.isAfter(expiresAt!)) return CertStatus.expired;
    if (now.isAfter(expiresAt!.subtract(const Duration(days: 14)))) {
      return CertStatus.expiring;
    }
    return CertStatus.valid;
  }

  /// Number of days until expiry (negative if already expired).
  int get remainingDays {
    if (expiresAt == null) return 999;
    return expiresAt!.difference(DateTime.now()).inDays;
  }

  /// Progress 0.0 - 1.0 indicating how much of the certificate lifetime has
  /// elapsed. Used by [ExpiryBar].
  double get lifetimeProgress {
    if (issuedAt == null || expiresAt == null) return 0.0;
    final total = expiresAt!.difference(issuedAt!).inSeconds;
    if (total <= 0) return 1.0;
    final elapsed = DateTime.now().difference(issuedAt!).inSeconds;
    return (elapsed / total).clamp(0.0, 1.0);
  }

  factory Certificate.fromJson(Map<String, dynamic> json) => Certificate(
    id: stringId(json['id']),
    domain: json['domain'] as String? ?? '',
    ca: json['ca'] as String?,
    issuedAt: dateTimeOrNull(json['issued_at']),
    expiresAt: dateTimeOrNull(json['expires_at']),
    isSelfSigned: json['self_signed'] as bool? ?? false,
    associatedRules: stringList(json['associated_rules']),
    fingerprint: json['fingerprint'] as String?,
    scope: json['scope']?.toString(),
    issuerMode: json['issuer_mode']?.toString(),
    certificateType: json['certificate_type']?.toString(),
    backendStatus: json['status']?.toString(),
    targetAgentIds: stringList(json['target_agent_ids']),
  );

  Map<String, dynamic> toJson() => {
    'id': id,
    'domain': domain,
    if (ca != null) 'ca': ca,
    if (issuedAt != null) 'issued_at': issuedAt!.toIso8601String(),
    if (expiresAt != null) 'expires_at': expiresAt!.toIso8601String(),
    'self_signed': isSelfSigned,
    'associated_rules': associatedRules,
    if (fingerprint != null) 'fingerprint': fingerprint,
    if (scope != null) 'scope': scope,
    if (issuerMode != null) 'issuer_mode': issuerMode,
    if (certificateType != null) 'certificate_type': certificateType,
    if (backendStatus != null) 'status': backendStatus,
    'target_agent_ids': targetAgentIds,
  };
}

class CreateCertificateRequest {
  const CreateCertificateRequest({
    required this.domain,
    this.enabled = true,
    this.scope = 'domain',
    this.issuerMode = 'local_http01',
    this.certificateType = 'acme',
    this.targetAgentIds = const [],
    this.certificatePem,
    this.privateKeyPem,
    this.caPem,
  });

  final String domain;
  final bool enabled;
  final String scope;
  final String issuerMode;
  final String certificateType;
  final List<String> targetAgentIds;
  final String? certificatePem;
  final String? privateKeyPem;
  final String? caPem;

  Map<String, dynamic> toJson() => {
    'domain': domain,
    'enabled': enabled,
    'scope': scope,
    'issuer_mode': issuerMode,
    'certificate_type': certificateType,
    'target_agent_ids': targetAgentIds,
    if (certificatePem != null) 'certificate_pem': certificatePem,
    if (privateKeyPem != null) 'private_key_pem': privateKeyPem,
    if (caPem != null) 'ca_pem': caPem,
  };
}
