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
  });

  final String id;
  final String domain;
  final String? ca; // e.g. "Let's Encrypt", "Self-signed"
  final DateTime? issuedAt;
  final DateTime? expiresAt;
  final bool isSelfSigned;
  final List<String> associatedRules; // domain names using this cert
  final String? fingerprint;

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
        id: json['id'] as String? ?? '',
        domain: json['domain'] as String? ?? '',
        ca: json['ca'] as String?,
        issuedAt: json['issued_at'] != null
            ? DateTime.tryParse(json['issued_at'] as String)
            : null,
        expiresAt: json['expires_at'] != null
            ? DateTime.tryParse(json['expires_at'] as String)
            : null,
        isSelfSigned: json['self_signed'] as bool? ?? false,
        associatedRules: (json['associated_rules'] as List<dynamic>?)
                ?.whereType<String>()
                .toList() ??
            [],
        fingerprint: json['fingerprint'] as String?,
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
      };
}
